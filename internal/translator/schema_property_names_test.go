package translator

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"testing"
)

// #344: the schema sanitizer deletes keywords Gemini's protobuf has no field
// for — `title`, `default`, `const`, `x-*`, and the rest of
// UNSUPPORTED_SCHEMA_CONSTRAINTS. Those deletions are only correct on a *schema
// node*. A `properties` value is a dictionary keyed by **parameter name**, so a
// tool parameter literally called `title`, `default` or `x-custom` is not a
// keyword at all — and a parameter called `properties` is not the properties
// container.
//
// Upstream hit exactly this twice: `ensureObjectType` recursed into the
// properties map instead of its values, so a parameter named `properties` made
// the map itself look like an `type:"object"` schema and Google rejected the
// request (decolua/9router#4620), and `removeUnsupportedKeywords` /
// `convertConstToEnum` walk `Object.values`, which deletes keyword-named
// parameters and rewrites a parameter named `const` into an enum
// (decolua/9router#4831).
//
// This port already resolves `properties` as a dictionary — the recursion below
// visits its values, never the map — so the tests exist to pin that invariant
// against a future "simplify the recursion" refactor, which is precisely how the
// upstream bug was introduced.

// keywordNamedParams is the set of parameter names that double as schema
// keywords the sanitizer strips.
var keywordNamedParams = []string{
	"title", "default", "const", "x-custom", "format", "pattern", "required",
	"properties", "items", "enum", "example", "examples", "description",
}

func TestCleanParametersSchemaPreservesKeywordNamedParameters(t *testing.T) {
	for _, name := range keywordNamedParams {
		t.Run("parameter named "+name, func(t *testing.T) {
			raw := jsontext.Value(`{"type":"object","properties":{"` + name +
				`":{"type":"string","description":"d"},"keep":{"type":"string"}},"required":["` + name + `","keep"]}`)

			out := CleanParametersSchema(raw)

			var parsed struct {
				Properties map[string]any `json:"properties"`
				Required   []string       `json:"required"`
			}
			if err := json.Unmarshal(out, &parsed); err != nil {
				t.Fatalf("decode cleaned schema: %v (%s)", err, string(out))
			}
			if _, ok := parsed.Properties[name]; !ok {
				t.Fatalf("parameter %q was deleted by the sanitizer:\n%s", name, string(out))
			}
			if _, ok := parsed.Properties["keep"]; !ok {
				t.Fatalf("sibling parameter \"keep\" was lost alongside %q:\n%s", name, string(out))
			}
			if !containsString(parsed.Required, name) {
				t.Errorf("required no longer lists %q: %v (%s)", name, parsed.Required, string(out))
			}
		})
	}
}

// A parameter named `properties` is the case that produced a live 400: the
// properties dictionary must never be treated as a schema node, so the sanitizer
// must not inject `type:"object"` next to the parameter names.
func TestCleanParametersSchemaDoesNotTreatPropertiesMapAsSchema(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{
			name: "a parameter named properties",
			in:   `{"type":"object","properties":{"properties":{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}}}`,
		},
		{
			name: "a parameter named properties beside plain siblings",
			in:   `{"type":"object","properties":{"query":{"type":"string"},"properties":{"type":"object","properties":{"a":{"type":"string"}}}}}`,
		},
		{
			name: "a parameter named properties nested two levels down",
			in:   `{"type":"object","properties":{"outer":{"type":"object","properties":{"properties":{"type":"object","properties":{"a":{"type":"string"}}}}}}}`,
		},
		{
			name: "a parameter named properties inside an array's items",
			in:   `{"type":"object","properties":{"list":{"type":"array","items":{"type":"object","properties":{"properties":{"type":"object","properties":{"a":{"type":"string"}}}}}}}}`,
		},
		{
			name: "a parameter named properties in a union branch",
			in:   `{"type":"object","properties":{"mode":{"anyOf":[{"type":"object","properties":{"properties":{"type":"string"}}},{"type":"null"}]}}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := CleanParametersSchema(jsontext.Value(tt.in))

			var parsed map[string]any
			if err := json.Unmarshal(out, &parsed); err != nil {
				t.Fatalf("decode cleaned schema: %v (%s)", err, string(out))
			}
			if _, ok := parsed["type"]; !ok {
				t.Fatalf("the converted schema lost its own type: %s", string(out))
			}
			if got := countPropertyNamed(t, parsed, "properties"); got == 0 {
				t.Fatalf("the parameter named \"properties\" was removed:\n%s", string(out))
			}
		})
	}
}

// A parameter named `const` must stay a parameter: the constant→enum rewrite is
// a schema-node operation, so nothing may turn `properties.const` into
// `properties.const = [<schema>]` the way upstream's Object.values walk did.
func TestCleanParametersSchemaDoesNotConvertConstParameterToEnum(t *testing.T) {
	raw := jsontext.Value(`{"type":"object","properties":{"const":{"type":"string"}},"required":["const"]}`)
	out := CleanParametersSchema(raw)

	var parsed struct {
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("decode cleaned schema: %v (%s)", err, string(out))
	}
	param, ok := parsed.Properties["const"]
	if !ok {
		t.Fatalf("parameter \"const\" was deleted:\n%s", string(out))
	}
	if _, hasEnum := param["enum"]; hasEnum {
		t.Fatalf("parameter \"const\" was rewritten as a schema constant:\n%s", string(out))
	}
	if param["type"] != "string" {
		t.Fatalf("parameter \"const\" lost its schema: %v", param)
	}
}

// countPropertyNamed counts the parameters called name at any depth, treating
// `properties` as a dictionary of parameter names rather than as a schema.
func countPropertyNamed(t *testing.T, node any, name string) int {
	t.Helper()

	switch v := node.(type) {
	case []any:
		total := 0
		for _, item := range v {
			total += countPropertyNamed(t, item, name)
		}
		return total
	case map[string]any:
		total := 0
		if props, ok := v["properties"].(map[string]any); ok {
			if _, found := props[name]; found {
				total++
			}
			for paramName, paramSchema := range props {
				if paramName == name {
					continue
				}
				total += countPropertyNamed(t, paramSchema, name)
			}
		}
		for key, value := range v {
			if key == "properties" {
				continue
			}
			total += countPropertyNamed(t, value, name)
		}
		return total
	default:
		return 0
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
