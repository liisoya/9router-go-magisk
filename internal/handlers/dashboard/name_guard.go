package dashboard

import (
	"net/http"
	"strings"

	"9router/proxy/internal/handlerutil"
)

// nameNamespace labels one of the three user-editable spaces a bare model
// string can be written into from the dashboard.
const (
	nsCombo       = "combo"
	nsModelAlias  = "modelAlias"
	nsCustomModel = "customModel"
)

// guardNameCollision refuses a write whose name is already taken in one of the
// other two spaces.
//
// A bare model string the client sends is resolved against a model alias first
// (resolveModel step 2), then a combo name (step 3), then a provider node
// prefix. All three are editable from the dashboard, so the same string could
// be written into two of them: the alias then silently wins over the combo and
// the combo can no longer be reached by name at all, with nothing but a model
// name in the failure. A custom model id is reached as "<node-prefix>/<id>",
// which is a different address, but /v1/models then advertises "combo-wombo"
// and "xai/combo-wombo" side by side and a name copied out of the list no
// longer says which one it lands on — the state observed on the xai node,
// which carried custom model ids "combo-wombo" and "agy" alongside combos of
// exactly those names.
//
// Comparison is exact after trimming, matching resolution itself: resolveModel
// looks combos up with `WHERE name = ?` and aliases by exact kv key, so
// "Combo" and "combo" are two genuinely different addresses and the guard must
// not refuse either. It guards the write only, so rows that already collide
// keep working — nothing is migrated or hidden.
//
// Returns true when it has already written the 409 and the caller must return.
func (h *DashboardHandler) guardNameCollision(w http.ResponseWriter, namespace, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}

	if namespace != nsCombo {
		if combo, err := h.Repo.GetComboByName(name); err == nil && combo != nil {
			writeNameConflict(w, "COMBO_NAME_CONFLICT", name,
				"a combo named \""+name+"\" already answers to this name; a bare request for \""+name+
					"\" is the combo, so rename the "+namespaceLabel(namespace)+" or the combo so the name addresses one target")
			return true
		}
	}

	if namespace != nsModelAlias {
		if target, err := h.Repo.GetModelAlias(name); err == nil && target != "" {
			writeNameConflict(w, "MODEL_ALIAS_CONFLICT", name,
				"a model alias \""+name+"\" already resolves this name, and an alias is consulted before a combo, so the combo would be unreachable")
			return true
		}
	}

	if namespace != nsCustomModel {
		if owner, ok := customModelOwner(h, name); ok {
			writeNameConflict(w, "CUSTOM_MODEL_NAME_CONFLICT", name,
				"the custom model \""+owner+"/"+name+"\" already uses this id; rename one of them so the name addresses one target")
			return true
		}
	}

	return false
}

// namespaceLabel names the space the caller is writing into, so a conflict
// message can point at what the user was actually saving.
func namespaceLabel(namespace string) string {
	switch namespace {
	case nsCustomModel:
		return "custom model"
	case nsModelAlias:
		return "model alias"
	default:
		return "entry"
	}
}

// customModelOwner returns the provider alias of a custom model carrying id,
// reporting false when no custom model uses it. Only the id is compared: two
// nodes may both expose "glm-5.3" as <prefix>/glm-5.3 without either shadowing
// the other.
func customModelOwner(h *DashboardHandler, id string) (string, bool) {
	customs, err := h.Repo.GetCustomModels()
	if err != nil {
		return "", false
	}
	for _, cm := range customs {
		if cm != nil && cm.ID == id {
			return cm.ProviderAlias, true
		}
	}
	return "", false
}

// writeNameConflict emits the typed 409 the connection-name guard established
// (PROVIDER_NAME_CONFLICT), so clients already handling that shape can handle
// this one too.
func writeNameConflict(w http.ResponseWriter, code, name, detail string) {
	handlerutil.WriteJSON(w, http.StatusConflict, map[string]any{
		"error":   detail,
		"code":    code,
		"name":    name,
		"details": detail,
	})
}
