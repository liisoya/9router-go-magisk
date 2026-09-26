package dashboard

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"9router/proxy/internal/auth"
	"9router/proxy/internal/handlerutil"
)

// HandleGetApiKeys handles GET /api/keys.
// Upstream parity: the Next dashboard returns full key values here and the
// media example cards use them directly as Bearer credentials for Run.
// Full secrets are returned only to fully authenticated dashboard callers
// (login session cookie, local CLI token, or requireLogin=false which
// upstream treats as authenticated). Callers presenting only a low-privilege
// client API key get masked display values: unlike upstream (which rejects
// them at the guard), this router lets API keys through dashboard auth for
// CLI compat, so listing full secrets there would let one leaked key dump
// them all. Creation still returns the full value once.
func (h *DashboardHandler) HandleGetApiKeys(w http.ResponseWriter, r *http.Request) {
	reveal := auth.SessionValid(r) ||
		auth.ValidCLIToken(r.Header.Get(auth.CLITokenHeader)) ||
		!auth.RequireLogin(h.Repo)
	keys, err := h.Repo.GetApiKeys()
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sanitized := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		if k == nil {
			continue
		}
		key := k.Key
		if !reveal {
			key = maskClientKey(key)
		}
		sanitized = append(sanitized, map[string]any{
			"id": k.ID, "key": key, "name": k.Name,
			"machineId": k.MachineID, "isActive": k.IsActive, "createdAt": k.CreatedAt,
		})
	}
	handlerutil.WriteJSON(w, http.StatusOK, sanitized)
}

// maskClientKey shows the first/last few chars of a client key (dashboard
// display only); the full value is returned once at creation.
func maskClientKey(key string) string {
	if len(key) <= 12 {
		return "***"
	}
	return key[:6] + "…" + key[len(key)-4:]
}

// HandleCreateApiKey handles POST /api/keys.
// Creates new apiKey (generates uuid if empty, or key if empty sk-...).
func (h *DashboardHandler) HandleCreateApiKey(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var req struct {
		ID        string `json:"id"`
		Key       string `json:"key"`
		Name      string `json:"name"`
		MachineID string `json:"machineId"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	}

	if req.ID == "" {
		req.ID = uuid.New().String()
	}
	if req.Key == "" {
		req.Key = "sk-" + strings.ReplaceAll(uuid.New().String(), "-", "")
	}

	if err := h.Repo.CreateApiKey(req.ID, req.Key, req.Name, req.MachineID); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"id":     req.ID,
		"key":    req.Key,
	})
}

// HandleDeleteApiKey handles DELETE /api/keys/{id}.
// Deletes client apiKey by ID.
func (h *DashboardHandler) HandleDeleteApiKey(w http.ResponseWriter, r *http.Request) {
	id := getURLParam(r, "id")
	if id == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing apiKey id")
		return
	}

	if err := h.Repo.DeleteApiKey(id); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "id": id})
}

// HandleToggleApiKey handles PUT /api/keys/{id}/toggle.
// Toggles isActive flag of client apiKey or sets it to specified boolean if provided.
func (h *DashboardHandler) HandleToggleApiKey(w http.ResponseWriter, r *http.Request) {
	id := getURLParam(r, "id")
	if id == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing apiKey id")
		return
	}

	var req struct {
		IsActive *bool `json:"isActive"`
	}
	if r.Body != nil {
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if len(body) > 0 {
			_ = json.Unmarshal(body, &req)
		}
	}

	var newStatus bool
	if req.IsActive != nil {
		newStatus = *req.IsActive
	} else {
		keys, err := h.Repo.GetApiKeys()
		if err != nil {
			handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		found := false
		for _, k := range keys {
			if k.ID == id {
				newStatus = !(k.IsActive == 1)
				found = true
				break
			}
		}
		if !found {
			handlerutil.WriteJSONError(w, http.StatusNotFound, "api key not found")
			return
		}
	}

	if err := h.Repo.SetApiKeyStatus(id, newStatus); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"id":       id,
		"isActive": newStatus,
	})
}
