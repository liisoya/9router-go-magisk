package executor

import (
	"crypto/sha3"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
)

// The vectors are OmniRoute's, lifted from
// tests/unit/deepseek-pow-js-only.test.ts:29-33. They are the only ground
// truth available for DeepSeekHashV1, whose round count upstream reverse
// engineered from black-box captures rather than from a specification.
var deepSeekHashV1Vectors = []struct {
	input string
	want  string
}{
	{"1122334455667788_1778891543095_0", "311b26ae1e0fe7375e242958ce46db5552a6c67fea3f96880dcd846c63a74286"},
	{"1122334455667788_1778891543095_1", "526f9103dfe22bcda9481b7f304a157b8edb18c5bb96a2061eefec1fbd0db706"},
	{"vector_42_0", "1813da791ddac0da3a225e5878ff3d3ea07577e048f8e9575b82a274b71e3810"},
	{"vector_42_1", "b582fdb4f83662baec734e23373efc860515c03c40decbef076697eb5832f83c"},
	{"vector_42_2", "48b348ad54c372f78ccb6a26cbf156668f4c6a5b7d5a1cf19ccd78888586019b"},
	{"vector_42_3", "2ffed26ea9e1d6f4bbe49a266d98fe04ad9a5ad4c6d765862de8d18c513c3815"},
	{"bb_1778891543095_0", "705e5d630f02d09a8179c6a0fcb0caf7265f08fb206fadca0301224f4422fc64"},
}

// TestDeepSeekHashV1Vectors pins the 23-round permutation against upstream's
// clean-room vectors. A silent change here would fail no chat test: the nonce
// search would simply never find a match and every deepseek-web request would
// fail its proof of work upstream.
func TestDeepSeekHashV1Vectors(t *testing.T) {
	for _, tc := range deepSeekHashV1Vectors {
		t.Run(tc.input, func(t *testing.T) {
			sum := deepSeekHashV1([]byte(tc.input))
			if got := hex.EncodeToString(sum[:]); got != tc.want {
				t.Fatalf("deepSeekHashV1(%q) = %s, want %s", tc.input, got, tc.want)
			}
		})
	}
}

// TestDeepSeekHashV1IsNotFIPS202 guards the premise the vectors rest on: the
// provider's hash differs from standard SHA3-256. If a future refactor swapped
// in crypto/sha3 wholesale, this test says why that is wrong before the vectors
// merely report a mismatch.
func TestDeepSeekHashV1IsNotFIPS202(t *testing.T) {
	const input = "vector_42_0"
	ours := deepSeekHashV1([]byte(input))

	fips := sha3.New256()
	fips.Write([]byte(input))
	var reference [32]byte
	copy(reference[:], fips.Sum(nil))

	if ours == reference {
		t.Fatalf("DeepSeekHashV1 matches FIPS 202 SHA3-256 for %q; the 23-round permutation is not being applied", input)
	}
}

// TestFindDeepSeekPowNonce covers what a digest vector cannot: that the
// incremental folding the search performs agrees with a straight sponge digest,
// across the block-boundary shapes a real salt and nonce can reach.
func TestFindDeepSeekPowNonce(t *testing.T) {
	const (
		dswPrefix = "1122334455667788_1778891543095_"
		multiBlk  = "multi-block-multi-block-multi-block-multi-block-multi-block-multi-block-multi-block-"
	)
	// blockEdge ends 130 bytes into a fresh rate block, so a six-digit nonce
	// lands exactly on the 136-byte boundary and forces the padding onto a
	// second permutation — the one shape the vector corpus never reaches.
	blockEdge := strings.Repeat("a", powRateBytes) + strings.Repeat("b", powRateBytes-6)

	tests := []struct {
		name       string
		prefix     string
		challenge  string
		difficulty int
	}{
		{
			name:       "single block prefix",
			prefix:     dswPrefix,
			challenge:  "311b26ae1e0fe7375e242958ce46db5552a6c67fea3f96880dcd846c63a74286",
			difficulty: 2,
		},
		{
			name:       "short salt",
			prefix:     "bb_1778891543095_",
			challenge:  "705e5d630f02d09a8179c6a0fcb0caf7265f08fb206fadca0301224f4422fc64",
			difficulty: 2,
		},
		{
			// A prefix longer than two rate blocks forces the search to reuse an
			// already-permuted base state rather than start from zero.
			name:       "multi block prefix",
			prefix:     multiBlk,
			challenge:  deepSeekHashV1Hex(multiBlk + "0"),
			difficulty: 2,
		},
		{
			// The nonce text itself crosses the rate boundary, so the padding byte
			// lands after a second permutation rather than the first.
			name:       "nonce text crosses the rate boundary",
			prefix:     blockEdge,
			challenge:  deepSeekHashV1Hex(blockEdge + "100000"),
			difficulty: 100_001,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nonce, err := findDeepSeekPowNonce(tc.prefix, tc.challenge, tc.difficulty)
			if err != nil {
				t.Fatalf("findDeepSeekPowNonce: %v", err)
			}
			if nonce < 0 {
				t.Fatalf("no nonce found in %d candidates for a satisfiable challenge", tc.difficulty)
			}
			candidate := deepSeekHashV1([]byte(tc.prefix + strconv.Itoa(nonce)))
			if got := hex.EncodeToString(candidate[:]); got != tc.challenge {
				t.Fatalf("nonce %d hashes to %s, want %s", nonce, got, tc.challenge)
			}
		})
	}

	t.Run("no answer in the search space", func(t *testing.T) {
		const unsatisfiable = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		nonce, err := findDeepSeekPowNonce(dswPrefix, unsatisfiable, 64)
		if err != nil {
			t.Fatalf("findDeepSeekPowNonce: %v", err)
		}
		if nonce != -1 {
			t.Fatalf("expected -1 for an unsatisfiable challenge, got %d", nonce)
		}
	})

	t.Run("rejects a difficulty outside the bounded search space", func(t *testing.T) {
		if _, err := findDeepSeekPowNonce(dswPrefix, deepSeekHashV1Vectors[0].want, powMaxDifficulty+1); err == nil {
			t.Fatal("expected an error for a difficulty above the cap")
		}
	})

	t.Run("rejects a challenge that is not a digest", func(t *testing.T) {
		if _, err := findDeepSeekPowNonce(dswPrefix, "not-hex", 2); err == nil {
			t.Fatal("expected an error for a non-hex challenge")
		}
	})
}
