package executor

import (
	"regexp"
	"testing"
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestCodebuddyConversationHeaders_FamilyPresent(t *testing.T) {
	req := &Request{Body: []byte(`{"messages":[{"role":"user","content":"hi"}]}`)}
	h := codebuddyConversationHeaders(req, req.Body)
	for _, k := range []string{"X-Conversation-ID", "X-Conversation-Request-ID", "X-Conversation-Message-ID"} {
		if !uuidRE.MatchString(h[k]) {
			t.Errorf("%s = %q, want uuid", k, h[k])
		}
	}
}

func TestCodebuddyConversationHeaders_StableAcrossRetries(t *testing.T) {
	req := &Request{Body: []byte(`{"messages":[{"role":"user","content":"same opening"}]}`)}
	a := codebuddyConversationHeaders(req, req.Body)
	b := codebuddyConversationHeaders(req, req.Body)
	if a["X-Conversation-ID"] != b["X-Conversation-ID"] {
		t.Errorf("conversation id not stable: %s != %s", a["X-Conversation-ID"], b["X-Conversation-ID"])
	}
	if a["X-Conversation-Request-ID"] == b["X-Conversation-Request-ID"] {
		t.Error("request id must be fresh per request")
	}
	if a["X-Conversation-Message-ID"] == b["X-Conversation-Message-ID"] {
		t.Error("message id must be fresh per message")
	}
}

func TestCodebuddyConversationHeaders_SessionIDPrecedence(t *testing.T) {
	a := codebuddyConversationHeaders(&Request{SessionID: "sess-123"}, []byte(`{"messages":[]}`))
	b := codebuddyConversationHeaders(&Request{SessionID: "sess-456"}, []byte(`{"messages":[]}`))
	c := codebuddyConversationHeaders(&Request{SessionID: "sess-123"}, []byte(`{"messages":[]}`))
	if a["X-Conversation-ID"] != c["X-Conversation-ID"] {
		t.Error("same session id must map to same conversation id")
	}
	if a["X-Conversation-ID"] == b["X-Conversation-ID"] {
		t.Error("different session ids must map to different conversation ids")
	}
}

func TestCodebuddyConversationHeaders_TypedBlockContent(t *testing.T) {
	req := &Request{Body: []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"typed block"}]}]}`)}
	h := codebuddyConversationHeaders(req, req.Body)
	if !uuidRE.MatchString(h["X-Conversation-ID"]) {
		t.Errorf("typed-block derivation produced non-uuid: %q", h["X-Conversation-ID"])
	}
}

func TestCodebuddyConversationHeaders_RandomFallback(t *testing.T) {
	req := &Request{Body: []byte(`{"messages":[]}`)}
	a := codebuddyConversationHeaders(req, req.Body)
	b := codebuddyConversationHeaders(req, req.Body)
	if !uuidRE.MatchString(a["X-Conversation-ID"]) {
		t.Errorf("fallback id not a uuid: %q", a["X-Conversation-ID"])
	}
	if a["X-Conversation-ID"] == b["X-Conversation-ID"] {
		t.Error("with nothing to derive from, ids must be random")
	}
}

func TestFirstUserText_StringAndBlocks(t *testing.T) {
	s := firstUserText([]byte(`{"messages":[{"role":"assistant","content":"skip"},{"role":"user","content":"plain"}]}`))
	if s != "plain" {
		t.Errorf("string content: got %q", s)
	}
	b := firstUserText([]byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"blk"}]}]}`))
	if b != "blk" {
		t.Errorf("blocks content: got %q", b)
	}
}
