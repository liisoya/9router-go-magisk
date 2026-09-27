package executor

import (
	json "encoding/json/v2"
	"testing"
)

// TestBuildResponsesBody_CodexResponsesLite covers the request shape Codex 0.155
// expects from gpt-6-sol / gpt-6-luna: tools and instructions travel as an
// input prefix, the top-level fields are cleared, and reasoning carries
// all_turns with no summary. Without it those two models 400 — the catalogue
// entry alone is not enough.
func TestBuildResponsesBody_CodexResponsesLite(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantPrefix []string
		wantEffort string
		// keepFields marks a body that arrived already carrying the lite prefix:
		// upstream then leaves the top-level fields alone, so the move assertions
		// do not apply.
		keepFields bool
	}{
		{
			name:       "chat completions with instructions and tools",
			body:       `{"model":"gpt-6-sol","instructions":"be brief","tools":[{"type":"function","function":{"name":"get_weather","description":"w","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],"messages":[{"role":"user","content":"hi"}]}`,
			wantPrefix: []string{"additional_tools", "message", "message"},
			wantEffort: "medium",
		},
		{
			name:       "effort from the client is kept",
			body:       `{"model":"gpt-6-luna","reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`,
			wantPrefix: []string{"additional_tools", "message"},
			wantEffort: "high",
		},
		{
			name:       "responses body already shaped still gets the prefix",
			body:       `{"model":"gpt-6-sol(high)","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"instructions":"be brief"}`,
			wantPrefix: []string{"additional_tools", "message", "message"},
			// The "(high)" suffix is a model-id override, not a reasoning effort;
			// Go does not fold it into reasoning.effort, so the lite default stands.
			wantEffort: "medium",
		},
		{
			// Upstream leaves an already-prefixed body completely alone — it only
			// guards against wrapping twice — so instructions and tools stay put.
			name:       "a prefix the client supplied is not double-wrapped",
			body:       `{"model":"gpt-6-sol","input":[{"type":"additional_tools","role":"developer","tools":[]},{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"instructions":"be brief"}`,
			wantPrefix: []string{"additional_tools", "message"},
			wantEffort: "medium",
			keepFields: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, _, err := buildResponsesBody([]byte(tt.body))
			if err != nil {
				t.Fatalf("buildResponsesBody: %v", err)
			}

			var req map[string]any
			if err := json.Unmarshal(out, &req); err != nil {
				t.Fatalf("decode %s: %v", out, err)
			}

			if !tt.keepFields {
				if got, _ := req["instructions"].(string); got != "" {
					t.Errorf("instructions = %q, want empty (moved into the prefix)", got)
				}
				if got, present := req["tools"]; present && got != nil {
					t.Errorf("tools = %v, want nil (moved into the prefix)", got)
				}
				if got, _ := req["tool_choice"].(string); got != "auto" {
					t.Errorf("tool_choice = %q, want auto", got)
				}
				if got, ok := req["parallel_tool_calls"].(bool); !ok || got {
					t.Errorf("parallel_tool_calls = %v, want false", req["parallel_tool_calls"])
				}
			}

			input, _ := req["input"].([]any)
			if len(input) != len(tt.wantPrefix) {
				t.Fatalf("input has %d items, want %d: %v", len(input), len(tt.wantPrefix), input)
			}
			for i, wantType := range tt.wantPrefix {
				m, _ := input[i].(map[string]any)
				if got, _ := m["type"].(string); got != wantType {
					t.Errorf("input[%d].type = %q, want %q", i, got, wantType)
				}
			}

			reasoning, _ := req["reasoning"].(map[string]any)
			if reasoning == nil {
				t.Fatalf("no reasoning block: %s", out)
			}
			if _, has := reasoning["summary"]; has {
				t.Error("reasoning.summary must be absent on a lite model")
			}
			if got, _ := reasoning["context"].(string); got != "all_turns" {
				t.Errorf("reasoning.context = %q, want all_turns", got)
			}
			if got, _ := reasoning["effort"].(string); got != tt.wantEffort {
				t.Errorf("reasoning.effort = %q, want %q", got, tt.wantEffort)
			}
		})
	}
}

// TestBuildResponsesBody_ClassicCodexUnchanged is the other half: every other
// Codex model keeps the shape it had, so this stays additive.
func TestBuildResponsesBody_ClassicCodexUnchanged(t *testing.T) {
	body := `{"model":"gpt-5.6-sol","instructions":"be brief","tools":[{"type":"function","function":{"name":"get_weather","description":"w","parameters":{"type":"object"}}}],"reasoning_effort":"high","messages":[{"role":"user","content":"hi"}]}`

	out, _, err := buildResponsesBody([]byte(body))
	if err != nil {
		t.Fatalf("buildResponsesBody: %v", err)
	}

	var req map[string]any
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}

	if got, _ := req["instructions"].(string); got != "be brief" {
		t.Errorf("instructions = %q, want the caller's text to stay top-level", got)
	}
	if _, has := req["tools"]; !has {
		t.Error("tools should stay top-level on a classic model")
	}
	if _, has := req["parallel_tool_calls"]; has {
		t.Error("parallel_tool_calls must not be added to a classic model")
	}

	reasoning, _ := req["reasoning"].(map[string]any)
	if got, _ := reasoning["summary"].(string); got != "auto" {
		t.Errorf("reasoning.summary = %q, want auto on a classic model", got)
	}
	if _, has := reasoning["context"]; has {
		t.Error("reasoning.context is a lite-only field")
	}

	for _, item := range req["input"].([]any) {
		if m, _ := item.(map[string]any); m["type"] == "additional_tools" {
			t.Error("a classic model must not get the lite prefix")
		}
	}
}

func TestIsCodexResponsesLiteModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "gpt-6-sol", want: true},
		{model: "gpt-6-luna", want: true},
		{model: "gpt-6-sol(high)", want: true}, // the level suffix is a request override
		{model: "gpt-6-astra", want: false},
		{model: "gpt-5.6-sol", want: false},
		{model: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := isCodexResponsesLiteModel(tt.model); got != tt.want {
				t.Errorf("isCodexResponsesLiteModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}
