package executor

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"9router/proxy/internal/providers"
)

// decodeBody is a small test helper: the executors work on raw JSON bytes, so
// the assertions below read the body back as a map rather than comparing
// serialised strings (key order is not stable).
func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	return parsed
}

// The ChatGPT Codex OAuth endpoint answers 400 "Unsupported parameter:
// max_output_tokens" for a body that carries one, so the value must not reach
// it. Clients (OpenCode, Cline, Orca) always send max_tokens on chat
// completions, and buildResponsesBody folds that into max_output_tokens.
func TestStripCodexUnsupportedTokenParams_FromChatCompletions(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.6-terra",
		"max_tokens": 4096,
		"messages": [{"role": "user", "content": "hi"}]
	}`)

	body := stripCodexUnsupportedTokenParams(raw)
	parsed := decodeBody(t, body)

	for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if _, ok := parsed[key]; ok {
			t.Errorf("expected %q to be stripped for the Codex backend, got %v", key, parsed[key])
		}
	}
	if parsed["model"] != "gpt-5.6-terra" {
		t.Errorf("model must survive the strip, got %v", parsed["model"])
	}
}

// A client that already speaks the Responses API sends max_output_tokens
// directly. That one is stripped too — it is the exact field the backend
// rejects, so passing it through unchanged is the reported bug.
func TestStripCodexUnsupportedTokenParams_FromResponsesInput(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.6-terra",
		"input": [{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "hi"}]}],
		"max_output_tokens": 2048
	}`)

	body := stripCodexUnsupportedTokenParams(raw)
	parsed := decodeBody(t, body)

	if _, ok := parsed["max_output_tokens"]; ok {
		t.Errorf("a client-supplied max_output_tokens must not reach the Codex backend, got %v", parsed["max_output_tokens"])
	}
	if _, ok := parsed["input"]; !ok {
		t.Error("input must survive the strip")
	}
}

// Stripping is destructive, so a body that cannot be parsed must be returned
// byte-for-byte: a malformed payload should fail at the upstream with a useful
// error, not be silently rewritten into a valid-looking one.
func TestStripCodexUnsupportedTokenParams_LeavesUnparsableBodyAlone(t *testing.T) {
	raw := []byte(`{"model": "gpt-5.6-terra", "max_tokens":`)

	if body := stripCodexUnsupportedTokenParams(raw); string(body) != string(raw) {
		t.Errorf("an unparsable body must be passed through unchanged, got %s", body)
	}
}

// grok-cli speaks the same Responses dialect over a different backend that does
// accept the parameter, so the strip must stay scoped to the Codex path rather
// than living inside buildResponsesBody.
func TestStripCodexUnsupportedTokenParams_LeavesBuildResponsesBodyAlone(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.6-terra",
		"max_tokens": 4096,
		"messages": [{"role": "user", "content": "hi"}]
	}`)

	transformed, _, err := buildResponsesBody(raw)
	if err != nil {
		t.Fatalf("buildResponsesBody failed: %v", err)
	}

	parsed := decodeBody(t, transformed)
	if _, ok := parsed["max_output_tokens"]; !ok {
		t.Error("buildResponsesBody must keep emitting max_output_tokens; the strip is a Codex-backend concern")
	}
}

// The compaction marker and every other field must survive: ForwardCodex runs
// the strip and applyCodexCompact over the same body, and a compaction request
// is still a request the backend validates.
func TestStripCodexUnsupportedTokenParams_KeepsOtherFields(t *testing.T) {
	raw := []byte(`{
		"model": "gpt-5.6-terra",
		"max_output_tokens": 100,
		"_compact": true,
		"stream": true,
		"store": false,
		"reasoning": {"effort": "medium"}
	}`)

	parsed := decodeBody(t, stripCodexUnsupportedTokenParams(raw))

	if parsed["_compact"] != true {
		t.Error("the _compact marker must survive so applyCodexCompact still sees it")
	}
	if parsed["stream"] != true || parsed["store"] != false {
		t.Error("stream/store must survive the strip")
	}
	if _, ok := parsed["reasoning"]; !ok {
		t.Error("reasoning must survive the strip")
	}
}

// The unit tests above pin the helper; this one pins the wiring, because a
// helper that ForwardCodex never calls would leave the 400 exactly as
// reported. The fake upstream rejects the body the way ChatGPT does.
func TestForwardCodex_UpstreamNeverSeesTokenParams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Errorf("upstream got non-JSON: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
			if _, ok := m[key]; ok {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"detail":"Unsupported parameter: ` + key + `"}`))
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &providers.ProviderConfig{BaseURL: srv.URL, AuthHeader: "Authorization", AuthScheme: "bearer"}
	rec := httptest.NewRecorder()
	body := []byte(`{"model":"cx/gpt-5.6-terra","max_tokens":4096,"messages":[{"role":"user","content":"hi"}],"stream":true}`)

	// A 400 here means the parameter still reached the backend — the reported
	// bug, and the only thing this test exists to catch.
	if err := ForwardCodex(rec, &Request{
		Client:   srv.Client(),
		Config:   cfg,
		APIKey:   "test-token",
		Body:     body,
		IsStream: true,
	}); err != nil {
		t.Fatalf("ForwardCodex errored: %v", err)
	}
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("upstream rejected the request as unsupported-parameter: %s", rec.Body.String())
	}
}
