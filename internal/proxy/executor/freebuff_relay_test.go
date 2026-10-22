package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"9router/proxy/internal/providers"
	"9router/proxy/internal/proxy"
)

// relayFake stands in for an edge relay worker (internal/handlers/media/
// deploy.go, netlify_relay.go): it ignores the URL it was dialed at and
// forwards to x-relay-target + x-relay-path. It records the destination so a
// test can assert where a control-plane call actually landed.
type relayFake struct {
	*httptest.Server
	gotTarget string
	gotPath   string
	calls     int
}

func newRelayFake(t *testing.T, body string) *relayFake {
	t.Helper()
	f := &relayFake{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.gotTarget = r.Header.Get("x-relay-target")
		f.gotPath = r.Header.Get("x-relay-path")
		f.calls++
		if f.gotTarget == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)
	return f
}

// freebuffRelayHeaders is the header set the chat pipeline stamps on a relayed
// freebuff connection. It is built by the production helper rather than
// hand-written: the bug this test guards is that x-relay-path is derived from
// the provider base URL — the chat route — and copying it onto a control-plane
// call sent every session/run/release request to the chat endpoint.
func freebuffRelayHeaders(t *testing.T) map[string]string {
	t.Helper()
	cfg, ok := providers.KnownProviders["freebuff"]
	if !ok {
		t.Fatal("freebuff missing from the provider registry")
	}
	headers := proxy.BuildEdgeRelayHeaders(cfg.BaseURL, cfg.StaticHeaders)
	if headers["x-relay-target"] == "" {
		t.Fatal("relay headers carry no x-relay-target")
	}
	return headers
}

func assertLanded(t *testing.T, relay *relayFake, wantPath string) {
	t.Helper()
	if relay.calls == 0 {
		t.Fatal("relay was never called: the control-plane request bypassed it")
	}
	if relay.gotTarget != "https://www.codebuff.com" {
		t.Errorf("x-relay-target = %q, want https://www.codebuff.com", relay.gotTarget)
	}
	if relay.gotPath != wantPath {
		t.Errorf("control-plane call routed to %q, want %q", relay.gotPath, wantPath)
	}
}

// A relayed connection must reach the session endpoint, not the chat route the
// connection stamped into x-relay-path.
func TestFreebuffRelay_SessionRequestLandsOnSessionEndpoint(t *testing.T) {
	relay := newRelayFake(t, `{"status":"active","instanceId":"inst-1","expiresAt":"2099-01-01T00:00:00Z"}`)
	headers := freebuffRelayHeaders(t)

	if _, err := postFreebuffSession(context.Background(), relay.Client(), relay.URL, "tok", "m-1", headers); err != nil {
		t.Fatalf("postFreebuffSession through a relay: %v", err)
	}
	assertLanded(t, relay, "/api/v1/freebuff/session")
}

func TestFreebuffRelay_RunStartLandsOnRunEndpoint(t *testing.T) {
	relay := newRelayFake(t, `{"runId":"run-1"}`)
	headers := freebuffRelayHeaders(t)

	runID, err := startFreebuffRun(context.Background(), relay.Client(), relay.URL, "tok", "m-1", headers)
	if err != nil {
		t.Fatalf("startFreebuffRun through a relay: %v", err)
	}
	if runID != "run-1" {
		t.Errorf("runId = %q, want run-1", runID)
	}
	assertLanded(t, relay, "/api/v1/agent-runs")
}

func TestFreebuffRelay_RunFinishLandsOnRunEndpoint(t *testing.T) {
	relay := newRelayFake(t, `{}`)
	headers := freebuffRelayHeaders(t)

	finishFreebuffRun(context.Background(), relay.Client(), relay.URL, "tok", "run-1", "completed", headers)
	assertLanded(t, relay, "/api/v1/agent-runs")
}

func TestFreebuffRelay_SessionReleaseLandsOnSessionEndpoint(t *testing.T) {
	relay := newRelayFake(t, `{"status":"released","freebucksRefund":3}`)
	headers := freebuffRelayHeaders(t)

	refund, err := releaseFreebuffSession(context.Background(), relay.Client(), relay.URL, "tok", "inst-1", headers)
	if err != nil {
		t.Fatalf("releaseFreebuffSession through a relay: %v", err)
	}
	if refund != 3 {
		t.Errorf("refund = %d, want 3", refund)
	}
	assertLanded(t, relay, "/api/v1/freebuff/session")
}

// A connection that is not relayed must not gain relay headers: those calls go
// straight to the origin freebuffOrigin already built.
func TestFreebuffRelay_UnrelayedConnectionSendsNoRelayHeaders(t *testing.T) {
	var gotTarget, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTarget = r.Header.Get("x-relay-target")
		gotPath = r.Header.Get("x-relay-path")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"active","instanceId":"inst-1"}`))
	}))
	defer srv.Close()

	if _, err := postFreebuffSession(context.Background(), srv.Client(), srv.URL, "tok", "m-1", nil); err != nil {
		t.Fatalf("postFreebuffSession without a relay: %v", err)
	}
	if gotTarget != "" || gotPath != "" {
		t.Errorf("unrelayed call carried relay headers: target=%q path=%q", gotTarget, gotPath)
	}
}

// A connection-supplied x-relay-path must not survive: the destination belongs
// to the call, not to the connection.
func TestFreebuffRelay_ConnectionSuppliedPathIsOverridden(t *testing.T) {
	relay := newRelayFake(t, `{"status":"active","instanceId":"inst-1"}`)
	headers := freebuffRelayHeaders(t)
	headers["x-relay-path"] = "/api/v1/chat/completions"

	if _, err := postFreebuffSession(context.Background(), relay.Client(), relay.URL, "tok", "m-1", headers); err != nil {
		t.Fatalf("postFreebuffSession through a relay: %v", err)
	}
	assertLanded(t, relay, "/api/v1/freebuff/session")
}
