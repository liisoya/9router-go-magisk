package executor

import (
	"strings"
	"testing"
)

// The chat request signs with sigPath "/api/v2/service/pro/sse/agent_chat_generation"
// (leading "/algo" stripped) — this used to be hardcoded and must stay identical
// now that it is derived from the request URL.
func TestQoderCosySigPath(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://api3.qoder.sh/algo/api/v2/service/pro/sse/agent_chat_generation", "/api/v2/service/pro/sse/agent_chat_generation"},
		{"https://api2.qoder.sh/algo/api/v2/model/list", "/api/v2/model/list"},
		{"https://api3.qoder.sh/api/v2/model/list", "/api/v2/model/list"},
	}
	for _, tc := range cases {
		if got := qoderCosySigPath(tc.url); got != tc.want {
			t.Errorf("qoderCosySigPath(%s) = %q, want %q", tc.url, got, tc.want)
		}
	}
}

func TestBuildQoderCosyHeaders_SignsRequestedPath(t *testing.T) {
	const modelListURL = "https://api2.qoder.sh/algo/api/v2/model/list"
	headers, err := BuildQoderCosyHeaders(nil, modelListURL, QoderCosyCreds{UserID: "user-1", AuthToken: "jt-abc"})
	if err != nil {
		t.Fatalf("BuildQoderCosyHeaders: %v", err)
	}
	if got := headers["Cosy-Sigpath"]; got != "/api/v2/model/list" {
		t.Errorf("Cosy-Sigpath = %q, want /api/v2/model/list", got)
	}
	if got := headers["Cosy-User"]; got != "user-1" {
		t.Errorf("Cosy-User = %q, want user-1", got)
	}
	if got := headers["Authorization"]; !strings.HasPrefix(got, "Bearer COSY.") {
		t.Errorf("Authorization = %q, want a COSY bearer header", got)
	}
	if headers["Cosy-Key"] == "" || headers["Cosy-Date"] == "" {
		t.Errorf("missing COSY key/date headers: %v", headers)
	}
}

// A fabricated user id is what made every Qoder request fail with
// 403 {"code":"105","message":"Login expired"}: the signature is verified
// against the real account, so a placeholder can never be accepted.
func TestBuildQoderCosyHeaders_RequiresRealUserID(t *testing.T) {
	const url = "https://api2.qoder.sh/algo/api/v2/model/list"

	if _, err := BuildQoderCosyHeaders(nil, url, QoderCosyCreds{AuthToken: "dt-abc"}); err == nil {
		t.Error("expected an error when the connection has no userId")
	}
	if _, err := BuildQoderCosyHeaders(nil, url, QoderCosyCreds{UserID: "user-1"}); err == nil {
		t.Error("expected an error when the connection has no auth token")
	}
	if _, err := BuildQoderCosyHeaders(nil, url, QoderCosyCreds{UserID: "user-1", AuthToken: "dt-abc"}); err != nil {
		t.Errorf("unexpected error for a complete credential: %v", err)
	}
}

func TestQoderCosyCreds_ReadsConnectionIdentity(t *testing.T) {
	psd := map[string]any{
		"authMethod": "device",
		"userId":     "019f951a-cacd",
		"machineId":  "machine-uuid",
		"name":       "Luqmanul Hakim",
		"email":      "n.loekman@gmail.com",
	}
	creds := qoderCosyCreds(psd, "dt-token")
	if creds.UserID != "019f951a-cacd" {
		t.Errorf("UserID = %q", creds.UserID)
	}
	if creds.MachineID != "machine-uuid" {
		t.Errorf("MachineID = %q", creds.MachineID)
	}
	if creds.AuthToken != "dt-token" {
		t.Errorf("AuthToken = %q", creds.AuthToken)
	}
	if creds.Name != "Luqmanul Hakim" {
		t.Errorf("Name = %q", creds.Name)
	}
}

// A connection without a stored userId must fail loudly rather than sign with
// a made-up account.
func TestQoderCosyCreds_MissingUserIDStaysEmpty(t *testing.T) {
	if got := qoderCosyCreds(map[string]any{"authMethod": "device"}, "dt-token"); got.UserID != "" {
		t.Errorf("UserID = %q, want empty so signing fails loudly", got.UserID)
	}
}
