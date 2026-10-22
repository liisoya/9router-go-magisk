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

// guardNameCollision refuses a write whose bare name is already taken by the
// other bare-name space.
//
// A bare model string the client sends is resolved against a model alias first
// (resolveModel step 2) and a combo name second (step 3), and both are writable
// from the dashboard. The same string could therefore be written into both: the
// alias then silently outranks the combo, so the combo answers no request at
// all and the only symptom is a model nobody meant to reach. That is the whole
// collision this guard exists for — one bare name, two targets, one of them
// unreachable.
//
// A custom model id is not such a name. It is reached as "<node-prefix>/<id>"
// (resolveModel step 1: the id is only ever addressed with a prefix), and
// /v1/models publishes it in that same prefixed form, so it can neither occupy
// a bare name nor be shadowed by one. An id colliding with an alias or a combo
// is two distinguishable addresses, not a name that resolves to the wrong
// target. Custom model writes are therefore not cross-checked at all —
// refusing them is what broke "Import from /models", where a bare-name alias is
// usually the same model on another node and a single already-aliased id in the
// list was enough to fail the import. The refusal was also asymmetric: an alias
// written over an existing custom id was refused while the identical pair in
// the other order was accepted.
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
	if name == "" || namespace == nsCustomModel {
		return false
	}

	if namespace != nsCombo {
		if combo, err := h.Repo.GetComboByName(name); err == nil && combo != nil {
			writeNameConflict(w, "COMBO_NAME_CONFLICT", name,
				"a combo named \""+name+"\" already answers to this name, and a bare request for \""+name+
					"\" is the combo, so the "+namespaceLabel(namespace)+" would never be reached; rename one of them")
			return true
		}
	}

	if namespace != nsModelAlias {
		if target, err := h.Repo.GetModelAlias(name); err == nil && target != "" {
			writeNameConflict(w, "MODEL_ALIAS_CONFLICT", name,
				"a model alias \""+name+"\" already resolves this name, and an alias is consulted before a combo, so the combo would be unreachable; rename one of them")
			return true
		}
	}

	return false
}

// namespaceLabel names the bare-name space the caller is writing into, so a
// conflict message can say which entry loses its name.
func namespaceLabel(namespace string) string {
	switch namespace {
	case nsModelAlias:
		return "model alias"
	default:
		return "entry"
	}
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
