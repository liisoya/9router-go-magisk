package executor

import (
	"bytes"
	"testing"
)

// An OpenAI-shaped client echoes back the `reasoning_content` this proxy
// streamed to it. Replayed assistant turns used to keep that field through the
// OpenAI→Claude conversion, and Anthropic rejects any key a message object does
// not define:
//
//	400 messages.7.reasoning_content: Extra inputs are not permitted
//
// Because the offending turn stays in the client's history, every later request
// of that conversation fails, not just the one that introduced it. The field
// cannot be turned into a `thinking` block either: the signature Anthropic
// needs to accept one back is not in the OpenAI shape.

// TestEnsureClaudeMessages_DropsReplayedReasoningContent drives both conversion
// paths — an assistant turn carrying tool_calls and a plain replayed one — and
// asserts on the body the upstream receives.
func TestEnsureClaudeMessages_DropsReplayedReasoningContent(t *testing.T) {
	body := []byte(`{"model":"cc/claude-sonnet-5-5","max_tokens":2048,"messages":[` +
		`{"role":"user","content":"list the files"},` +
		`{"role":"assistant","content":"","reasoning_content":"I should call bash",` +
		`"tool_calls":[{"id":"toolu_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]},` +
		`{"role":"tool","tool_call_id":"toolu_1","content":"PROJETO.md"},` +
		`{"role":"assistant","content":"done","reasoning_content":"no tool call here","refusal":null}` +
		`]}`)

	out := EnsureClaudeMessages(body, "cc/claude-sonnet-5-5")

	for _, key := range []string{"reasoning_content", "refusal", "tool_calls"} {
		if bytes.Contains(out, []byte(`"`+key+`"`)) {
			t.Errorf("%q reached the Claude body: %s", key, out)
		}
	}
	// The turns themselves must survive: dropping the field is not dropping history.
	for _, want := range []string{`"type":"tool_use"`, `"tool_use_id":"toolu_1"`, `"content":"done"`} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("conversion lost %s: %s", want, out)
		}
	}
}

// TestEnsureClaudeMessages_MessageKeysAreAllowlisted: the fix is an allowlist,
// not a denylist of today's known reasoning fields, so a message key Anthropic
// has never heard of is dropped too — otherwise the next client shape reopens
// the same 400.
func TestEnsureClaudeMessages_MessageKeysAreAllowlisted(t *testing.T) {
	body := []byte(`{"messages":[{"role":"assistant","content":"hi",` +
		`"reasoning":{"effort":"high"},"reasoning_details":[{"type":"reasoning.text"}],` +
		`"annotations":[],"audio":{"id":"a_1"}}]}`)

	out := EnsureClaudeMessages(body, "m")

	for _, key := range []string{"reasoning", "reasoning_details", "annotations", "audio"} {
		if bytes.Contains(out, []byte(`"`+key+`"`)) {
			t.Errorf("%q survived: %s", key, out)
		}
	}
	if !bytes.Contains(out, []byte(`"content":"hi"`)) {
		t.Errorf("the assistant text was dropped: %s", out)
	}
}

// TestEnsureClaudeMessages_ThinkingDisplaySurvivesTheCleanup guards the
// interaction with the request-side thinking capture: reasoning_effort is the
// input that asks for thinking, and it is read before the messages are rebuilt.
func TestEnsureClaudeMessages_ThinkingDisplaySurvivesTheCleanup(t *testing.T) {
	out := EnsureClaudeMessages(
		[]byte(`{"reasoning_effort":"high","messages":[{"role":"assistant","content":"hi","reasoning_content":"x"}]}`),
		"claude-haiku-4-5",
	)

	if bytes.Contains(out, []byte(`"reasoning_content"`)) {
		t.Errorf("reasoning_content survived: %s", out)
	}
	if !WantsThinkingSummaries(out) {
		t.Errorf("the thinking request was lost with the messages rebuild: %s", out)
	}
}
