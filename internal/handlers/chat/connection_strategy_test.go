package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"strings"

	"9router/proxy/internal/db"
	"9router/proxy/internal/models"
)

func TestApplyConnectionStrategy_RoundRobin(t *testing.T) {
	h := NewChatHandler(nil)

	c1 := &models.ProviderConnection{ID: "conn-1"}
	c2 := &models.ProviderConnection{ID: "conn-2"}
	c3 := &models.ProviderConnection{ID: "conn-3"}
	conns := []*models.ProviderConnection{c1, c2, c3}

	strat := db.ProviderStrategy{RotateStrategy: "round-robin"}

	// 1st request -> c1
	r1 := h.ApplyConnectionStrategy(conns, strat)
	if r1[0].ID != "conn-1" {
		t.Fatalf("call 1: expected conn-1, got %s", r1[0].ID)
	}

	// 2nd request -> c2
	r2 := h.ApplyConnectionStrategy(conns, strat)
	if r2[0].ID != "conn-2" {
		t.Fatalf("call 2: expected conn-2, got %s", r2[0].ID)
	}

	// 3rd request -> c3
	r3 := h.ApplyConnectionStrategy(conns, strat)
	if r3[0].ID != "conn-3" {
		t.Fatalf("call 3: expected conn-3, got %s", r3[0].ID)
	}

	// 4th request -> c1 (wrapped)
	r4 := h.ApplyConnectionStrategy(conns, strat)
	if r4[0].ID != "conn-1" {
		t.Fatalf("call 4: expected conn-1, got %s", r4[0].ID)
	}
}

func TestApplyConnectionStrategy_Sticky(t *testing.T) {
	h := NewChatHandler(nil)

	c1 := &models.ProviderConnection{ID: "conn-1"}
	c2 := &models.ProviderConnection{ID: "conn-2"}
	conns := []*models.ProviderConnection{c1, c2}

	strat := db.ProviderStrategy{
		RotateStrategy: "sticky",
		StickyLimit:    3,
	}

	// First 3 calls should stay on conn-1
	for i := range 3 {
		r := h.ApplyConnectionStrategy(conns, strat)
		if r[0].ID != "conn-1" {
			t.Fatalf("call %d: expected conn-1 (sticky), got %s", i+1, r[0].ID)
		}
	}

	// 4th call rotates to conn-2
	r4 := h.ApplyConnectionStrategy(conns, strat)
	if r4[0].ID != "conn-2" {
		t.Fatalf("call 4: expected conn-2 after sticky limit, got %s", r4[0].ID)
	}

	// Next 2 calls also stay on conn-2 (total 3 on conn-2)
	for i := range 2 {
		r := h.ApplyConnectionStrategy(conns, strat)
		if r[0].ID != "conn-2" {
			t.Fatalf("call %d on conn-2: expected conn-2, got %s", i+2, r[0].ID)
		}
	}

	// Rotates back to conn-1
	r7 := h.ApplyConnectionStrategy(conns, strat)
	if r7[0].ID != "conn-1" {
		t.Fatalf("call 7: expected conn-1, got %s", r7[0].ID)
	}
}

func TestGetBestConnection_WithRoundRobinStrategy(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	// Seed 3 active connections for antigravity
	seedConnDB(t, database, "antigravity", "conn-ag-1", "tok-1", "https://mock.example.com/1")
	seedConnDB(t, database, "antigravity", "conn-ag-2", "tok-2", "https://mock.example.com/2")
	seedConnDB(t, database, "antigravity", "conn-ag-3", "tok-3", "https://mock.example.com/3")

	// Set round-robin strategy for antigravity
	if err := repo.SetProviderStrategy("antigravity", db.ProviderStrategy{
		RotateStrategy: "round-robin",
	}); err != nil {
		t.Fatalf("SetProviderStrategy: %v", err)
	}

	// Verify rotation through GetBestConnection
	conn1, _, err := h.GetBestConnection("antigravity", "", nil, "")
	if err != nil || conn1 == nil || conn1.ID != "conn-ag-1" {
		t.Fatalf("step 1: expected conn-ag-1, got %v (err=%v)", conn1, err)
	}

	conn2, _, err := h.GetBestConnection("antigravity", "", nil, "")
	if err != nil || conn2 == nil || conn2.ID != "conn-ag-2" {
		t.Fatalf("step 2: expected conn-ag-2, got %v (err=%v)", conn2, err)
	}

	conn3, _, err := h.GetBestConnection("antigravity", "", nil, "")
	if err != nil || conn3 == nil || conn3.ID != "conn-ag-3" {
		t.Fatalf("step 3: expected conn-ag-3, got %v (err=%v)", conn3, err)
	}

	conn4, _, err := h.GetBestConnection("antigravity", "", nil, "")
	if err != nil || conn4 == nil || conn4.ID != "conn-ag-1" {
		t.Fatalf("step 4: expected conn-ag-1, got %v (err=%v)", conn4, err)
	}
}

func TestHandleAccountFallback_RotatesUpstreamRequests(t *testing.T) {
	var hits1, hits2 atomic.Int32

	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits1.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"srv1"}}]}`))
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits2.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"srv2"}}]}`))
	}))
	defer srv2.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	// Clear pre-seeded deepseek connection
	if _, err := database.Exec(`DELETE FROM providerConnections WHERE id IN ('conn-1', 'conn-2')`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}

	seedConnDB(t, database, "deepseek", "conn-ds-1", "sk-1", srv1.URL)
	seedConnDB(t, database, "deepseek", "conn-ds-2", "sk-2", srv2.URL)
	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	// Enable round-robin strategy on deepseek
	if err := repo.SetProviderStrategy("deepseek", db.ProviderStrategy{
		RotateStrategy: "round-robin",
	}); err != nil {
		t.Fatalf("SetProviderStrategy: %v", err)
	}

	body := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)

	// 1st request -> srv1
	rec1 := httptest.NewRecorder()
	if err := h.handleAccountFallback(context.Background(), rec1, "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions"); err != nil {
		t.Fatalf("request 1: %v", err)
	}
	if hits1.Load() != 1 || hits2.Load() != 0 {
		t.Errorf("after req 1: hits1=%d, hits2=%d (want 1, 0)", hits1.Load(), hits2.Load())
	}

	// 2nd request -> srv2 (rotated!)
	rec2 := httptest.NewRecorder()
	if err := h.handleAccountFallback(context.Background(), rec2, "deepseek", "deepseek-chat", "", body, false, false, "/v1/chat/completions"); err != nil {
		t.Fatalf("request 2: %v", err)
	}
	if hits1.Load() != 1 || hits2.Load() != 1 {
		t.Errorf("after req 2: hits1=%d, hits2=%d (want 1, 1)", hits1.Load(), hits2.Load())
	}
}

func TestGetBestConnection_DashboardFallbackStrategyKey(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	// Clear seeded connections
	if _, err := database.Exec(`DELETE FROM providerConnections WHERE id IN ('conn-1', 'conn-2')`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}

	seedConnDB(t, database, "deepseek", "conn-ds-1", "sk-1", "https://mock.example.com/1")
	seedConnDB(t, database, "deepseek", "conn-ds-2", "sk-2", "https://mock.example.com/2")

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	// Write raw settings JSON matching Next.js / Svelte 5 dashboard
	settingsJSON := `{
		"providerStrategies": {
			"deepseek": {
				"fallbackStrategy": "round-robin",
				"stickyRoundRobinLimit": 2
			}
		}
	}`
	if _, err := database.Exec(`INSERT INTO settings (id, data) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`, settingsJSON); err != nil {
		t.Fatalf("failed to insert settings: %v", err)
	}

	// Call 1 -> conn-ds-1
	c1, _, err := h.GetBestConnection("deepseek", "", nil, "")
	if err != nil || c1.ID != "conn-ds-1" {
		t.Fatalf("call 1: expected conn-ds-1, got %v (err=%v)", c1, err)
	}

	// Call 2 -> conn-ds-1 (sticky limit 2)
	c2, _, err := h.GetBestConnection("deepseek", "", nil, "")
	if err != nil || c2.ID != "conn-ds-1" {
		t.Fatalf("call 2: expected conn-ds-1 (sticky), got %v (err=%v)", c2, err)
	}

	// Call 3 -> rotates to conn-ds-2
	c3, _, err := h.GetBestConnection("deepseek", "", nil, "")
	if err != nil || c3.ID != "conn-ds-2" {
		t.Fatalf("call 3: expected conn-ds-2, got %v (err=%v)", c3, err)
	}
}

func TestGetBestConnection_GlobalFallbackStrategy(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	if _, err := database.Exec(`DELETE FROM providerConnections WHERE id IN ('conn-1', 'conn-2')`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}

	seedConnDB(t, database, "deepseek", "conn-ds-1", "sk-1", "https://mock.example.com/1")
	seedConnDB(t, database, "deepseek", "conn-ds-2", "sk-2", "https://mock.example.com/2")

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	// Global fallbackStrategy = "round-robin", stickyRoundRobinLimit = 1
	settingsJSON := `{
		"fallbackStrategy": "round-robin",
		"stickyRoundRobinLimit": 1
	}`
	if _, err := database.Exec(`INSERT INTO settings (id, data) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`, settingsJSON); err != nil {
		t.Fatalf("failed to insert settings: %v", err)
	}

	c1, _, err := h.GetBestConnection("deepseek", "", nil, "")
	if err != nil || c1.ID != "conn-ds-1" {
		t.Fatalf("call 1: expected conn-ds-1, got %v (err=%v)", c1, err)
	}

	c2, _, err := h.GetBestConnection("deepseek", "", nil, "")
	if err != nil || c2.ID != "conn-ds-2" {
		t.Fatalf("call 2: expected conn-ds-2 (rotated), got %v (err=%v)", c2, err)
	}

	c3, _, err := h.GetBestConnection("deepseek", "", nil, "")
	if err != nil || c3.ID != "conn-ds-1" {
		t.Fatalf("call 3: expected conn-ds-1 (wrapped), got %v (err=%v)", c3, err)
	}
}

func TestComboResolution_FromDashboardSettings(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	// Seed a combo in the combos table (without strategy column)
	if _, err := database.Exec(`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES
		('c-rr', 'round-robin-combo', 'fallback', '["deepseek/deepseek-chat","groq/llama-3-70b"]', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatalf("insert combo: %v", err)
	}

	// Write combo routing strategy in settings table as written by dashboard
	settingsJSON := `{
		"comboStrategies": {
			"round-robin-combo": {
				"fallbackStrategy": "round-robin",
				"stickyRoundRobinLimit": 3,
				"judgeModel": "openai/gpt-4o"
			}
		}
	}`
	if _, err := database.Exec(`INSERT INTO settings (id, data) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`, settingsJSON); err != nil {
		t.Fatalf("insert settings: %v", err)
	}

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	// 1. Check Repo.GetComboByName resolves strategy from settings
	combo, err := repo.GetComboByName("round-robin-combo")
	if err != nil || combo == nil {
		t.Fatalf("GetComboByName failed: %v", err)
	}
	if combo.Strategy != "round-robin" {
		t.Errorf("expected combo.Strategy 'round-robin', got %q", combo.Strategy)
	}

	// 2. Check ChatHandler.ResolveModel resolves Strategy, StickyLimit, and JudgeModel
	info, err := h.ResolveModel("round-robin-combo")
	if err != nil || info == nil {
		t.Fatalf("ResolveModel failed: %v", err)
	}
	if info.Strategy != "round-robin" {
		t.Errorf("expected info.Strategy 'round-robin', got %q", info.Strategy)
	}
	if info.StickyLimit != 3 {
		t.Errorf("expected info.StickyLimit 3, got %d", info.StickyLimit)
	}
	if info.JudgeModel != "openai/gpt-4o" {
		t.Errorf("expected info.JudgeModel 'openai/gpt-4o', got %q", info.JudgeModel)
	}
}

func TestComboExecution_RoundRobinRotation(t *testing.T) {
	h := NewChatHandler(nil)
	models := []string{"deepseek/deepseek-chat", "groq/llama-3-70b", "openai/gpt-4o"}

	// 1st turn: first model is deepseek
	r1 := h.ApplyComboStrategy("round-robin", models, "my-combo", 1)
	if r1[0] != "deepseek/deepseek-chat" {
		t.Errorf("call 1: expected deepseek, got %s", r1[0])
	}

	// 2nd turn: rotates to groq
	r2 := h.ApplyComboStrategy("round-robin", models, "my-combo", 1)
	if r2[0] != "groq/llama-3-70b" {
		t.Errorf("call 2: expected groq, got %s", r2[0])
	}

	// 3rd turn: rotates to openai
	r3 := h.ApplyComboStrategy("round-robin", models, "my-combo", 1)
	if r3[0] != "openai/gpt-4o" {
		t.Errorf("call 3: expected openai, got %s", r3[0])
	}

	// 4th turn: wraps back to deepseek
	r4 := h.ApplyComboStrategy("round-robin", models, "my-combo", 1)
	if r4[0] != "deepseek/deepseek-chat" {
		t.Errorf("call 4: expected deepseek, got %s", r4[0])
	}
}

func TestEndToEnd_RoundRobin_ComboAndProviderViaHTTP(t *testing.T) {
	// 1. Setup mock upstreams for combo models
	var hitsA, hitsB atomic.Int32
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsA.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"a","choices":[{"message":{"content":"response from model A"}}]}`))
	}))
	defer srvA.Close()

	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsB.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"b","choices":[{"message":{"content":"response from model B"}}]}`))
	}))
	defer srvB.Close()

	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	// Clear seeded connections
	if _, err := database.Exec(`DELETE FROM providerConnections WHERE id IN ('conn-1', 'conn-2')`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}

	// Seed connections for providerA and providerB
	seedConnDB(t, database, "provA", "conn-a", "key-a", srvA.URL)
	seedConnDB(t, database, "provB", "conn-b", "key-b", srvB.URL)

	// Seed combo in combos table
	if _, err := database.Exec(`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES
		('c-rr-test', 'combo-e2e', 'fallback', '["provA/model-a","provB/model-b"]', '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`); err != nil {
		t.Fatalf("insert combo: %v", err)
	}

	// Configure combo routing to round-robin via settings table (dashboard key format from Issue #20)
	settingsJSON := `{
		"comboStrategies": {
			"combo-e2e": {
				"fallbackStrategy": "round-robin",
				"stickyRoundRobinLimit": 1
			}
		}
	}`
	if _, err := database.Exec(`INSERT INTO settings (id, data) VALUES (1, ?) ON CONFLICT(id) DO UPDATE SET data = excluded.data`, settingsJSON); err != nil {
		t.Fatalf("insert settings: %v", err)
	}

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)

	reqBody := `{"model":"combo-e2e","messages":[{"role":"user","content":"hi"}]}`

	// Request 1 -> Model A
	rec1 := httptest.NewRecorder()
	h.HandleChatCompletions(rec1, httptest.NewRequest("POST", "/chat/completions", strings.NewReader(reqBody)))
	if rec1.Code != http.StatusOK {
		t.Fatalf("request 1 failed: code=%d body=%s", rec1.Code, rec1.Body.String())
	}
	if !strings.Contains(rec1.Body.String(), "response from model A") {
		t.Errorf("req 1: expected response from model A, got: %s", rec1.Body.String())
	}

	// Request 2 -> Model B (Rotated!)
	rec2 := httptest.NewRecorder()
	h.HandleChatCompletions(rec2, httptest.NewRequest("POST", "/chat/completions", strings.NewReader(reqBody)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("request 2 failed: code=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(rec2.Body.String(), "response from model B") {
		t.Errorf("req 2: expected response from model B, got: %s", rec2.Body.String())
	}

	// Request 3 -> Model A (Wrapped!)
	rec3 := httptest.NewRecorder()
	h.HandleChatCompletions(rec3, httptest.NewRequest("POST", "/chat/completions", strings.NewReader(reqBody)))
	if rec3.Code != http.StatusOK {
		t.Fatalf("request 3 failed: code=%d body=%s", rec3.Code, rec3.Body.String())
	}
	if !strings.Contains(rec3.Body.String(), "response from model A") {
		t.Errorf("req 3: expected response from model A, got: %s", rec3.Body.String())
	}

	if hitsA.Load() != 2 || hitsB.Load() != 1 {
		t.Errorf("expected 2 hits on A and 1 hit on B, got A=%d B=%d", hitsA.Load(), hitsB.Load())
	}

	// 2. Setup mock upstreams for provider accounts
	var hitsAcc1, hitsAcc2 atomic.Int32
	srvAcc1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsAcc1.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"acc1","choices":[{"message":{"content":"response from account 1"}}]}`))
	}))
	defer srvAcc1.Close()

	srvAcc2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitsAcc2.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"acc2","choices":[{"message":{"content":"response from account 2"}}]}`))
	}))
	defer srvAcc2.Close()

	seedConnDB(t, database, "multiacc", "acc-1", "key-1", srvAcc1.URL)
	seedConnDB(t, database, "multiacc", "acc-2", "key-2", srvAcc2.URL)

	// Configure provider routing to round-robin via settings table (dashboard key format from Issue #20)
	if err := repo.UpdateSettingsRaw(map[string]any{
		"providerStrategies": map[string]any{
			"multiacc": map[string]any{
				"fallbackStrategy":      "round-robin",
				"stickyRoundRobinLimit": 1,
			},
		},
	}); err != nil {
		t.Fatalf("update provider strategies: %v", err)
	}

	accReqBody := `{"model":"multiacc/some-model","messages":[{"role":"user","content":"hi"}]}`

	// Request 1 -> Account 1
	pRec1 := httptest.NewRecorder()
	h.HandleChatCompletions(pRec1, httptest.NewRequest("POST", "/chat/completions", strings.NewReader(accReqBody)))
	if pRec1.Code != http.StatusOK || !strings.Contains(pRec1.Body.String(), "response from account 1") {
		t.Fatalf("acc req 1 failed: code=%d body=%s", pRec1.Code, pRec1.Body.String())
	}

	// Request 2 -> Account 2 (Rotated!)
	pRec2 := httptest.NewRecorder()
	h.HandleChatCompletions(pRec2, httptest.NewRequest("POST", "/chat/completions", strings.NewReader(accReqBody)))
	if pRec2.Code != http.StatusOK || !strings.Contains(pRec2.Body.String(), "response from account 2") {
		t.Fatalf("acc req 2 failed: code=%d body=%s", pRec2.Code, pRec2.Body.String())
	}

	// Request 3 -> Account 1 (Wrapped!)
	pRec3 := httptest.NewRecorder()
	h.HandleChatCompletions(pRec3, httptest.NewRequest("POST", "/chat/completions", strings.NewReader(accReqBody)))
	if pRec3.Code != http.StatusOK || !strings.Contains(pRec3.Body.String(), "response from account 1") {
		t.Fatalf("acc req 3 failed: code=%d body=%s", pRec3.Code, pRec3.Body.String())
	}

	if hitsAcc1.Load() != 2 || hitsAcc2.Load() != 1 {
		t.Errorf("expected 2 hits on Acc1 and 1 hit on Acc2, got Acc1=%d Acc2=%d", hitsAcc1.Load(), hitsAcc2.Load())
	}
}
