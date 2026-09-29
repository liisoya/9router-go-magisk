package dashboard

import (
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"9router/proxy/internal/handlerutil"
)

// HandleGetCustomModels handles GET /api/models/custom.
// Returns custom models from kv scope customModels.
func (h *DashboardHandler) HandleGetCustomModels(w http.ResponseWriter, r *http.Request) {
	kv, err := h.Repo.GetKVScope("customModels")
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	models := make([]any, 0, len(kv))
	for _, v := range kv {
		var item any
		if err := json.Unmarshal([]byte(v), &item); err == nil {
			models = append(models, item)
		} else {
			models = append(models, v)
		}
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"models": models,
	})
}

// HandleSaveCustomModel handles POST /api/models/custom.
// Saves a custom model definition to kv scope customModels.
func (h *DashboardHandler) HandleSaveCustomModel(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var key string
	if k, ok := data["key"].(string); ok && k != "" {
		key = k
	} else if p, ok1 := data["providerAlias"].(string); ok1 && p != "" {
		mid, _ := data["id"].(string)
		mtype, _ := data["type"].(string)
		if mtype == "" {
			mtype = "llm"
		}
		key = fmt.Sprintf("%s|%s|%s", p, mid, mtype)
	} else if mid, ok2 := data["id"].(string); ok2 && mid != "" {
		key = mid
	}

	if key == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing custom model key or id")
		return
	}

	// The id is the segment a client sees as "<node-prefix>/<id>" and copies
	// into its config, so it is the part that must not collide with a combo
	// name or a model alias.
	modelID, _ := data["id"].(string)
	if modelID == "" {
		if parts := strings.SplitN(key, "|", 3); len(parts) >= 2 {
			modelID = parts[1]
		} else {
			modelID = key
		}
	}
	if h.guardNameCollision(w, nsCustomModel, modelID) {
		return
	}

	var valStr string
	if v, ok := data["value"]; ok {
		switch val := v.(type) {
		case string:
			valStr = val
		default:
			b, err := json.Marshal(val)
			if err != nil {
				handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to marshal model value")
				return
			}
			valStr = string(b)
		}
	} else {
		b, err := json.Marshal(data)
		if err != nil {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to marshal model data")
			return
		}
		valStr = string(b)
	}

	if err := h.Repo.SetKV("customModels", key, valStr); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"key":    key,
	})
}

// HandleDeleteCustomModel handles DELETE /api/models/custom/{key}.
// Deletes a custom model from kv scope customModels.
func (h *DashboardHandler) HandleDeleteCustomModel(w http.ResponseWriter, r *http.Request) {
	key := getURLParam(r, "key")
	if unescaped, err := url.PathUnescape(key); err == nil && unescaped != "" {
		key = unescaped
	}
	if key == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing model key")
		return
	}

	if err := h.Repo.DeleteKV("customModels", key); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"key":    key,
	})
}

// HandleGetDisabledModels handles GET /api/models/disabled.
// Returns disabled models per provider from kv scope disabledModels.
func (h *DashboardHandler) HandleGetDisabledModels(w http.ResponseWriter, r *http.Request) {
	kv, err := h.Repo.GetKVScope("disabledModels")
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	result := make(map[string]any, len(kv))
	for k, v := range kv {
		var arr any
		if err := json.Unmarshal([]byte(v), &arr); err == nil {
			result[k] = arr
		} else {
			result[k] = v
		}
	}

	handlerutil.WriteJSON(w, http.StatusOK, result)
}

// HandleSaveDisabledModels handles PUT /api/models/disabled/{provider}.
// Saves an array of disabled model IDs for a provider in kv scope disabledModels.
func (h *DashboardHandler) HandleSaveDisabledModels(w http.ResponseWriter, r *http.Request) {
	provider := getURLParam(r, "provider")
	if unescaped, err := url.PathUnescape(provider); err == nil && unescaped != "" {
		provider = unescaped
	}
	if provider == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing provider")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var modelsArr any
	switch v := raw.(type) {
	case []any:
		modelsArr = v
	case map[string]any:
		if m, ok := v["models"]; ok {
			modelsArr = m
		} else if dm, ok := v["disabledModels"]; ok {
			modelsArr = dm
		} else {
			modelsArr = []any{}
		}
	default:
		modelsArr = []any{}
	}

	b, err := json.Marshal(modelsArr)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to marshal models")
		return
	}

	if err := h.Repo.SetKV("disabledModels", provider, string(b)); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"provider":       provider,
		"disabledModels": modelsArr,
	})
}

// HandleGetModelAliases handles GET /api/models/alias.
func (h *DashboardHandler) HandleGetModelAliases(w http.ResponseWriter, r *http.Request) {
	aliases, err := h.Repo.GetModelAliases()
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res := make(map[string]string, len(aliases))
	for k, v := range aliases {
		res[string(k)] = v
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"aliases": res})
}

// HandleSetModelAlias handles PUT /api/models/alias.
func (h *DashboardHandler) HandleSetModelAlias(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var req struct {
		Model string `json:"model"`
		Alias string `json:"alias"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Model == "" || req.Alias == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "model and alias required")
		return
	}

	if h.guardNameCollision(w, nsModelAlias, req.Alias) {
		return
	}

	valBytes, _ := json.Marshal(req.Model)
	if err := h.Repo.SetKV("modelAliases", req.Alias, string(valBytes)); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "model": req.Model, "alias": req.Alias})
}

// HandleDeleteModelAlias handles DELETE /api/models/alias.
func (h *DashboardHandler) HandleDeleteModelAlias(w http.ResponseWriter, r *http.Request) {
	alias := r.URL.Query().Get("alias")
	if alias == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "alias required")
		return
	}
	if err := h.Repo.DeleteKV("modelAliases", alias); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}
