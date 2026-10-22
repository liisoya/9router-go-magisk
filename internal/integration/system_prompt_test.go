//go:build integration

package integration

import (
	json "encoding/json/v2"
	"strings"
	"testing"
)

// TestSystemPromptSettingsAndOrdering pins the global system prompt feature
// across the real integration router: settings persistence via PUT /api/settings
// and dual-slot prefix/suffix injection into a chat completion request.
func TestSystemPromptSettingsAndOrdering(t *testing.T) {
	env := newEnv(t)

	upstream := env.NewUpstream(t, chatCompletionResponder())
	env.AddConnection(t, "conn-sys", "deepseek", "System Prompt Test", upstream, "sk-sys")

	// 1. Update settings to enable global system prompt with a prefix and suffix.
	settingsBody, err := json.Marshal(map[string]any{
		"systemPromptEnabled": true,
		"systemPromptPrefix":  "INTEGRATION_PREFIX",
		"systemPromptSuffix":  "INTEGRATION_SUFFIX",
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}

	putRes := env.Put(t, "/api/settings", string(settingsBody))
	if putRes.Status != 200 {
		t.Fatalf("PUT /api/settings = %d, want 200 (body: %s)", putRes.Status, putRes.Body)
	}

	// 2. Send a chat completion request and verify the upstream received body.
	res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false))
	if res.Status != 200 {
		t.Fatalf("POST /v1/chat/completions = %d, want 200 (body: %s)", res.Status, res.Body)
	}

	lastReq := upstream.Last(t)
	reqBody := lastReq.Body
	var req map[string]any
	if err := json.Unmarshal([]byte(reqBody), &req); err != nil {
		t.Fatalf("unmarshal upstream body: %v\nbody was: %s", err, reqBody)
	}

	messages, ok := req["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("upstream messages missing or empty: %s", reqBody)
	}

	firstMsg, _ := messages[0].(map[string]any)
	content, _ := firstMsg["content"].(string)

	if !strings.HasPrefix(content, "INTEGRATION_PREFIX") {
		t.Errorf("system prompt %q must start with prefix INTEGRATION_PREFIX", content)
	}
	if !strings.HasSuffix(content, "INTEGRATION_SUFFIX") {
		t.Errorf("system prompt %q must end with suffix INTEGRATION_SUFFIX", content)
	}
}
