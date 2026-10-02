package dashboard

import (
	"net/http"
	"strings"

	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/providers"
)

// modelCaps is the per-model capability block the dashboard needs to render the
// icons next to a model row and to decide which thinking levels apply. It is the
// subset of /v1/models capabilities the provider detail page consumes, plus the
// thinking level list that upstream computes in getThinkingLevels and the
// dashboard uses for the "Thinking: <level>" picker and the "(level)" suffix.
type modelCaps struct {
	Vision         bool     `json:"vision"`
	Search         bool     `json:"search"`
	Reasoning      bool     `json:"reasoning"`
	ContextWindow  int      `json:"contextWindow"`
	MaxOutput      int      `json:"maxOutput"`
	ThinkingLevels []string `json:"thinkingLevels"`
}

// HandleGetModelCaps handles GET /api/models/caps?provider=<id>.
//
// The model catalog is served to the dashboard as a static bundle, but
// capabilities and thinking levels are server-side state: they depend on the
// provider registry, the capability tables and the synced models.dev catalog, and
// upstream computes them on the server for exactly this reason. The provider is
// resolved by id or by alias, so both `/dashboard/providers/commandcode` and the
// `cmc` storage prefix resolve to the same block.
func (h *DashboardHandler) HandleGetModelCaps(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "provider is required")
		return
	}

	resolved := providers.ResolveAlias(provider)
	models := providers.GetProviderModels(resolved)
	if len(models) == 0 {
		if strings.HasPrefix(provider, "openai-compatible") || strings.HasPrefix(provider, "anthropic-compatible") {
			handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
				"provider": provider,
				"caps":     map[string]any{},
			})
			return
		}
		if h.Repo != nil {
			if node, _, err := h.Repo.GetProviderNodeByID(provider); err == nil && node != nil {
				handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
					"provider": provider,
					"caps":     map[string]any{},
				})
				return
			}
		}
		handlerutil.WriteJSONError(w, http.StatusNotFound, "unknown provider or no static models")
		return
	}

	caps := make(map[string]modelCaps, len(models))
	for _, model := range models {
		detail := providers.GetCapabilitiesDetailForModel(resolved, model)
		entry := modelCaps{
			Vision:        detail.Vision,
			Search:        detail.Search,
			Reasoning:     detail.Reasoning,
			ContextWindow: detail.ContextWindow,
			MaxOutput:     detail.MaxOutput,
		}
		if levels := providers.GetThinkingLevels(resolved, model); levels != nil {
			entry.ThinkingLevels = levels
		}
		caps[model] = entry
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"provider": resolved,
		"caps":     caps,
	})
}
