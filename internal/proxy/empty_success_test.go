package proxy

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// A 200 that carries no answer must be reported as a retryable failure, or
// the router reads it as a served turn and never leaves the model.
func TestEmptyUpstreamError(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantFail bool
	}{
		{
			name:     "blank body",
			body:     "",
			wantFail: true,
		},
		{
			name:     "whitespace only body",
			body:     "  \n\t ",
			wantFail: true,
		},
		{
			name:     "html error page served with 200",
			body:     "<!DOCTYPE html><html><body>502 Bad Gateway</body></html>",
			wantFail: true,
		},
		{
			name:     "bare text",
			body:     "upstream unavailable",
			wantFail: true,
		},
		{
			name:     "truncated json",
			body:     `{"choices":[{"message":{"content":"hi"`,
			wantFail: true,
		},
		{
			name:     "error envelope behind a 200",
			body:     `{"error":{"message":"model overloaded","type":"server_error","code":503}}`,
			wantFail: true,
		},
		{
			name:     "error envelope as a string",
			body:     `{"error":"no capacity"}`,
			wantFail: true,
		},
		{
			name:     "choices is empty",
			body:     `{"id":"chatcmpl-1","object":"chat.completion","choices":[]}`,
			wantFail: true,
		},
		{
			name:     "choice with empty content",
			body:     `{"id":"chatcmpl-1","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`,
			wantFail: true,
		},
		{
			name:     "choice with whitespace-only content",
			body:     `{"choices":[{"message":{"content":"   "}}]}`,
			wantFail: true,
		},
		{
			name:     "claude message with empty content array",
			body:     `{"id":"msg_1","type":"message","role":"assistant","content":[],"stop_reason":"end_turn"}`,
			wantFail: true,
		},
		{
			name:     "responses with empty output marked completed",
			body:     `{"id":"resp_1","object":"response","status":"completed","output":[]}`,
			wantFail: true,
		},
		{
			name:     "body naming no completion carrier at all",
			body:     `{"id":"chatcmpl-1","object":"chat.completion"}`,
			wantFail: true,
		},
		{
			name:     "ordinary chat completion",
			body:     `{"id":"chatcmpl-1","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`,
			wantFail: false,
		},
		{
			name:     "empty content alongside tool calls is a real answer",
			body:     `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"c1","function":{"name":"go","arguments":"{}"}}]}}]}`,
			wantFail: false,
		},
		{
			name:     "empty content alongside reasoning is a real answer",
			body:     `{"choices":[{"message":{"content":"","reasoning_content":"thinking..."}}]}`,
			wantFail: false,
		},
		{
			name:     "empty content alongside a refusal is a real answer",
			body:     `{"choices":[{"message":{"content":"","refusal":"I cannot help with that"}}]}`,
			wantFail: false,
		},
		{
			name:     "empty content alongside a function call is a real answer",
			body:     `{"choices":[{"message":{"content":"","function_call":{"name":"go","arguments":"{}"}}}]}`,
			wantFail: false,
		},
		{
			name:     "content as a block array",
			body:     `{"choices":[{"message":{"content":[{"type":"text","text":"hi"}]}}]}`,
			wantFail: false,
		},
		{
			name:     "legacy text completion",
			body:     `{"choices":[{"text":"hello","index":0}]}`,
			wantFail: false,
		},
		{
			name:     "claude message with a thinking block",
			body:     `{"id":"msg_1","type":"message","content":[{"type":"thinking","thinking":"..."}]}`,
			wantFail: false,
		},
		{
			name:     "responses incomplete with empty output is the answer",
			body:     `{"id":"resp_1","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"output":[]}`,
			wantFail: false,
		},
		{
			name:     "responses failed with empty output is the answer",
			body:     `{"id":"resp_1","status":"failed","output":[]}`,
			wantFail: false,
		},
		{
			name:     "responses with output present",
			body:     `{"id":"resp_1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]}]}`,
			wantFail: false,
		},
		{
			name:     "error field explicitly null is not an error",
			body:     `{"error":null,"choices":[{"message":{"content":"hello"}}]}`,
			wantFail: false,
		},
		{
			name:     "an unfolded event stream is left to the caller",
			body:     "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n",
			wantFail: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := EmptyUpstreamError([]byte(tt.body))
			if !tt.wantFail {
				if err != nil {
					t.Fatalf("EmptyUpstreamError() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("EmptyUpstreamError() = nil, want a failure")
			}
			var ue *UpstreamError
			if !errors.As(err, &ue) {
				t.Fatalf("EmptyUpstreamError() = %T, want *UpstreamError", err)
			}
			if ue.StatusCode != http.StatusBadGateway {
				t.Errorf("StatusCode = %d, want 502 so fallback retries", ue.StatusCode)
			}
			if !strings.Contains(string(ue.Body), "error") {
				t.Errorf("Body = %s, want an error envelope", ue.Body)
			}
		})
	}
}

func TestLooksLikeSSE(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "data frame", body: "data: {}\n\n", want: true},
		{name: "event frame", body: "event: error\ndata: {}\n\n", want: true},
		{name: "leading blank lines", body: "\n\n  data: {}\n\n", want: true},
		{name: "json document", body: `{"choices":[]}`, want: false},
		{name: "html page", body: "<html></html>", want: false},
		{name: "empty", body: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LooksLikeSSE([]byte(tt.body)); got != tt.want {
				t.Errorf("LooksLikeSSE(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}
