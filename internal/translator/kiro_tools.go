package translator

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Kiro tool-spec normalization, ported from
// open-sse/translator/concerns/kiroConversation.js (normalizeKiroToolSpecs).
//
// Kiro's CodeWhisperer protocol has no OpenAI-style `tools` array: the tool
// catalogue travels on the LAST user turn as
// `userInputMessage.userInputMessageContext.tools`, each entry shaped
// `{toolSpecification: {name, description, inputSchema: {json: …}}}`. Without
// this the model never learns which tools exist and answers with a
// pseudo-tool-call written as text (`<invoke name="browser">`) instead of a
// real toolUseEvent, so clients never receive `tool_calls`.

const (
	kiroToolNameMaxLength        = 64
	kiroToolDescriptionMaxLength = 10237
)

var kiroToolNamePattern = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// kiroToolSpec is one entry of the Kiro tool catalogue.
type kiroToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"-"`
}

// kiroNormalizeToolSpecs converts OpenAI (`{type, function:{name, description,
// parameters}}`) or Claude (`{name, description, input_schema}`) tool
// definitions into Kiro tool specifications. It returns the specs plus the
// source-name → Kiro-name map so tool results coming back from a client can be
// matched to the spec they were derived from.
func kiroNormalizeToolSpecs(tools []map[string]any) ([]any, map[string]string) {
	specs := make([]any, 0, len(tools))
	nameMap := make(map[string]string, len(tools))
	used := make(map[string]bool, len(tools))

	for index, tool := range tools {
		if tool == nil {
			continue
		}
		fn, _ := tool["function"].(map[string]any)
		rawName, _ := fn["name"].(string)
		if rawName == "" {
			rawName, _ = tool["name"].(string)
		}
		rawName = strings.TrimSpace(rawName)
		if rawName == "" {
			continue
		}
		// A repeated definition with the same source name is the same tool.
		if _, seen := nameMap[rawName]; seen {
			continue
		}
		name := kiroUniqueToolName(rawName, index, used)
		nameMap[rawName] = name

		description, _ := fn["description"].(string)
		if description == "" {
			description, _ = tool["description"].(string)
		}
		if description == "" {
			description = "Tool: " + rawName
		}

		var schema map[string]any
		if raw, ok := fn["parameters"]; ok {
			schema, _ = raw.(map[string]any)
		} else if raw, ok := tool["parameters"]; ok {
			schema, _ = raw.(map[string]any)
		} else if raw, ok := tool["input_schema"]; ok {
			schema, _ = raw.(map[string]any)
		}

		specs = append(specs, map[string]any{
			"toolSpecification": map[string]any{
				"name":        name,
				"description": kiroTrimCodePoints(description, kiroToolDescriptionMaxLength),
				"inputSchema": map[string]any{"json": kiroNormalizeRootSchema(schema)},
			},
		})
	}
	return specs, nameMap
}

// kiroUniqueToolName mirrors upstream uniqueName: sanitize, then de-duplicate
// with a numeric suffix that stays inside the length budget.
func kiroUniqueToolName(rawName string, index int, used map[string]bool) string {
	cleaned := strings.TrimSpace(rawName)
	cleaned = kiroToolNamePattern.ReplaceAllString(cleaned, "_")
	cleaned = strings.Trim(cleaned, "_")
	if cleaned == "" {
		cleaned = "tool_" + strconv.Itoa(index+1)
	}
	base := kiroTrimCodePoints(cleaned, kiroToolNameMaxLength)
	candidate := base
	for suffix := 2; used[candidate]; suffix++ {
		tail := "_" + strconv.Itoa(suffix)
		limit := kiroToolNameMaxLength - len(tail)
		if limit < 0 {
			limit = 0
		}
		head := base
		if len(head) > limit {
			head = head[:limit]
		}
		candidate = head + tail
	}
	used[candidate] = true
	return candidate
}

// kiroTrimCodePoints truncates to limit code points, not bytes, so a multibyte
// description is never cut mid-rune.
func kiroTrimCodePoints(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	count := 0
	for i := range value {
		if count == limit {
			return value[:i]
		}
		count++
	}
	return value
}

// kiroNormalizeRootSchema mirrors normalizeRootSchema: force an object schema,
// drop `additionalProperties` anywhere, drop empty `required` arrays, and keep
// only required names that actually exist in properties.
func kiroNormalizeRootSchema(schema map[string]any) map[string]any {
	cleaned, _ := kiroCleanSchemaValue(schema).(map[string]any)
	if cleaned == nil {
		cleaned = map[string]any{}
	}
	cleaned["type"] = "object"
	props, ok := cleaned["properties"].(map[string]any)
	if !ok {
		cleaned["properties"] = map[string]any{}
		props = map[string]any{}
	}
	if required, ok := cleaned["required"].([]any); ok {
		seen := make(map[string]bool, len(required))
		kept := make([]any, 0, len(required))
		for _, item := range required {
			name, isStr := item.(string)
			if !isStr {
				continue
			}
			if _, declared := props[name]; !declared {
				continue
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			kept = append(kept, name)
		}
		if len(kept) == 0 {
			delete(cleaned, "required")
		} else {
			cleaned["required"] = kept
		}
	}
	return cleaned
}

func kiroCleanSchemaValue(value any) any {
	switch typed := value.(type) {
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, kiroCleanSchemaValue(item))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if key == "additionalProperties" {
				continue
			}
			if key == "required" {
				if arr, isArr := child.([]any); isArr && len(arr) == 0 {
					continue
				}
			}
			out[key] = kiroCleanSchemaValue(child)
		}
		return out
	default:
		return value
	}
}
