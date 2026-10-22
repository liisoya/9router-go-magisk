package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"9router/proxy/internal/db"
	"9router/proxy/internal/handlers/shared"
	"9router/proxy/internal/models"
)

// TestLiveCatalogFlightSurvivesLeaderCancellation pins that the shared catalog
// fetch is not cancelled when the caller who started it disconnects.
//
// resolveLiveCatalog collapses N racing requests into one upstream call via
// singleflight, but the closure captured the leader's request context. When the
// leader's client hung up, context cancellation tore down the fetch for every
// waiter as well — and because an empty result caches nothing, the next
// dashboard poll repeated the whole call. The log showed the consequence: far
// more "context canceled" than "context deadline exceeded".
//
// The upstream here answers the first request only after the test releases it,
// and fails every later one, so the shared call must complete for the follower
// to see anything. That also pins that exactly one upstream call was made.
func TestLiveCatalogFlightSurvivesLeaderCancellation(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			w.Write([]byte(`{"data":[{"id":"m-one"}]}`))
			return
		}
		// Any second call means the flight was not shared: it would find
		// nothing, which is exactly the regression this test guards.
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()
	h := NewChatHandler(db.NewRepo(database))

	// A distinct connection id keeps the catalog cache key unique, so a result
	// cached by an earlier test cannot satisfy this one.
	conn := &models.ProviderConnection{
		ID:       "conn-flight-cancel",
		Provider: "openai-compatible-flight",
		IsActive: 1,
	}
	connData := &shared.ConnectionData{
		APIKey:               "sk-flight",
		ProviderSpecificData: map[string]any{"baseUrl": srv.URL},
	}
	const providerID = "openai-compatible-flight"

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	defer cancelLeader()
	leaderDone := make(chan []LiveModel, 1)
	go func() {
		leaderDone <- h.resolveLiveCatalog(leaderCtx, conn, connData, providerID)
	}()

	// The shared fetch is now in flight. Hang the leader's client up, which is
	// the ctx.Err() != nil state a disconnect leaves behind.
	<-entered
	cancelLeader()
	time.Sleep(50 * time.Millisecond)

	followerDone := make(chan []LiveModel, 1)
	go func() {
		followerDone <- h.resolveLiveCatalog(context.Background(), conn, connData, providerID)
	}()

	// Let the follower reach the in-flight call, then let the upstream answer.
	time.Sleep(50 * time.Millisecond)
	close(release)

	follower := <-followerDone
	<-leaderDone

	if len(follower) != 1 || follower[0].ID != "m-one" {
		t.Fatalf("follower got %v, want the leader's shared result [m-one]", idsOf(follower))
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("upstream calls = %d, want 1 — the follower should join the leader's flight", got)
	}
}
