package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The handler's parameter parsing is what decides how much of the billing ledger
// a single request deletes, so the refusals matter as much as the happy path: a
// window that silently rounded to zero would read in the UI as "nothing to prune"
// while the operator believed they had asked for a deletion.
func TestRequestedLedgerDays(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		body     string
		want     int
		wantErr  bool
		errMatch string
	}{
		{name: "query days wins", target: "/x?days=90", want: 90},
		{name: "query days zero refused", target: "/x?days=0", wantErr: true, errMatch: "positive"},
		{name: "query days negative refused", target: "/x?days=-5", wantErr: true, errMatch: "positive"},
		{name: "query days non-numeric refused", target: "/x?days=abc", wantErr: true, errMatch: "positive"},
		{name: "query days above ceiling refused", target: "/x?days=999999", wantErr: true, errMatch: "ceiling"},
		{name: "query days at ceiling accepted", target: "/x?days=36500", want: 36500},
		{name: "body days accepted", target: "/x", body: `{"days":45}`, want: 45},
		{name: "body days zero refused", target: "/x", body: `{"days":0}`, wantErr: true, errMatch: "positive"},
		{name: "empty body defers to setting", target: "/x", want: 0},
		{name: "shared body with other keys defers", target: "/x", body: `{}`, want: 0},
		{name: "query wins over body", target: "/x?days=10", body: `{"days":99}`, want: 10},
		{name: "malformed body leaves days unset", target: "/x", body: `not json`, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r *http.Request
			if tt.body == "" {
				r = httptest.NewRequest(http.MethodPost, tt.target, nil)
			} else {
				r = httptest.NewRequest(http.MethodPost, tt.target, strings.NewReader(tt.body))
			}
			got, err := requestedLedgerDays(r)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("requestedLedgerDays = %d, want an error", got)
				}
				if tt.errMatch != "" && !strings.Contains(err.Error(), tt.errMatch) {
					t.Errorf("error = %q, want it to mention %q", err, tt.errMatch)
				}
				return
			}
			if err != nil {
				t.Fatalf("requestedLedgerDays: %v", err)
			}
			if got != tt.want {
				t.Errorf("requestedLedgerDays = %d, want %d", got, tt.want)
			}
		})
	}
}

// An explicit cutoff is converted into the day window the repository takes, and
// the derived value answers the same ceilings and refusals as a literal count.
func TestDaysFromBefore(t *testing.T) {
	now := time.Now().UTC()

	t.Run("a past date yields the equivalent window", func(t *testing.T) {
		got, err := daysFromBefore(now.AddDate(0, 0, -40).Format("2006-01-02"))
		if err != nil {
			t.Fatalf("daysFromBefore: %v", err)
		}
		if got != 40 {
			t.Errorf("days = %d, want 40", got)
		}
	})

	t.Run("an RFC3339 instant is accepted", func(t *testing.T) {
		got, err := daysFromBefore(now.Add(-72 * time.Hour).Format(time.RFC3339))
		if err != nil {
			t.Fatalf("daysFromBefore: %v", err)
		}
		if got != 3 {
			t.Errorf("days = %d, want 3", got)
		}
	})

	t.Run("the same day rounds up to one day, never zero", func(t *testing.T) {
		got, err := daysFromBefore(now.Add(-1 * time.Hour).Format(time.RFC3339))
		if err != nil {
			t.Fatalf("daysFromBefore: %v", err)
		}
		if got != 1 {
			t.Errorf("days = %d, want 1 (a window must never round to the 'not configured' zero)", got)
		}
	})

	t.Run("a future instant is refused", func(t *testing.T) {
		if _, err := daysFromBefore(now.Add(48 * time.Hour).Format(time.RFC3339)); err == nil {
			t.Error("daysFromBefore accepted an instant in the future")
		}
	})

	t.Run("an unparseable cutoff is refused", func(t *testing.T) {
		if _, err := daysFromBefore("last tuesday"); err == nil {
			t.Error("daysFromBefore accepted an unparseable cutoff")
		}
	})

	t.Run("an ancient cutoff is refused by the ceiling", func(t *testing.T) {
		if _, err := daysFromBefore("1800-01-01"); err == nil {
			t.Error("daysFromBefore accepted a date far beyond the ceiling")
		}
	})
}

// ?dryRun= only accepts the affirmative spellings; anything else must be read as
// a real deletion request, never silently treated as a preview.
func TestIsDryRun(t *testing.T) {
	for _, tt := range []struct {
		query string
		want  bool
	}{
		{"", false},
		{"?dryRun=1", true},
		{"?dryRun=true", true},
		{"?dryRun=yes", true},
		{"?dryRun=0", false},
		{"?dryRun=false", false},
		{"?dryRun=", false},
	} {
		r := httptest.NewRequest(http.MethodPost, "/x"+tt.query, nil)
		if got := isDryRun(r); got != tt.want {
			t.Errorf("isDryRun(%q) = %v, want %v", tt.query, got, tt.want)
		}
	}
}
