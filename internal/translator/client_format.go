package translator

import "context"

// clientFormatCtxKey stores the wire format the calling client speaks, so the
// executors can tell a /v1/responses caller from an OpenAI Chat Completions one
// without another field on every request struct — the same trick
// WithRequestedModel already plays for the echoed model.
type clientFormatCtxKey struct{}

// ClientFormat names a wire format the gateway accepts.
type ClientFormat string

const (
	// ClientFormatChat is the OpenAI Chat Completions wire format, the default
	// for every endpoint but /v1/responses.
	ClientFormatChat ClientFormat = "openai"
	// ClientFormatResponses is the OpenAI Responses wire format of /v1/responses.
	ClientFormatResponses ClientFormat = "openai-responses"
)

// WithClientFormat marks the format the client is speaking on this request.
func WithClientFormat(ctx context.Context, format ClientFormat) context.Context {
	return context.WithValue(ctx, clientFormatCtxKey{}, format)
}

// ClientFormatFrom reports the client wire format, defaulting to Chat
// Completions when nothing was recorded.
func ClientFormatFrom(ctx context.Context) ClientFormat {
	if ctx == nil {
		return ClientFormatChat
	}
	if v, ok := ctx.Value(clientFormatCtxKey{}).(ClientFormat); ok {
		return v
	}
	return ClientFormatChat
}

// IsResponsesClient reports whether the caller speaks the Responses API, the
// only format /v1/responses requests arrive in.
func IsResponsesClient(ctx context.Context) bool {
	return ClientFormatFrom(ctx) == ClientFormatResponses
}

// responsesBridgeCtxKey records that a Responses client is being served by a
// Chat Completions upstream, so the response has to be replayed as Responses
// events. It is set per connection, next to the resolved provider config,
// because the same client can be routed to a Responses-native connection in one
// picker pass and a Chat-native one in the next.
type responsesBridgeCtxKey struct{}

// WithResponsesBridge marks the request as one whose Chat Completions response
// must be translated into the Responses wire format.
func WithResponsesBridge(ctx context.Context) context.Context {
	return context.WithValue(ctx, responsesBridgeCtxKey{}, true)
}

// NeedsResponsesBridge reports whether the Chat Completions response produced
// upstream has to be translated before it reaches a Responses client.
func NeedsResponsesBridge(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(responsesBridgeCtxKey{}).(bool)
	return v
}

// customToolNamesCtxKey carries the freeform custom tools the client declared
// in a Responses request, so the response bridge can replay their calls as
// custom_tool_call_input. It travels in the context because the request
// conversion that discovers them happens in the handler while the response
// that needs them is produced in an executor.
type customToolNamesCtxKey struct{}

// WithCustomToolNames records the freeform custom tools of a Responses request.
func WithCustomToolNames(ctx context.Context, names []string) context.Context {
	return context.WithValue(ctx, customToolNamesCtxKey{}, names)
}

// CustomToolNamesFrom reports the freeform custom tools of a Responses request.
func CustomToolNamesFrom(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(customToolNamesCtxKey{}).([]string); ok {
		return v
	}
	return nil
}
