// Package auth implements the dashboard login session: an HS256 JWT stored in
// an httpOnly `auth_token` cookie. Mirrors upstream src/lib/auth/dashboardSession.js
// so the Go dashboard can enforce login the way the Next dashboard does.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"math"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	json "encoding/json/v2"

	"9router/proxy/internal/config"
	"9router/proxy/internal/db"
)

const (
	// CookieName is the dashboard session cookie name (upstream parity).
	CookieName = "auth_token"
	// TokenTTL is the session lifetime (upstream SESSION_MAX_AGE_SEC = 24h).
	TokenTTL = 24 * time.Hour
	// CLITokenHeader lets local CLI clients bypass the login gate, mirroring
	// upstream's x-9r-cli-token bypass in dashboardGuard.
	CLITokenHeader = "x-9r-cli-token"
)

// Secret returns the HS256 signing secret: JWT_SECRET if set, otherwise the
// persisted DATA_DIR/jwt-secret generated at config load.
func Secret() string {
	return config.LoadConfig().JWTSecret
}

// RequireLogin mirrors upstream's `settings.requireLogin !== false`: login is
// required unless the operator explicitly disabled it, and an unset value (or
// an unreadable settings row) means login is on.
func RequireLogin(repo *db.Repo) bool {
	raw, err := repo.GetSettingsRaw()
	if err != nil || raw == nil {
		return true
	}
	v, ok := raw["requireLogin"]
	if !ok {
		return true
	}
	b, ok := v.(bool)
	if !ok {
		return true
	}
	return b
}

// SessionClaims is the JWT payload. Upstream issues {authenticated:true} with a
// 24h expiry (plus oidc/saml identity claims on SSO logins); iat/exp are added
// here so a token can be validated statelessly.
type SessionClaims struct {
	Authenticated bool   `json:"authenticated"`
	Oidc          bool   `json:"oidc,omitempty"`
	OidcName      string `json:"oidcName,omitempty"`
	OidcEmail     string `json:"oidcEmail,omitempty"`
	Saml          bool   `json:"saml,omitempty"`
	SamlName      string `json:"samlName,omitempty"`
	SamlEmail     string `json:"samlEmail,omitempty"`
	Iat           int64  `json:"iat"`
	Exp           int64  `json:"exp"`
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// Sign creates a signed HS256 JWT carrying the `authenticated` claim.
func Sign(secret string, now time.Time) (string, error) {
	return SignWith(secret, now, SessionClaims{Authenticated: true})
}

// SignWith creates a signed HS256 JWT with extra claims (SSO login flows).
func SignWith(secret string, now time.Time, extra SessionClaims) (string, error) {
	if secret == "" {
		return "", errors.New("no jwt secret available")
	}
	payload, err := json.Marshal(SessionClaims{
		Authenticated: true,
		Oidc:          extra.Oidc,
		OidcName:      extra.OidcName,
		OidcEmail:     extra.OidcEmail,
		Saml:          extra.Saml,
		SamlName:      extra.SamlName,
		SamlEmail:     extra.SamlEmail,
		Iat:           now.Unix(),
		Exp:           now.Add(TokenTTL).Unix(),
	})
	if err != nil {
		return "", err
	}
	signingInput := b64([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + b64(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	return signingInput + "." + b64(mac.Sum(nil)), nil
}

// Verify checks a session token's signature, authenticated claim and expiry.
func Verify(token, secret string) bool {
	if token == "" || secret == "" {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || subtle.ConstantTimeCompare(mac.Sum(nil), got) != 1 {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims SessionClaims
	if err := json.Unmarshal(raw, &claims); err != nil {
		return false
	}
	if !claims.Authenticated {
		return false
	}
	if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
		return false
	}
	return true
}

// SessionValid reports whether the request carries a valid session cookie.
func SessionValid(r *http.Request) bool {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return false
	}
	return Verify(c.Value, Secret())
}

// SessionClaimSet decodes the verified session payload, or nil when the
// request carries no valid auth_token cookie. The fields are the upstream SSO
// session payload (oidc/saml identity on SSO logins).
func SessionClaimSet(r *http.Request) *SessionClaims {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	return decodeClaims(c.Value, Secret())
}

// decodeClaims verifies the signature/expiry and returns the payload.
func decodeClaims(token, secret string) *SessionClaims {
	if token == "" || secret == "" {
		return nil
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || subtle.ConstantTimeCompare(mac.Sum(nil), got) != 1 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims SessionClaims
	if err := json.Unmarshal(raw, &claims); err != nil || !claims.Authenticated {
		return nil
	}
	if claims.Exp > 0 && time.Now().Unix() > claims.Exp {
		return nil
	}
	return &claims
}

// secureCookie mirrors upstream shouldUseSecureCookie: secure when forced by
// env, or when the request reached us over HTTPS via a reverse proxy.
func secureCookie(r *http.Request) bool {
	if strings.EqualFold(r.Header.Get("x-forwarded-proto"), "https") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(os.Getenv("AUTH_COOKIE_SECURE")), "true")
}

// SetCookie writes the session cookie (httpOnly, SameSite=Lax, 24h max-age).
func SetCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookie(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(TokenTTL.Seconds()),
	})
}

// ClearCookie expires the session cookie (logout).
func ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// Login lockout mirrors upstream src/lib/auth/loginLimiter.js: 5 failures
// inside a 1h window locks one client bucket for 30s, then 2m, 10m, 30m.
// Buckets reset on success (or process restart) and on 1h of silence.
const (
	maxLoginFailsBeforeLock = 5
	loginFailWindow         = time.Hour
)

var loginLockSteps = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	30 * time.Minute,
}

type loginAttempt struct {
	fails     int
	lockUntil time.Time
	lockLevel int
	lastFail  time.Time
}

var (
	loginMu       sync.Mutex
	loginAttempts = map[string]*loginAttempt{}
)

// LoginLocked reports whether the client bucket is currently locked and how
// many whole seconds remain. Unknown state reads as unlocked.
func LoginLocked(ip string) (bool, int) {
	loginMu.Lock()
	defer loginMu.Unlock()
	e := loginEntryLocked(ip)
	if e == nil || e.lockUntil.IsZero() {
		return false, 0
	}
	remaining := time.Until(e.lockUntil)
	if remaining <= 0 {
		return false, 0
	}
	return true, int(math.Ceil(remaining.Seconds()))
}

// maxLoginBuckets caps scanner-driven growth: one-off probes from thousands
// of unique IPs must not grow the map forever (pruning is otherwise only
// opportunistic per-IP on repeat visits).
const maxLoginBuckets = 5000

// RecordLoginFail bumps the failure bucket, locking it once the threshold is
// hit, and returns attempts left before the next lockout.
func RecordLoginFail(ip string) int {
	loginMu.Lock()
	defer loginMu.Unlock()
	e := loginEntryLocked(ip)
	if e == nil {
		if len(loginAttempts) >= maxLoginBuckets {
			// Evict buckets silent for a full window; if still full (active
			// distributed scan), drop the oldest lastFail to stay bounded.
			sweepLoginBucketsLocked()
			if len(loginAttempts) >= maxLoginBuckets {
				evictOldestLoginBucketLocked()
			}
		}
		e = &loginAttempt{}
		loginAttempts[ip] = e
	}
	e.fails++
	e.lastFail = time.Now()
	if e.fails >= maxLoginFailsBeforeLock {
		step := loginLockSteps[min(e.lockLevel, len(loginLockSteps)-1)]
		e.lockUntil = time.Now().Add(step)
		e.lockLevel++
		e.fails = 0
	}
	return max(maxLoginFailsBeforeLock-e.fails, 0)
}

// RecordLoginSuccess clears the client bucket after a good login.
func RecordLoginSuccess(ip string) {
	loginMu.Lock()
	defer loginMu.Unlock()
	delete(loginAttempts, ip)
}

// ResetLoginLimiter drops every login bucket. Tests only.
func ResetLoginLimiter() {
	loginMu.Lock()
	defer loginMu.Unlock()
	loginAttempts = map[string]*loginAttempt{}
}

// sweepLoginBucketsLocked drops every bucket silent for a full window.
// Caller must hold loginMu.
func sweepLoginBucketsLocked() {
	now := time.Now()
	for ip, e := range loginAttempts {
		if !e.lastFail.IsZero() && now.Sub(e.lastFail) > loginFailWindow &&
			(now.After(e.lockUntil) || e.lockUntil.IsZero()) {
			delete(loginAttempts, ip)
		}
	}
}

// evictOldestLoginBucketLocked drops the stalest bucket to stay under cap.
// Caller must hold loginMu.
func evictOldestLoginBucketLocked() {
	var oldestIP string
	var oldest time.Time
	first := true
	for ip, e := range loginAttempts {
		if first || e.lastFail.Before(oldest) {
			oldestIP, oldest, first = ip, e.lastFail, false
		}
	}
	if !first {
		delete(loginAttempts, oldestIP)
	}
}

// loginEntryLocked returns the live bucket, pruning entries silent for a full
// window while unlocked (upstream FAIL_WINDOW_MS auto-reset).
func loginEntryLocked(ip string) *loginAttempt {
	e, ok := loginAttempts[ip]
	if !ok {
		return nil
	}
	if !e.lastFail.IsZero() &&
		time.Since(e.lastFail) > loginFailWindow &&
		(time.Now().After(e.lockUntil) || e.lockUntil.IsZero()) {
		delete(loginAttempts, ip)
		return nil
	}
	return e
}

// LoginClientIP buckets login attempts. The stamped x-9r-real-ip is trusted
// only with the per-process peer proof (upstream trustedPeer); a TRUST_PROXY
// XFF is honored only when explicitly enabled. Otherwise every caller shares
// one bucket so spoofed headers cannot escape the limiter.
func LoginClientIP(r *http.Request) string {
	if token := os.Getenv("NINEROUTER_PEER_TOKEN"); token != "" {
		if subtle.ConstantTimeCompare(
			[]byte(r.Header.Get("x-9r-peer-token")),
			[]byte(token),
		) == 1 {
			if ip := strings.TrimSpace(r.Header.Get("x-9r-real-ip")); ip != "" {
				return ip
			}
		}
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TRUST_PROXY")), "true") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("TRUST_CLOUDFLARE")), "true") {
		if cfIP := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cfIP != "" {
			return cfIP
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first, _, _ := strings.Cut(xff, ","); strings.TrimSpace(first) != "" {
				return strings.TrimSpace(first)
			}
		}
	}
	if strings.TrimSpace(r.Header.Get("x-9r-real-ip")) != "" ||
		strings.TrimSpace(r.Header.Get("X-Forwarded-For")) != "" ||
		strings.TrimSpace(r.Header.Get("CF-Connecting-IP")) != "" {
		return "unknown"
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host)
	}
	if addr := strings.TrimSpace(r.RemoteAddr); addr != "" {
		return addr
	}
	return "unknown"
}
