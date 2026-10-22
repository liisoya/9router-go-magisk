package dashboard

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestConnectionModelsErrorClassification(t *testing.T) {
	tests := []struct {
		name           string
		probeErr       error
		expectedStatus int
		expectedSubstr string
	}{
		{
			name:           "Timeout error returns 504 Gateway Timeout",
			probeErr:       osTimeoutError{},
			expectedStatus: http.StatusGatewayTimeout,
			expectedSubstr: "Request timeout",
		},
		{
			name:           "DeadlineExceeded returns 504 Gateway Timeout",
			probeErr:       context.DeadlineExceeded,
			expectedStatus: http.StatusGatewayTimeout,
			expectedSubstr: "Request timeout",
		},
		{
			name:           "Connection refused returns 502 Bad Gateway",
			probeErr:       syscall.ECONNREFUSED,
			expectedStatus: http.StatusBadGateway,
			expectedSubstr: "Connection refused",
		},
		{
			name:           "DNS error returns 502 Bad Gateway",
			probeErr:       &net.DNSError{Err: "no such host", Name: "example.com"},
			expectedStatus: http.StatusBadGateway,
			expectedSubstr: "DNS lookup failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origProbe := validateProbeDo
			defer func() { validateProbeDo = origProbe }()

			validateProbeDo = func(ctx context.Context, method, rawURL string, headers map[string]string, body []byte) (int, []byte, error) {
				return 0, nil, tt.probeErr
			}

			repo, cleanup := setupTestDB(t)
			defer cleanup()

			connID := "conn-test"
			if _, err := repo.RawDB().Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
				(?, 'antigravity', 'oauth', 'AG', 1, 1, '{"accessToken":"tok"}', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, connID); err != nil {
				t.Fatalf("failed to seed connection: %v", err)
			}

			h := NewDashboardHandler(repo)
			r := chi.NewRouter()
			r.Get("/api/providers/{id}/models", h.HandleGetConnectionModels)

			req := httptest.NewRequest(http.MethodGet, "/api/providers/"+connID+"/models", nil)
			rec := httptest.NewRecorder()

			r.ServeHTTP(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d (body: %s)", tt.expectedStatus, rec.Code, rec.Body.String())
			}
			if bodyStr := rec.Body.String(); !contains(bodyStr, tt.expectedSubstr) {
				t.Errorf("expected body to contain %q, got %q", tt.expectedSubstr, bodyStr)
			}
		})
	}
}

type osTimeoutError struct{}

func (osTimeoutError) Error() string   { return "i/o timeout" }
func (osTimeoutError) Timeout() bool   { return true }
func (osTimeoutError) Temporary() bool { return true }

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || searchSubstr(s, substr))
}

func searchSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
