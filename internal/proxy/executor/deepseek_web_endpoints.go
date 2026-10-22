package executor

import "strings"

// deepSeekEndpoints are the chat.deepseek.com API URLs one turn needs.
//
// They are derived from the provider's configured BaseURL rather than hardcoded
// per call. Two reasons: a connection carries its own baseUrl override, and a
// hardcoded host would ignore it — the request would leave for the public site
// while the dashboard showed a different host; and the test suite can then
// point a whole turn at one httptest server without a transport seam that only
// tests know about.
type deepSeekEndpoints struct {
	usersCurrent  string
	sessionCreate string
	sessionDelete string
	powChallenge  string
	completion    string
}

// deepSeekEndpointsFor derives the endpoint set from a configured base URL.
//
// The completion endpoint is <root>/chat/completion, so every sibling is the
// same root with its last segment swapped. Deriving them rather than
// concatenating a fixed /api/v0 prefix keeps the root whatever the connection
// configured, which is what makes a baseUrl override work.
func deepSeekEndpointsFor(baseURL string) deepSeekEndpoints {
	completion := strings.TrimRight(strings.TrimSpace(baseURL), "/")

	const suffix = "/chat/completion"
	root := completion
	if base, ok := strings.CutSuffix(completion, suffix); ok {
		root = base
	}
	return deepSeekEndpoints{
		usersCurrent:  root + "/users/current",
		sessionCreate: root + "/chat_session/create",
		sessionDelete: root + "/chat_session/delete",
		powChallenge:  root + "/chat/create_pow_challenge",
		completion:    completion,
	}
}

// deepSeekOrigin returns the scheme://host the browser fingerprint headers must
// name. A request whose Origin and Referer point at a different host than the
// one it is sent to is the single most obvious sign of a scripted client, so
// both follow whatever the connection configured.
func deepSeekOrigin(baseURL string) string {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(baseURL), "://")
	if !ok {
		return deepSeekWebBase
	}
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		return scheme + "://" + rest[:slash]
	}
	return scheme + "://" + rest
}
