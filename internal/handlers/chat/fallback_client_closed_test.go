package chat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"9router/proxy/internal/db"
)

// TestHandleAccountFallback_ClientCancelReports499 pins the status a client
// abort produces on the account-fallback path.
//
// handleAccountFallback used to return the raw forward error, which is not an
// *upstreamError, so the write sites in chat.go fell through to their
// http.StatusBadGateway default: a client that hung up was reported to the
// caller — and recorded in the request log — as a 502 "upstream error:
// context canceled". The combo path already returned 499 for the same
// condition, so the two disagreed.
func TestHandleAccountFallback_ClientCancelReports499(t *testing.T) {
	tests := []struct {
		name string
		// cancelBefore hangs the client up before the call is made, which is
		// the same ctx.Err() != nil condition a disconnect mid-flight leaves
		// behind, without the flakiness of racing a goroutine against the
		// upstream request.
		cancelBefore bool
		want499      bool
	}{
		{name: "client abort reports 499 instead of a bare 502", cancelBefore: true, want499: true},
		{name: "a live request keeps its upstream status", cancelBefore: false, want499: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				w.Write([]byte(`{"error":"upstream down"}`))
			}))
			defer srv.Close()

			database, cleanup := setupChatTestDB(t)
			defer cleanup()
			// Remove the helper's pre-seeded connections so conn-cancel is the
			// only deepseek connection.
			if _, err := database.Exec(`DELETE FROM providerConnections WHERE id IN ('conn-1', 'conn-2')`); err != nil {
				t.Fatalf("clear seeded connections: %v", err)
			}
			seedConnDB(t, database, "deepseek", "conn-cancel", "sk-cancel", srv.URL)

			h := NewChatHandler(db.NewRepo(database))

			ctx := context.Background()
			if tt.cancelBefore {
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}

			body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
			rec := httptest.NewRecorder()
			err := h.handleAccountFallback(ctx, rec, "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions")
			if err == nil {
				t.Fatal("expected an error, got nil")
			}

			var ue *upstreamError
			if !errors.As(err, &ue) {
				t.Fatalf("expected *upstreamError so the caller can choose a status, got %T: %v", err, err)
			}
			if got := ue.StatusCode == StatusClientClosedRequest; got != tt.want499 {
				t.Errorf("StatusCode = %d, want499 = %v", ue.StatusCode, tt.want499)
			}
		})
	}
}

// TestClientClosedBodyMatchesCombo pins that the body travelling with the 499
// is the same payload the combo path has always sent, so a client cannot tell
// which of the two paths answered it.
func TestClientClosedBodyMatchesCombo(t *testing.T) {
	if want := `{"error":{"message":"client closed request","type":"client_closed_request","code":499}}`; string(clientClosedBody) != want {
		t.Errorf("clientClosedBody = %s, want %s", clientClosedBody, want)
	}
}
