package media

import (
	"errors"
	"net/http"
	"strings"

	"9router/proxy/internal/handlerutil"
	"golang.org/x/text/unicode/norm"
)

// Search error envelope shared by handleAntigravitySearch and handleXquikSearch,
// mirroring upstream open-sse/handlers/search errorResult: the original upstream
// status is preserved in a typed error so media.go can map it to the right HTTP
// status instead of collapsing everything to 502.
const (
	defaultSearchMaxResults = 5
	maxSearchMaxResults     = 100
)

// searchUpstreamError carries an upstream search failure with its HTTP status
// and whether another account may be tried (upstream checkFallbackError:
// 4xx request-scoped errors like 400/405/409/422 must not lock the account).
type searchUpstreamError struct {
	Status    int
	Message   string
	Retryable bool
}

func (e *searchUpstreamError) Error() string { return e.Message }

// searchError builds a non-retryable request-scoped search error.
func searchError(status int, msg string) *searchUpstreamError {
	return &searchUpstreamError{Status: status, Message: msg}
}

// searchErrorParts extracts the message and HTTP status from a search error,
// defaulting to 502 for legacy plain errors.
func searchErrorParts(err error) (string, int) {
	var serr *searchUpstreamError
	if errors.As(err, &serr) && serr != nil {
		status := serr.Status
		if status == 0 {
			status = http.StatusBadGateway
		}
		return serr.Message, status
	}
	return err.Error(), http.StatusBadGateway
}

// writeSearchError writes a search failure with its original upstream status
// (upstream errorResult parity) instead of collapsing everything to 502.
func writeSearchError(w http.ResponseWriter, err error) {
	msg, status := searchErrorParts(err)
	handlerutil.WriteJSONError(w, status, msg)
}

// sanitizeSearchQuery mirrors upstream sanitizeQuery (open-sse/handlers/search):
// reject control characters, NFKC-normalize, trim and collapse whitespace.
// Returns "" when the query is empty after normalization.
func sanitizeSearchQuery(query string) string {
	if query == "" {
		return ""
	}
	for _, r := range query {
		if r < 0x20 && r != '\t' && r != '\n' || r == 0x7f {
			return ""
		}
	}
	clean := norm.NFKC.String(query)
	if clean == "" {
		clean = query
	}
	return strings.Join(strings.Fields(clean), " ")
}
