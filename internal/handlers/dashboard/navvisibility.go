package dashboard

import (
	"regexp"
	"slices"
	"strings"
)

// navVisibilitySettingKey is the settings key the dashboard's Settings > Sidebar
// switch writes and the sidebar reads. web/src/lib/nav.ts owns the canonical id
// list; this file only keeps the stored shape honest, so a stale or hand-crafted
// payload can never hand the sidebar an id it cannot address.
const navVisibilitySettingKey = "navVisibility"

// navHiddenIDPattern matches the ids the sidebar can emit: lowercase tab ids
// such as `quota`, `usage-cache` and `media-tts`.
var navHiddenIDPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// maxHiddenNavEntries caps the stored list so a hostile payload cannot grow the
// settings row without bound. The sidebar renders 20 entries today.
const maxHiddenNavEntries = 32

// navAlwaysVisibleEntries are entries the visibility panel itself lives behind.
// Hiding `settings` would remove the only way back, so it is dropped on write
// and again on read, whatever the client sent.
var navAlwaysVisibleEntries = []string{"settings"}

// normalizeNavVisibility coerces a client payload into the stored shape
// {"hidden": [...]}: lower-cased, de-duplicated, capped, always-visible entries
// removed.
//
// It returns nil when the payload carries no usable list, which callers treat as
// "clear the setting" — a write that cannot be understood must not leave a
// half-applied list behind.
func normalizeNavVisibility(value any) map[string]any {
	container, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	raw, present := container["hidden"]
	if !present {
		return nil
	}
	hidden, ok := normalizeHiddenNavIDs(raw)
	if !ok {
		return nil
	}
	return map[string]any{"hidden": hidden}
}

// normalizeHiddenNavIDs keeps only the ids that can address a sidebar entry. The
// second result reports whether the payload was a list at all, so a scalar
// `"hidden": "quota"` clears the setting instead of silently storing nothing.
func normalizeHiddenNavIDs(value any) ([]string, bool) {
	var candidates []string
	switch typed := value.(type) {
	case []any:
		candidates = make([]string, 0, len(typed))
		for _, item := range typed {
			if id, ok := item.(string); ok {
				candidates = append(candidates, id)
			}
		}
	case []string:
		candidates = typed
	default:
		return nil, false
	}

	hidden := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		id := strings.ToLower(strings.TrimSpace(candidate))
		if !navHiddenIDPattern.MatchString(id) {
			continue
		}
		if slices.Contains(navAlwaysVisibleEntries, id) {
			continue
		}
		if slices.Contains(hidden, id) {
			continue
		}
		if len(hidden) == maxHiddenNavEntries {
			break
		}
		hidden = append(hidden, id)
	}
	return hidden, true
}

// applyNavVisibilityUpdate normalizes the key in HandleUpdateSettings' payload.
//
// An unparsable payload is stored as an explicit null rather than deleted:
// UpdateSettingsRaw merges into the stored row, so dropping the map entry would
// leave the previous list in place and the panel would keep hiding entries the
// operator just tried to restore.
func applyNavVisibilityUpdate(updates map[string]any) {
	raw, present := updates[navVisibilitySettingKey]
	if !present {
		return
	}
	if normalized := normalizeNavVisibility(raw); normalized != nil {
		updates[navVisibilitySettingKey] = normalized
		return
	}
	updates[navVisibilitySettingKey] = nil
}

// sanitizeNavVisibility re-reads the stored value through the same normalizer,
// so a row written by an older build (or by hand) reaches the dashboard in the
// shape the sidebar expects. An entry that normalizes to nothing is dropped
// instead of being sent as a value the client would have to defend against.
func sanitizeNavVisibility(settings map[string]any) {
	raw, present := settings[navVisibilitySettingKey]
	if !present {
		return
	}
	if normalized := normalizeNavVisibility(raw); normalized != nil {
		settings[navVisibilitySettingKey] = normalized
		return
	}
	delete(settings, navVisibilitySettingKey)
}
