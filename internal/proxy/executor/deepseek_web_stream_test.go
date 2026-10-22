package executor

import (
	"bufio"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	json "encoding/json/v2"
)

// streamedChunk is the shape of one `data:` frame this executor emits.
type streamedChunk struct {
	Object  string `json:"object"`
	Choices []struct {
		Index        int            `json:"index"`
		Delta        map[string]any `json:"delta"`
		FinishReason any            `json:"finish_reason"`
	} `json:"choices"`
}

func readStreamedChunks(t *testing.T, body string) []streamedChunk {
	t.Helper()
	var chunks []streamedChunk
	scanner := bufio.NewScanner(strings.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if strings.TrimSpace(payload) == "[DONE]" {
			continue
		}
		var chunk streamedChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("frame is not JSON: %v\n%s", err, payload)
		}
		chunks = append(chunks, chunk)
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Fatalf("scan: %v", err)
	}
	return chunks
}

// toolCallsOf returns the tool_calls array of a chunk, or nil.
func toolCallsOf(chunk streamedChunk) []any {
	calls, _ := chunk.Choices[0].Delta["tool_calls"].([]any)
	return calls
}

// TestDeepSeekStreamedCallsCarryTheirIndex is the regression for the failure the
// user hit: two recovered calls were written in one frame with no index, so the
// client had nothing to key its accumulator on and concatenated both argument
// objects into `{}{}` — a tool call whose arguments are not JSON, rejected before
// any command ran.
func TestDeepSeekStreamedCallsCarryTheirIndex(t *testing.T) {
	calls := deepSeekExtractToolCalls(twoCityAnswer, []deepSeekToolSpec{readTool()})
	if len(calls) != 2 {
		t.Fatalf("extracted %d calls, want 2", len(calls))
	}

	recorder := httptest.NewRecorder()
	req := &Request{Ctx: t.Context()}
	if err := deepSeekWriteStreamedToolCalls(recorder, req, "deepseek-v4-flash", calls, ""); err != nil {
		t.Fatalf("write: %v", err)
	}

	chunks := readStreamedChunks(t, recorder.Body.String())

	var indices []float64
	for _, chunk := range chunks {
		for _, raw := range toolCallsOf(chunk) {
			call, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("tool call is not an object: %T", raw)
			}
			index, ok := call["index"].(float64)
			if !ok {
				t.Fatalf("a streamed tool call has no index, so a client cannot tell it "+
					"from the next one: %v", call)
			}
			indices = append(indices, index)
		}
	}

	if len(indices) != 2 {
		t.Fatalf("streamed %d indexed calls, want 2", len(indices))
	}
	if indices[0] == indices[1] {
		t.Fatalf("both calls carry index %v; they are indistinguishable", indices[0])
	}
	if indices[0] != 0 || indices[1] != 1 {
		t.Errorf("indices = %v, want [0 1]", indices)
	}
}

// TestDeepSeekStreamedCallsAreSeparateFrames pins the other half: one call per
// frame, which is the shape every other executor in this repository emits.
func TestDeepSeekStreamedCallsAreSeparateFrames(t *testing.T) {
	calls := deepSeekExtractToolCalls(twoCityAnswer, []deepSeekToolSpec{readTool()})

	recorder := httptest.NewRecorder()
	req := &Request{Ctx: t.Context()}
	if err := deepSeekWriteStreamedToolCalls(recorder, req, "m", calls, ""); err != nil {
		t.Fatalf("write: %v", err)
	}

	framesWithCalls := 0
	for _, chunk := range readStreamedChunks(t, recorder.Body.String()) {
		if len(toolCallsOf(chunk)) > 0 {
			framesWithCalls++
		}
	}
	if framesWithCalls != 2 {
		t.Fatalf("%d frames carried calls, want one frame per call", framesWithCalls)
	}
}

// TestDeepSeekStreamedArgumentsAreOneValidObject is the client's-eye view: each
// call's arguments must parse on their own, which is exactly what `{}{}` did not.
func TestDeepSeekStreamedArgumentsAreOneValidObject(t *testing.T) {
	calls := deepSeekExtractToolCalls(twoCityAnswer, []deepSeekToolSpec{readTool()})

	recorder := httptest.NewRecorder()
	req := &Request{Ctx: t.Context()}
	if err := deepSeekWriteStreamedToolCalls(recorder, req, "m", calls, ""); err != nil {
		t.Fatalf("write: %v", err)
	}

	seen := 0
	for _, chunk := range readStreamedChunks(t, recorder.Body.String()) {
		for _, raw := range toolCallsOf(chunk) {
			call := raw.(map[string]any)
			function, _ := call["function"].(map[string]any)
			arguments, _ := function["arguments"].(string)
			if !deepSeekValidJSON(arguments) {
				t.Errorf("call %v arguments are not a single JSON object: %q", call["index"], arguments)
			}
			seen++
		}
	}
	if seen != 2 {
		t.Fatalf("checked %d calls, want 2", seen)
	}
}

// TestDeepSeekStreamedTurnEndsProperly keeps the terminator and the finish reason
// intact through the rewrite.
func TestDeepSeekStreamedTurnEndsProperly(t *testing.T) {
	calls := deepSeekExtractToolCalls(twoCityAnswer, []deepSeekToolSpec{readTool()})

	recorder := httptest.NewRecorder()
	req := &Request{Ctx: t.Context()}
	if err := deepSeekWriteStreamedToolCalls(recorder, req, "m", calls, "Let me check."); err != nil {
		t.Fatalf("write: %v", err)
	}

	body := recorder.Body.String()
	if !strings.Contains(body, "data: [DONE]") {
		t.Error("the stream has no terminator; a client waits forever")
	}

	chunks := readStreamedChunks(t, body)
	if len(chunks) == 0 {
		t.Fatal("no frames")
	}
	if got := chunks[len(chunks)-1].Choices[0].FinishReason; got != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", got)
	}
	if got, _ := chunks[0].Choices[0].Delta["content"].(string); got != "Let me check." {
		t.Errorf("first frame content = %q, want the model's prose", got)
	}
	if recorder.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q", recorder.Header().Get("Content-Type"))
	}
}
