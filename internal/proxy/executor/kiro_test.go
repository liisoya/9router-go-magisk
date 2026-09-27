package executor

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/providers"
)

func TestKiroUpstreamBody_ConvertsOpenAIToConversationState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		// The gateway answers 400 on anything without conversationState; assert it here.
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Errorf("upstream got non-JSON: %v", err)
		}
		if _, ok := m["conversationState"]; !ok {
			t.Errorf("upstream body missing conversationState (would 400): %s", data)
		}
		if _, ok := m["systemPrompt"]; ok {
			t.Error("upstream body must not carry top-level systemPrompt")
		}
		// Empty eventstream so handleKiroStream completes without events.
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &providers.ProviderConfig{BaseURL: srv.URL, AuthHeader: "Authorization", AuthScheme: "bearer"}
	rec := httptest.NewRecorder()
	body := []byte(`{"model":"kr/claude-sonnet-4.5","messages":[{"role":"user","content":"hi"}],"stream":false}`)
	_ = ForwardKiro(rec, &Request{
		Client:    srv.Client(),
		Config:    cfg,
		APIKey:    "bearer-token",
		Body:      body,
		IsStream:  false,
		ModelName: "claude-sonnet-4.5",
	})
	// Error is fine (empty eventstream); the wire-format assertion is the point.
}

func TestKiroUpstreamBody_PassesThroughConversationState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		// cleanKiroBody may normalize the envelope, but it must stay an
		// envelope (not be re-wrapped from an OpenAI body).
		cs, ok := m["conversationState"].(map[string]any)
		if !ok {
			t.Errorf("MITM passthrough lost conversationState: %s", data)
		}
		if _, hasMessages := m["messages"]; hasMessages {
			t.Error("MITM passthrough should not inject an OpenAI messages array")
		}
		if cs["conversationId"] != "x" {
			t.Errorf("passthrough conversationId changed: %v", cs["conversationId"])
		}
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &providers.ProviderConfig{BaseURL: srv.URL, AuthHeader: "Authorization", AuthScheme: "bearer"}
	rec := httptest.NewRecorder()
	body := []byte(`{"conversationState":{"chatTriggerType":"MANUAL","conversationId":"x","currentMessage":{"userInputMessage":{"content":"hi","modelId":"m","origin":"AI_EDITOR"}},"history":[]}}`)
	_ = ForwardKiro(rec, &Request{
		Client: srv.Client(),
		Config: cfg,
		APIKey: "bearer-token",
		Body:   body,
	})
}

func TestKiroUpstreamBody_StripsProviderPrefixAndSuffixes(t *testing.T) {
	var sent string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		sent = string(data)
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &providers.ProviderConfig{BaseURL: srv.URL, AuthHeader: "Authorization", AuthScheme: "bearer"}
	rec := httptest.NewRecorder()
	body := []byte(`{"model":"kr/claude-sonnet-4.5-thinking","messages":[{"role":"user","content":"hi"}]}`)
	_ = ForwardKiro(rec, &Request{
		Client: srv.Client(),
		Config: cfg,
		APIKey: "bearer-token",
		Body:   body,
	})
	if !strings.Contains(sent, `"modelId":"claude-sonnet-4.5"`) {
		t.Errorf("expected stripped modelId claude-sonnet-4.5, got %s", sent)
	}
	if strings.Contains(sent, "kr/") {
		t.Errorf("provider prefix leaked into body: %s", sent)
	}
}
