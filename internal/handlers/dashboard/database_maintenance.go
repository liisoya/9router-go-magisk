package dashboard

import (
	"net/http"
	"time"

	"9router/proxy/internal/handlerutil"
)

// HandleGetDatabaseStats handles GET /api/settings/database/stats.
func (h *DashboardHandler) HandleGetDatabaseStats(w http.ResponseWriter, r *http.Request) {
	if h.Repo == nil {
		handlerutil.WriteJSONError(w, http.StatusServiceUnavailable, "database repo unavailable")
		return
	}
	stats, err := h.Repo.GetDatabaseStats(r.Context())
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to get database stats: "+err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, stats)
}

// HandlePruneDatabase handles POST /api/settings/database/prune.
func (h *DashboardHandler) HandlePruneDatabase(w http.ResponseWriter, r *http.Request) {
	if h.Repo == nil {
		handlerutil.WriteJSONError(w, http.StatusServiceUnavailable, "database repo unavailable")
		return
	}
	retention := 7 * 24 * time.Hour
	if settings, err := h.Repo.GetSettings(); err == nil && settings != nil && settings.RetentionDays > 0 {
		retention = time.Duration(settings.RetentionDays) * 24 * time.Hour
	}
	result, err := h.Repo.PruneRequestDetails(r.Context(), retention)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to prune database: "+err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"pruned":  result.Total(),
		"byAge":   result.ByAge,
		"bySize":  result.BySize,
	})
}

// HandleVacuumDatabase handles POST /api/settings/database/vacuum.
func (h *DashboardHandler) HandleVacuumDatabase(w http.ResponseWriter, r *http.Request) {
	if h.Repo == nil {
		handlerutil.WriteJSONError(w, http.StatusServiceUnavailable, "database repo unavailable")
		return
	}
	if err := h.Repo.VacuumDatabase(r.Context()); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to vacuum database: "+err.Error())
		return
	}
	stats, _ := h.Repo.GetDatabaseStats(r.Context())
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"success":           true,
		"databaseSizeBytes": stats.DatabaseSizeBytes,
		"reclaimableBytes":  stats.ReclaimableBytes,
	})
}
