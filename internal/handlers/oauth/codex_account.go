package oauth

import (
	"encoding/base64"
	"strings"

	json "encoding/json/v2"
)

// codexAccountClaims is what the Codex OAuth id_token tells us about the
// signed-in ChatGPT account.
type codexAccountClaims struct {
	Email     string
	AccountID string
	PlanType  string
}

// codexAuthClaimKey is the namespaced claim block OpenAI puts the ChatGPT
// account metadata in. The claims live there rather than at the JWT root;
// root-level account_id/plan_type are kept as the legacy fallback.
const codexAuthClaimKey = "https://api.openai.com/auth"

// codexAccountInfo decodes the Codex id_token and extracts the account email,
// ChatGPT account id and plan type.
//
// The account id is not cosmetic: the codex responses endpoint rejects
// requests that do not carry it in the chatgpt-account-id header, so a
// connection stored without it authorizes but cannot complete a single call.
// Ported from upstream extractCodexAccountInfo (src/lib/oauth/providerHelpers.js).
func codexAccountInfo(idToken string) codexAccountClaims {
	payload, ok := decodeJWTClaims(idToken)
	if !ok {
		return codexAccountClaims{}
	}
	auth, _ := payload[codexAuthClaimKey].(map[string]any)
	return codexAccountClaims{
		Email:     stringClaim(payload, "email"),
		AccountID: firstNonEmpty(stringClaim(auth, "chatgpt_account_id"), stringClaim(payload, "account_id")),
		PlanType:  firstNonEmpty(stringClaim(auth, "chatgpt_plan_type"), stringClaim(payload, "plan_type")),
	}
}

// decodeJWTClaims base64url-decodes a JWT payload, tolerating both the padded
// and unpadded encodings. Signature verification is the token endpoint's job:
// the id_token reached us over TLS from the provider's own token endpoint, so
// these claims are trusted for routing metadata only, never for authorization.
func decodeJWTClaims(token string) (map[string]any, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 || parts[1] == "" {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, false
	}
	claims := map[string]any{}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return nil, false
	}
	return claims, true
}

// stringClaim reads a string field, tolerating a missing or non-string value.
func stringClaim(claims map[string]any, key string) string {
	if claims == nil {
		return ""
	}
	v, _ := claims[key].(string)
	return v
}
