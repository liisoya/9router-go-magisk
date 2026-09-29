package chat

import (
	json "encoding/json/v2"
	"strings"
	"testing"
)

func TestInjectMimoMarker(t *testing.T) {
	marker := "You are MiMoCode, an interactive CLI tool that helps users with software engineering tasks."

	t.Run("no system message adds marker", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":"hello"}]}`)
		got := injectMimoMarker(body)
		var parsed map[string]any
		if err := json.Unmarshal(got, &parsed); err != nil {
			t.Fatalf("invalid JSON result: %v", err)
		}
		msgs, _ := parsed["messages"].([]any)
		if len(msgs) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(msgs))
		}
		first, _ := msgs[0].(map[string]any)
		if first["role"] != "system" {
			t.Errorf("expected first message role 'system', got %v", first["role"])
		}
		content, _ := first["content"].(string)
		if !strings.Contains(content, marker) {
			t.Errorf("first message content missing marker")
		}
	})

	t.Run("already has marker is not added twice", func(t *testing.T) {
		// Asserted semantically, not byte-for-byte: injectMimoMarker re-marshals
		// through a map, and Go randomises map key order per run, so comparing raw
		// bytes failed this subtest on roughly one run in six for reasons that
		// have nothing to do with the marker.
		body := []byte(`{"messages":[{"role":"system","content":"` + marker + `"},{"role":"user","content":"hi"}]}`)
		var parsed map[string]any
		if err := json.Unmarshal(injectMimoMarker(body), &parsed); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}
		msgs, _ := parsed["messages"].([]any)
		if len(msgs) != 2 {
			t.Fatalf("marker was added again: expected 2 messages, got %d", len(msgs))
		}
		first, _ := msgs[0].(map[string]any)
		content, _ := first["content"].(string)
		if n := strings.Count(content, marker); n != 1 {
			t.Errorf("marker should appear exactly once, got %d occurrences", n)
		}
	})

	t.Run("existing system message without marker prepends marker", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"system","content":"you are helpful"},{"role":"user","content":"hi"}]}`)
		got := injectMimoMarker(body)
		var parsed map[string]any
		if err := json.Unmarshal(got, &parsed); err != nil {
			t.Fatalf("invalid JSON result: %v", err)
		}
		msgs, _ := parsed["messages"].([]any)
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(msgs))
		}
		first, _ := msgs[0].(map[string]any)
		content, _ := first["content"].(string)
		if !strings.Contains(content, marker) {
			t.Errorf("first message should contain marker")
		}
		second, _ := msgs[1].(map[string]any)
		if role, _ := second["role"].(string); role != "system" {
			t.Errorf("second message role should be 'system', got %q", role)
		}
		if second["content"] != "you are helpful" {
			t.Errorf("second message content should be original system message")
		}
	})

	t.Run("invalid json returns original", func(t *testing.T) {
		body := []byte(`not json`)
		got := injectMimoMarker(body)
		if string(got) != string(body) {
			t.Errorf("expected unchanged body for invalid JSON")
		}
	})
}

func TestGetMimoSessionID(t *testing.T) {
	s1 := getMimoSessionID()
	s2 := getMimoSessionID()
	if !strings.HasPrefix(s1, "ses_") {
		t.Errorf("expected prefix 'ses_', got %q", s1)
	}
	if len(s1) != 4+sessionIDLength {
		t.Errorf("expected length %d, got %d", 4+sessionIDLength, len(s1))
	}
	if s1 != s2 {
		t.Errorf("getMimoSessionID() returned different values: %q vs %q", s1, s2)
	}
}
