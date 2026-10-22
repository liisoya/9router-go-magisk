package providers

import (
	"slices"
	"testing"
)

// Upstream decolua/9router#4793 (commit 51a1827e) retired the GitHub Models
// inference API (models.github.ai, retired 2026-07-30) and replaced the GitHub
// embedding catalogue entry text-embedding-3-large with text-embedding-ada-002.
// A stale id here is not cosmetic: /v1/models serves this catalogue, so the
// dashboard kept offering a model the Copilot embeddings endpoint no longer
// accepts.
func TestGitHubEmbeddingModelParity(t *testing.T) {
	const want = "text-embedding-ada-002"
	const retired = "text-embedding-3-large"

	tests := []struct {
		name     string
		provider string
	}{
		{name: "short alias gh", provider: "gh"},
		{name: "canonical id github", provider: "github"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models := GetProviderModels(tt.provider)
			if !slices.Contains(models, want) {
				t.Errorf("GetProviderModels(%q) = %v, want it to contain %q", tt.provider, models, want)
			}
			if slices.Contains(models, retired) {
				t.Errorf("GetProviderModels(%q) still lists the retired model %q", tt.provider, retired)
			}
			if kind := GetProviderModelKind(tt.provider, want); kind != "embedding" {
				t.Errorf("GetProviderModelKind(%q, %q) = %q, want %q", tt.provider, want, kind, "embedding")
			}
			if kind := GetProviderModelKind(tt.provider, retired); kind != "" {
				t.Errorf("GetProviderModelKind(%q, %q) = %q, want no kind for a retired model", tt.provider, retired, kind)
			}
			if kind := GetProviderModelKind(tt.provider, "text-embedding-3-small"); kind != "embedding" {
				t.Errorf("GetProviderModelKind(%q, %q) = %q, want %q", tt.provider, "text-embedding-3-small", kind, "embedding")
			}
		})
	}
}
