package dashboard

import "testing"

// TestMergeClineFreeTier covers the free-tier merge: /api/v1/models carries no
// `cline-free/*` ids, so they come from a second feed, and a dead feed must not
// disturb what the catalogue already returned.
func TestMergeClineFreeTier(t *testing.T) {
	id := func(s string) map[string]any { return map[string]any{"id": s, "name": s} }

	tests := []struct {
		name      string
		catalogue []any
		free      []any
		wantIDs   []string
	}{
		{
			name:      "free tier is appended",
			catalogue: []any{id("deepseek-v4.1-flash")},
			free:      []any{id("cline-free/deepseek-v4.1-flash"), id("cline-free/grok-4.5")},
			wantIDs:   []string{"deepseek-v4.1-flash", "cline-free/deepseek-v4.1-flash", "cline-free/grok-4.5"},
		},
		{
			name:      "catalogue entry wins on a shared id",
			catalogue: []any{map[string]any{"id": "cline-free/grok-4.5", "name": "From catalogue"}},
			free:      []any{map[string]any{"id": "cline-free/grok-4.5", "name": "From feed"}},
			wantIDs:   []string{"cline-free/grok-4.5"},
		},
		{
			name:      "a dead feed leaves the catalogue untouched",
			catalogue: []any{id("deepseek-v4.1-flash")},
			free:      nil,
			wantIDs:   []string{"deepseek-v4.1-flash"},
		},
		{
			name:      "an empty catalogue still gets the tier",
			catalogue: nil,
			free:      []any{id("cline-free/grok-4.5")},
			wantIDs:   []string{"cline-free/grok-4.5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeClineFreeTier(tt.catalogue, tt.free)

			if len(got) != len(tt.wantIDs) {
				t.Fatalf("len = %d, want %d (%v)", len(got), len(tt.wantIDs), got)
			}
			for i, m := range got {
				mm, _ := m.(map[string]any)
				if mm["id"] != tt.wantIDs[i] {
					t.Errorf("item %d id = %v, want %q", i, mm["id"], tt.wantIDs[i])
				}
			}
		})
	}
}
