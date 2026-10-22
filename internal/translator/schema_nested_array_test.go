package translator

import (
	json "encoding/json/v2"
	"testing"
)

// Upstream #4798: a 2D array whose inner items carry no schema reached Gemini as
// "...properties[where].items.items: missing field." and 400'd the whole
// request. Every array items — at any depth — must leave the cleaner with a
// concrete type.
func TestCleanParametersSchema_NestedArrayItems(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		verify func(t *testing.T, got map[string]any)
	}{
		{
			// The reported shape: 2D array, inner items empty.
			name:  "2d array with empty inner items",
			input: `{"type":"object","properties":{"query":{"type":"object","properties":{"where":{"type":"array","items":{"type":"array","items":{}}}}}}}`,
			verify: func(t *testing.T, got map[string]any) {
				if v := schemaNode(t, got, "properties", "query", "properties", "where", "items", "type"); v != "array" {
					t.Errorf("outer items type must be array, got %#v", v)
				}
				if v := schemaNode(t, got, "properties", "query", "properties", "where", "items", "items", "type"); v != "string" {
					t.Errorf("empty inner items must get a type, got %#v", v)
				}
			},
		},
		{
			// items without a type of its own: inferred from its own items key.
			name:  "untyped nested array items infer array",
			input: `{"type":"object","properties":{"grid":{"type":"array","items":{"items":{"type":"number"}}}}}`,
			verify: func(t *testing.T, got map[string]any) {
				if v := schemaNode(t, got, "properties", "grid", "items", "type"); v != "array" {
					t.Errorf("items carrying its own items must infer array, got %#v", v)
				}
				if v := schemaNode(t, got, "properties", "grid", "items", "items", "type"); v != "number" {
					t.Errorf("deepest items must survive, got %#v", v)
				}
			},
		},
		{
			name:  "untyped object items infer object",
			input: `{"type":"object","properties":{"rows":{"type":"array","items":{"properties":{"cell":{"type":"string"}}}}}}`,
			verify: func(t *testing.T, got map[string]any) {
				if v := schemaNode(t, got, "properties", "rows", "items", "type"); v != "object" {
					t.Errorf("items carrying properties must infer object, got %#v", v)
				}
				if v := schemaNode(t, got, "properties", "rows", "items", "properties", "cell", "type"); v != "string" {
					t.Errorf("nested property must survive, got %#v", v)
				}
			},
		},
		{
			// title/default are stripped before items are resolved, so the item
			// schema can be reduced to {} without ever being empty on input.
			name:  "items emptied by keyword removal",
			input: `{"type":"object","properties":{"tags":{"type":"array","items":{"title":"Tag","default":"x","description":"keep me"}}}}`,
			verify: func(t *testing.T, got map[string]any) {
				items := schemaNode(t, got, "properties", "tags", "items").(map[string]any)
				if items["type"] != "string" {
					t.Errorf("items left empty by the strip must get a type, got %#v", items)
				}
				if items["description"] != "keep me" {
					t.Errorf("supported sibling must survive, got %#v", items)
				}
			},
		},
		{
			name:  "3d array with empty middle items",
			input: `{"type":"array","items":{"type":"array","items":{"type":"array","items":{}}}}`,
			verify: func(t *testing.T, got map[string]any) {
				if v := schemaNode(t, got, "items", "items", "items", "type"); v != "string" {
					t.Errorf("deepest empty items must get a type, got %#v", v)
				}
			},
		},
		{
			name:  "array with no items at all",
			input: `{"type":"object","properties":{"flags":{"type":"array"}}}`,
			verify: func(t *testing.T, got map[string]any) {
				if v := schemaNode(t, got, "properties", "flags", "items", "type"); v != "string" {
					t.Errorf("missing items must be filled, got %#v", v)
				}
			},
		},
		{
			// Tuple form (draft-04/07 items list) — first empty entry must not win.
			name:  "tuple items skip empty first entry",
			input: `{"type":"array","items":[{},{"type":"number"}]}`,
			verify: func(t *testing.T, got map[string]any) {
				if v := schemaNode(t, got, "items", "type"); v != "number" {
					t.Errorf("tuple items must collapse to the first usable entry, got %#v", v)
				}
			},
		},
		{
			name:  "tuple items of only empty schemas",
			input: `{"type":"array","items":[{},{}]}`,
			verify: func(t *testing.T, got map[string]any) {
				if v := schemaNode(t, got, "items", "type"); v != "string" {
					t.Errorf("all-empty tuple items must fall back to string, got %#v", v)
				}
			},
		},
		{
			name:  "tuple items nested inside another array",
			input: `{"type":"array","items":{"type":"array","items":[{},{"items":{"type":"boolean"}}]}}`,
			verify: func(t *testing.T, got map[string]any) {
				if v := schemaNode(t, got, "items", "items", "type"); v != "array" {
					t.Errorf("promoted tuple entry must infer array, got %#v", v)
				}
				if v := schemaNode(t, got, "items", "items", "items", "type"); v != "boolean" {
					t.Errorf("tuple entry items must survive, got %#v", v)
				}
			},
		},
		{
			name:  "items is a non-schema scalar",
			input: `{"type":"array","items":true}`,
			verify: func(t *testing.T, got map[string]any) {
				if v := schemaNode(t, got, "items", "type"); v != "string" {
					t.Errorf("scalar items must fall back to string, got %#v", v)
				}
			},
		},
		{
			name:  "typed nested items keep their own type",
			input: `{"type":"array","items":{"type":"array","items":{"type":"string","description":"keep me"}}}`,
			verify: func(t *testing.T, got map[string]any) {
				if v := schemaNode(t, got, "items", "items", "type"); v != "string" {
					t.Errorf("existing type must survive, got %#v", v)
				}
				if v := schemaNode(t, got, "items", "items", "description"); v != "keep me" {
					t.Errorf("description must survive, got %#v", v)
				}
			},
		},
		{
			name:  "array with items having anyOf with enums",
			input: `{"type":"object","properties":{"choices":{"type":"array","items":{"anyOf":[{"enum":["a","b"]},{"type":"number"}]}}}}`,
			verify: func(t *testing.T, got map[string]any) {
				if v := schemaNode(t, got, "properties", "choices", "items", "type"); v != "string" {
					t.Errorf("flattened anyOf items must pick first branch type string, got %#v", v)
				}
				if enum, ok := schemaNode(t, got, "properties", "choices", "items", "enum").([]any); !ok || len(enum) != 2 {
					t.Errorf("enum must survive, got %#v", enum)
				}
			},
		},
		{
			name:  "array with items having anyOf with nested items",
			input: `{"type":"object","properties":{"matrix":{"type":"array","items":{"anyOf":[{"items":{"type":"string"}},{"type":"string"}]}}}}`,
			verify: func(t *testing.T, got map[string]any) {
				if v := schemaNode(t, got, "properties", "matrix", "items", "type"); v != "array" {
					t.Errorf("flattened anyOf items must pick first branch type array, got %#v", v)
				}
				if v := schemaNode(t, got, "properties", "matrix", "items", "items", "type"); v != "string" {
					t.Errorf("nested items type must survive, got %#v", v)
				}
			},
		},
		{
			name:  "2d array with items having oneOf with enums",
			input: `{"type":"object","properties":{"grid":{"type":"array","items":{"type":"array","items":{"oneOf":[{"enum":["x","y"]},{"type":"integer"}]}}}}}`,
			verify: func(t *testing.T, got map[string]any) {
				if v := schemaNode(t, got, "properties", "grid", "items", "items", "type"); v != "string" {
					t.Errorf("flattened oneOf inner items must pick first branch type string, got %#v", v)
				}
				if enum, ok := schemaNode(t, got, "properties", "grid", "items", "items", "enum").([]any); !ok || len(enum) != 2 {
					t.Errorf("enum must survive in inner items, got %#v", enum)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := CleanParametersSchema([]byte(tt.input))

			var got map[string]any
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("unmarshal cleaned schema: %v (out: %s)", err, string(out))
			}

			assertNoUntypedArrayItems(t, got)
			tt.verify(t, got)
		})
	}
}

// assertNoUntypedArrayItems walks every schema node and fails on an array whose
// items schema is missing, not an object, or carries no type — the three shapes
// Gemini reports as "...items: missing field." / "...items.items: missing field."
func assertNoUntypedArrayItems(t *testing.T, node any) {
	t.Helper()
	switch v := node.(type) {
	case map[string]any:
		if v["type"] == "array" {
			items, ok := v["items"].(map[string]any)
			if !ok {
				t.Errorf("array items must be an object schema, got %#v", v["items"])
				return
			}
			if _, hasType := items["type"]; !hasType {
				t.Errorf("array items must carry a type, got %#v", items)
			}
		}
		for _, child := range v {
			assertNoUntypedArrayItems(t, child)
		}
	case []any:
		for _, elem := range v {
			assertNoUntypedArrayItems(t, elem)
		}
	}
}

// The OpenAI-compat path sanitizes tool schemas through the same cleaner, so
// the nested-array fix has to reach the body actually put on the wire.
func TestSanitizeOpenAITools_NestedArrayItems(t *testing.T) {
	body := []byte(`{
		"model": "gemini-3.5-flash",
		"tools": [{
			"type": "function",
			"function": {
				"name": "run_query",
				"parameters": {
					"type": "object",
					"properties": {
						"query": {
							"type": "object",
							"properties": {
								"where": {
									"type": "array",
									"items": {"type": "array", "items": {}}
								}
							}
						}
					}
				}
			}
		}]
	}`)

	out, err := SanitizeOpenAITools(body)
	if err != nil {
		t.Fatalf("SanitizeOpenAITools failed: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal sanitized body: %v (out: %s)", err, string(out))
	}
	assertNoUntypedArrayItems(t, got)

	fn := schemaNode(t, got, "tools").([]any)[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "run_query" {
		t.Errorf("tool declaration must survive, got %#v", fn)
	}
	if v := schemaNode(t, fn, "parameters", "properties", "query", "properties", "where", "items", "items", "type"); v != "string" {
		t.Errorf("nested empty items must get a type, got %#v", v)
	}
}
