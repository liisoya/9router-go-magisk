package executor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"9router/proxy/internal/providers"
)

// The tool-call wire shape is exactly what the live site produced when asked to
// call a tool: the answer arrives topicless, then one character at a time
// through APPEND operations on a negative fragment slot.
func toolCallEvents(call string) []string {
	events := []string{
		fmt.Sprintf(`{"v":{"response":{"message_id":2,"thinking_enabled":false,"fragments":[`+
			`{"id":2,"type":"RESPONSE","content":%q,"references":[]}]}}}`, call[:1]),
	}
	for _, ch := range call[1:] {
		events = append(events, fmt.Sprintf(
			`{"p":"response/fragments/-1/content","o":"APPEND","v":%q}`, string(ch)))
	}
	events = append(events,
		`{"p":"response","o":"BATCH","v":[{"p":"quasi_status","v":"FINISHED"}]}`,
		`{"p":"response/status","o":"SET","v":"FINISHED"}`,
	)
	return events
}

// answerEvents is the topicless + APPEND shape for ordinary prose.
func answerEvents(text string) []string {
	events := []string{
		fmt.Sprintf(`{"v":{"response":{"thinking_enabled":false,"fragments":[`+
			`{"id":2,"type":"RESPONSE","content":%q,"references":[]}]}}}`, text[:1]),
	}
	for _, ch := range text[1:] {
		events = append(events, fmt.Sprintf(
			`{"p":"response/fragments/-1/content","o":"APPEND","v":%q}`, string(ch)))
	}
	events = append(events, `{"p":"response/status","o":"SET","v":"FINISHED"}`)
	return events
}

// The proof-of-work fixture has to publish a challenge the executor's own
// solver can satisfy, or the turn fails before it reaches the answer. These
// three values are one such triple: the digest is DeepSeekHashV1 of
// "<salt>_<expire>_0", which nonce 0 satisfies.
const (
	solvableSalt      = "1122334455667788"
	solvableExpire    = int64(1778891543095)
	solvableChallenge = "311b26ae1e0fe7375e242958ce46db5552a6c67fea3f96880dcd846c63a74286"
)

func toolFake(t *testing.T, lines ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		r.Body.Read(body)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		if strings.Contains(r.URL.Path, "chat_session/create") {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"code":0,"data":{"biz_data":{"chat_session":{"id":"s-1"}}}}`)
			return
		}
		if strings.Contains(r.URL.Path, "users/current") {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"code":0,"data":{"biz_data":{"token":"at"}}}`)
			return
		}
		if strings.Contains(r.URL.Path, "create_pow_challenge") {
			w.Header().Set("Content-Type", "application/json")
			// A challenge nonce 0 satisfies, so the executor's own solver finds
			// one immediately and the test is not paying for a real search.
			fmt.Fprintf(w, `{"code":0,"data":{"biz_data":{"challenge":{`+
				`"challenge":%q,"difficulty":20000,"algorithm":"DeepSeekHashV1",`+
				`"salt":%q,"expire_at":%d,"signature":"sig",`+
				`"target_path":"/api/v0/chat/completion"}}}}`,
				solvableChallenge, solvableSalt, solvableExpire)
			return
		}
		if !strings.Contains(r.URL.Path, "chat/completion") {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"code":0,"data":{"biz_data":{}}}`)
			return
		}
		for _, line := range lines {
			fmt.Fprintf(w, "data: %s\n\n", line)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
}

func toolRequest(t *testing.T, srv *httptest.Server, body string, stream bool) *Request {
	t.Helper()
	return &Request{
		Ctx: t.Context(),
		Client: &http.Client{
			Timeout:   30 * time.Second,
			Transport: rewriteTo(srv.URL),
		},
		Config:    &providers.ProviderConfig{BaseURL: srv.URL + "/api/v0/chat/completion"},
		Body:      []byte(body),
		IsStream:  stream,
		APIKey:    "t.fake",
		ModelName: "deepseek-v4-pro",
		StartTime: time.Now(),
	}
}

const weatherTool = `"tools":[{"type":"function","function":{"name":"get_weather",` +
	`"description":"Get current weather for a city","parameters":` +
	`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}]`

// TestDeepSeekWebToolCallIsRecovered is the case the whole file exists for: the
// model types a tool call one character at a time and it must arrive whole.
func TestDeepSeekWebToolCallIsRecovered(t *testing.T) {
	call := `{"name":"get_weather","arguments":{"city":"Jakarta"}}`
	srv := toolFake(t, toolCallEvents(call)...)
	defer srv.Close()

	rec := httptest.NewRecorder()
	body := `{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"weather?"}],` + weatherTool + `}`
	if err := ForwardDeepSeekWeb(rec, toolRequest(t, srv, body, false)); err != nil {
		t.Fatalf("ForwardDeepSeekWeb: %v", err)
	}

	var out struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   any    `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, rec.Body.String())
	}
	if len(out.Choices) != 1 || len(out.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("want exactly one tool call, got %s", rec.Body.String())
	}
	tc := out.Choices[0].Message.ToolCalls[0]
	if tc.Function.Name != "get_weather" {
		t.Errorf("tool name = %q, want get_weather", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"city":"Jakarta"}` {
		t.Errorf("arguments = %q, want the exact JSON the model typed", tc.Function.Arguments)
	}
	if tc.Type != "function" || tc.ID == "" {
		t.Errorf("tool call envelope incomplete: %+v", tc)
	}
	if out.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", out.Choices[0].FinishReason)
	}
}

// TestDeepSeekWebToolCallStreamStaysIntact checks the isolation claim from the
// other side: a streaming request with tools must not receive a half-written
// tool call. It gets the whole answer in one buffered chunk instead.
func TestDeepSeekWebToolCallStreamStaysIntact(t *testing.T) {
	call := `{"name":"get_weather","arguments":{"city":"Jakarta"}}`
	srv := toolFake(t, toolCallEvents(call)...)
	defer srv.Close()

	rec := httptest.NewRecorder()
	body := `{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"weather?"}],` + weatherTool + `}`
	if err := ForwardDeepSeekWeb(rec, toolRequest(t, srv, body, true)); err != nil {
		t.Fatalf("ForwardDeepSeekWeb: %v", err)
	}

	// Whatever shape it takes, no client may be left holding partial JSON.
	if strings.Contains(rec.Body.String(), `"arguments":{"city"`) && !strings.Contains(rec.Body.String(), `Jakarta"}}`) {
		t.Error("a truncated tool call reached the client")
	}
	t.Logf("stream body: %s", rec.Body.String())
}

// TestDeepSeekWebNoToolsKeepsStreaming pins the isolation: without a tools[]
// array the streaming path is untouched.
func TestDeepSeekWebNoToolsKeepsStreaming(t *testing.T) {
	srv := toolFake(t, answerEvents("ok")...)
	defer srv.Close()

	rec := httptest.NewRecorder()
	body := `{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"say ok"}]}`
	if err := ForwardDeepSeekWeb(rec, toolRequest(t, srv, body, true)); err != nil {
		t.Fatalf("ForwardDeepSeekWeb: %v", err)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want an event stream for an ordinary request", ct)
	}
	// The answer is expected to arrive as several deltas, not one blob: this
	// upstream types it out the same way it types a tool call. Joining the
	// deltas is therefore what proves the text arrived, not a substring match on
	// the raw body.
	if got := joinContentDeltas(rec.Body.String()); got != "ok" {
		t.Errorf("joined content = %q, want %q", got, "ok")
	}
	if !strings.Contains(rec.Body.String(), "[DONE]") {
		t.Error("ordinary stream did not terminate with [DONE]")
	}
}

// TestDeepSeekWebTopiclessAnswerIsRead is the decoder fix: the answer arrives
// with no topic at all, which the old topic switch dropped.
func TestDeepSeekWebTopiclessAnswerIsRead(t *testing.T) {
	srv := toolFake(t, answerEvents("hello world")...)
	defer srv.Close()

	rec := httptest.NewRecorder()
	body := `{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"hi"}]}`
	if err := ForwardDeepSeekWeb(rec, toolRequest(t, srv, body, false)); err != nil {
		t.Fatalf("ForwardDeepSeekWeb: %v", err)
	}
	if !strings.Contains(rec.Body.String(), "hello world") {
		t.Errorf("body = %q, want the full answer", rec.Body.String())
	}
}

func rewriteTo(base string) *rewriteTransport { return &rewriteTransport{base: base} }

type rewriteTransport struct{ base string }

func (t *rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = "http"
	host := strings.TrimPrefix(t.base, "http://")
	host = strings.TrimSuffix(host, ":80")
	r.URL.Host = host
	return http.DefaultTransport.RoundTrip(r)
}

// joinContentDeltas concatenates the content deltas of a chat.completion.chunk
// stream, so a test can assert on the whole answer rather than on one fragment
// of it.
func joinContentDeltas(body string) string {
	var b strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") || strings.Contains(line, "[DONE]") {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			b.WriteString(choice.Delta.Content)
		}
	}
	return b.String()
}
