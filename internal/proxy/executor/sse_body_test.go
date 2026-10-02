package executor

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/proxy"
)

// jsonResponse now folds an event stream into one chat.completion, so the
// risk this test guards is a false positive: an ordinary JSON body must reach
// the client byte-for-byte. Every other provider on this path (openai, iflow,
// kimchi, commandcode, opencode) answers with plain JSON.
func TestJSONResponse_LeavesPlainJSONUntouched(t *testing.T) {
	const upstream = `{"id":"chatcmpl-7","object":"chat.completion","created":9,` +
		`"choices":[{"index":0,"message":{"role":"assistant","content":"plain"},"finish_reason":"stop"}]}`

	rec := httptest.NewRecorder()
	if err := jsonResponse(t.Context(), rec, strings.NewReader(upstream), false, nil); err != nil {
		t.Fatalf("jsonResponse: %v", err)
	}
	if got := rec.Body.String(); got != upstream {
		t.Errorf("body was rewritten.\n got: %s\nwant: %s", got, upstream)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// A provider that answers 200 with an error envelope is a failure, not a
// body to relay: relaying it ended combo fallback and marked the account
// healthy, so the router stayed on the model that had just failed.
func TestJSONResponse_ErrorEnvelopeBehindA200FailsOver(t *testing.T) {
	const upstream = `{"error":{"message":"model not found","type":"invalid_request_error","code":404}}`

	rec := httptest.NewRecorder()
	err := jsonResponse(t.Context(), rec, strings.NewReader(upstream), false, nil)
	if err == nil {
		t.Fatal("jsonResponse served an error envelope as a completion")
	}
	var ue *proxy.UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("error is %T, want *proxy.UpstreamError", err)
	}
	if ue.StatusCode != http.StatusBadGateway {
		t.Errorf("StatusCode = %d, want 502", ue.StatusCode)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("nothing may reach the client on a failure, got %q", rec.Body.String())
	}
}

// A 200 with a blank body is the same failure with no envelope to inspect.
func TestJSONResponse_BlankBodyFailsOver(t *testing.T) {
	rec := httptest.NewRecorder()
	err := jsonResponse(t.Context(), rec, strings.NewReader(""), false, nil)
	if err == nil {
		t.Fatal("jsonResponse served an empty body as a completion")
	}
	var ue *proxy.UpstreamError
	if !errors.As(err, &ue) || ue.StatusCode != http.StatusBadGateway {
		t.Fatalf("want a 502 *proxy.UpstreamError, got %v", err)
	}
}
