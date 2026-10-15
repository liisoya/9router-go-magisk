package proxy

import (
	json "encoding/json/v2"
)

// deterministicOpts sorts map members by name. encoding/json/v2 leaves map
// members in Go's randomized iteration order by default, so the same logical
// request can serialize to different bytes on every attempt. Prefix-based
// prompt caches upstream (DeepSeek, Anthropic, OpenAI, Kimi, ...) key on the
// exact request bytes, so a randomized order turns the shared prefix of every
// turn after the first into a full cache miss. The JavaScript 9router this
// project rewrites keeps insertion order and is deterministic by construction;
// sorting restores that property here.
var deterministicOpts = json.Deterministic(true)

// canonicalChatBody re-serializes a chat-completions request body with sorted
// map members. Only bodies that look like chat requests (carry model+messages)
// are touched; OAuth token posts, Gemini payloads and other non-chat JSON pass
// through byte-identical. Serialization failure falls back to the original
// bytes, preserving the caller's behavior.
func canonicalChatBody(body []byte) []byte {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}
	if _, ok := m["model"]; !ok {
		return body
	}
	if _, ok := m["messages"]; !ok {
		return body
	}
	out, err := json.Marshal(m, deterministicOpts)
	if err != nil {
		return body
	}
	return out
}
