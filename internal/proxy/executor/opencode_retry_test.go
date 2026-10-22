package executor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"9router/proxy/internal/providers"
	"9router/proxy/internal/proxy"
)

// The gate occasionally refuses a correctly-fingerprinted request and serves the
// same request on a repeat. These tests pin that exactly one repeat is made, and
// that nothing else is retried.

const gateRefusalBody = `{"type":"error","error":{"type":"FreeTierError","message":"OpenCode's free tier can only be used from within OpenCode. Download it at https://opencode.ai and keep building."}}`

// gateStub answers with the scripted handler per attempt, repeating the last.
func gateStub(t *testing.T, handlers ...http.HandlerFunc) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(atomic.AddInt32(&calls, 1)) - 1
		if n >= len(handlers) {
			n = len(handlers) - 1
		}
		handlers[n](w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func refuse(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func succeed(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
}

// send builds the closure the call sites pass to forwardOpenCode, dialling the
// stub through the same request path the executors use.
func send(srv *httptest.Server) func() (*http.Response, error) {
	cfg := &providers.ProviderConfig{BaseURL: srv.URL, StaticHeaders: map[string]string{}}
	return func() (*http.Response, error) {
		return proxy.ForwardOpenAI(context.Background(), srv.Client(), cfg, "public", []byte(`{}`), true)
	}
}

func TestForwardOpenCodeRetriesRefusalOnceThenSucceeds(t *testing.T) {
	srv, calls := gateStub(t,
		func(w http.ResponseWriter, _ *http.Request) { refuse(w, http.StatusForbidden, gateRefusalBody) },
		succeed,
	)
	resp, err := forwardOpenCode(context.Background(), send(srv))
	if err != nil {
		t.Fatalf("a single refusal must be retried, got: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 after the retry", resp.StatusCode)
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Errorf("upstream saw %d attempts, want exactly 2 (one refusal, one retry)", got)
	}
}

// The budget is one retry: a gate that refuses everything must be dialled twice,
// not more. This is the property the reviewer asked for.
func TestForwardOpenCodeStopsAfterOneRetry(t *testing.T) {
	srv, calls := gateStub(t, func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusForbidden, gateRefusalBody)
	})
	resp, err := forwardOpenCode(context.Background(), send(srv))
	if err == nil {
		resp.Body.Close()
		t.Fatal("every attempt refused, so an error is required")
	}
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Errorf("upstream saw %d attempts, want exactly 2 — one retry, not a loop", got)
	}
	var ue *proxy.UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("error = %T, want an upstream error carrying the status", err)
	}
	if ue.StatusCode != http.StatusForbidden {
		t.Errorf("reported status = %d, want the refusal's 403", ue.StatusCode)
	}
}

// A 403 that is not the free-tier refusal means the key or account is wrong and
// fails identically forever; repeating it would only burn quota.
func TestForwardOpenCodeDoesNotRetryOtherForbidden(t *testing.T) {
	srv, calls := gateStub(t, func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusForbidden, `{"error":{"message":"insufficient credit"}}`)
	})
	resp, err := forwardOpenCode(context.Background(), send(srv))
	if err == nil {
		resp.Body.Close()
		t.Fatal("a real 403 must be reported")
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("upstream saw %d attempts, want 1 — a quota 403 must not be retried", got)
	}
}

// Other statuses keep their existing handling (transport retry, account
// failover); this layer must not add a retry of its own.
func TestForwardOpenCodeLeavesOtherStatusesAlone(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv, calls := gateStub(t, func(w http.ResponseWriter, _ *http.Request) {
				refuse(w, status, `{"error":{"message":"x"}}`)
			})
			resp, err := forwardOpenCode(context.Background(), send(srv))
			if err == nil {
				resp.Body.Close()
				t.Fatalf("%d must be reported as an error", status)
			}
			if got := atomic.LoadInt32(calls); got != 1 {
				t.Errorf("upstream saw %d attempts, want 1 — only the free-tier refusal is retried here", got)
			}
		})
	}
}

// A caller that has gone away must not be held for the retry.
func TestForwardOpenCodeStopsOnCancelledContext(t *testing.T) {
	srv, calls := gateStub(t, func(w http.ResponseWriter, _ *http.Request) {
		refuse(w, http.StatusForbidden, gateRefusalBody)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp, err := forwardOpenCode(ctx, send(srv))
	if err == nil {
		resp.Body.Close()
		t.Fatal("a cancelled request must not report success")
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("upstream saw %d attempts after cancellation, want 1", got)
	}
}

// The retry wait stays small: it recovers a blip, it does not stall a completion.
func TestOpenCodeRetryDelayIsSmall(t *testing.T) {
	if openCodeRetryDelay > 500*time.Millisecond {
		t.Errorf("openCodeRetryDelay = %v, want under 500ms so the retry is not felt", openCodeRetryDelay)
	}
}

func TestIsOpenCodeRefusal(t *testing.T) {
	cases := []struct {
		name string
		ue   *proxy.UpstreamError
		want bool
	}{
		{"gate refusal", &proxy.UpstreamError{StatusCode: 403, Body: []byte(gateRefusalBody)}, true},
		{"refusal without prose", &proxy.UpstreamError{StatusCode: 403, Body: []byte(`{"error":{"type":"FreeTierError"}}`)}, true},
		{"quota 403", &proxy.UpstreamError{StatusCode: 403, Body: []byte(`{"error":{"message":"insufficient credit"}}`)}, false},
		{"401 with the token", &proxy.UpstreamError{StatusCode: 401, Body: []byte(gateRefusalBody)}, false},
		{"429 with the token", &proxy.UpstreamError{StatusCode: 429, Body: []byte(gateRefusalBody)}, false},
		{"empty body", &proxy.UpstreamError{StatusCode: 403}, false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOpenCodeRefusal(tc.ue); got != tc.want {
				t.Errorf("isOpenCodeRefusal = %v, want %v", got, tc.want)
			}
		})
	}
}
