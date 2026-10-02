package dashboard

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"9router/proxy/internal/db"
)

// fetchCaps calls GET /api/models/caps?provider=<provider> and returns the
// decoded caps map keyed by model id.
func fetchCaps(t *testing.T, provider string) (int, map[string]modelCaps) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/models/caps?provider="+provider, nil)
	rec := httptest.NewRecorder()
	setupTestRouter(setupTestRepoForCaps(t)).ServeHTTP(rec, req)

	var body struct {
		Caps map[string]modelCaps `json:"caps"`
	}
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal caps response: %v", err)
		}
	}
	return rec.Code, body.Caps
}

func setupTestRepoForCaps(t *testing.T) *db.Repo {
	t.Helper()
	repo, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)
	return repo
}

// TestHandleGetModelCaps_CommandCode covers what the provider detail page
// consumes: the vision/reasoning icons, the declared limits, and the thinking
// level list the "Thinking:" picker offers.
func TestHandleGetModelCaps_CommandCode(t *testing.T) {
	code, caps := fetchCaps(t, "commandcode")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if len(caps) != 22 {
		t.Fatalf("expected 22 commandcode models, got %d", len(caps))
	}

	tests := []struct {
		model            string
		wantVision       bool
		wantContext      int
		wantMaxOutput    int
		wantThinkingLvls []string
	}{
		{
			// denylisted: no image input, but the effort picker still applies
			model: "deepseek/deepseek-v4-pro", wantVision: false,
			wantContext: 1000000, wantMaxOutput: 384000,
			wantThinkingLvls: []string{"none", "low", "medium", "high", "xhigh", "max"},
		},
		{
			model: "moonshotai/Kimi-K2.7-Code", wantVision: true,
			wantContext: 1000000, wantMaxOutput: 384000,
			wantThinkingLvls: []string{"none", "low", "medium", "high", "xhigh", "max"},
		},
		{
			model: "nvidia/nemotron-3-ultra-550b-a55b", wantVision: false,
			wantContext: 1000000, wantMaxOutput: 384000,
			wantThinkingLvls: []string{"none", "low", "medium", "high", "xhigh", "max"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			got, ok := caps[tt.model]
			if !ok {
				t.Fatalf("model %q missing from caps", tt.model)
			}
			if got.Vision != tt.wantVision {
				t.Errorf("Vision = %v, want %v", got.Vision, tt.wantVision)
			}
			if !got.Reasoning {
				t.Error("Reasoning = false, want true")
			}
			if got.ContextWindow != tt.wantContext {
				t.Errorf("ContextWindow = %d, want %d", got.ContextWindow, tt.wantContext)
			}
			if got.MaxOutput != tt.wantMaxOutput {
				t.Errorf("MaxOutput = %d, want %d", got.MaxOutput, tt.wantMaxOutput)
			}
			if !slices.Equal(got.ThinkingLevels, tt.wantThinkingLvls) {
				t.Errorf("ThinkingLevels = %v, want %v", got.ThinkingLevels, tt.wantThinkingLvls)
			}
		})
	}
}

// TestHandleGetModelCaps_AliasResolution checks that the storage prefix the
// dashboard links models with ("cmc/…") resolves to the same block as the
// provider id the route carries.
func TestHandleGetModelCaps_AliasResolution(t *testing.T) {
	_, byID := fetchCaps(t, "commandcode")
	_, byAlias := fetchCaps(t, "cmc")

	if len(byAlias) != len(byID) {
		t.Fatalf("alias resolved %d models, id resolved %d", len(byAlias), len(byID))
	}
	for model, want := range byID {
		got, ok := byAlias[model]
		if !ok {
			t.Fatalf("model %q missing when resolved via cmc", model)
		}
		if !slices.Equal(got.ThinkingLevels, want.ThinkingLevels) {
			t.Errorf("%s thinking levels via cmc = %v, want %v", model, got.ThinkingLevels, want.ThinkingLevels)
		}
	}
}

func TestHandleGetModelCaps_BadRequest(t *testing.T) {
	t.Run("missing provider", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/models/caps", nil)
		rec := httptest.NewRecorder()
		setupTestRouter(setupTestRepoForCaps(t)).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/models/caps?provider=does-not-exist", nil)
		rec := httptest.NewRecorder()
		setupTestRouter(setupTestRepoForCaps(t)).ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rec.Code)
		}
	})
}

func TestHandleGetModelCaps_CompatibleNode(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/models/caps?provider=openai-compatible-chat-2eb28394-dbf3-4c93-8477-2690aeea7041", nil)
	rec := httptest.NewRecorder()
	setupTestRouter(setupTestRepoForCaps(t)).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
