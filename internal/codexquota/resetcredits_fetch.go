package codexquota

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxResetCreditBodyBytes bounds a wham reset-credit response. The list is
// small; a large body is a sign something is not a reset-credit response.
const maxResetCreditBodyBytes = 1 << 20

// TokenRefresher re-authenticates a Codex connection and returns a fresh access
// token. The dashboard passes one in; the codexquota package deliberately does
// not know how OAuth is stored, so the 401/403 retry does not drag that
// dependency in.
type TokenRefresher func(ctx context.Context) (string, error)

// resetCreditHeaders builds the request headers. `chatgpt-account-id` is the
// canonical Codex backend identity header and is what the wham endpoints scope
// the credits to, so it is sent whenever the connection carries an account id.
func resetCreditHeaders(accessToken, accountID string) map[string]string {
	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
		"Content-Type":  "application/json",
		"Accept":        "application/json",
	}
	if accountID != "" {
		headers["chatgpt-account-id"] = accountID
	}
	return headers
}

// doResetCreditRequest performs one wham call and returns the status and the
// decoded payload. The payload is decoded leniently — a non-JSON body becomes
// the raw string — because the outcome can legitimately be a bare token.
func doResetCreditRequest(
	ctx context.Context,
	client *http.Client,
	method, url, accessToken, accountID string,
	body []byte,
) (int, any, error) {
	if client == nil {
		client = defaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("codexquota: build reset-credit request: %w", err)
	}
	for k, v := range resetCreditHeaders(accessToken, accountID) {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		if proxyRefused(err) {
			return 0, nil, &ProxyRefusedError{URL: url, Err: err}
		}
		return 0, nil, fmt.Errorf("codexquota: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResetCreditBodyBytes))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("codexquota: read reset-credit body: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return resp.StatusCode, nil, nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return resp.StatusCode, string(raw), nil
	}
	return resp.StatusCode, payload, nil
}

// withAuthRetry runs call, and on a 401/403 refreshes the token and runs it
// once more. Codex rotates access tokens, so a credit that is perfectly valid
// can fail purely because the stored token aged out; retrying is the
// difference between "reset credits are broken" and a working button.
func withAuthRetry(
	ctx context.Context,
	accessToken string,
	refresh TokenRefresher,
	call func(token string) (int, any, error),
) (int, any, string, error) {
	status, payload, err := call(accessToken)
	if err != nil {
		return status, payload, accessToken, err
	}
	if (status != http.StatusUnauthorized && status != http.StatusForbidden) || refresh == nil {
		return status, payload, accessToken, nil
	}
	fresh, refreshErr := refresh(ctx)
	if refreshErr != nil {
		return status, payload, accessToken, refreshErr
	}
	status, payload, err = call(fresh)
	return status, payload, fresh, err
}

// ListResetCredits reads the redeemable credits on a Codex account.
func ListResetCredits(
	ctx context.Context,
	client *http.Client,
	accessToken, accountID string,
	refresh TokenRefresher,
	now time.Time,
) (ResetCreditList, error) {
	if accessToken == "" {
		return ResetCreditList{}, resetError(401, "codex_access_token_missing",
			"Codex OAuth access token is missing.")
	}
	list, _, err := listWithRefresh(ctx, client, accessToken, accountID, refresh, now)
	if err != nil {
		return ResetCreditList{}, err
	}
	return list, nil
}

// ConsumeResetCredit redeems one credit. requestedCreditID may be empty, in
// which case the soonest-expiring available credit is used.
func ConsumeResetCredit(
	ctx context.Context,
	client *http.Client,
	accessToken, accountID, idempotencyKey, requestedCreditID string,
	refresh TokenRefresher,
	now time.Time,
) (string, ResetCredit, error) {
	if accessToken == "" {
		return "", ResetCredit{}, resetError(401, "codex_access_token_missing",
			"Codex OAuth access token is missing.")
	}
	if idempotencyKey == "" {
		return "", ResetCredit{}, resetError(400, "idempotency_key_required",
			"An idempotency key is required to redeem a reset credit.")
	}
	// List first and select from what is actually redeemable, so the button
	// cannot ask Codex to consume a credit the server will refuse. The
	// refreshed token, if the list call triggered one, is carried into the
	// consume so the two calls agree on the account.
	list, token, err := listWithRefresh(ctx, client, accessToken, accountID, refresh, now)
	if err != nil {
		return "", ResetCredit{}, err
	}
	credit, err := SelectResetCredit(list, requestedCreditID)
	if err != nil {
		return "", ResetCredit{}, err
	}
	consumeBody, _ := json.Marshal(map[string]string{
		"redeem_request_id": idempotencyKey,
		"credit_id":         credit.ID,
	})
	// The same idempotency key is reused across the auth retry, so a retry
	// after a 401 can never redeem the credit twice.
	status, payload, _, err := withAuthRetry(ctx, token, refresh,
		func(fresh string) (int, any, error) {
			return doResetCreditRequest(ctx, client, http.MethodPost, ConsumeResetCreditsURL, fresh, accountID, consumeBody)
		})
	if err != nil {
		return "", ResetCredit{}, err
	}
	if status < 200 || status >= 300 {
		if typed := knownConsumeError(payload); typed != nil {
			return "", ResetCredit{}, typed
		}
		return "", ResetCredit{}, resetError(status, "codex_reset_credit_upstream_error",
			fmt.Sprintf("Codex reset-credit API returned HTTP %d.", status))
	}
	outcome, err := ParseConsumeOutcome(payload)
	if err != nil {
		return "", ResetCredit{}, err
	}
	return outcome, credit, nil
}

// listWithRefresh fetches the credit list, retrying once on 401/403, and
// returns the token that actually succeeded so a follow-up call reuses it
// instead of bouncing off the stale one.
func listWithRefresh(
	ctx context.Context,
	client *http.Client,
	accessToken, accountID string,
	refresh TokenRefresher,
	now time.Time,
) (ResetCreditList, string, error) {
	status, payload, token, err := withAuthRetry(ctx, accessToken, refresh,
		func(current string) (int, any, error) {
			return doResetCreditRequest(ctx, client, http.MethodGet, ResetCreditsURL, current, accountID, nil)
		})
	if err != nil {
		return ResetCreditList{}, token, err
	}
	if status < 200 || status >= 300 {
		if typed := knownConsumeError(payload); typed != nil {
			return ResetCreditList{}, token, typed
		}
		return ResetCreditList{}, token, resetError(status, "codex_reset_credit_upstream_error",
			fmt.Sprintf("Codex reset-credit API returned HTTP %d.", status))
	}
	list, ok := ParseAvailableResetCredits(payload, now)
	if !ok {
		return ResetCreditList{}, token, resetError(502, "unknown_reset_credit_response",
			"Codex returned an unreadable reset-credit list.")
	}
	return list, token, nil
}
