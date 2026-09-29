package translator

import (
	json "encoding/json/v2"
	"testing"
)

func convertRequest(t *testing.T, body string) *ResponsesRequestResult {
	t.Helper()
	result, err := ResponsesToChatRequest([]byte(body))
	if err != nil {
		t.Fatalf("ResponsesToChatRequest: %v", err)
	}
	return result
}

func convertedBody(t *testing.T, body string) map[string]any {
	t.Helper()
	result := convertRequest(t, body)
	if !result.Converted {
		t.Fatalf("body was not converted: %s", result.Body)
	}
	var out map[string]any
	if err := json.Unmarshal(result.Body, &out); err != nil {
		t.Fatalf("unmarshal converted body: %v", err)
	}
	return out
}

func messagesOf(t *testing.T, body string) []any {
	t.Helper()
	converted := convertedBody(t, body)
	messages, ok := converted["messages"].([]any)
	if !ok {
		t.Fatalf("messages = %T, want an array", converted["messages"])
	}
	return messages
}

func messageAt(t *testing.T, messages []any, idx int) map[string]any {
	t.Helper()
	if idx >= len(messages) {
		t.Fatalf("message %d out of range (have %d)", idx, len(messages))
	}
	msg, ok := messages[idx].(map[string]any)
	if !ok {
		t.Fatalf("message %d = %T, want an object", idx, messages[idx])
	}
	return msg
}

func partTexts(t *testing.T, msg map[string]any) []any {
	t.Helper()
	parts, ok := msg["content"].([]any)
	if !ok {
		t.Fatalf("content = %T, want an array of parts", msg["content"])
	}
	return parts
}

// --- input normalization ---

func TestResponsesToChatRequest_StringInput(t *testing.T) {
	messages := messagesOf(t, `{"input":"hello there"}`)

	if len(messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(messages))
	}
	msg := messageAt(t, messages, 0)
	if msg["role"] != "user" {
		t.Errorf("role = %v, want user", msg["role"])
	}
	parts := partTexts(t, msg)
	if len(parts) != 1 || parts[0].(map[string]any)["text"] != "hello there" {
		t.Errorf("content = %v, want a single input_text part", parts)
	}
	if parts[0].(map[string]any)["type"] != "text" {
		t.Errorf("part type = %v, want Chat block type \"text\"", parts[0].(map[string]any)["type"])
	}
}

func TestResponsesToChatRequest_BlankInputGetsPlaceholder(t *testing.T) {
	// Providers reject a request with no user turn, so a blank prompt becomes
	// an explicit placeholder rather than an empty message list.
	for _, body := range []string{
		`{"input":""}`,
		`{"input":"   "}`,
		`{"input":[]}`,
	} {
		messages := messagesOf(t, body)
		if len(messages) != 1 {
			t.Fatalf("%s produced %d messages, want 1", body, len(messages))
		}
		parts := partTexts(t, messageAt(t, messages, 0))
		if parts[0].(map[string]any)["text"] != "..." {
			t.Errorf("%s produced %v, want the \"...\" placeholder", body, parts[0])
		}
	}
}

func TestResponsesToChatRequest_NotConvertedWithoutInput(t *testing.T) {
	result := convertRequest(t, `{"model":"gpt-5","stream":true}`)

	if result.Converted {
		t.Error("a body with no input must be left for a Responses-native upstream")
	}
	if string(result.Body) != `{"model":"gpt-5","stream":true}` {
		t.Errorf("body = %s, want it untouched", result.Body)
	}
}

func TestResponsesToChatRequest_InstructionsBecomeSystemMessage(t *testing.T) {
	messages := messagesOf(t, `{"instructions":"be terse","input":"hi"}`)

	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(messages))
	}
	first := messageAt(t, messages, 0)
	if first["role"] != "system" || first["content"] != "be terse" {
		t.Errorf("first message = %v, want a system message with the instructions", first)
	}
}

func TestResponsesToChatRequest_StripsResponsesOnlyFields(t *testing.T) {
	converted := convertedBody(t, `{
		"input":"hi",
		"instructions":"be terse",
		"include":["reasoning.encrypted_content"],
		"store":false,
		"prompt_cache_key":"abc",
		"client_metadata":{"a":1},
		"max_output_tokens":512,
		"reasoning":{"effort":"high","summary":"auto"}
	}`)

	for _, key := range []string{"input", "instructions", "include", "store", "prompt_cache_key", "client_metadata", "max_output_tokens", "reasoning"} {
		if _, ok := converted[key]; ok {
			t.Errorf("%q leaked into the upstream body", key)
		}
	}
	if converted["max_tokens"] != float64(512) {
		t.Errorf("max_tokens = %v, want 512 mapped from max_output_tokens", converted["max_tokens"])
	}
	if converted["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v, want \"high\"", converted["reasoning_effort"])
	}
}

func TestResponsesToChatRequest_MaxTokensNotOverwritten(t *testing.T) {
	converted := convertedBody(t, `{"input":"hi","max_output_tokens":512,"max_tokens":64}`)
	if converted["max_tokens"] != float64(64) {
		t.Errorf("max_tokens = %v, want the explicit 64 to win", converted["max_tokens"])
	}
}

// --- content conversion ---

func TestResponsesToChatRequest_ContentBlockMapping(t *testing.T) {
	messages := messagesOf(t, `{"input":[{"type":"message","role":"user","content":[
		{"type":"input_text","text":"describe"},
		{"type":"output_text","text":"(echo)"},
		{"type":"input_image","image_url":"https://example.com/a.png","detail":"high"},
		{"type":"input_audio","input_audio":{"data":"zzz"}}
	]}]}`)

	parts := partTexts(t, messageAt(t, messages, 0))
	if len(parts) != 4 {
		t.Fatalf("got %d parts, want 4", len(parts))
	}
	if parts[0].(map[string]any)["type"] != "text" {
		t.Errorf("input_text mapped to %v, want text", parts[0].(map[string]any)["type"])
	}
	if parts[1].(map[string]any)["type"] != "text" {
		t.Errorf("output_text mapped to %v, want text", parts[1].(map[string]any)["type"])
	}
	image := parts[2].(map[string]any)
	if image["type"] != "image_url" {
		t.Errorf("input_image mapped to %v, want image_url", image["type"])
	}
	imageURL := image["image_url"].(map[string]any)
	if imageURL["url"] != "https://example.com/a.png" || imageURL["detail"] != "high" {
		t.Errorf("image_url = %v, want the url and detail carried over", imageURL)
	}
	if parts[3].(map[string]any)["type"] != "input_audio" {
		t.Errorf("unknown block %v was rewritten, want it passed through untouched", parts[3])
	}
}

func TestResponsesToChatRequest_ImageDetailDefaultsToAuto(t *testing.T) {
	messages := messagesOf(t, `{"input":[{"type":"message","role":"user","content":[
		{"type":"input_image","file_id":"file_123"}
	]}]}`)

	parts := partTexts(t, messageAt(t, messages, 0))
	imageURL := parts[0].(map[string]any)["image_url"].(map[string]any)
	if imageURL["url"] != "file_123" {
		t.Errorf("url = %v, want file_id used when image_url is absent", imageURL["url"])
	}
	if imageURL["detail"] != "auto" {
		t.Errorf("detail = %v, want the \"auto\" default", imageURL["detail"])
	}
}

func TestResponsesToChatRequest_StringContentPassesThrough(t *testing.T) {
	messages := messagesOf(t, `{"input":[{"type":"message","role":"user","content":"plain"}]}`)

	if got := messageAt(t, messages, 0)["content"]; got != "plain" {
		t.Errorf("content = %v, want the plain string preserved", got)
	}
}

// --- tool calls ---

func TestResponsesToChatRequest_ConsecutiveToolCallsShareOneMessage(t *testing.T) {
	messages := messagesOf(t, `{"input":[
		{"type":"function_call","call_id":"c1","name":"alpha","arguments":"{}"},
		{"type":"function_call","call_id":"c2","name":"beta","arguments":"{\"x\":1}"}
	]}`)

	if len(messages) != 1 {
		t.Fatalf("got %d messages, want 1 — consecutive calls belong to one assistant turn", len(messages))
	}
	assistant := messageAt(t, messages, 0)
	if assistant["role"] != "assistant" {
		t.Errorf("role = %v, want assistant", assistant["role"])
	}
	calls := assistant["tool_calls"].([]any)
	if len(calls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(calls))
	}
	first := calls[0].(map[string]any)
	if first["id"] != "c1" || first["type"] != "function" {
		t.Errorf("first call = %v, want id c1 as a function call", first)
	}
	fn := first["function"].(map[string]any)
	if fn["name"] != "alpha" || fn["arguments"] != "{}" {
		t.Errorf("first function = %v, want alpha with its arguments", fn)
	}
}

func TestResponsesToChatRequest_NamelessToolCallSkipped(t *testing.T) {
	// Codex and OpenAI reject a nameless call, so it must never reach the wire.
	messages := messagesOf(t, `{"input":[
		{"type":"function_call","call_id":"c1","name":"  ","arguments":"{}"}
	]}`)

	if len(messages) != 1 {
		t.Fatalf("got %d messages, want 1 (the nameless call dropped)", len(messages))
	}
	calls := messageAt(t, messages, 0)["tool_calls"].([]any)
	if len(calls) != 0 {
		t.Errorf("tool_calls = %v, want none", calls)
	}
}

func TestResponsesToChatRequest_ToolResultBecomesToolMessage(t *testing.T) {
	messages := messagesOf(t, `{"input":[
		{"type":"function_call","call_id":"c1","name":"alpha","arguments":"{}"},
		{"type":"function_call_output","call_id":"c1","output":"42"}
	]}`)

	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(messages))
	}
	result := messageAt(t, messages, 1)
	if result["role"] != "tool" {
		t.Errorf("role = %v, want tool", result["role"])
	}
	if result["tool_call_id"] != "c1" {
		t.Errorf("tool_call_id = %v, want c1 so the call correlates", result["tool_call_id"])
	}
	if result["content"] != "42" {
		t.Errorf("content = %v, want the string output as-is", result["content"])
	}
}

func TestResponsesToChatRequest_ObjectToolOutputIsStringified(t *testing.T) {
	messages := messagesOf(t, `{"input":[
		{"type":"function_call_output","call_id":"c1","output":{"ok":true}}
	]}`)

	content := messageAt(t, messages, 0)["content"]
	if content != `{"ok":true}` {
		t.Errorf("content = %v, want the object stringified once, not double-encoded", content)
	}
}

func TestResponsesToChatRequest_CustomToolCallIsRecorded(t *testing.T) {
	result := convertRequest(t, `{"input":[
		{"type":"custom_tool_call","call_id":"c1","name":"exec","input":"ls -la"}
	]}`)

	if len(result.CustomToolNames) != 1 || result.CustomToolNames[0] != "exec" {
		t.Errorf("CustomToolNames = %v, want [exec] so the response emits freeform input", result.CustomToolNames)
	}

	var out map[string]any
	if err := json.Unmarshal(result.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, leaked := out["_customToolNames"]; leaked {
		t.Error("_customToolNames must not reach the upstream body")
	}

	call := out["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	args := call["function"].(map[string]any)["arguments"]
	if args != `{"input":"ls -la"}` {
		t.Errorf("arguments = %v, want the freeform program wrapped in an input envelope", args)
	}
}

// --- reasoning ---

func TestResponsesToChatRequest_ReasoningAttachesToFollowingAssistant(t *testing.T) {
	messages := messagesOf(t, `{"input":[
		{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking hard"}],"encrypted_content":"blob"},
		{"type":"function_call","call_id":"c1","name":"alpha","arguments":"{}"}
	]}`)

	assistant := messageAt(t, messages, 0)
	if assistant["reasoning_content"] != "thinking hard" {
		t.Errorf("reasoning_content = %v, want the summary text", assistant["reasoning_content"])
	}
	if assistant["encrypted_content"] != "blob" {
		t.Errorf("encrypted_content = %v, want the continuity blob preserved", assistant["encrypted_content"])
	}
}

func TestResponsesToChatRequest_ReasoningTextDoesNotLeakIntoUserTurn(t *testing.T) {
	// Reasoning belongs to the assistant turn that follows it; a user turn in
	// between must drop the buffer rather than inherit it.
	messages := messagesOf(t, `{"input":[
		{"type":"reasoning","summary":[{"type":"summary_text","text":"thinking"}]},
		{"type":"message","role":"user","content":"next question"}
	]}`)

	first := messageAt(t, messages, 0)
	if first["role"] != "user" {
		t.Fatalf("first message role = %v, want user", first["role"])
	}
	if _, ok := first["reasoning_content"]; ok {
		t.Error("reasoning leaked onto a user turn")
	}
}

func TestResponsesToChatRequest_EncryptedContentAloneIsNotVisibleText(t *testing.T) {
	messages := messagesOf(t, `{"input":[
		{"type":"reasoning","encrypted_content":"blob"},
		{"type":"message","role":"assistant","content":"answer"}
	]}`)

	assistant := messageAt(t, messages, 0)
	if _, ok := assistant["reasoning_content"]; ok {
		t.Error("encrypted_content is continuity data, not displayable reasoning text")
	}
	if assistant["encrypted_content"] != "blob" {
		t.Errorf("encrypted_content = %v, want blob preserved", assistant["encrypted_content"])
	}
}

func TestResponsesToChatRequest_MultipleReasoningItemsJoin(t *testing.T) {
	messages := messagesOf(t, `{"input":[
		{"type":"reasoning","summary":[{"type":"summary_text","text":"first"}]},
		{"type":"reasoning","summary":[{"type":"summary_text","text":"second"}]},
		{"type":"message","role":"assistant","content":"answer"}
	]}`)

	if got := messageAt(t, messages, 0)["reasoning_content"]; got != "first\nsecond" {
		t.Errorf("reasoning_content = %v, want the parts joined by a newline", got)
	}
}

// --- tools ---

func TestResponsesToChatRequest_FunctionToolConverted(t *testing.T) {
	converted := convertedBody(t, `{"input":"hi","tools":[
		{"type":"function","name":"get_weather","description":"look it up","parameters":{"type":"object","properties":{"city":{"type":"string"}}},"strict":true}
	]}`)

	tools := converted["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "get_weather" || fn["description"] != "look it up" {
		t.Errorf("function = %v, want the name and description carried over", fn)
	}
	if fn["strict"] != true {
		t.Errorf("strict = %v, want true", fn["strict"])
	}
}

func TestResponsesToChatRequest_HostedToolWithoutNameDropped(t *testing.T) {
	// A hosted tool carries no name and cannot become a function declaration;
	// forwarding one reaches providers such as Gemini, which validate names.
	converted := convertedBody(t, `{"input":"hi","tools":[
		{"type":"request_user_input"},
		{"type":"function","name":"alpha","parameters":{"type":"object"}}
	]}`)

	tools := converted["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1 (the nameless hosted tool dropped)", len(tools))
	}
}

func TestResponsesToChatRequest_ObjectSchemaGainsProperties(t *testing.T) {
	converted := convertedBody(t, `{"input":"hi","tools":[
		{"type":"function","name":"alpha","parameters":{"type":"object"}}
	]}`)

	fn := converted["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	params := fn["parameters"].(map[string]any)
	props, ok := params["properties"].(map[string]any)
	if !ok || len(props) != 0 {
		t.Errorf("parameters = %v, want an empty properties object added", params)
	}
}

func TestResponsesToChatRequest_CustomToolBecomesInputFunction(t *testing.T) {
	result := convertRequest(t, `{"input":"hi","tools":[
		{"type":"custom","name":"exec","description":"run a program","format":{"syntax":"js","definition":"// js"}}
	]}`)

	if len(result.CustomToolNames) != 1 || result.CustomToolNames[0] != "exec" {
		t.Errorf("CustomToolNames = %v, want [exec] declared as a custom tool", result.CustomToolNames)
	}

	var out map[string]any
	if err := json.Unmarshal(result.Body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	fn := out["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "exec" {
		t.Errorf("name = %v, want exec", fn["name"])
	}
	if fn["type"] != nil {
		t.Errorf("type = %v, want a bare function object", fn["type"])
	}
	description, _ := fn["description"].(string)
	if description != "run a program\n\njs\n// js" {
		t.Errorf("description = %q, want the format hint appended after a blank line", description)
	}
	params := fn["parameters"].(map[string]any)
	if _, ok := params["properties"].(map[string]any)["input"]; !ok {
		t.Errorf("parameters = %v, want an input string property", params)
	}
}

func TestResponsesToChatRequest_AlreadyChatToolPassedThrough(t *testing.T) {
	converted := convertedBody(t, `{"input":"hi","tools":[
		{"type":"function","function":{"name":"alpha","description":"already chat shaped"}}
	]}`)

	tools := converted["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	if _, nested := tools[0].(map[string]any)["function"].(map[string]any)["function"]; nested {
		t.Error("an already-Chat tool was re-wrapped")
	}
}

func TestResponsesToChatRequest_AdditionalToolsMerged(t *testing.T) {
	converted := convertedBody(t, `{"input":[
		{"type":"additional_tools","tools":[{"type":"function","name":"from_input","parameters":{"type":"object"}}]},
		{"type":"message","role":"user","content":"hi"}
	],"tools":[{"type":"function","name":"from_body","parameters":{"type":"object"}}]}`)

	tools := converted["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2 (body tools plus additional_tools)", len(tools))
	}
	var names []string
	for _, tool := range tools {
		names = append(names, tool.(map[string]any)["function"].(map[string]any)["name"].(string))
	}
	if names[0] != "from_body" || names[1] != "from_input" {
		t.Errorf("tool names = %v, want body tools first then additional_tools", names)
	}
}

func TestResponsesToChatRequest_NoToolsLeavesFieldAbsent(t *testing.T) {
	converted := convertedBody(t, `{"input":"hi"}`)
	if _, ok := converted["tools"]; ok {
		t.Error("tools must stay absent when the request declared none")
	}
}
