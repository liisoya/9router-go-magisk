package executor

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"strings"
	"testing"
)

// parseToolsForTest unmarshals a tools[] array the way the executor does.
func parseToolsForTest(t *testing.T, body string) []deepSeekToolSpec {
	t.Helper()
	var tools []deepSeekToolSpec
	if err := json.Unmarshal([]byte(body), &tools); err != nil {
		t.Fatalf("parse tools: %v", err)
	}
	return tools
}

// The Claude/Anthropic spelling — `name` and `input_schema` at the top level, no
// `type`/`function` wrapper — used to parse into a spec whose Function.Name was
// empty. The prompt builder skips those, so the model was told about no tools at
// all, reached for its native DSML instead, and the markup reached the client as
// if it were the answer.
func TestDeepSeekToolSpecReadsClaudeShape(t *testing.T) {
	body := `[{"name":"web_search","description":"search the web",
	  "input_schema":{"type":"object","properties":{"query":{"type":"string"}}}}]`

	tools := parseToolsForTest(t, body)

	if len(tools) != 1 {
		t.Fatalf("parsed %d tools, want 1", len(tools))
	}
	if tools[0].Function.Name != "web_search" {
		t.Errorf("name = %q, want web_search", tools[0].Function.Name)
	}
	if len(tools[0].Function.Parameters) == 0 {
		t.Error("input_schema did not become the parameter schema")
	}

	// A declaration the prompt cannot name is worse than none: the model answers
	// in DSML and the wrapper passes the markup through.
	rendered := deepSeekRenderTools(tools)
	if !strings.Contains(rendered, "web_search") {
		t.Errorf("the tool never reached the prompt:\n%s", rendered)
	}
}

// A call recovered from DSML has to match against the flat declaration too, or
// the tool is recognised in the prompt and then rejected at extraction.
func TestDeepSeekDSMLMatchesClaudeShapedDeclaration(t *testing.T) {
	tools := parseToolsForTest(t, `[{"name":"web_search","input_schema":{"type":"object"}}]`)

	answer := "<｜｜DSML｜｜ invoke name=\"web_search\">" +
		"<｜｜DSML｜｜ parameter name=\"query\" string=\"true\">jakarta<｜｜DSML｜｜ parameter>" +
		"<｜｜DSML｜｜ invoke>"

	call, ok := extractOne(answer, tools)
	if !ok {
		t.Fatal("a call against a Claude-shaped declaration was rejected")
	}
	if !strings.Contains(call.Arguments, "jakarta") {
		t.Errorf("arguments = %s, want the query", call.Arguments)
	}
}

// The OpenAI shape must keep working.
func TestDeepSeekToolSpecReadsOpenAIShape(t *testing.T) {
	body := `[{"type":"function","function":{"name":"web_search","description":"d",
	  "parameters":{"type":"object"}}}]`

	tools := parseToolsForTest(t, body)
	if len(tools) != 1 || tools[0].Function.Name != "web_search" {
		t.Fatalf("OpenAI shape broken: %+v", tools)
	}
}

// A mixed list is what a client sends when it merges its own tools with an MCP
// server's, and both spellings have to survive the round trip.
func TestDeepSeekToolSpecReadsMixedShapes(t *testing.T) {
	body := `[
	  {"type":"function","function":{"name":"a_tool","parameters":{"type":"object"}}},
	  {"name":"b_tool","input_schema":{"type":"object"}}
	]`

	tools := parseToolsForTest(t, body)
	if len(tools) != 2 {
		t.Fatalf("parsed %d tools, want 2", len(tools))
	}
	if tools[0].Function.Name != "a_tool" || tools[1].Function.Name != "b_tool" {
		t.Errorf("names = %q, %q; want a_tool, b_tool",
			tools[0].Function.Name, tools[1].Function.Name)
	}
}

// A declaration carrying both spellings resolves to the nested one, which is the
// shape the prompt renders.
func TestDeepSeekToolSpecPrefersNestedShape(t *testing.T) {
	body := `[{"type":"function","name":"flat_name",
	  "function":{"name":"nested_name","parameters":{"type":"object"}}}]`

	tools := parseToolsForTest(t, body)
	if len(tools) != 1 {
		t.Fatalf("parsed %d tools, want 1", len(tools))
	}
	if tools[0].Function.Name != "nested_name" {
		t.Errorf("name = %q, want nested_name", tools[0].Function.Name)
	}
	if len(tools[0].Function.Parameters) == 0 {
		t.Error("the nested schema was dropped")
	}
}

// A malformed declaration must be an error the caller can see, not a silently
// empty spec that then fails to render.
func TestDeepSeekToolSpecRejectsMalformedDeclaration(t *testing.T) {
	var spec deepSeekToolSpec
	if err := json.Unmarshal([]byte(`{"name":`), &spec); err == nil {
		t.Error("a truncated declaration must not parse")
	}
	if err := json.Unmarshal([]byte(`[]`), &spec); err == nil {
		t.Error("an array is not a tool declaration")
	}
}

// A schema that arrives stringified is still usable — several SDKs do this.
func TestDeepSeekToolSpecReadsStringifiedSchema(t *testing.T) {
	schema := jsontext.Value(`"{\"type\":\"object\"}"`)
	spec := deepSeekToolSpec{}
	spec.Function.Name = "t"
	spec.Function.Parameters = schema

	if !strings.Contains(deepSeekSchemaText(schema), `"type"`) {
		t.Error("a stringified schema did not render as a schema")
	}
}
