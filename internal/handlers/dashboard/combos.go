package dashboard

import (
	json "encoding/json/v2"
	"io"
	"net/http"

	"github.com/google/uuid"

	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/models"
)

// HandleGetCombos handles GET /api/combos.
// Returns a list of combos from Repo.GetCombos().
func (h *DashboardHandler) HandleGetCombos(w http.ResponseWriter, r *http.Request) {
	combos, err := h.Repo.GetCombos()
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if combos == nil {
		combos = []*models.Combo{}
	}
	handlerutil.WriteJSON(w, http.StatusOK, combos)
}

// HandleCreateCombo handles POST /api/combos.
// Parses id, name, kind, models (json array/string), strategy. Calls CreateCombo.
func (h *DashboardHandler) HandleCreateCombo(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var req struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Models   any    `json:"models"`
		Strategy string `json:"strategy"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.Name == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing combo name")
		return
	}
	if req.ID == "" {
		req.ID = uuid.New().String()
	}
	if req.Strategy == "" {
		req.Strategy = "fallback"
	}

	modelsJSON := "[]"
	if req.Models != nil {
		switch m := req.Models.(type) {
		case string:
			modelsJSON = m
		default:
			b, err := json.Marshal(m)
			if err == nil {
				modelsJSON = string(b)
			}
		}
	}
	// A combo name is addressed bare, so it must not be shadowed by a model
	// alias (consulted first) or read as a custom model id in /v1/models.
	if h.guardNameCollision(w, nsCombo, req.Name) {
		return
	}
	if err := h.Repo.CreateCombo(req.ID, req.Name, req.Kind, modelsJSON, req.Strategy); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "id": req.ID})
}

// HandleUpdateCombo handles PUT /api/combos/{id}.
// Updates combo.
func (h *DashboardHandler) HandleUpdateCombo(w http.ResponseWriter, r *http.Request) {
	id := getURLParam(r, "id")
	if id == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing combo id")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var req struct {
		Name     string `json:"name"`
		Kind     string `json:"kind"`
		Models   any    `json:"models"`
		Strategy string `json:"strategy"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	existing, err := h.Repo.GetComboById(id)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing == nil {
		handlerutil.WriteJSONError(w, http.StatusNotFound, "combo not found")
		return
	}

	name := req.Name
	if name == "" {
		name = existing.Name
	}
	kind := req.Kind
	if kind == "" && existing.Kind != nil {
		kind = *existing.Kind
	}
	modelsJSON := existing.Models
	if req.Models != nil {
		switch m := req.Models.(type) {
		case string:
			modelsJSON = m
		default:
			b, err := json.Marshal(m)
			if err == nil {
				modelsJSON = string(b)
			}
		}
	}
	strategy := req.Strategy
	if strategy == "" {
		strategy = existing.Strategy
	}
	// Renaming a combo moves the bare name it answers to, so a rename gets the
	// same check as a create. Leaving the name alone is unaffected: the guard
	// skips the combo space when the caller is writing a combo.
	if name != existing.Name && h.guardNameCollision(w, nsCombo, name) {
		return
	}
	if err := h.Repo.UpdateCombo(id, name, kind, modelsJSON, strategy); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "id": id})
}

// HandleDeleteCombo handles DELETE /api/combos/{id}.
// Deletes combo.
func (h *DashboardHandler) HandleDeleteCombo(w http.ResponseWriter, r *http.Request) {
	id := getURLParam(r, "id")
	if id == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing combo id")
		return
	}

	if err := h.Repo.DeleteCombo(id); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "id": id})
}
