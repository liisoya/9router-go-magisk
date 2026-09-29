package translator

import "fmt"

// startReasoning opens a reasoning output item on first use, so a stream that
// reasons before answering gets its summary part announced exactly once.
func (s *ResponsesState) startReasoning(out *[]ResponsesEvent, idx int) {
	if s.ReasoningID != "" {
		return
	}
	s.ReasoningID = fmt.Sprintf("%s_%s_%d", responsesPrefixReasoningItem, s.ResponseID, idx)
	s.ReasoningIndex = idx

	s.emit(out, "response.output_item.added", map[string]any{
		"type":         "response.output_item.added",
		"output_index": idx,
		"item": map[string]any{
			"id":      s.ReasoningID,
			"type":    responsesItemReasoning,
			"summary": []any{},
		},
	})

	s.emit(out, "response.reasoning_summary_part.added", map[string]any{
		"type":          "response.reasoning_summary_part.added",
		"item_id":       s.ReasoningID,
		"output_index":  idx,
		"summary_index": 0,
		"part": map[string]any{
			"type": responsesItemSummaryText,
			"text": "",
		},
	})
	s.ReasoningPartAdded = true
}

// emitReasoningDelta streams a slice of reasoning text.
func (s *ResponsesState) emitReasoningDelta(out *[]ResponsesEvent, text string) {
	if text == "" {
		return
	}
	s.ReasoningBuf += text
	s.emit(out, "response.reasoning_summary_text.delta", map[string]any{
		"type":          "response.reasoning_summary_text.delta",
		"item_id":       s.ReasoningID,
		"output_index":  s.ReasoningIndex,
		"summary_index": 0,
		"delta":         text,
	})
}

// closeReasoning finalizes the reasoning item. Idempotent: the answer text and
// the finish frame both call it.
func (s *ResponsesState) closeReasoning(out *[]ResponsesEvent) {
	if s.ReasoningID == "" || s.ReasoningDone {
		return
	}
	s.ReasoningDone = true

	s.emit(out, "response.reasoning_summary_text.done", map[string]any{
		"type":          "response.reasoning_summary_text.done",
		"item_id":       s.ReasoningID,
		"output_index":  s.ReasoningIndex,
		"summary_index": 0,
		"text":          s.ReasoningBuf,
	})

	s.emit(out, "response.reasoning_summary_part.done", map[string]any{
		"type":          "response.reasoning_summary_part.done",
		"item_id":       s.ReasoningID,
		"output_index":  s.ReasoningIndex,
		"summary_index": 0,
		"part": map[string]any{
			"type": responsesItemSummaryText,
			"text": s.ReasoningBuf,
		},
	})

	item := map[string]any{
		"id":   s.ReasoningID,
		"type": responsesItemReasoning,
		"summary": []any{
			map[string]any{
				"type": responsesItemSummaryText,
				"text": s.ReasoningBuf,
			},
		},
	}

	s.emit(out, "response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": s.ReasoningIndex,
		"item":         item,
	})
	s.recordCompletedOutputItem(s.ReasoningIndex, item)
}

// emitTextContent streams answer text, lazily opening the message item and its
// content part so a tool-only turn never announces an empty message.
func (s *ResponsesState) emitTextContent(out *[]ResponsesEvent, idx int, content string) {
	if !s.MsgItemAdded[idx] {
		s.MsgItemAdded[idx] = true
		s.emit(out, "response.output_item.added", map[string]any{
			"type":         "response.output_item.added",
			"output_index": idx,
			"item": map[string]any{
				"id":      s.messageItemID(idx),
				"type":    responsesItemMessage,
				"content": []any{},
				"role":    responsesAssistantRole,
			},
		})
	}

	if !s.MsgContentAdded[idx] {
		s.MsgContentAdded[idx] = true
		s.emit(out, "response.content_part.added", map[string]any{
			"type":          "response.content_part.added",
			"item_id":       s.messageItemID(idx),
			"output_index":  idx,
			"content_index": 0,
			"part": map[string]any{
				"type":        responsesItemOutputText,
				"annotations": []any{},
				"logprobs":    []any{},
				"text":        "",
			},
		})
	}

	s.emit(out, "response.output_text.delta", map[string]any{
		"type":          "response.output_text.delta",
		"item_id":       s.messageItemID(idx),
		"output_index":  idx,
		"content_index": 0,
		"delta":         content,
		"logprobs":      []any{},
	})

	s.MsgTextBuf[idx] += content
}

// closeMessage finalizes the message item carrying the buffered answer text.
func (s *ResponsesState) closeMessage(out *[]ResponsesEvent, idx int) {
	if !s.MsgItemAdded[idx] || s.MsgItemDone[idx] {
		return
	}
	s.MsgItemDone[idx] = true

	fullText := s.MsgTextBuf[idx]

	s.emit(out, "response.output_text.done", map[string]any{
		"type":          "response.output_text.done",
		"item_id":       s.messageItemID(idx),
		"output_index":  idx,
		"content_index": 0,
		"text":          fullText,
		"logprobs":      []any{},
	})

	s.emit(out, "response.content_part.done", map[string]any{
		"type":          "response.content_part.done",
		"item_id":       s.messageItemID(idx),
		"output_index":  idx,
		"content_index": 0,
		"part": map[string]any{
			"type":        responsesItemOutputText,
			"annotations": []any{},
			"logprobs":    []any{},
			"text":        fullText,
		},
	})

	item := map[string]any{
		"id":   s.messageItemID(idx),
		"type": responsesItemMessage,
		"content": []any{
			map[string]any{
				"type":        responsesItemOutputText,
				"annotations": []any{},
				"logprobs":    []any{},
				"text":        fullText,
			},
		},
		"role": responsesAssistantRole,
	}

	s.emit(out, "response.output_item.done", map[string]any{
		"type":         "response.output_item.done",
		"output_index": idx,
		"item":         item,
	})
	s.recordCompletedOutputItem(idx, item)
}

func (s *ResponsesState) messageItemID(idx int) string {
	return fmt.Sprintf("%s_%s_%d", responsesPrefixMessage, s.ResponseID, idx)
}
