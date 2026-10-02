//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"

	json "encoding/json/v2"
)

// TestChatCompletionForwardsToUpstream is the core gateway contract: a client
// request with an API key reaches a configured provider, the outbound envelope
// is rewritten to the bare upstream model, the connection's credential is
// presented, and the upstream body comes back to the client untouched.
func TestChatCompletionForwardsToUpstream(t *testing.T) {
	env, upstream := newProviderEnv(t)

	res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false))
	if res.Status != http.StatusOK {
		t.Fatalf("POST /v1/chat/completions = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}

	var completion struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	res.Decode(t, &completion)
	if completion.Object != "chat.completion" {
		t.Errorf("object = %q, want \"chat.completion\"", completion.Object)
	}
	if len(completion.Choices) != 1 {
		t.Fatalf("choices = %d, want 1 (body: %s)", len(completion.Choices), truncate(res.Body))
	}
	if got := completion.Choices[0].Message.Content; got != "upstream reply" {
		t.Errorf("content = %q, want the upstream content verbatim", got)
	}
	if got := completion.Choices[0].FinishReason; got != "stop" {
		t.Errorf("finish_reason = %q, want the upstream finish_reason", got)
	}

	sent := upstream.Last(t)
	if sent.Method != http.MethodPost {
		t.Errorf("upstream method = %s, want POST", sent.Method)
	}
	if sent.Path != "/chat/completions" {
		t.Errorf("upstream path = %q, want /chat/completions (the connection baseUrl is used verbatim)", sent.Path)
	}
	// The provider prefix is a 9router routing concept; the upstream only
	// knows the bare model id.
	if got := sent.Model(t); got != "deepseek-chat" {
		t.Errorf("upstream model = %q, want \"deepseek-chat\" (the prefix must be stripped)", got)
	}
	if got := sent.Header.Get("Authorization"); got != "Bearer sk-upstream" {
		t.Errorf("upstream Authorization = %q, want the connection credential", got)
	}
	if got := sent.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("upstream Content-Type = %q, want application/json", got)
	}
	if got := sent.Header.Get("Accept"); strings.Contains(got, "text/event-stream") {
		t.Errorf("upstream Accept = %q, a non-stream request must not ask for SSE", got)
	}
}

// TestChatCompletionPreservesClientPayload pins that only the model field is
// rewritten on the way out. Everything else the client sent — messages,
// temperature, tools — must reach the provider, or a gateway change silently
// degrades real client behaviour.
func TestChatCompletionPreservesClientPayload(t *testing.T) {
	env, upstream := newProviderEnv(t)

	res := env.Post(t, "/v1/chat/completions", map[string]any{
		"model":       "deepseek/deepseek-chat",
		"stream":      false,
		"temperature": 0.25,
		"max_tokens":  256,
		"messages": []map[string]any{
			{"role": "system", "content": "be terse"},
			{"role": "user", "content": "Summarize the Go memory model."},
		},
		"tools": []map[string]any{
			{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}},
		},
	})
	if res.Status != http.StatusOK {
		t.Fatalf("POST /v1/chat/completions = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}

	var forwarded struct {
		Model       string  `json:"model"`
		Temperature float64 `json:"temperature"`
		MaxTokens   int     `json:"max_tokens"`
		Messages    []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	sent := upstream.Last(t)
	if err := json.Unmarshal(sent.Body, &forwarded); err != nil {
		t.Fatalf("decode forwarded payload: %v", err)
	}

	if forwarded.Temperature != 0.25 {
		t.Errorf("temperature = %v, want 0.25", forwarded.Temperature)
	}
	if forwarded.MaxTokens != 256 {
		t.Errorf("max_tokens = %d, want 256", forwarded.MaxTokens)
	}
	if len(forwarded.Messages) != 2 {
		t.Fatalf("messages = %d, want 2 (body: %s)", len(forwarded.Messages), truncate(sent.Body))
	}
	if forwarded.Messages[0].Role != "system" || forwarded.Messages[1].Content != "Summarize the Go memory model." {
		t.Errorf("messages = %+v, want the client conversation unchanged", forwarded.Messages)
	}
	if len(forwarded.Tools) != 1 || forwarded.Tools[0].Function.Name != "lookup" {
		t.Errorf("tools = %+v, want the client tool definitions unchanged", forwarded.Tools)
	}
}

// TestUpstreamErrorIsRelayedVerbatim pins the contract every OpenAI-compatible
// client depends on: a provider rejection reaches the caller with the
// provider's own status and body, not a generic 500. A non-retryable status
// must not be retried on another account, because the request itself was bad.
func TestUpstreamErrorIsRelayedVerbatim(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantStatus  int
		wantMessage string
	}{
		{
			name:        "bad request stays a 400",
			status:      http.StatusBadRequest,
			body:        `{"error":{"message":"context length exceeded","type":"invalid_request_error"}}`,
			wantStatus:  http.StatusBadRequest,
			wantMessage: "context length exceeded",
		},
		{
			name:        "not found stays a 404",
			status:      http.StatusNotFound,
			body:        `{"error":{"message":"model does not exist","type":"invalid_request_error"}}`,
			wantStatus:  http.StatusNotFound,
			wantMessage: "model does not exist",
		},
		{
			name:        "server error stays a 500",
			status:      http.StatusInternalServerError,
			body:        `{"error":{"message":"upstream exploded","type":"server_error"}}`,
			wantStatus:  http.StatusInternalServerError,
			wantMessage: "upstream exploded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEnv(t)
			upstream := env.NewUpstream(t, JSONResponder(tt.status, tt.body))
			env.AddConnection(t, "conn-deepseek", "deepseek", "DeepSeek Integration", upstream, "sk-upstream")

			res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false))
			if res.Status != tt.wantStatus {
				t.Fatalf("POST /v1/chat/completions = %d, want %d (body: %s)",
					res.Status, tt.wantStatus, truncate(res.Body))
			}
			if got := res.ErrorMessage(t); got != tt.wantMessage {
				t.Errorf("relayed message = %q, want the upstream message %q verbatim", got, tt.wantMessage)
			}
			// A non-retryable status must not spend another account.
			if got := upstream.Count(); got != 1 {
				t.Errorf("upstream received %d requests, want 1 (a non-retryable status must not rotate accounts)", got)
			}
		})
	}
}

// TestProviderWithoutConnectionReturnsBadGateway pins the client-facing status
// for the most common misconfiguration: a model whose provider has no account
// configured. A 502 tells the operator to add a connection; a 400 would tell
// them to fix a request that is already correct.
func TestProviderWithoutConnectionReturnsBadGateway(t *testing.T) {
	env := newEnv(t)

	res := env.Post(t, "/v1/chat/completions", ChatBody("deepseek/deepseek-chat", false))
	if res.Status != http.StatusBadGateway {
		t.Fatalf("POST /v1/chat/completions with no connection = %d, want 502 (body: %s)",
			res.Status, truncate(res.Body))
	}
	if msg := res.ErrorMessage(t); !strings.Contains(msg, "connection") {
		t.Errorf("error message = %q, want it to name the missing connection", msg)
	}
}

// TestMalformedChatRequestsAreRejected covers the request-validation boundary.
// Each body must be refused before any upstream call is attempted, so a broken
// client cannot burn provider quota.
func TestMalformedChatRequestsAreRejected(t *testing.T) {
	tests := []struct {
		name string
		body any
	}{
		{name: "not json at all", body: "this is not json"},
		{name: "truncated json", body: `{"model":`},
		{name: "missing model", body: map[string]any{"messages": []any{}}},
		{name: "empty model", body: map[string]any{"model": "", "messages": []any{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, upstream := newProviderEnv(t)

			res := env.Post(t, "/v1/chat/completions", tt.body)
			if res.Status != http.StatusBadRequest {
				t.Fatalf("POST /v1/chat/completions = %d, want 400 (body: %s)", res.Status, truncate(res.Body))
			}
			if got := upstream.Count(); got != 0 {
				t.Errorf("upstream received %d requests, want 0: an invalid request must not reach a provider", got)
			}
		})
	}
}
