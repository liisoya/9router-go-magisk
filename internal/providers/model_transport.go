package providers

import "strings"

// Model transport markers. Upstream carries a `transport` field on individual
// registry model entries and dispatches the media handlers on the marker rather
// than on a model id, so a new realtime provider is added through data instead
// of a hardcoded branch (open-sse/handlers/sttCore.js resolveModelTransport).
// The Go model catalog is a flat id list, so the field lives here.

// TransportGeminiLive routes a model through the Gemini Live bidirectional
// WebSocket instead of a whole-file REST transcription.
const TransportGeminiLive = "gemini-live"

// modelTransports maps provider → model → transport marker.
var modelTransports = map[string]map[string]string{
	"gemini": {
		// Realtime transcription over :bidiGenerateContent; the REST path can
		// only transcribe whole files inline.
		"gemini-2.5-flash-native-audio-preview-09-17": TransportGeminiLive,
	},
}

// ResolveModelTransport returns the transport marker a provider's catalog entry
// declares for a model, or "" when it declares none. Never keyed on the model
// id itself: the marker is the extension point.
func ResolveModelTransport(provider, model string) string {
	byModel, ok := modelTransports[strings.ToLower(provider)]
	if !ok {
		return ""
	}
	return strings.TrimSpace(byModel[model])
}
