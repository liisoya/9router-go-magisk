package executor

import (
	"bytes"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"9router/proxy/internal/constants"
	"9router/proxy/internal/proxy"
)

// ForwardCodebuddyCN forwards to Tencent CodeBuddy with force-stream
// and reasoning_summary injection.
//
// CodeBuddy is OpenAI-compatible but rejects non-stream chat requests
// (HTTP 400, code 11101 "Non-stream chat request is currently not supported").
// This executor forces stream=true in the request body, mirroring the
// JS version's CodeBuddyExecutor.transformRequest behaviour.
//
// It also handles reasoning_effort:
//   - "none"/"off" → stripped (CodeBuddy gateway has no "none")
//   - any other value → injects reasoning_summary="auto" so CodeBuddy
//     surfaces the model's reasoning output.
func ForwardCodebuddyCN(w http.ResponseWriter, req *Request) error {
	body, err := transformCodebuddyBody(req.Body)
	if err != nil {
		return fmt.Errorf("transform codebuddy body: %w", err)
	}

	ctx := req.Ctx
	// Always forward as stream — CodeBuddy rejects stream=false
	resp, err := proxy.ForwardOpenAI(ctx, req.Client, req.Config, req.APIKey, body, true)
	if err != nil {
		return fmt.Errorf("ForwardCodebuddyCN upstream: %w", err)
	}
	defer resp.Body.Close()

	if req.IsStream {
		stallReader := proxy.NewStallReaderWithContext(req.Ctx, resp.Body, 0, "codebuddy-cn")
		defer stallReader.Close() // stops the shutdown watcher + stall timer
		return execSSEStream(w, stallReader, req)
	}
	// Client asked for non-stream, but we sent stream=true upstream, so the
	// upstream body is OpenAI-chat SSE. Re-aggregate the chunks into a single
	// JSON chat.completion response (mirrors the JS parseSSEToOpenAIResponse
	// path). If it isn't SSE (e.g. an upstream error JSON), pass it through.
	data, err := io.ReadAll(io.LimitReader(resp.Body, constants.MaxUpstreamBodyBytes))
	if err != nil {
		return fmt.Errorf("read codebuddy response: %w", err)
	}
	if converted, ok := sseToOpenAIJSON(data); ok {
		data = converted
	}
	return jsonResponse(req.Ctx, w, bytes.NewReader(data), req.TranslateResp, req.ResponseBuf)
}

// transformCodebuddyBody forces stream=true and handles reasoning params.
//
// 注：此处曾有一段「agent 系统提示词清洗」（应对 Tencent 内容过滤的 11128），
// 2026-09-27 随上游 v1.9.3 同步撤除 —— 下面上游带来的 shaping 会**丢弃全部
// system/developer 消息**并前置固定的 "You are CodeBuddy Code."，清洗在其之前执行
// 因而恒为 no-op（留着只会让人以为它在起作用）。若 11128 复发，正确修法是把清洗
// 放到 shaping **之后**。裁决记录：AGENT-CONVENTIONS §10.2 / docs/adr/0003。
func transformCodebuddyBody(body []byte) ([]byte, error) {
	var reqMap map[string]any
	if err := json.Unmarshal(body, &reqMap); err != nil {
		return nil, fmt.Errorf("parse body: %w", err)
	}

	// Force stream — CodeBuddy rejects non-stream (HTTP 400, code 11101)
	reqMap["stream"] = true

	// CodeBuddy rejects plain OpenAI shape (11101 invalid request): needs a
	// leading system prompt + user content as typed blocks, not a bare string
	// (upstream codebuddy-intl.js transformRequest parity).
	var msgs []any
	if arr, ok := reqMap["messages"].([]any); ok {
		msgs = arr
	}
	shaped := []any{map[string]any{"role": "system", "content": "You are CodeBuddy Code."}}
	for _, raw := range msgs {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		if role == "system" || role == "developer" {
			continue
		}
		if role == "user" {
			if s, ok := m["content"].(string); ok {
				cp := map[string]any{}
				for k, v := range m {
					cp[k] = v
				}
				cp["content"] = []any{map[string]any{"type": "text", "text": s}}
				shaped = append(shaped, cp)
				continue
			}
		}
		shaped = append(shaped, m)
	}
	reqMap["messages"] = shaped

	// Handle reasoning_effort / reasoning_summary
	if eff, ok := reqMap["reasoning_effort"].(string); ok {
		switch eff {
		case "none", "off":
			// CodeBuddy gateway has no "none" — just omit
			delete(reqMap, "reasoning_effort")
		default:
			// Client explicitly asked for reasoning — mirror the CLI's
			// reasoning_summary so CodeBuddy surfaces the model's reasoning.
			reqMap["reasoning_summary"] = "auto"
		}
	}

	return json.Marshal(reqMap)
}

// sseToOpenAIJSON re-aggregates OpenAI chat-completions SSE chunks into a
// single chat.completion JSON object. Returns (nil, false) when raw is not
// SSE-shaped (e.g. an upstream error JSON that should pass through).
func sseToOpenAIJSON(raw []byte) ([]byte, bool) {
	var chunks []map[string]any
	var streamErr map[string]any
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}
		payload := strings.TrimSpace(trimmed[5:])
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		if e, ok := chunk["error"]; ok {
			streamErr, _ = e.(map[string]any)
			continue
		}
		chunks = append(chunks, chunk)
	}
	if streamErr != nil {
		b, _ := json.Marshal(map[string]any{"error": streamErr})
		return b, true
	}
	if len(chunks) == 0 {
		return nil, false
	}

	var contentParts, reasoningParts []string
	toolCallIdx := map[int]map[string]any{}
	var toolCalls []map[string]any
	finishReason := "stop"
	var usage any
	var first map[string]any

	for _, chunk := range chunks {
		if first == nil {
			first = chunk
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		if c, ok := delta["content"].(string); ok && c != "" {
			contentParts = append(contentParts, c)
		}
		if r, ok := delta["reasoning_content"].(string); ok && r != "" {
			reasoningParts = append(reasoningParts, r)
		}
		if fr, ok := choice["finish_reason"].(string); ok {
			finishReason = fr
		}
		if u, ok := chunk["usage"]; ok {
			usage = u
		}
		if tcs, ok := delta["tool_calls"].([]any); ok {
			for _, tcAny := range tcs {
				tc, _ := tcAny.(map[string]any)
				idx, _ := tc["index"].(float64)
				entry, ok := toolCallIdx[int(idx)]
				if !ok {
					entry = map[string]any{
						"id":       "",
						"type":     "function",
						"function": map[string]any{"name": "", "arguments": ""},
					}
					toolCallIdx[int(idx)] = entry
					toolCalls = append(toolCalls, entry)
				}
				if id, ok := tc["id"].(string); ok && id != "" {
					entry["id"] = id
				}
				if fn, ok := tc["function"].(map[string]any); ok {
					fEntry, _ := entry["function"].(map[string]any)
					if n, ok := fn["name"].(string); ok && n != "" {
						if existing, _ := fEntry["name"].(string); existing == "" {
							fEntry["name"] = n
						} else if existing != n && !strings.Contains(existing, n) {
							fEntry["name"] = existing + n
						}
					}
					if a, ok := fn["arguments"].(string); ok && a != "" {
						existing, _ := fEntry["arguments"].(string)
						if existing == "" {
							fEntry["arguments"] = a
						} else if existing != a && !strings.Contains(existing, a) {
							// Only append if not already present (avoid duplicating done after delta)
							// For split arguments, delta fragments should be appended
							if len(a) > 0 && !strings.HasSuffix(existing, a) {
								fEntry["arguments"] = existing + a
							}
						}
					}
				}
			}
		}
	}

	msg := map[string]any{"role": "assistant"}
	content := strings.Join(contentParts, "")
	if content == "" {
		if len(toolCalls) > 0 {
			msg["content"] = nil
		} else {
			msg["content"] = ""
		}
	} else {
		msg["content"] = content
	}
	var validToolCalls []map[string]any
	for _, tc := range toolCalls {
		if fn, ok := tc["function"].(map[string]any); ok {
			if n, ok := fn["name"].(string); ok && strings.TrimSpace(n) != "" {
				validToolCalls = append(validToolCalls, tc)
			}
		}
	}
	if len(validToolCalls) > 0 {
		msg["tool_calls"] = validToolCalls
	}
	if len(reasoningParts) > 0 {
		msg["reasoning_content"] = strings.Join(reasoningParts, "")
	}

	result := map[string]any{
		"id":      first["id"],
		"object":  "chat.completion",
		"created": first["created"],
		"model":   first["model"],
		"choices": []map[string]any{{
			"index":         0,
			"message":       msg,
			"finish_reason": finishReason,
		}},
	}
	if usage != nil {
		result["usage"] = usage
	}
	if result["id"] == nil {
		result["id"] = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	if result["created"] == nil {
		result["created"] = time.Now().Unix()
	}
	if result["model"] == nil {
		result["model"] = ""
	}
	b, err := json.Marshal(result)
	if err != nil {
		return nil, false
	}
	return b, true
}
