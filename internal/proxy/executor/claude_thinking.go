package executor

import (
	json "encoding/json/v2"
	"strings"
)

// Claude decides whether the thinking *text* comes back at all, not just
// whether thinking runs: a thinking block is returned as a signature only unless
// the request asks for `thinking.display: "summarized"`. OpenAI-shaped clients
// have no such field — their only way to ask for reasoning is `reasoning_effort`
// (Chat Completions) or `reasoning.summary` (Responses) — so that intent has to
// be captured during translation, before the OpenAI-only keys are stripped.
//
// Mirrors upstream open-sse/translator/concerns/thinkingUnified.js
// captureThinking/openAIThinkingDisplay.

// applyClaudeThinkingDisplay folds an OpenAI-shaped client's reasoning intent
// into the Claude `thinking` object. A body that already carries an explicit
// display wins, and a client that asked for no reasoning is left alone.
func applyClaudeThinkingDisplay(reqMap map[string]any) {
	thinking, _ := reqMap["thinking"].(map[string]any)
	if thinking != nil {
		if display, ok := thinking["display"].(string); ok && display != "" {
			return
		}
	}

	display := openAIThinkingDisplay(reqMap)
	if display == "" {
		return
	}
	if thinking == nil {
		thinking = map[string]any{}
		reqMap["thinking"] = thinking
	}
	thinking["display"] = display
}

// openAIThinkingDisplay reports the Claude display value an OpenAI-shaped body
// is asking for, or "" when it is not asking for reasoning at all.
func openAIThinkingDisplay(reqMap map[string]any) string {
	// Responses API: reasoning.summary is the explicit request for reasoning text.
	if reasoning, ok := reqMap["reasoning"].(map[string]any); ok {
		if summary, ok := reasoning["summary"].(string); ok && summary != "" && summary != "none" {
			return "summarized"
		}
	}
	// Chat Completions has no summary knob. A client setting reasoning_effort is
	// asking for reasoning; "none"/"off" asks for it to be switched off, in
	// which case there is no text to display.
	if effort, ok := reqMap["reasoning_effort"].(string); ok && effort != "" {
		switch strings.ToLower(effort) {
		case "none", "off", "minimal_off":
			return ""
		}
		return "summarized"
	}
	return ""
}

// WantsThinkingSummaries reports whether a Claude-shaped request body asks for
// thinking text. Callers use it to drop the redact-thinking beta flag, which
// asks Anthropic for signature-only thinking blocks and would blank the very
// summaries the client requested.
func WantsThinkingSummaries(body []byte) bool {
	var reqMap map[string]any
	if err := json.Unmarshal(body, &reqMap); err != nil {
		return false
	}
	thinking, ok := reqMap["thinking"].(map[string]any)
	if !ok {
		return false
	}
	display, _ := thinking["display"].(string)
	return display == "summarized"
}
