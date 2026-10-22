package executor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/proxy"
)

func TestFreebuffCountryRefusal_MatchesRegionCopyOnly(t *testing.T) {
	cases := []struct {
		body []byte
		want bool
	}{
		{[]byte(`{"status":"country_blocked"}`), true},
		{[]byte(`{"countryBlockReason":"country_not_allowed"}`), true},
		{[]byte(`{"message":"This model is not available in your country"}`), true},
		{[]byte(`{"error":"Authentication failed"}`), false},
		{[]byte(`{"status":"model_locked","currentModel":"a/b"}`), false},
	}
	for _, tc := range cases {
		if got := freebuffCountryRefusal(tc.body); got != tc.want {
			t.Errorf("freebuffCountryRefusal(%s) = %v, want %v", tc.body, got, tc.want)
		}
	}
}

func TestRequestFreebuffSession_CountryRefusalBecomesAStructuredError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"status":"country_blocked","countryCode":"ID","countryBlockReason":"country_not_allowed"}`))
	}))
	defer server.Close()

	_, err := requestFreebuffSession(context.Background(), server.Client(), server.URL, "tok", "deepseek/deepseek-v4-flash", nil)
	if err == nil {
		t.Fatal("expected a country refusal to fail admission")
	}

	var ue *proxy.UpstreamError
	if !errors.As(err, &ue) {
		t.Fatalf("expected an UpstreamError, got %T: %v", err, err)
	}
	if ue.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", ue.StatusCode)
	}
	body := string(ue.Body)
	if !strings.Contains(body, "country_blocked") {
		t.Errorf("expected a machine-readable country_blocked code, got %s", body)
	}
	if !strings.Contains(body, "ID") {
		t.Errorf("expected the detected country to be named, got %s", body)
	}
}

// A blocked region still gets an admitted session — the refusal lands on the
// turn instead. This is the live payload for such an account, and treating it
// as a failure would break every request in that region.
func TestRequestFreebuffSession_AdmittedSessionInABlockedRegionStillWorks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "active",
			"accessTier": "limited",
			"instanceId": "inst-blocked",
			"model": "deepseek/deepseek-v4-flash",
			"expiresAt": "2030-01-01T00:00:00Z",
			"countryCode": "ID",
			"countryBlockReason": "country_not_allowed"
		}`))
	}))
	defer server.Close()

	sess, err := requestFreebuffSession(context.Background(), server.Client(), server.URL, "tok", "deepseek/deepseek-v4-flash", nil)
	if err != nil {
		t.Fatalf("expected the session to be admitted, got %v", err)
	}
	if sess.InstanceID != "inst-blocked" {
		t.Errorf("expected instance inst-blocked, got %q", sess.InstanceID)
	}
}
