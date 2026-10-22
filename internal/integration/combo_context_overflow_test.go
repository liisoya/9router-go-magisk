//go:build integration

package integration

import (
	"net/http"
	"strings"
	"testing"
)

// #338: a combo member that is simply too small for the request must not end the
// turn. When a provider answers 400 because the prompt exceeds that model's
// context window, the request says nothing about the account — the same prompt
// sent to a larger member of the same combo succeeds. Returning the 400 to the
// client made the operator's fallback useless exactly when it was needed.
const contextOverflowBody = `{"error":{"type":"invalid_request_error","message":"This model's maximum context length is 65536 tokens, however you requested 91234 tokens (91000 in your prompt; 0 for the completion). Please reduce your prompt.","code":"context_length_exceeded"}}`

func TestComboAdvancesPastAModelTooSmallForTheRequest(t *testing.T) {
	env := newEnv(t)

	small := env.NewUpstream(t, JSONResponder(http.StatusBadRequest, contextOverflowBody))
	large := env.NewUpstream(t, chatCompletionResponder())

	env.AddConnection(t, "conn-small", "deepseek", "Small Context", small, "sk-small")
	env.AddConnection(t, "conn-large", "groq", "Large Context", large, "sk-large")
	env.AddCombo(t, "combo-1", "wide", []string{"deepseek/deepseek-chat", "groq/llama-3.3-70b-versatile"})

	res := env.Post(t, "/v1/chat/completions", ChatBody("wide", false))
	if res.Status != http.StatusOK {
		t.Fatalf("POST /v1/chat/completions = %d, want 200 from the larger member (body: %s)",
			res.Status, truncate(res.Body))
	}
	if got := large.Count(); got != 1 {
		t.Fatalf("larger member received %d requests, want 1", got)
	}
	if got := large.Last(t).Model(t); got != "llama-3.3-70b-versatile" {
		t.Errorf("larger member was asked for %q, want its own combo model", got)
	}
}

// A context overflow is a fact about the model, not about the account, so the
// combo must move on after ONE attempt — not spend every remaining connection of
// the same too-small model on a request that cannot succeed. The number of
// requests the too-small member receives is the contract here: 10 connections
// means the fallback is delayed by ten pointless upstream round-trips.
func TestComboDoesNotRetryAModelTooSmallForTheRequest(t *testing.T) {
	env := newEnv(t)

	small := env.NewUpstream(t, JSONResponder(http.StatusBadRequest, contextOverflowBody))
	large := env.NewUpstream(t, chatCompletionResponder())

	env.AddConnection(t, "conn-small", "deepseek", "Small Context", small, "sk-small")
	env.AddConnection(t, "conn-large", "groq", "Large Context", large, "sk-large")
	env.AddCombo(t, "combo-1", "wide", []string{"deepseek/deepseek-chat", "groq/llama-3.3-70b-versatile"})

	if res := env.Post(t, "/v1/chat/completions", ChatBody("wide", false)); res.Status != http.StatusOK {
		t.Fatalf("POST /v1/chat/completions = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}

	if got := small.Count(); got != 1 {
		t.Errorf("too-small member received %d requests, want 1: an overflow is model-scoped, so the combo must not retry it",
			got)
	}
}

// Every member too small: the client gets the upstream 400 verbatim rather than
// a synthesized "all combo models failed". The operator needs to read the model
// limit to know what to change.
func TestComboTooSmallEverywhereReturnsTheUpstream400(t *testing.T) {
	env := newEnv(t)

	first := env.NewUpstream(t, JSONResponder(http.StatusBadRequest, contextOverflowBody))
	second := env.NewUpstream(t, JSONResponder(http.StatusBadRequest, contextOverflowBody))

	env.AddConnection(t, "conn-small-a", "deepseek", "Small A", first, "sk-a")
	env.AddConnection(t, "conn-small-b", "groq", "Small B", second, "sk-b")
	env.AddCombo(t, "combo-1", "wide", []string{"deepseek/deepseek-chat", "groq/llama-3.3-70b-versatile"})

	res := env.Post(t, "/v1/chat/completions", ChatBody("wide", false))
	if res.Status != http.StatusBadRequest {
		t.Fatalf("POST /v1/chat/completions = %d, want the upstream 400 (body: %s)",
			res.Status, truncate(res.Body))
	}
	if msg := res.ErrorMessage(t); !strings.Contains(msg, "maximum context length") {
		t.Errorf("error message = %q, want the upstream limit text", msg)
	}
}
