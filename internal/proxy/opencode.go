package proxy

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultOpenCodeUA is the official OpenCode fingerprint User-Agent.
	// OpenCode v2 fingerprint required by the free-tier /inference/v1 API.
	DefaultOpenCodeUA = "opencode/latest/2.0.29/cli"

	// DefaultOpenCodeV1UA is the pre-v2 identity kept by the keyed lanes
	// (opencode-zen, opencode-go): upstream decolua/9router#4842 moves only
	// the keyless free executor to v2 and leaves every keyed executor on v1.
	DefaultOpenCodeV1UA = "opencode/1.18.31"

	base62Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// OpenCodeSessionRegex matches canonical OpenCode session IDs: ses_ + 12 hex + 14 Base62 (30 chars).
var OpenCodeSessionRegex = regexp.MustCompile(`^ses_[0-9a-f]{12}[0-9A-Za-z]{14}$`)

// OpenCodeRequestRegex matches canonical OpenCode request IDs: msg_ + 12 hex + 14 Base62 (30 chars).
var OpenCodeRequestRegex = regexp.MustCompile(`^msg_[0-9a-f]{12}[0-9A-Za-z]{14}$`)

func unstableRandomBase62(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	var sb strings.Builder
	sb.Grow(n)
	for _, v := range b {
		sb.WriteByte(base62Chars[int(v)%62])
	}
	return sb.String()
}

// GenerateOpenCodeSessionID generates a descending canonical OpenCode session ID (30 chars).
// The counter is pinned to 1 like the upstream CLI: upstream decodes the
// prefix and rejects a non-small counter with 403 FreeTierError, so an
// incrementing counter would start failing under concurrent load
// (decolua/9router#4842 descendingTimeHex).
func GenerateOpenCodeSessionID() string {
	return "ses_" + descendingTimeHex(uint64(time.Now().UnixMilli()), 1) + unstableRandomBase62(14)
}

// descendingTimeHex renders the 12-hex session prefix: the low 48 bits of
// ~(timeMs * 0x1000 + counter), exactly like the upstream CLI.
func descendingTimeHex(timeMs, counter uint64) string {
	current := timeMs*0x1000 + counter
	value := ^current & (uint64(1)<<48 - 1)
	var timeBytes [6]byte
	for i := range 6 {
		timeBytes[i] = byte((value >> (40 - 8*uint64(i))) & 0xff)
	}
	return hex.EncodeToString(timeBytes[:])
}

// GenerateOpenCodeRequestID generates a canonical OpenCode request ID (30 chars).
func GenerateOpenCodeRequestID() string {
	now := time.Now().UnixMilli()
	current := uint64(now)*0x1000 + 1
	var timeBytes [6]byte
	for i := range 6 {
		timeBytes[i] = byte((current >> (40 - 8*uint64(i))) & 0xff)
	}
	return "msg_" + hex.EncodeToString(timeBytes[:]) + unstableRandomBase62(14)
}

// TranslateOpenCodeSessionID deterministically maps foreign sessions to valid 30-char canonical IDs.
func TranslateOpenCodeSessionID(sessionID, clientTool string) string {
	trimmed := strings.TrimSpace(sessionID)
	if OpenCodeSessionRegex.MatchString(trimmed) {
		return trimmed
	}
	tool := clientTool
	if tool == "" {
		tool = "generic"
	}
	h := sha256.New()
	h.Write([]byte("opencode\x00" + tool + "\x00" + trimmed))
	digest := h.Sum(nil)
	// Keep the deterministic time component but pin the low 12 counter bits
	// to the small value accepted by the OpenCode v2 free-tier gate.
	timeValue := uint64(0)
	for _, b := range digest[:5] {
		timeValue = (timeValue << 8) | uint64(b)
	}
	timeValue &= (uint64(1) << 36) - 1
	current := timeValue*0x1000 + 1
	var prefix [6]byte
	for i := range 6 {
		prefix[i] = byte((^current >> (40 - 8*uint64(i))) & 0xff)
	}
	timeHex := hex.EncodeToString(prefix[:])
	var sb strings.Builder
	sb.Grow(14)
	for i := 6; i < 20; i++ {
		sb.WriteByte(base62Chars[int(digest[i])%62])
	}
	return "ses_" + timeHex + sb.String()
}

var opencodeUARegex = regexp.MustCompile(`(?i)^opencode/([a-z0-9-]+)/(\d+)\.(\d+)\.(\d+)/([a-z0-9-]+)$`)

// HasValidOpenCodeVersion checks whether a User-Agent uses the OpenCode v2 shape.
func HasValidOpenCodeVersion(ua string) bool {
	matches := opencodeUARegex.FindStringSubmatch(ua)
	if len(matches) < 6 {
		return false
	}
	major, err := strconv.Atoi(matches[2])
	if err != nil {
		return false
	}
	return major >= 2
}

// HasValidOpenCodeV1Version checks the pre-v2 User-Agent shape the keyed lanes
// still speak (upstream opencode-zen at decolua/9router#4842): opencode/<major>.<minor>[.patch].
func HasValidOpenCodeV1Version(ua string) bool {
	m := opencodeV1UARegex.FindStringSubmatch(ua)
	if len(m) < 3 {
		return false
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return false
	}
	minor, err := strconv.Atoi(m[2])
	if err != nil {
		return false
	}
	return major > 1 || (major == 1 && minor >= 17)
}

var opencodeV1UARegex = regexp.MustCompile(`(?i)opencode/(\d+)\.(\d+)(?:\.(\d+))?`)

func generateTraceHeaders() (string, string) {
	var traceID [16]byte
	var spanID [8]byte
	var parentSpanID [8]byte
	_, _ = rand.Read(traceID[:])
	_, _ = rand.Read(spanID[:])
	_, _ = rand.Read(parentSpanID[:])

	tHex := hex.EncodeToString(traceID[:])
	sHex := hex.EncodeToString(spanID[:])
	pHex := hex.EncodeToString(parentSpanID[:])

	b3 := fmt.Sprintf("%s-%s-1-%s", tHex, sHex, pHex)
	traceparent := fmt.Sprintf("00-%s-%s-01", tHex, sHex)
	return b3, traceparent
}

// BuildOpenCodeHeaders generates the official OpenCode fingerprint headers to prevent 403 FreeTierError.
func BuildOpenCodeHeaders(rawHeaders map[string]string, sessionID string, isStream bool) map[string]string {
	var session string
	if sessionID != "" {
		session = TranslateOpenCodeSessionID(sessionID, "")
	} else {
		session = GenerateOpenCodeSessionID()
	}

	b3, traceparent := generateTraceHeaders()

	res := map[string]string{
		"Content-Type":          "application/json",
		"Authorization":         "Bearer public",
		"User-Agent":            DefaultOpenCodeUA,
		"x-opencode-client":     "cli",
		"x-opencode-session":    session,
		"x-opencode-session-id": session,
		"x-session-affinity":    session,
		"x-session-id":          session,
		"b3":                    b3,
		"traceparent":           traceparent,
		// Upstream defaults the project to "global"
		// (open-sse/executors/opencode.js buildHeaders).
		"x-opencode-project": "global",
	}
	if isStream {
		res["Accept"] = "text/event-stream"
	} else {
		res["Accept"] = "*/*"
	}
	for _, h := range []string{"x-relay-target", "x-relay-path"} {
		if v, ok := rawHeaders[h]; ok {
			res[h] = v
		}
	}
	for k, v := range rawHeaders {
		lk := strings.ToLower(k)
		if lk == "user-agent" && HasValidOpenCodeVersion(v) {
			res["User-Agent"] = v
		}
		if lk == "x-opencode-session" {
			trimmed := strings.TrimSpace(v)
			if trimmed == "" {
				continue
			}
			hint := trimmed
			if !OpenCodeSessionRegex.MatchString(hint) {
				hint = TranslateOpenCodeSessionID(hint, "")
			}
			// Upstream derives one session and mirrors it on all four
			// headers; a downstream hint must move every mirror together.
			res["x-opencode-session"] = hint
			res["x-opencode-session-id"] = hint
			res["x-session-affinity"] = hint
			res["x-session-id"] = hint
		}
		if lk == "x-opencode-client" {
			res[k] = v
		}
		if lk == "x-opencode-project" {
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				res["x-opencode-project"] = trimmed
			}
		}
		if lk == "b3" {
			trimmed := strings.TrimSpace(v)
			if trimmed != "" {
				res["b3"] = trimmed
			}
		}
		if lk == "traceparent" {
			trimmed := strings.TrimSpace(v)
			if trimmed != "" {
				res["traceparent"] = trimmed
			}
		}
	}
	return res
}
