package middleware

import (
	"9router/proxy/internal/log"
	"context"
	"net/http"
	"strings"

	"9router/proxy/internal/db"
	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/models"
)

// ContextKey is a custom type for context keys to avoid collisions.
type ContextKey string

// ApiKeyContextKey is the context key for the authenticated API key object.
const ApiKeyContextKey ContextKey = "apiKey"

// RequireApiKey creates a middleware handler that authenticates requests using client API keys.
// It checks the Authorization header (Bearer <key>) and the query parameter `key`.
// Valid keys are retrieved from the SQLite database; inactive or disabled keys are rejected with 401.
func RequireApiKey(repo *db.Repo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKeyString := ExtractApiKey(r)
			if apiKeyString == "" {
				handlerutil.WriteJSONError(w, http.StatusUnauthorized, "Authentication required. Provide an API key via Authorization: Bearer <key> header or ?key=<key> query parameter.")
				return
			}

			// Validate via SQLite repository and retrieve details
			apiKeyObj, err := repo.GetApiKeyByKey(apiKeyString)
			if err != nil {
				log.Error("auth", "DB lookup error", "error", err)
				handlerutil.WriteJSONError(w, http.StatusInternalServerError, "Internal server error")
				return
			}
			if apiKeyObj == nil {
				handlerutil.WriteJSONError(w, http.StatusUnauthorized, "Invalid API key.")
				return
			}

			if apiKeyObj.IsActive != 1 {
				handlerutil.WriteJSONError(w, http.StatusUnauthorized, "Invalid or inactive API key.")
				return
			}

			// Inject API Key info into the request context for downstream handlers/logging
			ctx := context.WithValue(r.Context(), ApiKeyContextKey, apiKeyObj)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetAuthenticatedApiKey retrieves the authenticated APIKey object from the request context.
func GetAuthenticatedApiKey(r *http.Request) *models.APIKey {
	val := r.Context().Value(ApiKeyContextKey)
	if val == nil {
		return nil
	}
	keyObj, ok := val.(*models.APIKey)
	if !ok {
		return nil
	}
	return keyObj
}

// ExtractApiKey extracts the client API key from the request.
// For standard API routes, only header-based auth is accepted to avoid leaks.
// For EventSource / SSE stream endpoints (where browsers cannot set custom headers),
// query parameters `key` or `apiKey` are accepted as a fallback.
func ExtractApiKey(r *http.Request) string {
	// 1. Try Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
			return strings.TrimSpace(parts[1])
		}
	}

	// 2. Try custom X-API-Key header as fallback
	if xApiKey := r.Header.Get("X-API-Key"); xApiKey != "" {
		return xApiKey
	}

	// 3. For EventSource / SSE endpoints, accept query parameter key or apiKey
	if strings.HasSuffix(r.URL.Path, "/stream") {
		if qKey := r.URL.Query().Get("key"); qKey != "" {
			return qKey
		}
		if qKey := r.URL.Query().Get("apiKey"); qKey != "" {
			return qKey
		}
	}

	return ""
}
