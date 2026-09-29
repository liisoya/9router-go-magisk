// Package codexquota reads Codex (ChatGPT) rate-limit windows from the
// wham/usage endpoint. Port of open-sse/services/usage/codex.js, shared by
// the dashboard quota tracker and the chat-side quota-aware picker so both
// read the same shape from one parser.
package codexquota

import (
	json "encoding/json/v2"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Window is one Codex rate-limit window. UsedPercent is 0..100; ResetAt is nil
// when the response omitted a parseable reset.
type Window struct {
	UsedPercent float64
	ResetAt     *time.Time
}

// Remaining is the percentage of the window still available.
func (w *Window) Remaining() float64 {
	if w == nil {
		return 0
	}
	return math.Max(0, 100-w.UsedPercent)
}

// Usage is the parsed wham/usage snapshot. Session/Weekly are the account's
// 5h and 7d windows; the Review/Spark pairs are separate metered features that
// upstream also surfaces.
type Usage struct {
	Plan          string
	LimitReached  bool
	Session       *Window
	Weekly        *Window
	ReviewSession *Window
	ReviewWeekly  *Window
	SparkSession  *Window
	SparkWeekly   *Window
	ResetCredits  int
}

// ---- parsing ---------------------------------------------------------------

// ParseUsage decodes a wham/usage response body.
func ParseUsage(body []byte) (*Usage, error) {
	var wire wireUsage
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, fmt.Errorf("codexquota.ParseUsage: decode: %w", err)
	}

	main := wire.mainRateLimit()
	review := wire.reviewRateLimit()
	spark := wire.sparkRateLimit()

	plan := strings.TrimSpace(wire.PlanType)
	if plan == "" && wire.Summary != nil {
		plan = strings.TrimSpace(wire.Summary.Plan)
	}
	if plan == "" {
		plan = "unknown"
	}

	usage := &Usage{
		Plan:          plan,
		Session:       toWindow(main.primary()),
		Weekly:        toWindow(main.secondary()),
		ReviewSession: toWindow(review.primary()),
		ReviewWeekly:  toWindow(review.secondary()),
		SparkSession:  toWindow(spark.primary()),
		SparkWeekly:   toWindow(spark.secondary()),
	}
	if b := main.body(); b != nil {
		usage.LimitReached = b.LimitReached
	}
	if wire.ResetCredits != nil {
		usage.ResetCredits = int(math.Max(0, finiteNum(wire.ResetCredits.AvailableCount)))
	}
	return usage, nil
}

// toWindow mirrors formatCodexWindow. A nil window stays nil so the caller can
// omit the row — upstream never fabricates a window the account does not have.
func toWindow(w *wireWindow) *Window {
	if w == nil {
		return nil
	}
	used := 0.0
	if w.UsedPercent != nil {
		used = *w.UsedPercent
	} else if w.PercentUsed != nil {
		used = *w.PercentUsed
	}
	if math.IsNaN(used) || math.IsInf(used, 0) {
		used = 0
	}
	out := &Window{UsedPercent: math.Max(0, math.Min(100, used))}
	for _, v := range []any{w.ResetAt, w.ResetsAt, w.ResetAtAlt} {
		if t := parseResetTime(v); t != nil {
			out.ResetAt = t
			break
		}
	}
	return out
}

// parseResetTime mirrors upstream parseResetTime: unix seconds below 1e12,
// milliseconds above, numeric strings by the same rule, ISO strings as dates.
// Returns nil on anything unparseable rather than a zero time.
func parseResetTime(v any) *time.Time {
	switch t := v.(type) {
	case nil:
		return nil
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return nil
		}
		return epochMS(t)
	case int:
		return epochMS(float64(t))
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil
		}
		if allDigits(s) {
			n, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil
			}
			return epochMS(n)
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999999Z07:00", "2006-01-02 15:04:05", "2006-01-02"} {
			if parsed, err := time.Parse(layout, s); err == nil {
				return new(parsed.UTC())
			}
		}
	}
	return nil
}

func epochMS(v float64) *time.Time {
	ms := v
	if ms < 1e12 {
		ms *= 1000
	}
	return new(time.UnixMilli(int64(ms)).UTC())
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

func finiteNum(v any) float64 {
	switch n := v.(type) {
	case float64:
		if !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n
		}
	case int:
		return float64(n)
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f
		}
	case map[string]any:
		// protobuf-json {val: n} envelope.
		if _, ok := n["val"]; ok {
			return finiteNum(n["val"])
		}
	}
	return 0
}
