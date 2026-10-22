//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"9router/proxy/internal/middleware"
)

// bodyOf returns a JSON payload of exactly n bytes by padding the prompt field.
func bodyOf(t *testing.T, n int) []byte {
	t.Helper()
	const wrapper = `{"model":"probe-model","prompt":"` + `"}`
	pad := n - len(wrapper)
	if pad < 0 {
		t.Fatalf("requested body of %d bytes is smaller than the %d-byte wrapper", n, len(wrapper))
	}
	body := fmt.Sprintf(`{"model":"probe-model","prompt":"%s"}`, strings.Repeat("a", pad))
	if len(body) != n {
		t.Fatalf("built body of %d bytes, want %d", len(body), n)
	}
	return []byte(body)
}

// TestBodyLimitIsPathAwareThroughTheRealRouter drives app.ProvideRouter, so it
// asserts the middleware stack the binary actually serves — not a hand-built
// router that could mount the media lane differently.
//
// The contract from #125: a video upload above the 10MB JSON default reaches the
// media handler, while a chat body one byte past that default is still refused
// with a 413 that names the ceiling. Before the fix the global 10MB limit ran
// first for every path, so the media request died as a 413 and the client could
// not tell "file too large" from "malformed request".
func TestBodyLimitIsPathAwareThroughTheRealRouter(t *testing.T) {
	env := newEnv(t)

	const (
		jsonLimit    = middleware.DefaultMaxBodySize
		mediaLimit   = middleware.MediaMaxBodySize
		videoBodyLen = 25 * 1024 * 1024 // 25MB clip, the size that opened the issue
	)

	t.Run("chat body one byte over the default is refused with a 413", func(t *testing.T) {
		res := env.Post(t, "/v1/chat/completions", bodyOf(t, int(jsonLimit)+1))
		if res.Status != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413 (body: %s)", res.Status, truncate(res.Body))
		}
		if !strings.Contains(string(res.Body), "payload_too_large") {
			t.Errorf("body %q does not carry the payload_too_large error code", truncate(res.Body))
		}
		if !strings.Contains(string(res.Body), middleware.HumanBytes(jsonLimit)) {
			t.Errorf("body %q does not name the %s ceiling", truncate(res.Body), middleware.HumanBytes(jsonLimit))
		}
	})

	t.Run("chat body at the default is not refused for its size", func(t *testing.T) {
		res := env.Post(t, "/v1/chat/completions", bodyOf(t, int(jsonLimit)))
		if res.Status == http.StatusRequestEntityTooLarge {
			t.Fatalf("status = 413 for a body exactly at the %d default (body: %s)", jsonLimit, truncate(res.Body))
		}
	})

	t.Run("25MB video upload clears the JSON default", func(t *testing.T) {
		res := env.Post(t, "/v1/videos/generations", bodyOf(t, videoBodyLen))
		if res.Status == http.StatusRequestEntityTooLarge {
			t.Fatalf("status = 413: the media lane never saw the body (body: %s)", truncate(res.Body))
		}
		if strings.Contains(string(res.Body), "too large") {
			t.Fatalf("body %q still reports a size refusal", truncate(res.Body))
		}
		t.Logf("25MB video upload reached the handler, status %d", res.Status)
	})

	t.Run("25MB audio transcription clears the JSON default", func(t *testing.T) {
		res := env.Post(t, "/v1/audio/transcriptions", bodyOf(t, videoBodyLen))
		if res.Status == http.StatusRequestEntityTooLarge {
			t.Fatalf("status = 413: the media lane never saw the body (body: %s)", truncate(res.Body))
		}
		t.Logf("25MB audio upload reached the handler, status %d", res.Status)
	})

	t.Run("media ceiling is the resolved limit for the media path", func(t *testing.T) {
		if got := middleware.BodyLimitForPath("/v1/videos/generations", jsonLimit, middleware.MediaBodyLimitRules()); got != mediaLimit {
			t.Errorf("limit = %d, want %d", got, mediaLimit)
		}
	})
}
