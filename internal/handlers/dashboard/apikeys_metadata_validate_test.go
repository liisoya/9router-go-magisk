package dashboard

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateAPIKeyExpiry(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt string
		wantErr   bool
	}{
		{name: "empty means no expiry", expiresAt: ""},
		{name: "UTC timestamp", expiresAt: "2026-12-31T23:59:59Z"},
		{name: "fractional seconds", expiresAt: "2026-12-31T23:59:59.5Z"},
		{name: "offset instead of UTC", expiresAt: "2027-01-01T00:00:00+07:00"},
		// The auth middleware parses with time.RFC3339 and skips a value it cannot
		// parse, so these used to be stored as contracts that never end.
		{name: "date without a time", expiresAt: "2026-12-31", wantErr: true},
		{name: "space-separated timestamp", expiresAt: "2026-12-31 23:59:59", wantErr: true},
		{name: "day-first timestamp", expiresAt: "31-12-2026", wantErr: true},
		{name: "not a timestamp at all", expiresAt: "never", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAPIKeyExpiry(tt.expiresAt)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateAPIKeyExpiry(%q) error = %v, wantErr %v", tt.expiresAt, err, tt.wantErr)
			}
		})
	}
}

func TestValidateAPIKeyMetadata(t *testing.T) {
	tests := []struct {
		name    string
		meta    string
		wantErr string // substring the refusal must name; empty means accepted
	}{
		{name: "empty clears the blob", meta: ""},
		{name: "empty object", meta: `{}`},
		{
			name: "full resale record",
			meta: `{"customerId":"acme","priceCents":5000,"billingCycle":"monthly","contact":"ops@acme.test","notes":"renewed by invoice"}`,
		},
		{name: "a free customer is allowed", meta: `{"customerId":"acme","priceCents":0}`},
		{name: "not JSON", meta: `{`, wantErr: "metadata must be a JSON object"},
		{name: "null is not an object", meta: `null`, wantErr: "metadata must be a JSON object"},
		{name: "array is not an object", meta: `[]`, wantErr: "metadata must be a JSON object"},
		{name: "typo in a key", meta: `{"customerID":"acme"}`, wantErr: `metadata has unknown key "customerID"`},
		{name: "unknown key lists what is accepted", meta: `{"tier":"gold"}`, wantErr: "accepted: billingCycle, contact, customerId, notes, priceCents"},
		{name: "priceCents as a string", meta: `{"priceCents":"5000"}`, wantErr: "metadata.priceCents must be a whole number of cents"},
		{name: "priceCents with a fraction", meta: `{"priceCents":5000.5}`, wantErr: "metadata.priceCents must be a whole number of cents"},
		{name: "negative priceCents", meta: `{"priceCents":-1}`, wantErr: "metadata.priceCents must be between 0 and"},
		{name: "blank customerId", meta: `{"customerId":"   "}`, wantErr: "metadata.customerId must not be empty"},
		{name: "customerId as a number", meta: `{"customerId":7}`, wantErr: "metadata.customerId must be a string"},
		{name: "unknown billing cycle", meta: `{"billingCycle":"weekly"}`, wantErr: "metadata.billingCycle must be one of one-off, monthly, quarterly, yearly"},
		{
			name:    "blob over the size cap",
			meta:    `{"notes":"` + strings.Repeat("x", maxAPIKeyMetadataBytes) + `"}`,
			wantErr: "metadata must be at most 2048 bytes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAPIKeyMetadata(tt.meta)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateAPIKeyMetadata(%q) = %v, want accepted", tt.meta, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateAPIKeyMetadata(%q) = nil, want an error naming %q", tt.meta, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("validateAPIKeyMetadata(%q) = %q, want it to name %q", tt.meta, err.Error(), tt.wantErr)
			}
		})
	}
}

// The two columns that carry a resale contract are the ones an operator can get
// wrong by typing: an expiry the middleware cannot parse used to be stored as a
// contract that never ends, and a mistyped metadata key used to be stored as
// bookkeeping nothing reads. Both are refused now, and a refused request leaves
// the row alone — including the rate limits that travelled in the same body.
func TestHandleUpdateApiKey_RejectsUnusableContractFields(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	const keyID = "key-contract"
	if err := repo.CreateApiKey(keyID, "sk-contract", "Contract", "machine-1"); err != nil {
		t.Fatalf("create api key: %v", err)
	}

	put := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/keys/"+keyID, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	rejected := []struct {
		name    string
		body    string
		wantMsg string
	}{
		{
			name:    "expiry the auth middleware cannot parse",
			body:    `{"expiresAt":"31-12-2026","rateLimitRpm":42}`,
			wantMsg: "expiresAt must be an RFC3339 timestamp",
		},
		{
			name:    "metadata that is not an object",
			body:    `{"metadata":"not json","rateLimitRpm":42}`,
			wantMsg: "metadata must be a JSON object",
		},
		{
			name:    "metadata with a key nothing reads",
			body:    `{"metadata":"{\"customerID\":\"acme\"}"}`,
			wantMsg: `unknown key "customerID"`,
		},
	}

	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			rec := put(tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("PUT %s = %d, want 400: %s", tt.body, rec.Code, rec.Body.String())
			}
			var payload struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("400 body is not JSON: %v (%s)", err, rec.Body.String())
			}
			if !strings.Contains(payload.Error.Message, tt.wantMsg) {
				t.Errorf("400 message %q does not name the reason %q", payload.Error.Message, tt.wantMsg)
			}
			stored, err := repo.GetApiKeyByID(keyID)
			if err != nil || stored == nil {
				t.Fatalf("read key back: %v", err)
			}
			if stored.ExpiresAt != nil && *stored.ExpiresAt != "" {
				t.Errorf("expiresAt = %q after a refused request, want it untouched", *stored.ExpiresAt)
			}
			if stored.Metadata != nil && *stored.Metadata != "" {
				t.Errorf("metadata = %q after a refused request, want it untouched", *stored.Metadata)
			}
			if stored.RateLimitRPM != nil && *stored.RateLimitRPM != 0 {
				t.Errorf("rateLimitRpm = %d after a refused request, want it untouched: "+
					"a refused policy must not half-apply", *stored.RateLimitRPM)
			}
		})
	}

	t.Run("a usable contract is stored", func(t *testing.T) {
		body := `{"expiresAt":"2027-06-30T00:00:00Z",` +
			`"metadata":"{\"customerId\":\"acme\",\"priceCents\":5000,\"billingCycle\":\"monthly\"}",` +
			`"rateLimitRpm":42}`
		if rec := put(body); rec.Code != http.StatusOK {
			t.Fatalf("PUT %s = %d, want 200: %s", body, rec.Code, rec.Body.String())
		}
		stored, err := repo.GetApiKeyByID(keyID)
		if err != nil || stored == nil {
			t.Fatalf("read key back: %v", err)
		}
		if stored.ExpiresAt == nil || *stored.ExpiresAt != "2027-06-30T00:00:00Z" {
			t.Errorf("expiresAt = %v, want the stored contract end", stored.ExpiresAt)
		}
		var meta map[string]any
		if stored.Metadata == nil {
			t.Fatal("metadata was not stored")
		}
		if err := json.Unmarshal([]byte(*stored.Metadata), &meta); err != nil {
			t.Fatalf("stored metadata is not JSON: %v", err)
		}
		if meta["customerId"] != "acme" || meta["priceCents"] != float64(5000) {
			t.Errorf("metadata = %v, want the customer and price that were sent", meta)
		}
		if stored.RateLimitRPM == nil || *stored.RateLimitRPM != 42 {
			t.Errorf("rateLimitRpm = %v, want the limit that travelled with the accepted policy", stored.RateLimitRPM)
		}
	})
}
