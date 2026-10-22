package executor

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"9router/proxy/internal/proxy"
)

// A Codex turn that fails with no content must not be served as an empty
// success. HTTP 200 is committed before the upstream is read, so the failure
// is only reportable in-band — but the previous code closed these turns with a
// fabricated `finish_reason: "stop"` (OpenAI) or `stop_reason: "end_turn"`
// (Claude), which a client reads as a completed answer with no text. That is
// the empty response reported in #334: the upstream schema rejection surfaced
// as nothing at all, and combo fallback settled on the model that failed.
func TestHandleCodexStream_FailedTurnIsNotServedAsEmptySuccess(t *testing.T) {
	upstreamErr := `{"type":"error","error":{"type":"invalid_request_error","message":"Invalid schema for function 'Artifact': '^\\p{Cc}...' is not a 'regex'"}}`
	responseFailed := `{"type":"response.failed","response":{"id":"r1","status":"failed","error":{"code":"invalid_request_error","message":"Invalid schema for function 'Artifact': '^\\p{Cc}...' is not a 'regex'"}}}`

	clients := []struct {
		name          string
		translateResp bool
		// Text a failed turn must not be closed with: it reads as success.
		successTerminal string
	}{
		{name: "claude /v1/messages", translateResp: true, successTerminal: "end_turn"},
		{name: "openai chat completions", translateResp: false, successTerminal: `"finish_reason":"stop"`},
	}
	events := []struct {
		name string
		data string
	}{
		{name: "error event", data: "data: " + upstreamErr + "\n\ndata: [DONE]\n\n"},
		{name: "response.failed event", data: "event: response.failed\ndata: " + responseFailed + "\n\n"},
	}

	for _, ev := range events {
		for _, c := range clients {
			t.Run(ev.name+"/"+c.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				req := &Request{IsStream: true, TranslateResp: c.translateResp}
				err := handleCodexStream(rec, req, strings.NewReader(ev.data))

				var ue *proxy.UpstreamError
				if !errors.As(err, &ue) {
					t.Fatalf("error = %v, want a *proxy.UpstreamError the fallback layer can retry on", err)
				}
				if ue.StatusCode != http.StatusBadGateway {
					t.Errorf("status = %d, want 502", ue.StatusCode)
				}
				if !strings.Contains(string(ue.Body), "is not a 'regex'") {
					t.Errorf("upstream message lost, body = %s", ue.Body)
				}

				body := rec.Body.String()
				if !strings.Contains(body, `"error"`) {
					t.Errorf("no in-band error frame for the client, body = %q", body)
				}
				if strings.Contains(body, c.successTerminal) {
					t.Errorf("failed turn closed as a success (%s), body = %q", c.successTerminal, body)
				}
			})
		}
	}
}

// The failed-turn branch must not swallow a turn that carried content: an
// error event after output has no empty answer to replace, so the stream is
// closed normally rather than replaced by an error frame.
func TestHandleCodexStream_ErrorAfterContentIsNotReplacedByErrorFrame(t *testing.T) {
	raw := "data: " + `{"type":"response.output_text.delta","delta":"partial answer"}` + "\n\n" +
		"data: " + `{"type":"error","error":{"type":"server_error","message":"connection reset"}}` + "\n\n"

	rec := httptest.NewRecorder()
	req := &Request{IsStream: true, TranslateResp: true}
	_ = handleCodexStream(rec, req, strings.NewReader(raw))

	body := rec.Body.String()
	if !strings.Contains(body, "partial answer") {
		t.Errorf("streamed content dropped, body = %q", body)
	}
	if strings.Contains(body, `"type":"error"`) {
		t.Errorf("a turn that carried content was replaced by an error frame, body = %q", body)
	}
}

// A healthy stream still terminates normally: the failed-turn branch must not
// swallow the finish frame or the [DONE] a client waits for.
func TestHandleCodexStream_HealthyTurnStillTerminates(t *testing.T) {
	raw := "data: " + `{"type":"response.output_text.delta","delta":"hi"}` + "\n\n" +
		"data: " + `{"type":"response.completed","response":{"id":"r1","status":"completed","output":[]}}` + "\n\n"

	rec := httptest.NewRecorder()
	req := &Request{IsStream: true, TranslateResp: true}
	if err := handleCodexStream(rec, req, strings.NewReader(raw)); err != nil {
		t.Fatalf("healthy stream returned %v", err)
	}
	if !strings.Contains(rec.Body.String(), "hi") || !strings.Contains(rec.Body.String(), "[DONE]") {
		t.Errorf("healthy stream lost its content or terminator, body = %q", rec.Body.String())
	}
}
