package oauth

import (
	"net/http"
	"net/url"
	"strings"
)

// defaultCallbackHost is the last-resort host when the request carries no Host
// (should not happen for real HTTP traffic; httptest always sets one).
const defaultCallbackHost = "localhost:8080"

// validRedirectURI reports whether v is an absolute http(s) URL with a host.
// Only such values are honored as an explicit ?redirect_uri= override, so a
// crafted value can never inject javascript:/data: URLs into provider auth URLs.
func validRedirectURI(v string) bool {
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// requestHost returns the host the dashboard was opened on, honoring
// reverse-proxy headers (vite dev proxy passes the browser host through).
func requestHost(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		if h := strings.TrimSpace(strings.Split(fwd, ",")[0]); h != "" {
			return h
		}
	}
	if r.Host != "" {
		return r.Host
	}
	return defaultCallbackHost
}

// requestScheme returns https when the request came over TLS or a proxy says so.
func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		if p := strings.ToLower(strings.TrimSpace(strings.Split(proto, ",")[0])); p == "https" || p == "http" {
			return p
		}
	}
	return "http"
}

// callbackRedirectURI resolves the OAuth callback URL for authorize handlers:
// an explicit ?redirect_uri= wins when valid (mirrors upstream 9router
// route.js), otherwise it is derived from the request host so the callback
// follows wherever the dashboard is served (host + port), e.g.
// http://localhost:20131/callback.
func callbackRedirectURI(r *http.Request) string {
	if v := strings.TrimSpace(r.URL.Query().Get("redirect_uri")); validRedirectURI(v) {
		return v
	}
	return requestScheme(r) + "://" + requestHost(r) + "/callback"
}

// callbackRedirectURIFor resolves the authorize-step redirect URI for a PKCE
// provider. A provider with a fixedRedirectURI (Codex) always wins over the
// dashboard-derived value: its public OAuth client registered exactly one
// loopback callback, so a dashboard-host URL makes auth.openai.com reject the
// authorize step with invalid_authorize_request before the user ever sees the
// login page. Every other provider keeps following the dashboard.
func callbackRedirectURIFor(cfg *pkceConfig, r *http.Request) string {
	if cfg != nil && cfg.fixedRedirectURI != "" {
		return cfg.fixedRedirectURI
	}
	return callbackRedirectURI(r)
}

// exchangeRedirectURIFor is the exchange-step twin of callbackRedirectURIFor.
// The frontend echoes back the redirectUri the authorize step handed it, so
// both legs always agree; the fixed value is the fallback for clients that
// omit it.
func exchangeRedirectURIFor(cfg *pkceConfig, r *http.Request, camel, snake string) string {
	if cfg != nil && cfg.fixedRedirectURI != "" {
		return cfg.fixedRedirectURI
	}
	return exchangeRedirectURI(r, camel, snake)
}

// exchangeRedirectURI resolves the redirect_uri for exchange handlers: the
// value sent back by the frontend (which echoes authorize's redirectUri, so
// both legs always match) wins, otherwise fall back to the request host.
func exchangeRedirectURI(r *http.Request, camel, snake string) string {
	if v := strings.TrimSpace(camel); v != "" {
		return v
	}
	if v := strings.TrimSpace(snake); v != "" {
		return v
	}
	return requestScheme(r) + "://" + requestHost(r) + "/callback"
}
