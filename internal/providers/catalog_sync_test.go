package providers

import (
	"context"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
)

// rewriteTransport sends every request to the mock server, so the real
// SyncModelCatalog (which targets the models.dev constant) can be driven
// end-to-end without touching production code.
type rewriteTransport struct{ base *url.URL }

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.base.Scheme
	clone.URL.Host = rt.base.Host
	return http.DefaultTransport.RoundTrip(clone)
}

// TestSyncModelCatalog_Modalities drives the real sync against the current
// models.dev shape. The schema is `modalities: {input: ["text","image",…]}`;
// the previous `modality: {image: true}` map no longer exists, and reading it
// silently produced an all-false catalog — every synced model looked
// text-only.
func TestSyncModelCatalog_Modalities(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", "etag-123")
		w.Header().Set("Content-Type", "application/json")
		json.MarshalWrite(w, map[string]any{
			"anthropic": map[string]any{
				"models": map[string]any{
					"claude-3-7-sonnet-20250219": map[string]any{
						"modalities": map[string]any{"input": []string{"text", "image", "pdf"}},
						"limit":      map[string]int{"context": 200000, "output": 64000},
					},
				},
			},
			"custom-ai": map[string]any{
				"models": map[string]any{
					"custom-multimodal-v1": map[string]any{
						"modalities": map[string]any{"input": []string{"image", "video", "audio"}},
					},
					"custom-text-only-v1": map[string]any{
						"modalities": map[string]any{"input": []string{"text"}},
					},
				},
			},
		})
	}))
	defer mock.Close()

	tmpFile := t.TempDir() + "/catalog.json"
	base, err := url.Parse(mock.URL)
	if err != nil {
		t.Fatalf("parse mock url: %v", err)
	}
	client := &http.Client{Transport: rewriteTransport{base: base}}

	if err := SyncModelCatalog(context.Background(), client, tmpFile); err != nil {
		t.Fatalf("SyncModelCatalog: %v", err)
	}

	tests := []struct {
		name     string
		provider string
		model    string
		want     SyncedModelModalities
	}{
		{
			name: "image and pdf", provider: "anthropic", model: "claude-3-7-sonnet-20250219",
			want: SyncedModelModalities{Vision: true, PDF: true},
		},
		{
			name: "video and audio", provider: "custom-ai", model: "custom-multimodal-v1",
			want: SyncedModelModalities{Vision: true, VideoInput: true, AudioInput: true},
		},
		{
			// No modalities are declared, so nothing is filed for the model at all.
			name: "text only stays absent", provider: "custom-ai", model: "custom-text-only-v1",
			want: SyncedModelModalities{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetCatalogModalities(tt.provider, tt.model)
			if tt.want == (SyncedModelModalities{}) {
				if got != nil {
					t.Fatalf("expected no entry, got %+v", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected modalities, got nil")
			}
			if *got != tt.want {
				t.Errorf("modalities = %+v, want %+v", *got, tt.want)
			}
		})
	}

	t.Run("capabilities inherit the synced modalities", func(t *testing.T) {
		InvalidateCapabilitiesCache()
		caps := GetCapabilitiesForModel("custom-ai", "custom-multimodal-v1")
		if !caps.Vision || !caps.VideoInput || !caps.AudioInput {
			t.Errorf("expected vision+video+audio, got %+v", caps)
		}
	})

	t.Run("limits stay keyed by provider and model", func(t *testing.T) {
		cw, maxOut := GetCatalogLimits("anthropic", "claude-3-7-sonnet-20250219")
		if cw != 200000 || maxOut != 64000 {
			t.Errorf("limits = (%d, %d), want (200000, 64000)", cw, maxOut)
		}
	})
}

// TestSyncModelCatalog_ModalitiesArePerProvider pins the key rule: modalities
// belong to a gateway, and gateways disagree about the same weights. Keying by
// bare model id would let one provider's entry answer for another's.
func TestSyncModelCatalog_ModalitiesArePerProvider(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.MarshalWrite(w, map[string]any{
			"visionyes": map[string]any{"models": map[string]any{
				"shared-model": map[string]any{"modalities": map[string]any{"input": []string{"image"}}},
			}},
			"visionno": map[string]any{"models": map[string]any{
				"shared-model": map[string]any{"modalities": map[string]any{"input": []string{"text"}}},
			}},
		})
	}))
	defer mock.Close()

	base, err := url.Parse(mock.URL)
	if err != nil {
		t.Fatalf("parse mock url: %v", err)
	}
	client := &http.Client{Transport: rewriteTransport{base: base}}
	if err := SyncModelCatalog(context.Background(), client, ""); err != nil {
		t.Fatalf("SyncModelCatalog: %v", err)
	}

	if got := GetCatalogModalities("visionyes", "shared-model"); got == nil || !got.Vision {
		t.Errorf("visionyes: expected vision, got %+v", got)
	}
	if got := GetCatalogModalities("visionno", "shared-model"); got != nil {
		t.Errorf("visionno: expected no entry, got %+v", *got)
	}
}

// TestCatalogProviderKeys covers the local-id aliasing: our `claude` is
// models.dev `anthropic`, so an entry filed under the upstream name has to
// resolve under the local one too.
func TestCatalogProviderKeys(t *testing.T) {
	tests := []struct {
		provider string
		want     []string
	}{
		{provider: "anthropic", want: []string{"anthropic", "claude"}},
		{provider: "google", want: []string{"google", "gemini"}},
		{provider: "custom-ai", want: []string{"custom-ai"}},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := catalogProviderKeys(tt.provider)
			for _, want := range tt.want {
				found := false
				for _, g := range got {
					if g == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("catalogProviderKeys(%q) = %v, missing %q", tt.provider, got, want)
				}
			}
		})
	}
}

func TestCatalogBaseID(t *testing.T) {
	tests := []struct {
		model string
		want  string
	}{
		{model: "deepseek-v4-flash", want: "deepseek-v4-flash"},
		{model: "moonshotai/Kimi-K2.5", want: "kimi-k2.5"},
		{model: "claude-opus-4-thinking:8192", want: "claude-opus-4-thinking"},
		{model: "GLM-4.6V", want: "glm-4.6v"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := catalogBaseID(tt.model); got != tt.want {
				t.Errorf("catalogBaseID(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

// TestLoadCatalogFromFile_LegacyBareKeys keeps catalogs written by an older
// build (keyed by bare model id) resolving, so an upgrade does not silently
// drop the modalities it already had on disk.
func TestLoadCatalogFromFile_LegacyBareKeys(t *testing.T) {
	path := t.TempDir() + "/legacy.json"
	legacy := `{"syncedAt":"2026-08-31T00:00:00Z","models":{"legacy-multimodal":{"vision":true,"pdf":false,"audioInput":false,"videoInput":false}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy catalog: %v", err)
	}

	if err := LoadCatalogFromFile(path); err != nil {
		t.Fatalf("LoadCatalogFromFile: %v", err)
	}

	got := GetCatalogModalities("any-provider", "legacy-multimodal")
	if got == nil || !got.Vision {
		t.Errorf("expected the legacy bare key to still resolve, got %+v", got)
	}
}

// TestLoadCatalogFromFile_SyncedAtForms covers the three shapes a catalog file
// arrives in: an ISO string from this build's writer, epoch milliseconds from
// upstream's, and absent from catalogs written before the field existed. The
// decoder used to read it as []byte, which encoding/json/v2 rejects for both
// string and number unless the string happens to be base64 — so a restart
// could not load its own catalog back.
func TestLoadCatalogFromFile_SyncedAtForms(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantSync string
	}{
		{
			name:     "iso string",
			body:     `{"syncedAt":"2026-08-31T00:00:00Z","models":{}}`,
			wantSync: "2026-08-31T00:00:00Z",
		},
		{
			name:     "epoch milliseconds as upstream writes it",
			body:     `{"v":1,"etag":"e","syncedAt":1787788800000,"models":{}}`,
			wantSync: "2026-08-27T00:00:00Z",
		},
		{
			name:     "absent",
			body:     `{"models":{}}`,
			wantSync: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := t.TempDir() + "/catalog.json"
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatalf("write catalog: %v", err)
			}
			if err := LoadCatalogFromFile(path); err != nil {
				t.Fatalf("LoadCatalogFromFile: %v", err)
			}
			if got := GetCatalogState().LastSync; got != tt.wantSync {
				t.Errorf("LastSync = %q, want %q", got, tt.wantSync)
			}
		})
	}
}
