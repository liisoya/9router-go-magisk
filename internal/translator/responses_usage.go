package translator

import "encoding/json"

// ParseResponsesUsage reads the usage a Responses-native upstream reports,
// which counts input_tokens/output_tokens instead of the Chat Completions
// prompt_tokens/completion_tokens. Usage logging and cost estimation read the
// Chat shape everywhere, so a Responses body relayed untouched would otherwise
// log zero tokens for every codex, grok-cli or perplexity-agent turn.
func ParseResponsesUsage(body []byte) *OpenAIUsage {
	var envelope struct {
		Usage *ResponsesUsage `json:"usage"`
		// The streaming form carries usage on the terminal event under the
		// response object rather than at the top level.
		Response *struct {
			Usage *ResponsesUsage `json:"usage"`
		} `json:"response"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil
	}

	u := envelope.Usage
	if u == nil && envelope.Response != nil {
		u = envelope.Response.Usage
	}
	if u == nil {
		return nil
	}

	out := &OpenAIUsage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		// The figure was counted against the Responses envelope, so the
		// Chat-shaped prompt/completion split already includes the cache.
		PromptCacheIncluded: true,
	}
	if u.InputTokensDetails != nil && u.InputTokensDetails.CachedTokens > 0 {
		out.CachedTokens = u.InputTokensDetails.CachedTokens
	}
	return out
}
