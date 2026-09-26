package oauth

import (
	"database/sql"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"9router/proxy/internal/db"
)

func setupTestDB(t *testing.T) (*sql.DB, func()) {
	tmpFile, err := os.CreateTemp("", "test_freebuff_*.sqlite")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()

	database, err := db.OpenDatabase(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("OpenDatabase failed: %v", err)
	}

	schema := `
	CREATE TABLE IF NOT EXISTS providerConnections (
		id TEXT PRIMARY KEY,
		provider TEXT NOT NULL,
		authType TEXT NOT NULL,
		name TEXT,
		email TEXT,
		priority INTEGER,
		isActive INTEGER DEFAULT 1,
		data TEXT NOT NULL,
		createdAt TEXT,
		updatedAt TEXT
	);`
	if _, err := database.Exec(schema); err != nil {
		database.Close()
		os.Remove(tmpFile.Name())
		t.Fatalf("exec schema failed: %v", err)
	}

	cleanup := func() {
		database.Close()
		os.Remove(tmpFile.Name())
	}
	return database, cleanup
}

func TestHandleFreebuffInitiate_Success(t *testing.T) {
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/freebuff/initiate", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleFreebuffInitiate(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}

	loginURL, _ := res["loginUrl"].(string)
	if !strings.HasPrefix(loginURL, "https://freebuff.com/login?auth_code=") {
		t.Errorf("unexpected loginUrl: %v", loginURL)
	}
	authCode, _ := res["authCode"].(string)
	if len(authCode) == 0 {
		t.Errorf("empty authCode")
	}
	fpID, _ := res["fingerprintId"].(string)
	if len(fpID) != 36 {
		t.Errorf("unexpected fingerprintId length: %v", fpID)
	}
	fpHash, _ := res["fingerprintHash"].(string)
	if len(fpHash) != 64 {
		t.Errorf("unexpected fingerprintHash length: %v", fpHash)
	}
	if res["expiresAt"] == "" {
		t.Errorf("empty expiresAt")
	}
}

func TestHandleFreebuffInitiate_MethodNotAllowed(t *testing.T) {
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/oauth/freebuff/initiate", nil)
	rec := httptest.NewRecorder()

	handler.HandleFreebuffInitiate(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestHandleFreebuffPoll_MissingFields(t *testing.T) {
	handler := NewOAuthHandler(nil)

	// Missing fingerprintHash
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/freebuff/poll", strings.NewReader(`{"fingerprintId":"fp-1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.HandleFreebuffPoll(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}

	// Empty body
	req2 := httptest.NewRequest(http.MethodPost, "/api/oauth/freebuff/poll", strings.NewReader(`{}`))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	handler.HandleFreebuffPoll(rec2, req2)

	if rec2.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec2.Code)
	}
}

func TestHandleFreebuffPoll_Pending(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/cli/status" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "pending"}`))
	}))
	defer mockServer.Close()

	oldURL := freebuffAuthBaseURL
	freebuffAuthBaseURL = mockServer.URL
	defer func() { freebuffAuthBaseURL = oldURL }()

	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/freebuff/poll", strings.NewReader(`{"fingerprintId":"fp-1","fingerprintHash":"hash-1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleFreebuffPoll(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if res["status"] != "pending" {
		t.Errorf("expected status 'pending', got %v", res["status"])
	}
	if res["connectionId"] != nil {
		t.Errorf("expected connectionId to be empty for pending, got %v", res["connectionId"])
	}
}

func TestHandleFreebuffPoll_Expired(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "expired"}`))
	}))
	defer mockServer.Close()

	oldURL := freebuffAuthBaseURL
	freebuffAuthBaseURL = mockServer.URL
	defer func() { freebuffAuthBaseURL = oldURL }()

	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/freebuff/poll", strings.NewReader(`{"fingerprintId":"fp-1","fingerprintHash":"hash-1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleFreebuffPoll(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if res["status"] != "expired" {
		t.Errorf("expected status 'expired', got %v", res["status"])
	}
}

func TestHandleFreebuffPoll_Authorized_CreatesConnection(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	testAuthToken := "fb_token_live_123456789abcdef"
	expectedConnID := "fb-" + shortHash(testAuthToken)

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/cli/status" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"status": "authorized",
			"authToken": "` + testAuthToken + `",
			"email": "user@freebuff.com",
			"name": "Freebuff Master"
		}`))
	}))
	defer mockServer.Close()

	oldURL := freebuffAuthBaseURL
	freebuffAuthBaseURL = mockServer.URL
	defer func() { freebuffAuthBaseURL = oldURL }()

	handler := NewOAuthHandler(repo)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/freebuff/poll", strings.NewReader(`{
		"fingerprintId": "fp-valid",
		"fingerprintHash": "hash-valid"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleFreebuffPoll(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}

	if res["status"] != "authorized" {
		t.Errorf("expected status 'authorized', got %v", res["status"])
	}
	if res["connectionId"] != expectedConnID {
		t.Errorf("expected connectionId %s, got %v", expectedConnID, res["connectionId"])
	}

	// Verify database record
	var provider, authType, name, data string
	var isActive int
	err := database.QueryRow(
		"SELECT provider, authType, name, isActive, data FROM providerConnections WHERE id = ?",
		expectedConnID,
	).Scan(&provider, &authType, &name, &isActive, &data)
	if err != nil {
		t.Fatalf("failed to query providerConnections: %v", err)
	}

	if provider != "freebuff" {
		t.Errorf("expected provider 'freebuff', got %s", provider)
	}
	if authType != "oauth" {
		t.Errorf("expected authType 'oauth', got %s", authType)
	}
	if name != "user@freebuff.com" {
		t.Errorf("expected name user@freebuff.com (email-first), got %s", name)
	}
	if isActive != 1 {
		t.Errorf("expected isActive 1, got %d", isActive)
	}

	var dataMap map[string]any
	if err := json.Unmarshal([]byte(data), &dataMap); err != nil {
		t.Fatalf("failed to parse connection data: %v", err)
	}
	if dataMap["authToken"] != testAuthToken {
		t.Errorf("expected authToken %s, got %v", testAuthToken, dataMap["authToken"])
	}
	// client_id cloaking: the request's fingerprintId must be stored per
	// account so chat requests can reuse it as codebuff_metadata.client_id.
	psd, _ := dataMap["providerSpecificData"].(map[string]any)
	if psd == nil || psd["fingerprintId"] != "fp-valid" {
		t.Errorf("expected providerSpecificData.fingerprintId 'fp-valid', got %v", dataMap["providerSpecificData"])
	}
}

func TestHandleFreebuffPoll_Authorized_UpdatesExisting(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	testAuthToken := "fb_token_update_test"
	connID := "fb-" + shortHash(testAuthToken)

	// Seed existing connection
	_, err := database.Exec(
		`INSERT INTO providerConnections (id, provider, authType, name, isActive, data, createdAt, updatedAt) VALUES (?, 'freebuff', 'oauth', 'Old Name', 1, '{}', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z')`,
		connID,
	)
	if err != nil {
		t.Fatalf("failed to seed connection: %v", err)
	}

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"status": "authorized",
			"authToken": "` + testAuthToken + `",
			"name": "Updated Freebuff Name",
			"email": "updated@freebuff.com"
		}`))
	}))
	defer mockServer.Close()

	oldURL := freebuffAuthBaseURL
	freebuffAuthBaseURL = mockServer.URL
	defer func() { freebuffAuthBaseURL = oldURL }()

	handler := NewOAuthHandler(repo)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/freebuff/poll", strings.NewReader(`{
		"fingerprint_id": "fp-snake",
		"fingerprint_hash": "hash-snake"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleFreebuffPoll(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify database record was updated, not duplicated
	var count int
	_ = database.QueryRow("SELECT count(*) FROM providerConnections WHERE id = ?", connID).Scan(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 connection, got %d", count)
	}

	var name, data string
	err = database.QueryRow("SELECT name, data FROM providerConnections WHERE id = ?", connID).Scan(&name, &data)
	if err != nil {
		t.Fatalf("failed to read updated row: %v", err)
	}
	if name != "updated@freebuff.com" {
		t.Errorf("expected name updated@freebuff.com (email-first), got %s", name)
	}
	var dataMap map[string]any
	_ = json.Unmarshal([]byte(data), &dataMap)
	if dataMap["email"] != "updated@freebuff.com" {
		t.Errorf("expected email 'updated@freebuff.com', got %v", dataMap["email"])
	}
}

func TestHandleFreebuffPoll_Authorized_NestedUser(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	testAuthToken := "fb_nested_token_abcdef123456"
	expectedConnID := "fb-" + shortHash(testAuthToken)

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/cli/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Mirrors the upstream CLI contract (cli/src/login/login-flow.ts): the
		// credentials are nested under `user`, not at the top level.
		_, _ = w.Write([]byte(`{
			"user": {
				"id": "user_123",
				"name": "Nested Freebuff User",
				"email": "nested@freebuff.com",
				"authToken": "` + testAuthToken + `"
			}
		}`))
	}))
	defer mockServer.Close()

	oldURL := freebuffAuthBaseURL
	freebuffAuthBaseURL = mockServer.URL
	defer func() { freebuffAuthBaseURL = oldURL }()

	handler := NewOAuthHandler(repo)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/freebuff/poll", strings.NewReader(`{
		"fingerprintId": "fp-nested",
		"fingerprintHash": "hash-nested"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleFreebuffPoll(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if res["status"] != "authorized" {
		t.Fatalf("expected status 'authorized', got %v", res["status"])
	}
	if res["connectionId"] != expectedConnID {
		t.Errorf("expected connectionId %s, got %v", expectedConnID, res["connectionId"])
	}

	var name, data string
	err := database.QueryRow(
		"SELECT name, data FROM providerConnections WHERE id = ?", expectedConnID,
	).Scan(&name, &data)
	if err != nil {
		t.Fatalf("failed to query providerConnections: %v", err)
	}
	if name != "nested@freebuff.com" {
		t.Errorf("expected name nested@freebuff.com (email-first), got %s", name)
	}

	var dataMap map[string]any
	if err := json.Unmarshal([]byte(data), &dataMap); err != nil {
		t.Fatalf("failed to parse connection data: %v", err)
	}
	if dataMap["authToken"] != testAuthToken {
		t.Errorf("expected authToken %s, got %v", testAuthToken, dataMap["authToken"])
	}
	if dataMap["email"] != "nested@freebuff.com" {
		t.Errorf("expected email 'nested@freebuff.com', got %v", dataMap["email"])
	}
	if dataMap["userId"] != "user_123" {
		t.Errorf("expected userId 'user_123', got %v", dataMap["userId"])
	}
}

func TestHandleFreebuffPoll_UserWithoutToken_StaysPending(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"user": {"name": "No Token"}}`))
	}))
	defer mockServer.Close()

	oldURL := freebuffAuthBaseURL
	freebuffAuthBaseURL = mockServer.URL
	defer func() { freebuffAuthBaseURL = oldURL }()

	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/freebuff/poll", strings.NewReader(`{
		"fingerprintId": "fp-no-token",
		"fingerprintHash": "hash-no-token"
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleFreebuffPoll(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var res map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if res["status"] != "pending" {
		t.Errorf("expected status 'pending', got %v", res["status"])
	}
	if res["connectionId"] != nil {
		t.Errorf("expected no connectionId, got %v", res["connectionId"])
	}
}

func TestHandleAntigravityAuthorize(t *testing.T) {
	handler := NewOAuthHandler(nil)
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/oauth/antigravity/authorize?state=custom_state_123&redirect_uri=http%3A%2F%2Flocalhost%3A20130%2Fcallback",
		nil,
	)
	rec := httptest.NewRecorder()
	handler.HandleAntigravityAuthorize(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var res struct {
		URL         string `json:"url"`
		RedirectURI string `json:"redirectUri"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	authURL, err := url.Parse(res.URL)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	if authURL.Host != "accounts.google.com" {
		t.Errorf("auth URL host = %q", authURL.Host)
	}
	if got := authURL.Query().Get("redirect_uri"); got != "http://localhost:20130/callback" {
		t.Errorf("redirect_uri = %q", got)
	}
	if res.RedirectURI != "http://localhost:20130/callback" {
		t.Errorf("response redirectUri = %q", res.RedirectURI)
	}
	if authURL.Query().Get("state") != "custom_state_123" {
		t.Errorf("state = %q", authURL.Query().Get("state"))
	}
	if authURL.Query().Get("access_type") != "offline" {
		t.Errorf("access_type = %q", authURL.Query().Get("access_type"))
	}
	expectedScopes := []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/userinfo.profile",
		"https://www.googleapis.com/auth/cclog",
		"https://www.googleapis.com/auth/experimentsandconfigs",
	}
	if got := authURL.Query().Get("scope"); got != strings.Join(expectedScopes, " ") {
		t.Errorf("scope = %q", got)
	}

	reqRedirect := httptest.NewRequest(http.MethodGet, "/api/oauth/antigravity/authorize?redirect=true", nil)
	recRedirect := httptest.NewRecorder()
	handler.HandleAntigravityAuthorize(recRedirect, reqRedirect)
	if recRedirect.Code != http.StatusFound {
		t.Errorf("expected 302 redirect, got %d", recRedirect.Code)
	}
}

func TestGetAntigravityRedirectURI(t *testing.T) {
	tests := []struct {
		name     string
		request  *http.Request
		expected string
	}{
		{
			name:     "explicit loopback callback wins",
			request:  httptest.NewRequest(http.MethodGet, "/?redirect_uri=http%3A%2F%2Flocalhost%3A20130%2Fcallback", nil),
			expected: "http://localhost:20130/callback",
		},
		{
			name:     "invalid callback falls back upstream default",
			request:  httptest.NewRequest(http.MethodGet, "/", nil),
			expected: "http://localhost:8080/callback",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := getAntigravityRedirectURI(test.request); got != test.expected {
				t.Fatalf("getAntigravityRedirectURI() = %q, expected %q", got, test.expected)
			}
		})
	}
}

func TestHandleAntigravityExchangeErrors(t *testing.T) {
	handler := NewOAuthHandler(nil)

	tests := []struct {
		name         string
		method       string
		body         string
		expectedCode int
	}{
		{name: "method must be post", method: http.MethodGet, expectedCode: http.StatusMethodNotAllowed},
		{name: "missing code", method: http.MethodPost, body: `{"redirectUri":"http://localhost:20130/callback"}`, expectedCode: http.StatusBadRequest},
		{name: "missing redirect uri", method: http.MethodPost, body: `{"code":"code-123"}`, expectedCode: http.StatusBadRequest},
		{name: "invalid redirect uri", method: http.MethodPost, body: `{"code":"code-123","redirectUri":"javascript:alert(1)"}`, expectedCode: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(test.method, "/api/oauth/antigravity/exchange", strings.NewReader(test.body))
			rec := httptest.NewRecorder()
			handler.HandleAntigravityExchange(rec, req)
			if rec.Code != test.expectedCode {
				t.Fatalf("status = %d, expected %d: %s", rec.Code, test.expectedCode, rec.Body.String())
			}
		})
	}
}

func TestHandleAntigravityExchangeSuccess(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)
	const redirectURI = "http://localhost:20130/callback"

	mockGoogleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		if got := r.Form.Get("redirect_uri"); got != redirectURI {
			http.Error(w, "redirect mismatch", http.StatusBadRequest)
			return
		}
		if got := r.Form.Get("code"); got != "valid_google_code_123" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}

		// Simple mock JWT id_token with claims: {"email":"antigravity-user@gmail.com"}
		// Header: {"alg":"none"} -> eyJhbGciOiJub25lIn0
		// Payload: {"email":"antigravity-user@gmail.com"} -> eyJlbWFpbCI6ImFudGlncmF2aXR5LXVzZXJAZ21haWwuY29tIn0
		mockIDToken := "eyJhbGciOiJub25lIn0.eyJlbWFpbCI6ImFudGlncmF2aXR5LXVzZXJAZ21haWwuY29tIn0."

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"access_token": "ya29.mock_access_token_xyz",
			"refresh_token": "1//mock_refresh_token_uvw",
			"expires_in": 3600,
			"token_type": "Bearer",
			"id_token": "` + mockIDToken + `",
			"scope": "https://www.googleapis.com/auth/cloud-platform"
		}`))
	}))
	defer mockGoogleServer.Close()

	oldTokenURL := googleOAuthTokenURL
	googleOAuthTokenURL = mockGoogleServer.URL + "/token"
	defer func() { googleOAuthTokenURL = oldTokenURL }()

	handler := NewOAuthHandler(repo)
	body := `{"code":"valid_google_code_123","redirectUri":"` + redirectURI + `","state":"state-123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/oauth/antigravity/exchange", strings.NewReader(body))
	rec := httptest.NewRecorder()

	handler.HandleAntigravityExchange(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var res struct {
		Success    bool `json:"success"`
		Connection struct {
			ID       string `json:"id"`
			Provider string `json:"provider"`
			Email    string `json:"email"`
		} `json:"connection"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if !res.Success {
		t.Error("expected success response")
	}
	if res.Connection.Provider != "antigravity" {
		t.Errorf("expected provider 'antigravity', got %q", res.Connection.Provider)
	}
	if res.Connection.Email != "antigravity-user@gmail.com" {
		t.Errorf("expected email 'antigravity-user@gmail.com', got %q", res.Connection.Email)
	}
	connID := res.Connection.ID
	if connID == "" {
		t.Fatalf("missing connection id in response: %s", rec.Body.String())
	}

	// Verify DB record
	var provider, authType, name, data string
	var isActive int
	err := database.QueryRow(
		"SELECT provider, authType, name, isActive, data FROM providerConnections WHERE id = ?",
		connID,
	).Scan(&provider, &authType, &name, &isActive, &data)
	if err != nil {
		t.Fatalf("failed to query providerConnections: %v", err)
	}

	if provider != "antigravity" {
		t.Errorf("expected provider 'antigravity', got %s", provider)
	}
	if authType != "oauth" {
		t.Errorf("expected authType 'oauth', got %s", authType)
	}
	if !strings.Contains(name, "antigravity-user@gmail.com") {
		t.Errorf("expected name to contain email, got %s", name)
	}
	if isActive != 1 {
		t.Errorf("expected isActive 1, got %d", isActive)
	}

	var dataMap map[string]any
	if err := json.Unmarshal([]byte(data), &dataMap); err != nil {
		t.Fatalf("failed to unmarshal stored data: %v", err)
	}
	if dataMap["accessToken"] != "ya29.mock_access_token_xyz" {
		t.Errorf("expected accessToken, got %v", dataMap["accessToken"])
	}
	if dataMap["refreshToken"] != "1//mock_refresh_token_uvw" {
		t.Errorf("expected refreshToken, got %v", dataMap["refreshToken"])
	}
	if dataMap["email"] != "antigravity-user@gmail.com" {
		t.Errorf("expected email 'antigravity-user@gmail.com', got %v", dataMap["email"])
	}
}
