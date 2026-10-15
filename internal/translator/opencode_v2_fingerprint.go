package translator

import (
	json "encoding/json/v2"
	"strconv"
	"strings"
)

const openCodeReadDescription = "Read the contents of a file or directory. Supports text files, images, and PDFs. Images and PDFs are presented directly to the model. Each text line is prefixed by its 1-based line number as <line>: <content>. The prefix is for reference and is not part of the file content. Directory entries are returned one per line. Use offset and limit to read large files or directories in sections. Prefer one larger read over many small slices, and use grep to find specific content in large files."
const openCodeShellDescription = "Execute a shell command and return its output. Commands run on Linux using bash. Quote file paths containing spaces or special characters. Prefer dedicated tools over shell commands when possible. When output is large, the full result is saved to a file and a truncated preview is returned. Rely on automatic truncation unless filtering the output is more useful. Commands accept an optional timeout, background commands have no timeout by default. Background commands return immediately, and you will be notified when they complete. DO NOT poll for completion."

// ConcealOpenCodeV2FingerprintTools ports the OpenCode v2 read/shell
// fingerprint and canonicalizes the request so any caller body — a clean
// direct API client or a harness sending Claude/OpenAI shapes with extra
// fields — ends up in the form the /inference/v1 gate accepts
// (decolua/9router#4842):
//
//   - caller tools named exactly read/shell with a different definition
//     travel under a *_client alias (history and tool_choice retargeted);
//   - the exact OpenCode read/shell definitions are appended when absent;
//   - on the Responses lane every caller tool is rebuilt into the canonical
//     flat shape, dropping foreign keys (cache_control, strict, …),
//     defaulting missing schemas and dropping nameless entries, like the
//     upstream executor's normalizeResponsesTools; the exact definitions are
//     appended afterwards so they keep strict:false;
//   - on the chat lane caller tools keep their shape but gain a default
//     object schema when missing, so no stub schema reaches the gate;
//   - tool_choice passes through untouched, except on the Responses lane,
//     where an explicit choice becomes "auto" on auto-only models (the
//     upstream forceAutoToolChoiceModels quirk) and a forced function choice
//     naming no declared tool is dropped.
//
// flat is true for Responses API bodies and false for Chat Completions.
func ConcealOpenCodeV2FingerprintTools(body []byte, flat bool, forceAutoChoice bool) ([]byte, map[string]string) {
	var request map[string]any
	if len(body) == 0 || json.Unmarshal(body, &request) != nil {
		return body, map[string]string{}
	}
	tools, _ := request["tools"].([]any)
	nameMap := make(map[string]string)
	taken, seen := make(map[string]bool), make(map[string]bool)
	for _, tool := range tools {
		if name := toolNameOf(tool); name != "" {
			taken[name] = true
		}
	}
	kept := make([]any, 0, len(tools)+2)
	for _, tool := range tools {
		name := toolNameOf(tool)
		if name != "read" && name != "shell" {
			kept = append(kept, tool)
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		if isExactOpenCodeV2Definition(tool, name) {
			kept = append(kept, tool)
			continue
		}
		alias := name + "_client"
		for i := 2; taken[alias]; i++ {
			alias = name + "_client" + strconv.Itoa(i)
		}
		taken[alias], nameMap[alias] = true, name
		setToolName(tool, alias)
		kept = append(kept, tool)
	}
	request["tools"] = kept

	var validNames map[string]bool
	droppedChoice := false
	if flat {
		validNames = canonicalizeOpenCodeV2ResponsesTools(request)
	} else {
		defaultOpenCodeV2ChatToolSchemas(request)
	}
	if len(nameMap) > 0 {
		aliasOpenCodeV2History(request, nameMap)
		aliasOpenCodeV2Choice(request, nameMap)
	}
	if flat {
		dropNamelessOpenCodeV2FunctionCalls(request)
	}
	// Append OpenCode's exact definitions after canonicalization, mirroring
	// the upstream executor (normalizeResponsesTools runs before
	// applyFingerprintTools): the appended tools keep strict:false on the
	// Responses lane, and the forced-choice check below only sees
	// caller-declared names.
	//
	// The presence check runs on post-alias names, like upstream's
	// appendMissingFingerprintTools: a caller read/shell with a different
	// definition travels under a *_client alias, so the name is absent and
	// the exact definition is still appended. Checking the pre-alias `seen`
	// set instead would send no exact fingerprint at all and the gate 403s.
	current, _ := request["tools"].([]any)
	present := make(map[string]bool, 2)
	for _, tool := range current {
		if name := toolNameOf(tool); name == "read" || name == "shell" {
			present[name] = true
		}
	}
	var appended []string
	for _, name := range []string{"read", "shell"} {
		if present[name] {
			continue
		}
		tool, err := openCodeV2Tool(name, flat)
		if err != nil {
			return body, map[string]string{}
		}
		current = append(current, tool)
		appended = append(appended, name)
	}
	request["tools"] = current
	droppedChoice = normalizeOpenCodeV2ToolChoice(request, flat, forceAutoChoice, validNames)
	if flat && request["tool_choice"] == nil && !droppedChoice {
		request["tool_choice"] = "auto"
	}
	if len(appended) > 0 {
		// Verbatim from the upstream executor (noteUnavailableTools): the
		// gate does not inspect prompt text, but the model does — backticks,
		// singular/plural, and all.
		quoted := make([]string, 0, len(appended))
		for _, name := range appended {
			quoted = append(quoted, "`"+name+"`")
		}
		list := strings.Join(quoted, " or ")
		var note string
		if len(appended) > 1 {
			note = "Do not call the " + list + " tools; they are placeholders that do nothing. Use the other tools you have been given instead."
		} else {
			note = "Do not call the " + list + " tool; it is a placeholder that does nothing. Use the other tools you have been given instead."
		}
		if flat {
			if s, ok := request["instructions"].(string); ok && strings.TrimSpace(s) != "" {
				request["instructions"] = s + "\n\n" + note
			} else {
				request["instructions"] = note
			}
		} else {
			addOpenCodeChatNote(request, note)
		}
	}
	out, err := json.Marshal(request, json.Deterministic(true))
	if err != nil {
		return body, map[string]string{}
	}
	return out, nameMap
}

// isExactOpenCodeV2Definition reports whether tool already carries OpenCode's
// verbatim description and parameter schema for the fingerprint name.
func isExactOpenCodeV2Definition(tool any, name string) bool {
	toolMap, ok := tool.(map[string]any)
	if !ok {
		return false
	}
	definition := toolMap
	if fn, ok := toolMap["function"].(map[string]any); ok {
		definition = fn
	}
	wantDescription := openCodeReadDescription
	if name == "shell" {
		wantDescription = openCodeShellDescription
	}
	params, _ := json.Marshal(definition["parameters"], json.Deterministic(true))
	return definition["description"] == wantDescription && string(params) == openCodeV2Parameters(name)
}

// openCodeV2DefaultSchema is the fallback the upstream executor applies when a
// tool carries no usable parameter schema: the gate rejects stub schemas.
func openCodeV2DefaultSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

// usableOpenCodeV2Parameters returns the tool's parameter schema when it is a
// JSON object, and reports whether the caller's entry (flat or nested) had one
// at all.
func usableOpenCodeV2Parameters(tool map[string]any) (map[string]any, bool) {
	if params, ok := tool["parameters"].(map[string]any); ok {
		return params, true
	}
	if fn, ok := tool["function"].(map[string]any); ok {
		if params, ok := fn["parameters"].(map[string]any); ok {
			return params, true
		}
	}
	return nil, false
}

// canonicalizeOpenCodeV2ResponsesTools rebuilds every tool in the canonical
// Responses shape (open-sse/executors/opencode.js normalizeResponsesTools):
// only type/name/description/parameters survive, the name is trimmed and
// capped, nameless or non-object entries are dropped, and a missing schema
// becomes the default object. It returns the set of names that survived.
func canonicalizeOpenCodeV2ResponsesTools(request map[string]any) map[string]bool {
	tools, _ := request["tools"].([]any)
	out := make([]any, 0, len(tools))
	valid := make(map[string]bool)
	for _, tool := range tools {
		m, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(toolNameOf(m))
		if name == "" {
			continue
		}
		if len(name) > 128 {
			name = name[:128]
		}
		description, _ := m["description"].(string)
		if fn, ok := m["function"].(map[string]any); ok {
			if d, ok := fn["description"].(string); ok && description == "" {
				description = d
			}
		}
		params, _ := usableOpenCodeV2Parameters(m)
		if params == nil {
			params = openCodeV2DefaultSchema()
		} else if params["type"] == "object" && params["properties"] == nil {
			params["properties"] = map[string]any{}
		}
		entry := map[string]any{"type": "function", "name": name, "parameters": params}
		if description != "" {
			entry["description"] = description
		}
		valid[name] = true
		out = append(out, entry)
	}
	request["tools"] = out
	return valid
}

// defaultOpenCodeV2ChatToolSchemas leaves chat-shaped caller tools alone but
// gives any tool without a usable parameter schema the default object schema,
// in whichever shape (flat or nested under function) it already uses.
func defaultOpenCodeV2ChatToolSchemas(request map[string]any) {
	tools, _ := request["tools"].([]any)
	for _, tool := range tools {
		m, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		if _, has := usableOpenCodeV2Parameters(m); has {
			continue
		}
		if fn, ok := m["function"].(map[string]any); ok {
			fn["parameters"] = openCodeV2DefaultSchema()
			continue
		}
		if _, hasName := m["name"]; hasName {
			m["parameters"] = openCodeV2DefaultSchema()
		}
	}
}

// dropNamelessOpenCodeV2FunctionCalls removes prior-turn function_call items
// with no name; the Responses validator rejects them and a harness history can
// carry one (open-sse/executors/opencode.js sanitizeResponsesItems).
func dropNamelessOpenCodeV2FunctionCalls(request map[string]any) {
	input, ok := request["input"].([]any)
	if !ok {
		return
	}
	out := make([]any, 0, len(input))
	for _, item := range input {
		m, ok := item.(map[string]any)
		if ok && m["type"] == "function_call" {
			if name, _ := m["name"].(string); strings.TrimSpace(name) == "" {
				continue
			}
		}
		out = append(out, item)
	}
	request["input"] = out
}

// normalizeOpenCodeV2ToolChoice ports the upstream tool_choice handling
// (open-sse/executors/opencode.js): the Responses lane rewrites any explicit
// choice to "auto" on auto-only models (the forceAutoToolChoiceModels quirk)
// and drops a forced function choice naming no declared tool. Anything else —
// including "none" and the chat lane — passes through untouched, exactly like
// upstream. It reports whether the choice was dropped, so the caller does not
// refill the slot with a default.
func normalizeOpenCodeV2ToolChoice(request map[string]any, flat, forceAutoChoice bool, validNames map[string]bool) bool {
	if !flat {
		return false
	}
	choice, present := request["tool_choice"]
	if !present || choice == nil {
		return false
	}
	if s, ok := choice.(string); ok {
		if s != "auto" && forceAutoChoice {
			request["tool_choice"] = "auto"
		}
		return false
	}
	m, ok := choice.(map[string]any)
	if !ok {
		return false
	}
	if forceAutoChoice {
		request["tool_choice"] = "auto"
		return false
	}
	if m["type"] == "function" {
		name, _ := m["name"].(string)
		if !validNames[strings.TrimSpace(name)] {
			delete(request, "tool_choice")
			return true
		}
	}
	return false
}

func openCodeV2Parameters(name string) string {
	if name == "read" {
		return "{\"additionalProperties\":false,\"properties\":{\"limit\":{\"description\":\"The maximum number of lines or directory entries to read (defaults to and capped at 2000)\",\"minimum\":0,\"type\":\"integer\"},\"offset\":{\"description\":\"The line or directory entry to start reading from (1-based)\",\"minimum\":0,\"type\":\"integer\"},\"path\":{\"description\":\"File or directory to read\",\"type\":\"string\"}},\"required\":[\"path\"],\"type\":\"object\"}"
	}
	return "{\"additionalProperties\":false,\"properties\":{\"background\":{\"description\":\"Run the command in the background and return immediately (useful for dev servers and long-running builds). You do not need to use '&' at the end of the command when using this parameter. You will be notified when it completes. DO NOT poll for completion.\",\"type\":\"boolean\"},\"command\":{\"description\":\"Shell command string to execute\",\"type\":\"string\"},\"timeout\":{\"description\":\"Timeout in milliseconds. Set to 0 to disable the timeout. Defaults to 120000 for foreground commands. Background commands have no timeout by default.\",\"minimum\":0,\"type\":\"integer\"},\"workdir\":{\"description\":\"Working directory to execute the command in. Defaults to the current working directory. When possible, avoid changing directories in the command and set the working directory here instead.\",\"type\":\"string\"}},\"required\":[\"command\"],\"type\":\"object\"}"
}

func openCodeV2Tool(name string, flat bool) (map[string]any, error) {
	parameters := openCodeV2Parameters(name)
	var schema map[string]any
	if err := json.Unmarshal([]byte(parameters), &schema); err != nil {
		return nil, err
	}
	description := openCodeReadDescription
	if name == "shell" {
		description = openCodeShellDescription
	}
	if flat {
		// Upstream appends flat tools with strict:false and asserts it.
		return map[string]any{"type": "function", "name": name, "description": description, "parameters": schema, "strict": false}, nil
	}
	return map[string]any{"type": "function", "function": map[string]any{"name": name, "description": description, "parameters": schema}}, nil
}

func aliasOpenCodeV2History(request map[string]any, sentToOriginal map[string]string) {
	aliases := make(map[string]string, len(sentToOriginal))
	for alias, original := range sentToOriginal {
		aliases[original] = alias
	}
	if input, ok := request["input"].([]any); ok {
		for _, item := range input {
			if m, ok := item.(map[string]any); ok && m["type"] == "function_call" {
				if n, ok := m["name"].(string); ok && aliases[n] != "" {
					m["name"] = aliases[n]
				}
			}
		}
	}
	if messages, ok := request["messages"].([]any); ok {
		for _, msg := range messages {
			m, ok := msg.(map[string]any)
			if !ok {
				continue
			}
			calls, _ := m["tool_calls"].([]any)
			for _, call := range calls {
				c, ok := call.(map[string]any)
				if !ok {
					continue
				}
				fn, _ := c["function"].(map[string]any)
				if n, ok := fn["name"].(string); ok && aliases[n] != "" {
					fn["name"] = aliases[n]
				}
			}
		}
	}
}

func aliasOpenCodeV2Choice(request map[string]any, sentToOriginal map[string]string) {
	aliases := make(map[string]string, len(sentToOriginal))
	for alias, original := range sentToOriginal {
		aliases[original] = alias
	}
	choice, ok := request["tool_choice"].(map[string]any)
	if !ok {
		return
	}
	if name, ok := choice["name"].(string); ok && aliases[name] != "" {
		choice["name"] = aliases[name]
	}
	if fn, ok := choice["function"].(map[string]any); ok {
		if name, ok := fn["name"].(string); ok && aliases[name] != "" {
			fn["name"] = aliases[name]
		}
	}
}

func addOpenCodeChatNote(request map[string]any, note string) {
	// Claude-shaped bodies carry `system` instead of a leading system
	// message; upstream returns early for them without a note.
	if _, hasSystem := request["system"]; hasSystem {
		return
	}
	messages, ok := request["messages"].([]any)
	if !ok {
		return
	}
	if len(messages) > 0 {
		if first, ok := messages[0].(map[string]any); ok && first["role"] == "system" {
			if content, ok := first["content"].(string); ok {
				first["content"] = content + "\n\n" + note
				return
			}
		}
	}
	message := map[string]any{"role": "system", "content": note}
	if system, ok := request["system"].(string); ok && strings.TrimSpace(system) != "" {
		message["content"] = system + "\n\n" + note
	}
	request["messages"] = append([]any{message}, messages...)
}
