package oauth

import (
	"database/sql"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
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
	if err := db.EnsureAdditiveColumns(database); err != nil {
		database.Close()
		os.Remove(tmpFile.Name())
		t.Fatalf("additive columns: %v", err)
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
