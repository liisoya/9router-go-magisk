package providers

import "testing"

// The models.dev catalog is keyed by provider, and a provider that ships as a
// user-created "openai-compatible-chat-*" node is filed under its own
// generated id rather than the upstream product name. Without an alias every
// model on that node misses the catalog and falls through to the substring
// table, which answers 128000/4096 from the name alone. OpenCode's free tier
// is the real case: the catalog carries 1M windows for beam-free and friends
// while /v1/models published 128k.
func TestCatalogLimits_OpenCodeBridgeAlias(t *testing.T) {
	if err := LoadCatalogFromFile("testdata/model-catalog.json"); err != nil {
		t.Skipf("catalog fixture unavailable: %v", err)
	}
	t.Cleanup(func() {
		catalogMu.Lock()
		globalCatalog = nil
		catalogMu.Unlock()
	})

	cw, maxOut := GetCatalogLimits("openai-compatible-chat-opencode-free-bridge", "beam-free")
	if cw == 0 && maxOut == 0 {
		t.Fatalf("opencode bridge missed the catalog; ProviderAliases needs the node id")
	}
}

// A published model id can carry two qualifiers — the provider alias and the
// vendor namespace — while the catalog files the model under its bare name.
// GetCatalogLimits used to strip only the first segment, so `nvidia/z-ai/glm-5.3`
// looked up `ai/glm-5.3` and missed.
func TestCatalogLimits_MultiSegmentID(t *testing.T) {
	if err := LoadCatalogFromFile("testdata/model-catalog.json"); err != nil {
		t.Skipf("catalog fixture unavailable: %v", err)
	}
	t.Cleanup(func() {
		catalogMu.Lock()
		globalCatalog = nil
		catalogMu.Unlock()
	})

	cw, maxOut := GetCatalogLimits("nvidia", "z-ai/glm-5.3")
	if cw == 0 && maxOut == 0 {
		t.Fatalf("multi-segment id missed the catalog")
	}
}

// CodeBuddy's catalog declares 393216 output for deepseek-v4.1-flash, but the
// provider-blind substring table answers 128000. codebuddy-intl has to carry
// the provider-qualified figure, because that table cannot: raising it there
// would over-report every other gateway serving the same model name.
func TestProviderTokenLimits_CodebuddyIntl(t *testing.T) {
	d := GetCapabilitiesDetailForModel("codebuddy-intl", "deepseek-v4.1-flash")
	if d.ContextWindow != 1000000 || d.MaxOutput != 393216 {
		t.Errorf("codebuddy-intl deepseek-v4.1-flash = (%d, %d), want (1000000, 393216)", d.ContextWindow, d.MaxOutput)
	}
}

// A provider-qualified limit must not drag the provider's thinking shape along.
// codebuddy-intl serves the same ids as codebuddy-cn but resolves its own
// levels; sharing the whole capability table made every intl model report
// codebuddy-cn's effort sets.
func TestProviderTokenLimits_DoesNotLeakThinking(t *testing.T) {
	InvalidateCapabilitiesCache()
	t.Cleanup(InvalidateCapabilitiesCache)

	caps := GetCapabilitiesForModel("codebuddy-intl", "deepseek-v4.1-flash")
	if caps.ThinkingFormat == "" {
		t.Fatalf("codebuddy-intl lost its thinking format")
	}
	cn := GetCapabilitiesForModel("codebuddy-cn", "deepseek-v4.1-flash")
	if caps.ThinkingFormat == cn.ThinkingFormat && caps.ContextWindow != cn.ContextWindow {
		t.Errorf("limits and thinking resolved inconsistently")
	}
}
