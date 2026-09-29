package codexquota

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"9router/proxy/internal/proxy"
)

// UsageURL is the wham usage endpoint. Settable so tests in other packages
// (dashboard, chat) can point it at an httptest server.
var UsageURL = "https://chatgpt.com/backend-api/wham/usage"

// fetchTimeout bounds a single wham round trip.
var fetchTimeout = 15 * time.Second

// ---- fetching --------------------------------------------------------------

// StatusError reports a non-2xx wham response. Callers fail open on it: the
// upstream dashboard handler turns it into a message with no quota rows, and
// the chat-side cache keeps its last known reading.
type StatusError struct {
	Status int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("codexquota.Fetch: wham usage returned status %d", e.Status)
}

// defaultClient is the client Fetch uses when the caller does not supply one.
//
// It wraps the environment transport in proxy.FallbackTransport for the same
// reason the chat path does (internal/proxy/fallback_transport.go): a sandbox
// or corporate HTTP(S)_PROXY in the environment refuses the CONNECT tunnel to
// some provider hosts with 403 Forbidden, and Go surfaces that refusal as
// `Get "https://…": Forbidden` — a transport error, not a status. Without the
// fallback the quota tracker breaks on exactly the hosts chat traffic reaches
// fine through the fallback.
var defaultClient = &http.Client{
	Transport: proxy.NewFallbackTransport(http.DefaultTransport),
	Timeout:   fetchTimeout,
}

// ProxyRefusedError reports that neither the configured proxy nor a direct
// connection could reach the endpoint. The underlying "Forbidden" text is a
// proxy refusal, not the provider rejecting the credential, so it is called
// out separately rather than being shown as a bare upstream error.
type ProxyRefusedError struct {
	URL string
	Err error
}

func (e *ProxyRefusedError) Error() string {
	return fmt.Sprintf("codexquota.Fetch: %s unreachable — the configured HTTP(S)_PROXY refused the tunnel and a direct connection also failed: %v", e.URL, e.Err)
}

func (e *ProxyRefusedError) Unwrap() error { return e.Err }

// proxyRefused reports whether an error is the Go transport's rendering of a
// refused CONNECT tunnel, which carries the reason phrase as its text.
func proxyRefused(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "forbidden")
}

// Fetch reads live quota for a Codex access token.
//
// Header parity with upstream getCodexUsage: exactly `Authorization: Bearer`
// and `Accept: application/json`. The Codex CLI identity headers the executor
// sends on the responses endpoint are deliberately NOT copied here — this
// endpoint authenticates on the bearer alone, and adding them changes nothing
// (verified against the live endpoint with and without them).
func Fetch(ctx context.Context, client *http.Client, accessToken string) (*Usage, error) {
	if strings.TrimSpace(accessToken) == "" {
		return nil, fmt.Errorf("codexquota.Fetch: missing access token")
	}
	if client == nil {
		client = defaultClient
	}

	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, UsageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("codexquota.Fetch: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		if proxyRefused(err) {
			return nil, &ProxyRefusedError{URL: UsageURL, Err: err}
		}
		return nil, fmt.Errorf("codexquota.Fetch: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("codexquota.Fetch: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &StatusError{Status: resp.StatusCode}
	}
	return ParseUsage(body)
}
