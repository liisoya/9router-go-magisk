package executor

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

// chat.deepseek.com gates /api/v0/chat/completion on a proof of work: the
// server publishes a SHA3-256-shaped challenge and the client must search for
// the nonce whose DeepSeekHashV1 digest equals it. Upstream solves this in
// open-sse/lib/deepseek-pow.ts and open-sse/lib/deepseek-pow-hash.js.
//
// DeepSeekHashV1 is SHA3-256 with the last 23 KECCAK-p[1600] rounds instead of
// the full 24. Upstream derived that by differential testing against FIPS 202
// and pins it with vectors in tests/unit/deepseek-pow-js-only.test.ts. Go's
// crypto/sha3 only exposes the 24-round permutation, so the sponge is spelled
// out here; the round constants and rotation offsets are the public FIPS 202
// tables and only the round count is provider-specific.

const (
	// powRateBytes is SHA3-256's rate: the 200-byte state minus its 64-byte
	// capacity.
	powRateBytes = 136
	// powDigestBytes is SHA3-256's output length.
	powDigestBytes = 32
	// powDomainSuffix is SHA-3's domain separation byte.
	powDomainSuffix = 0x06
	// powRounds is DeepSeekHashV1's KECCAK-p[1600] round count.
	powRounds = 23
	// powMaxDifficulty caps the nonce search space, matching upstream's
	// MAX_DEEPSEEK_POW_DIFFICULTY.
	powMaxDifficulty = 250_000

	// powDeadlineCheckInterval is how many nonces the cancellable search runs
	// between checks of its context.
	powDeadlineCheckInterval = 1024
)

// powRoundConstants is the FIPS 202 KECCAK-p[1600] iota table. DeepSeekHashV1
// applies the last powRounds entries (indices 1..23).
var powRoundConstants = [24]uint64{
	0x0000000000000001, 0x0000000000008082, 0x800000000000808a, 0x8000000080008000,
	0x000000000000808b, 0x0000000080000001, 0x8000000080008081, 0x8000000000008009,
	0x000000000000008a, 0x0000000000000088, 0x0000000080008009, 0x000000008000000a,
	0x000000008000808b, 0x800000000000008b, 0x8000000000008089, 0x8000000000008003,
	0x8000000000008002, 0x8000000000000080, 0x000000000000800a, 0x800000008000000a,
	0x8000000080008081, 0x8000000000008080, 0x0000000080000001, 0x8000000080008008,
}

// powRotationOffsets indexes the rho step by x+5*y, matching FIPS 202 state
// coordinates.
var powRotationOffsets = [25]uint{
	0, 1, 62, 28, 27,
	36, 44, 6, 55, 20,
	3, 10, 43, 25, 39,
	41, 45, 15, 21, 8,
	18, 2, 61, 56, 14,
}

// powState is the 25-lane KECCAK state.
type powState [25]uint64

// rotl64 is Keccak's rho step. The offsets arrive as uint because that is how
// FIPS 202 tables them; a shift count above 63 is never in the table.
func rotl64(v uint64, n uint) uint64 {
	if n == 0 {
		return v
	}
	return v<<n | v>>(64-n)
}

// keccakP1600 applies the final powRounds rounds of KECCAK-p[1600, 24] to a.
func keccakP1600(a *powState) {
	// Rho and pi write into a copy so chi reads the pre-rho/pi lanes, which is
	// what the FIPS 202 round definition requires.
	var b powState
	for round := 24 - powRounds; round < 24; round++ {
		var c [5]uint64
		for x := range 5 {
			c[x] = a[x] ^ a[x+5] ^ a[x+10] ^ a[x+15] ^ a[x+20]
		}
		for x := range 5 {
			d := c[(x+4)%5] ^ rotl64(c[(x+1)%5], 1)
			for y := range 5 {
				a[x+5*y] ^= d
			}
		}

		for y := range 5 {
			for x := range 5 {
				lane := x + 5*y
				b[y+5*((2*x+3*y)%5)] = rotl64(a[lane], powRotationOffsets[lane])
			}
		}

		for y := range 5 {
			row := 5 * y
			for x := range 5 {
				a[x+row] = b[x+row] ^ (^b[(x+1)%5+row] & b[(x+2)%5+row])
			}
		}

		a[0] ^= powRoundConstants[round]
	}
}

// xorByte folds one byte into the state at block position pos.
func (s *powState) xorByte(pos int, b byte) {
	s[pos/8] ^= uint64(b) << (8 * uint(pos%8))
}

// digest extracts the 32 output bytes, little-endian within each lane.
func (s *powState) digest() [powDigestBytes]byte {
	var out [powDigestBytes]byte
	for lane := range powDigestBytes / 8 {
		binary.LittleEndian.PutUint64(out[lane*8:], s[lane])
	}
	return out
}

// absorbBlocks folds every whole rate-sized block of data into state and
// returns the offset of the first unabsorbed byte.
func absorbBlocks(state *powState, data []byte) int {
	offset := 0
	for offset+powRateBytes <= len(data) {
		for i := range powRateBytes {
			state.xorByte(i, data[offset+i])
		}
		keccakP1600(state)
		offset += powRateBytes
	}
	return offset
}

// padAndPermute applies SHA-3's pad10*1 to the tail starting at tailPos, then
// permutes. tailPos is always below powRateBytes.
func padAndPermute(state *powState, tailPos int) {
	state.xorByte(tailPos, powDomainSuffix)
	state.xorByte(powRateBytes-1, 0x80)
	keccakP1600(state)
}

// deepSeekHashV1 is DeepSeekHashV1 over an arbitrary input. The nonce search
// below never calls it — it is the reference implementation the search is
// cross-checked against, and it is what proves the incremental folding in
// findDeepSeekPowNonce agrees with a straight-from-the-sponge digest.
func deepSeekHashV1(input []byte) [powDigestBytes]byte {
	var state powState
	offset := absorbBlocks(&state, input)
	tail := input[offset:]
	for i, b := range tail {
		state.xorByte(i, b)
	}
	padAndPermute(&state, len(tail))
	return state.digest()
}

// deepSeekHashV1Hex is deepSeekHashV1 rendered as lowercase hex, the form
// both the challenge and the X-Ds-Pow-Response payload use.
func deepSeekHashV1Hex(input string) string {
	sum := deepSeekHashV1([]byte(input))
	return hex.EncodeToString(sum[:])
}

// findDeepSeekPowNonce searches prefix+str(nonce) for the nonce whose
// DeepSeekHashV1 digest equals challenge (hex), over nonce in [0, difficulty).
// It returns -1 when the search space holds no match, which is upstream's
// "solver failed" signal.
//
// The prefix is absorbed once and reused for every candidate: at the
// difficulties chat.deepseek.com issues, re-encoding and re-absorbing the
// prefix per nonce would dominate the cost.
func findDeepSeekPowNonce(prefix, challenge string, difficulty int) (int, error) {
	return findDeepSeekPowNonceWithContext(context.Background(), prefix, challenge, difficulty, 0)
}

// findDeepSeekPowNonceWithContext is findDeepSeekPowNonce bounded by a deadline
// and cancellable from ctx. The nonce search is pure CPU work, so a request
// that is cancelled — or a challenge whose difficulty is far above anything
// this upstream issues — would otherwise hold the handler past any client's
// patience. A zero timeout means unbounded, which is what the context-free
// caller wants.
//
// The deadline is checked every powDeadlineCheckInterval candidates: one
// KECCAK permutation over 23 rounds is well under a microsecond, so a coarser
// check costs nothing next to the search it guards.
func findDeepSeekPowNonceWithContext(ctx context.Context, prefix, challenge string, difficulty int, timeout time.Duration) (int, error) {
	if difficulty < 1 || difficulty > powMaxDifficulty {
		return -1, fmt.Errorf("deepseek-web: pow difficulty %d out of range 1-%d", difficulty, powMaxDifficulty)
	}
	target, err := hex.DecodeString(challenge)
	if err != nil || len(target) != powDigestBytes {
		return -1, fmt.Errorf("deepseek-web: pow challenge must be %d hex characters", powDigestBytes*2)
	}

	searchCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		searchCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	prefixBytes := []byte(prefix)
	var base powState
	prefixOffset := absorbBlocks(&base, prefixBytes)
	tailLength := len(prefixBytes) - prefixOffset

	nonceBuf := make([]byte, 0, 20)
	for nonce := range difficulty {
		if nonce%powDeadlineCheckInterval == 0 {
			if err := searchCtx.Err(); err != nil {
				return -1, fmt.Errorf("deepseek-web: pow search stopped at nonce %d of %d: %w", nonce, difficulty, err)
			}
		}

		state := base
		for i := range tailLength {
			state.xorByte(i, prefixBytes[prefixOffset+i])
		}

		position := tailLength
		nonceBuf = strconv.AppendInt(nonceBuf[:0], int64(nonce), 10)
		for _, c := range nonceBuf {
			state.xorByte(position, c)
			position++
			if position == powRateBytes {
				keccakP1600(&state)
				position = 0
			}
		}

		padAndPermute(&state, position)
		if digestMatches(&state, target) {
			return nonce, nil
		}
	}
	return -1, nil
}

// digestMatches compares the state's 32 output bytes against target without
// materialising a digest.
func digestMatches(state *powState, target []byte) bool {
	for lane := range powDigestBytes / 8 {
		if state[lane] != binary.LittleEndian.Uint64(target[lane*8:]) {
			return false
		}
	}
	return true
}
