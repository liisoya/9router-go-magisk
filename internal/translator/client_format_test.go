package translator

import (
	"context"
	"testing"
)

// TestClientFormatFlags checks the three questions the executors ask about a
// request. A nil context must read as a Chat Completions client on every one of
// them, because that is what the overwhelming majority of traffic is and a
// panic here would take the whole proxy down.
func TestClientFormatFlags(t *testing.T) {
	t.Run("nil context is a chat client", func(t *testing.T) {
		//nolint:staticcheck // deliberately passing nil: callers do this
		if IsResponsesClient(nil) {
			t.Error("nil context must not report a Responses client")
		}
		if NeedsResponsesBridge(nil) {
			t.Error("nil context must not request a response translation")
		}
		if got := ClientFormatFrom(nil); got != ClientFormatChat {
			t.Errorf("ClientFormatFrom(nil) = %q, want %q", got, ClientFormatChat)
		}
	})

	t.Run("chat client carries no bridge", func(t *testing.T) {
		ctx := WithClientFormat(context.Background(), ClientFormatChat)
		if IsResponsesClient(ctx) {
			t.Error("chat client reported as a Responses client")
		}
		if NeedsResponsesBridge(ctx) {
			t.Error("chat client must not request a response translation")
		}
	})

	t.Run("responses client without a chat upstream carries no bridge", func(t *testing.T) {
		// The native case: codex answers /v1/responses, so nothing is translated
		// even though the client speaks Responses.
		ctx := WithClientFormat(context.Background(), ClientFormatResponses)
		if !IsResponsesClient(ctx) {
			t.Error("Responses client not detected")
		}
		if NeedsResponsesBridge(ctx) {
			t.Error("bridge requested before an upstream was known to be chat-native")
		}
	})

	t.Run("bridge is set only for a chat upstream", func(t *testing.T) {
		ctx := WithResponsesBridge(WithClientFormat(context.Background(), ClientFormatResponses))
		if !IsResponsesClient(ctx) {
			t.Error("adding the bridge must not clear the client format")
		}
		if !NeedsResponsesBridge(ctx) {
			t.Error("bridge flag not observed")
		}
	})
}

// TestCustomToolNamesRoundTrip pins the hand-off between the request conversion
// that discovers freeform custom tools and the response replay that has to
// emit their calls in the custom shape.
func TestCustomToolNamesRoundTrip(t *testing.T) {
	if got := CustomToolNamesFrom(context.Background()); got != nil {
		t.Errorf("expected no custom tools without a Responses request, got %v", got)
	}
	ctx := WithCustomToolNames(context.Background(), []string{"apply_patch"})
	got := CustomToolNamesFrom(ctx)
	if len(got) != 1 || got[0] != "apply_patch" {
		t.Errorf("CustomToolNamesFrom = %v, want [apply_patch]", got)
	}
}
