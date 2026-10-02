//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	json "encoding/json/v2"
)

// TestMessagesTranslatesToAnOpenAIUpstream pins the Claude-Code path against a
// non-Anthropic provider: the gateway must translate the Messages request into
// OpenAI chat format, and translate the OpenAI reply back into a Messages
// response. Getting this wrong breaks Claude Code entirely while every
// OpenAI-format test still passes.
func TestMessagesTranslatesToAnOpenAIUpstream(t *testing.T) {
	env, upstream := newProviderEnv(t)

	res := env.Post(t, "/v1/messages", claudeMessagesBody("deepseek/deepseek-chat", false))
	if res.Status != http.StatusOK {
		t.Fatalf("POST /v1/messages = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}

	// The client must receive the Anthropic shape, not the upstream's.
	var message struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	res.Decode(t, &message)
	if message.Type != "message" {
		t.Errorf("type = %q, want \"message\" (the Anthropic Messages shape)", message.Type)
	}
	if message.Role != "assistant" {
		t.Errorf("role = %q, want assistant", message.Role)
	}
	if len(message.Content) == 0 {
		t.Fatalf("content = %v, want at least one content block", message.Content)
	}
	if message.Content[0].Type != "text" || message.Content[0].Text != "upstream reply" {
		t.Errorf("content[0] = %+v, want the upstream reply in a text block", message.Content[0])
	}
	if message.StopReason != "end_turn" {
		t.Errorf("stop_reason = %q, want end_turn translated from finish_reason \"stop\"", message.StopReason)
	}

	// The upstream must have been called with the OpenAI body, not the Messages
	// body: OpenAI providers reject "system" at the top level.
	sent := upstream.Last(t)
	if got := sent.Model(t); got != "deepseek-chat" {
		t.Errorf("upstream model = %q, want deepseek-chat", got)
	}
	var forwarded struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		System  string `json:"system"`
		MaxToks int    `json:"max_tokens"`
	}
	if err := json.Unmarshal(sent.Body, &forwarded); err != nil {
		t.Fatalf("decode forwarded body: %v", err)
	}
	if len(forwarded.Messages) != 2 {
		t.Fatalf("upstream messages = %+v, want the system prompt folded in plus the user turn", forwarded.Messages)
	}
	if forwarded.Messages[0].Role != "system" || forwarded.Messages[0].Content != "be terse" {
		t.Errorf("messages[0] = %+v, want the system prompt as the first message", forwarded.Messages[0])
	}
	if forwarded.Messages[1].Role != "user" || forwarded.Messages[1].Content != "Summarize the Go memory model." {
		t.Errorf("messages[1] = %+v, want the user turn carried through", forwarded.Messages[1])
	}
	if forwarded.System != "" {
		t.Errorf("upstream body still has a top-level system field (%q), which OpenAI providers reject", forwarded.System)
	}
	if forwarded.MaxToks != 512 {
		t.Errorf("max_tokens = %d, want 512 carried through the translation", forwarded.MaxToks)
	}
}

// TestMessagesRejectsAnInvalidBody pins the validation boundary on the Claude
// route: a body the gateway cannot parse or route is a client error, refused
// before any provider call.
func TestMessagesRejectsAnInvalidBody(t *testing.T) {
	tests := []struct {
		name string
		body any
	}{
		{name: "not json", body: "{oops"},
		{name: "missing model", body: map[string]any{"max_tokens": 64, "messages": []any{}}},
		{name: "empty model", body: map[string]any{"model": "", "max_tokens": 64, "messages": []any{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, upstream := newProviderEnv(t)

			res := env.Post(t, "/v1/messages", tt.body)
			if res.Status != http.StatusBadRequest {
				t.Fatalf("POST /v1/messages = %d, want 400 (body: %s)", res.Status, truncate(res.Body))
			}
			if got := upstream.Count(); got != 0 {
				t.Errorf("upstream received %d requests, want 0", got)
			}
		})
	}
}

// TestMessagesSurfacesTheUpstreamError pins that a provider rejection on the
// Claude route is relayed with the provider's own status. Claude Code decides
// whether to retry or to surface a quota message from that status, so a
// rewritten 500 would turn a clear "quota exceeded" into an opaque crash.
func TestMessagesSurfacesTheUpstreamError(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, JSONResponder(http.StatusTooManyRequests,
		`{"error":{"message":"account quota exhausted","type":"rate_limit_error"}}`))
	env.AddConnection(t, "conn-deepseek", "deepseek", "DeepSeek Account", upstream, "sk-deepseek")

	res := env.Post(t, "/v1/messages", claudeMessagesBody("deepseek/deepseek-chat", false))
	if res.Status != http.StatusTooManyRequests {
		t.Fatalf("POST /v1/messages = %d, want 429 (body: %s)", res.Status, truncate(res.Body))
	}
	if msg := res.ErrorMessage(t); !strings.Contains(msg, "quota") {
		t.Errorf("error message = %q, want the upstream quota reason", msg)
	}
}
