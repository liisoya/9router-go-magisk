package executor

import (
	json "encoding/json/v2"
)

// stripCodexUnsupportedTokenParams removes the generation-limit parameters the
// ChatGPT Codex OAuth backend rejects with 400 "Unsupported parameter:
// max_output_tokens".
//
// The strip is deliberately NOT done in buildResponsesBody. That function is
// shared with the other Responses-API providers (grok-cli and friends), whose
// backends do accept the field, and the command-code body builder reads
// max_output_tokens off the incoming map to pick its own default. So the
// removal lives in the Codex forward path only, where the rejecting backend is
// a known destination.
//
// Port of open-sse/executors/codex.js transformRequest:
//
//	delete body.max_tokens;
//	delete body.max_completion_tokens;
//	delete body.max_output_tokens;
//
// A body that does not parse is returned untouched: rewriting a payload we
// could not read risks turning an upstream-parseable error into a silently
// different request.
func stripCodexUnsupportedTokenParams(body []byte) []byte {
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body
	}

	var changed bool
	for _, key := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if _, ok := envelope[key]; ok {
			delete(envelope, key)
			changed = true
		}
	}
	if !changed {
		return body
	}

	out, err := json.Marshal(envelope)
	if err != nil {
		return body
	}
	return out
}
