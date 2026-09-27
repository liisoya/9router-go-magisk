package oauth

import (
	"encoding/base64"
	json "encoding/json/v2"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Port of src/lib/oauth/kiroExternalIdp.js normalizeKiroExternalIdpAuth:
// validates CLIProxyAPI external_idp auth JSON for Microsoft accounts.

var microsoftTokenEndpointHosts = map[string]bool{
	"login.microsoftonline.com": true,
	"login.microsoft.com":       true,
	"login.windows.net":         true,
}

type kiroExternalIDPToken struct {
	AccessToken          string
	RefreshToken         string
	ExpiresAt            string
	Email                string
	ProviderSpecificData map[string]any
}

func kiroIDPStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func validateMicrosoftTokenEndpoint(raw string) (string, error) {
	endpoint := strings.TrimSpace(raw)
	if endpoint == "" {
		return "", fmt.Errorf("token_endpoint is required")
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("token_endpoint must be a valid URL")
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return "", fmt.Errorf("token_endpoint must use https")
	}
	if !microsoftTokenEndpointHosts[strings.ToLower(parsed.Hostname())] {
		return "", fmt.Errorf("token_endpoint must be a Microsoft login endpoint")
	}
	return parsed.String(), nil
}

func normalizeKiroIDPScope(v any) string {
	switch t := v.(type) {
	case string:
		fields := strings.Fields(t)
		return strings.Join(fields, " ")
	case []any:
		parts := make([]string, 0, len(t))
		for _, p := range t {
			if s, ok := p.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func decodeKiroIDPExpiry(accessToken string) string {
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp <= 0 {
		return ""
	}
	return time.Unix(int64(claims.Exp), 0).UTC().Format(time.RFC3339)
}

func resolveKiroIDPExpiresAt(input map[string]any) string {
	for _, k := range []string{"expired", "expires_at", "expiresAt"} {
		if s, ok := input[k].(string); ok && strings.TrimSpace(s) != "" {
			if tm, err := time.Parse(time.RFC3339, strings.TrimSpace(s)); err == nil {
				return tm.UTC().Format(time.RFC3339)
			}
		}
	}
	for _, k := range []string{"expires_in", "expiresIn"} {
		var secs float64
		switch v := input[k].(type) {
		case float64:
			secs = v
		case int:
			secs = float64(v)
		case int64:
			secs = float64(v)
		}
		if secs > 0 {
			return time.Now().Add(time.Duration(secs) * time.Second).UTC().Format(time.RFC3339)
		}
	}
	if exp := decodeKiroIDPExpiry(kiroIDPStr(input, "access_token", "accessToken")); exp != "" {
		return exp
	}
	return time.Now().Add(3600 * time.Second).UTC().Format(time.RFC3339)
}

// normalizeKiroExternalIDPAuth validates CLIProxyAPI external_idp auth JSON.
func normalizeKiroExternalIDPAuth(raw map[string]any) (*kiroExternalIDPToken, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("CLIProxyAPI auth JSON is required")
	}
	if am := kiroIDPStr(raw, "auth_method", "authMethod"); am != "" && am != "external_idp" {
		return nil, fmt.Errorf("Only external_idp Kiro auth is supported by this importer")
	}
	accessToken := kiroIDPStr(raw, "access_token", "accessToken")
	refreshToken := kiroIDPStr(raw, "refresh_token", "refreshToken")
	clientID := kiroIDPStr(raw, "client_id", "clientId")
	tokenEndpoint, err := validateMicrosoftTokenEndpoint(kiroIDPStr(raw, "token_endpoint", "tokenEndpoint"))
	if err != nil {
		return nil, err
	}
	profileArn := kiroIDPStr(raw, "profile_arn", "profileArn")
	region := kiroIDPStr(raw, "region")
	if region == "" {
		region = "us-east-1"
	}
	scope := normalizeKiroIDPScope(firstNonNil(raw["scopes"], raw["scope"]))

	if accessToken == "" {
		return nil, fmt.Errorf("access_token is required")
	}
	if refreshToken == "" {
		return nil, fmt.Errorf("refresh_token is required")
	}
	if clientID == "" {
		return nil, fmt.Errorf("client_id is required")
	}
	if scope == "" {
		return nil, fmt.Errorf("scopes is required")
	}
	if profileArn == "" {
		return nil, fmt.Errorf("profile_arn is required")
	}

	email := kiroIDPStr(raw, "email")
	if email == "" {
		email = extractEmailFromJWT(accessToken)
	}

	return &kiroExternalIDPToken{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    resolveKiroIDPExpiresAt(raw),
		Email:        email,
		ProviderSpecificData: map[string]any{
			"profileArn":    profileArn,
			"region":        region,
			"authMethod":    "external_idp",
			"provider":      "CLIProxyAPI",
			"clientId":      clientID,
			"tokenEndpoint": tokenEndpoint,
			"scope":         scope,
		},
	}, nil
}

func firstNonNil(vals ...any) any {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}
