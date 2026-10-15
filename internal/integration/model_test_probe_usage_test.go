//go:build integration

package integration

import (
	"testing"
)

// A dashboard "Test" probe reaches the provider for real, but it is not
// operator traffic. Recording it wrote a usageHistory row and a byProvider
// bucket, so the probed provider showed up on the Usage Overview topology map
// as if it had served a request nobody sent (#280).
func TestModelTestProbeRecordsNoUsage(t *testing.T) {
	env, up := newProviderEnv(t)

	res := env.Post(t, "/api/models/test", map[string]string{"model": "ds/deepseek-chat"})
	if res.Status != 200 {
		t.Fatalf("probe status = %d, body %s", res.Status, truncate(res.Body))
	}

	// The probe still has to actually reach the upstream — a guard that
	// short-circuited before the request would pass this test while breaking
	// the Test button entirely.
	if up.Count() == 0 {
		t.Fatal("probe never reached the upstream, so the Test button is broken")
	}

	var history int
	if err := env.Repo.RawDB().QueryRowContext(
		t.Context(), `SELECT COUNT(*) FROM usageHistory`).Scan(&history); err != nil {
		t.Fatalf("count usageHistory: %v", err)
	}
	if history != 0 {
		t.Errorf("usageHistory rows after one probe = %d, want 0: a probe was metered as traffic", history)
	}

	var daily int
	if err := env.Repo.RawDB().QueryRowContext(
		t.Context(), `SELECT COUNT(*) FROM usageDaily`).Scan(&daily); err != nil {
		t.Fatalf("count usageDaily: %v", err)
	}
	if daily != 0 {
		t.Errorf("usageDaily rows after one probe = %d, want 0: a probe reached the provider totals", daily)
	}

	// The overview is what renders the topology map, and it is keyed off
	// byProvider — the reason the phantom node appeared.
	stats := env.Get(t, "/api/usage/stats?range=24h")
	var payload struct {
		ByProvider map[string]struct {
			Requests int `json:"requests"`
		} `json:"byProvider"`
	}
	stats.Decode(t, &payload)
	if got := payload.ByProvider["deepseek"].Requests; got != 0 {
		t.Errorf("byProvider[deepseek].requests = %d, want 0: the topology map draws a node per byProvider key", got)
	}
}
