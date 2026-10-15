package translator

import (
	json "encoding/json/v2"
	"strings"
	"testing"
)

func TestConcealOpenCodeV2FingerprintToolsInjects(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		flat       bool
	}{
		{"chat", "{\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}", false},
		{"responses", "{\"input\":\"hi\"}", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := ConcealOpenCodeV2FingerprintTools([]byte(tc.body), tc.flat, false)
			var got map[string]any
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			tools, _ := got["tools"].([]any)
			if len(tools) != 2 {
				t.Fatalf("got %d tools, want read and shell", len(tools))
			}
			if tc.flat {
				if got["tool_choice"] != "auto" {
					t.Errorf("tool_choice=%v, want auto", got["tool_choice"])
				}
				if !strings.Contains(got["instructions"].(string), "placeholders") {
					t.Error("missing placeholder instruction")
				}
			} else {
				messages := got["messages"].([]any)
				if messages[0].(map[string]any)["role"] != "system" {
					t.Error("missing placeholder system note")
				}
			}
		})
	}
}

func TestConcealOpenCodeV2AliasesCallerReadAndHistory(t *testing.T) {
	body := []byte("{\"messages\":[{\"role\":\"assistant\",\"tool_calls\":[{\"function\":{\"name\":\"read\"}}]}],\"tools\":[{\"type\":\"function\",\"function\":{\"name\":\"read\",\"description\":\"caller read\",\"parameters\":{\"type\":\"object\",\"properties\":{}}}}],\"tool_choice\":{\"type\":\"function\",\"function\":{\"name\":\"read\"}}}")
	out, names := ConcealOpenCodeV2FingerprintTools(body, false, false)
	if names["read_client"] != "read" {
		t.Fatalf("alias map = %v", names)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	messages := got["messages"].([]any)
	call := messages[len(messages)-1].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
	if call["name"] != "read_client" {
		t.Errorf("history tool name=%v", call["name"])
	}
	choice := got["tool_choice"].(map[string]any)["function"].(map[string]any)
	if choice["name"] != "read_client" {
		t.Errorf("tool choice name=%v", choice["name"])
	}
}

// A harness body carries shapes a clean direct request never has. Each case
// pins one such shape to the form the /inference/v1 gate accepts, so whatever
// the caller sends no longer decides between 200 and 403.
func TestConcealOpenCodeV2CanonicalizesHarnessShapes(t *testing.T) {
	t.Run("responses tools are rebuilt into the flat shape and foreign keys dropped", func(t *testing.T) {
		body := []byte(`{"input":"hi","tools":[
			{"type":"function","name":"Read","description":"caller read","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]},"cache_control":{"type":"ephemeral"},"strict":true},
			{"type":"function","name":"","parameters":{"type":"object"}},
			"not-an-object"
		]}`)
		out, _ := ConcealOpenCodeV2FingerprintTools(body, true, false)
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		tools, _ := got["tools"].([]any)
		// The caller's Read survives (foreign keys stripped) plus the two
		// injected fingerprints, which keep the upstream strict:false.
		if len(tools) != 3 {
			t.Fatalf("got %d tools, want 3: %s", len(tools), out)
		}
		for _, tool := range tools {
			m := tool.(map[string]any)
			for _, foreign := range []string{"cache_control", "function"} {
				if _, has := m[foreign]; has {
					t.Errorf("tool %v still carries foreign key %q", m["name"], foreign)
				}
			}
			if _, has := m["name"]; !has {
				t.Errorf("tool missing canonical name: %v", m)
			}
			params, _ := m["parameters"].(map[string]any)
			if params == nil || params["type"] != "object" {
				t.Errorf("tool %v missing default schema: %v", m["name"], m["parameters"])
			}
		}
	})

	t.Run("chat tools without a schema gain the default object schema", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"Task","description":"run a task"}}]}`)
		out, _ := ConcealOpenCodeV2FingerprintTools(body, false, false)
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		tools, _ := got["tools"].([]any)
		for _, tool := range tools {
			m := tool.(map[string]any)
			fn := m["function"].(map[string]any)
			params, _ := fn["parameters"].(map[string]any)
			if params == nil || params["type"] != "object" {
				t.Errorf("tool %v left without a schema: %v", fn["name"], fn["parameters"])
			}
		}
	})

	t.Run("tool_choice passes through untouched, like upstream", func(t *testing.T) {
		for _, tc := range []struct {
			name, body string
			flat       bool
			want       any
		}{
			{"chat none string", `{"messages":[],"tool_choice":"none"}`, false, "none"},
			{"chat none object", `{"messages":[],"tool_choice":{"type":"none"}}`, false, map[string]any{"type": "none"}},
			{"responses none string", `{"input":[],"tool_choice":"none"}`, true, "none"},
			{"responses auto object", `{"input":[],"tool_choice":{"type":"auto","disable_parallel_tool_use":true}}`, true, map[string]any{"type": "auto"}},
			{"chat forced function", `{"messages":[],"tool_choice":{"type":"function","function":{"name":"Task"}}}`, false, "preserved"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				out, _ := ConcealOpenCodeV2FingerprintTools([]byte(tc.body), tc.flat, false)
				var got map[string]any
				if err := json.Unmarshal(out, &got); err != nil {
					t.Fatal(err)
				}
				switch want := tc.want.(type) {
				case string:
					if want == "preserved" {
						if _, ok := got["tool_choice"].(map[string]any); !ok {
							t.Errorf("tool_choice=%v, want the forced choice preserved: %s", got["tool_choice"], out)
						}
						return
					}
					if got["tool_choice"] != want {
						t.Errorf("tool_choice=%v, want %q (upstream never rewrites it): %s", got["tool_choice"], want, out)
					}
				default:
					choice, ok := got["tool_choice"].(map[string]any)
					if !ok || choice["type"] != want.(map[string]any)["type"] {
						t.Errorf("tool_choice=%v, want it preserved: %s", got["tool_choice"], out)
					}
				}
			})
		}
	})

	t.Run("auto-only models get a responses forced choice rewritten to auto", func(t *testing.T) {
		// Upstream applies the forceAutoToolChoiceModels quirk on the
		// Responses lane only; a chat-lane choice is never rewritten.
		out, _ := ConcealOpenCodeV2FingerprintTools([]byte(`{"input":[],"tool_choice":{"type":"tool","name":"Task"}}`), true, true)
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if got["tool_choice"] != "auto" {
			t.Errorf("tool_choice=%v, want auto for an auto-only model: %s", got["tool_choice"], out)
		}

		chatOut, _ := ConcealOpenCodeV2FingerprintTools([]byte(`{"messages":[],"tool_choice":{"type":"function","function":{"name":"Task"}}}`), false, true)
		var chatGot map[string]any
		if err := json.Unmarshal(chatOut, &chatGot); err != nil {
			t.Fatal(err)
		}
		if _, ok := chatGot["tool_choice"].(map[string]any); !ok {
			t.Errorf("tool_choice=%v, want the chat-lane choice preserved: %s", chatGot["tool_choice"], chatOut)
		}
	})

	t.Run("responses drops a forced choice naming no declared tool", func(t *testing.T) {
		body := []byte(`{"input":"hi","tools":[{"type":"function","name":"Task","parameters":{"type":"object","properties":{}}}],"tool_choice":{"type":"function","name":"DoesNotExist"}}`)
		out, _ := ConcealOpenCodeV2FingerprintTools(body, true, false)
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if _, has := got["tool_choice"]; has {
			t.Errorf("tool_choice=%v, want dropped (names no declared tool): %s", got["tool_choice"], out)
		}
	})

	t.Run("responses keeps a forced choice naming a declared tool", func(t *testing.T) {
		body := []byte(`{"input":"hi","tools":[{"type":"function","name":"Task","parameters":{"type":"object","properties":{}}}],"tool_choice":{"type":"function","name":"Task"}}`)
		out, _ := ConcealOpenCodeV2FingerprintTools(body, true, false)
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		choice, ok := got["tool_choice"].(map[string]any)
		if !ok {
			t.Fatalf("tool_choice=%v, want the forced choice preserved: %s", got["tool_choice"], out)
		}
		if choice["name"] != "Task" {
			t.Errorf("forced choice name=%v, want Task", choice["name"])
		}
	})

	t.Run("nameless prior function_call items are dropped from responses input", func(t *testing.T) {
		body := []byte(`{"input":[
			{"type":"function_call","call_id":"call_1","arguments":"{}"},
			{"type":"function_call","call_id":"call_2","name":"Task","arguments":"{}"},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}
		]}`)
		out, _ := ConcealOpenCodeV2FingerprintTools(body, true, false)
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		input, _ := got["input"].([]any)
		for _, item := range input {
			m := item.(map[string]any)
			if m["type"] == "function_call" {
				if name, _ := m["name"].(string); strings.TrimSpace(name) == "" {
					t.Errorf("nameless function_call survived: %s", out)
				}
			}
		}
	})

	t.Run("an exact caller read definition is kept without aliasing", func(t *testing.T) {		tool, err := openCodeV2Tool("read", false)
		if err != nil {
			t.Fatal(err)
		}
		toolJSON, err := json.Marshal(tool)
		if err != nil {
			t.Fatal(err)
		}
		body := []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[` + string(toolJSON) + `]}`)
		out, names := ConcealOpenCodeV2FingerprintTools(body, false, false)
		if len(names) != 0 {
			t.Errorf("alias map = %v, want empty (definition already exact)", names)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		tools, _ := got["tools"].([]any)
		if len(tools) != 2 {
			t.Fatalf("got %d tools, want the exact caller read plus the injected shell: %s", len(tools), out)
		}
		messages, _ := got["messages"].([]any)
		for _, msg := range messages {
			m := msg.(map[string]any)
			if m["role"] == "system" && strings.Contains(m["content"].(string), "Do not call the read") {
				t.Errorf("read was marked a placeholder even though the caller declared it exactly: %s", out)
			}
		}
	})
}

// The v2 gate compares the appended definitions byte for byte: descriptions,
// parameter schemas, and strict:false must match the OpenCode v2 CLI exactly
// (open-sse/utils/opencodeFingerprint.js FINGERPRINT_TOOL_DEFINITIONS).
func TestConcealOpenCodeV2MatchesUpstreamGateContract(t *testing.T) {
	out, _ := ConcealOpenCodeV2FingerprintTools([]byte(`{"input":"hi"}`), true, false)
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	tools, _ := got["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want read and shell: %s", len(tools), out)
	}
	byName := map[string]map[string]any{}
	for _, tool := range tools {
		m := tool.(map[string]any)
		byName[m["name"].(string)] = m
	}
	read, shell := byName["read"], byName["shell"]
	if read == nil || shell == nil {
		t.Fatalf("want read and shell, got %s", out)
	}
	if !strings.HasPrefix(read["description"].(string), "Read the contents of a file or directory.") {
		t.Errorf("read description diverges from upstream: %q", read["description"])
	}
	if !strings.HasPrefix(shell["description"].(string), "Execute a shell command and return its output.") {
		t.Errorf("shell description diverges from upstream: %q", shell["description"])
	}
	shellParams, _ := shell["parameters"].(map[string]any)
	props, _ := shellParams["properties"].(map[string]any)
	background, _ := props["background"].(map[string]any)
	if desc, _ := background["description"].(string); !strings.Contains(desc, "useful for dev servers and long-running builds") || !strings.Contains(desc, "when it completes. DO NOT poll") {
		t.Errorf("shell.background diverges from the verbatim upstream text: %q", desc)
	}
	if read["strict"] != false || shell["strict"] != false {
		t.Errorf("flat fingerprint tools must carry strict:false like upstream: %s", out)
	}
	readParams, _ := read["parameters"].(map[string]any)
	readProps, _ := readParams["properties"].(map[string]any)
	if _, ok := readProps["path"]; !ok {
		t.Errorf("read parameters must require path: %s", out)
	}
	instructions, _ := got["instructions"].(string)
	if !strings.Contains(instructions, "Do not call the `read` or `shell` tools; they are placeholders that do nothing.") {
		t.Errorf("placeholder note diverges from upstream wording: %q", instructions)
	}
}

// A lone appended tool gets the singular note; a Claude-shaped body (top-level
// system key) gets no note at all — both verbatim upstream behavior.
func TestConcealOpenCodeV2PlaceholderNoteShapes(t *testing.T) {
	shellOnly, err := openCodeV2Tool("shell", false)
	if err != nil {
		t.Fatal(err)
	}
	shellJSON, err := json.Marshal(shellOnly)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"messages":[{"role":"user","content":"hi"}],"tools":[` + string(shellJSON) + `]}`)
	out, _ := ConcealOpenCodeV2FingerprintTools(body, false, false)
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	messages, _ := got["messages"].([]any)
	if len(messages) == 0 {
		t.Fatalf("want a system note prepended: %s", out)
	}
	first, _ := messages[0].(map[string]any)
	content, _ := first["content"].(string)
	// The caller declared shell exactly, so only read is appended — and the
	// note uses the singular upstream wording for it.
	if !strings.Contains(content, "Do not call the `read` tool; it is a placeholder that does nothing.") {
		t.Errorf("want the singular upstream note, got %q", content)
	}

	claudeBody := []byte(`{"system":"you are helpful","messages":[{"role":"user","content":"hi"}]}`)
	claudeOut, _ := ConcealOpenCodeV2FingerprintTools(claudeBody, false, false)
	var claudeGot map[string]any
	if err := json.Unmarshal(claudeOut, &claudeGot); err != nil {
		t.Fatal(err)
	}
	claudeMessages, _ := claudeGot["messages"].([]any)
	for _, msg := range claudeMessages {
		m := msg.(map[string]any)
		if m["role"] == "system" {
			t.Errorf("Claude-shaped bodies must not gain a system note: %s", claudeOut)
		}
	}
}

// A caller tool reusing an exact fingerprint name with a different definition
// is aliased AND the exact definition is still appended: the gate 403s when
// no exact read/shell declaration travels (upstream appendMissingFingerprintTools
// checks post-alias names, so the port must too).
func TestConcealOpenCodeV2CollidingNamesStillAppendExact(t *testing.T) {
	for _, tc := range []struct {
		name string
		flat bool
	}{
		{"chat", false},
		{"responses", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"tools":[{"type":"function","name":"read","description":"caller read"},{"type":"function","name":"read_client","description":"x"},{"type":"function","name":"Edit","description":"caller edit"}]}`)
			out, names := ConcealOpenCodeV2FingerprintTools(body, tc.flat, false)
			if names["read_client2"] != "read" {
				t.Fatalf("alias map = %v, want read_client2 -> read", names)
			}
			var got map[string]any
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatal(err)
			}
			tools, _ := got["tools"].([]any)
			var order []string
			for _, tool := range tools {
				m := tool.(map[string]any)
				// Chat-lane tools keep whatever shape they arrived in (flat
				// or nested), exactly like upstream's withToolName.
				if name, ok := m["name"].(string); ok && name != "" {
					order = append(order, name)
				} else if fn, ok := m["function"].(map[string]any); ok {
					name, _ := fn["name"].(string)
					order = append(order, name)
				} else {
					t.Fatalf("tool lost its name: %v", m)
				}
			}
			want := []string{"read_client2", "read_client", "Edit", "read", "shell"}
			if strings.Join(order, ",") != strings.Join(want, ",") {
				t.Errorf("tool order = %v, want %v: %s", order, want, out)
			}
		})
	}
}
