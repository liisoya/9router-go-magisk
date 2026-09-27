package executor

import (
	"testing"

	"9router/proxy/internal/providers"
)

// RegisterAll runs at server startup, not at package init, so the test has to
// populate the registry itself before asserting on it.
func init() { RegisterAll() }

// Qoder and Qoder CN are separate deployments with separate gateways and
// separate credentials. A provider that reaches the dashboard catalog but not
// the executor registry is offered by the UI and then fails to route, so pin
// the transport half the same way internal/providers/aggregators_test.go pins
// the catalog half.
func TestQoderProviderTransport(t *testing.T) {
	tests := []struct {
		id        string
		baseURL   string
		wantAlias string
	}{
		{
			id:        "qoder",
			baseURL:   "https://api3.qoder.sh/algo/api/v2/service/pro/sse/agent_chat_generation",
			wantAlias: "qd",
		},
		{
			id:        "qoder-cn",
			baseURL:   "https://gateway.qoder.com.cn/algo/api/v2/service/pro/sse/agent_chat_generation",
			wantAlias: "qdcn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			cfg, ok := providers.KnownProviders[tt.id]
			if !ok {
				t.Fatalf("KnownProviders has no entry for %q", tt.id)
			}
			if cfg.BaseURL != tt.baseURL {
				t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, tt.baseURL)
			}

			if providers.ResolveAlias(tt.wantAlias) != tt.id {
				t.Errorf("ResolveAlias(%q) = %q, want %q",
					tt.wantAlias, providers.ResolveAlias(tt.wantAlias), tt.id)
			}

			if Get(tt.id) == nil {
				t.Fatalf("no executor registered for %q: requests for it cannot be served", tt.id)
			}
		})
	}
}

// The two deployments must not share endpoints — a cross-wired alias or
// transport would send one provider's credentials to the other's gateway.
func TestQoderProvidersAreIsolated(t *testing.T) {
	intl := providers.KnownProviders["qoder"].BaseURL
	cn := providers.KnownProviders["qoder-cn"].BaseURL
	if intl == cn {
		t.Fatalf("qoder and qoder-cn share a base URL: %q", intl)
	}
	if providers.ResolveAlias("qd") == "qoder-cn" {
		t.Error("alias qd must resolve to qoder, not qoder-cn")
	}
	if providers.ResolveAlias("qdcn") != "qoder-cn" {
		t.Error("alias qdcn must resolve to qoder-cn")
	}
}
