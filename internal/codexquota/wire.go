package codexquota

import (
	json "encoding/json/v2"
	"strings"
)

// Wire shapes for the wham/usage response.
//
// The endpoint has shipped three envelopes for the main window body
// (rate_limit, rate_limits, rate_limits_by_limit_id.codex), and
// getCodexRateLimitBody unwraps one further nested rate_limit level. All are
// decoded here and resolved in that precedence order, so a new layout is a
// field on wireUsage rather than a change to the parser.

type wireWindow struct {
	UsedPercent *float64 `json:"used_percent"`
	PercentUsed *float64 `json:"percent_used"`
	ResetAt     any      `json:"reset_at"`
	ResetsAt    any      `json:"resets_at"`
	ResetAtAlt  any      `json:"resetAt"`
}

type wireRateLimit struct {
	LimitReached    bool           `json:"limit_reached"`
	RateLimit       *wireRateLimit `json:"rate_limit"`
	PrimaryWindow   *wireWindow    `json:"primary_window"`
	Primary         *wireWindow    `json:"primary"`
	SecondaryWindow *wireWindow    `json:"secondary_window"`
	Secondary       *wireWindow    `json:"secondary"`
}

// body mirrors getCodexRateLimitBody: a nested rate_limit wins, else self.
func (r *wireRateLimit) body() *wireRateLimit {
	if r == nil {
		return nil
	}
	if r.RateLimit != nil {
		return r.RateLimit
	}
	return r
}

// primary resolves the 5h window. Upstream checks the unwrapped body first
// and falls back to the outer snapshot, so both levels are consulted.
func (r *wireRateLimit) primary() *wireWindow {
	if r == nil {
		return nil
	}
	if b := r.body(); b != nil {
		if b.PrimaryWindow != nil {
			return b.PrimaryWindow
		}
		if b.Primary != nil {
			return b.Primary
		}
	}
	if r.PrimaryWindow != nil {
		return r.PrimaryWindow
	}
	return r.Primary
}

// secondary resolves the 7d window, same precedence as primary.
func (r *wireRateLimit) secondary() *wireWindow {
	if r == nil {
		return nil
	}
	if b := r.body(); b != nil {
		if b.SecondaryWindow != nil {
			return b.SecondaryWindow
		}
		if b.Secondary != nil {
			return b.Secondary
		}
	}
	if r.SecondaryWindow != nil {
		return r.SecondaryWindow
	}
	return r.Secondary
}

type wireUsage struct {
	PlanType string `json:"plan_type"`
	Summary  *struct {
		Plan string `json:"plan"`
	} `json:"summary"`
	RateLimit  *wireRateLimit            `json:"rate_limit"`
	RateLimits *wireRateLimit            `json:"rate_limits"`
	ByLimitID  map[string]*wireRateLimit `json:"rate_limits_by_limit_id"`

	CodeReviewRateLimit *wireRateLimit `json:"code_review_rate_limit"`
	ReviewRateLimit     *wireRateLimit `json:"review_rate_limit"`
	SparkRateLimit      *wireRateLimit `json:"spark_rate_limit"`
	Gpt53SparkRateLimit *wireRateLimit `json:"gpt_5_3_codex_spark_rate_limit"`

	AdditionalRateLimits []map[string]any `json:"additional_rate_limits"`

	ResetCredits *struct {
		AvailableCount any `json:"available_count"`
	} `json:"rate_limit_reset_credits"`
}

// mainRateLimit mirrors upstream's
// `data.rate_limit || data.rate_limits || data.rate_limits_by_limit_id?.codex || {}`.
func (u *wireUsage) mainRateLimit() *wireRateLimit {
	switch {
	case u.RateLimit != nil:
		return u.RateLimit
	case u.RateLimits != nil:
		return u.RateLimits
	case u.ByLimitID != nil && u.ByLimitID["codex"] != nil:
		return u.ByLimitID["codex"]
	}
	return nil
}

// reviewRateLimit mirrors getCodexReviewRateLimit: direct keys, then
// rate_limits_by_limit_id, then an additional_rate_limits scan.
func (u *wireUsage) reviewRateLimit() *wireRateLimit {
	if u.CodeReviewRateLimit != nil {
		return u.CodeReviewRateLimit
	}
	if u.ReviewRateLimit != nil {
		return u.ReviewRateLimit
	}
	for _, key := range []string{"code_review", "codex_review", "review"} {
		if u.ByLimitID != nil && u.ByLimitID[key] != nil {
			return u.ByLimitID[key]
		}
	}
	return findAdditional(u.AdditionalRateLimits, func(id string) bool {
		return id == "code_review" || id == "codex_review" || id == "review" || strings.Contains(id, "review")
	})
}

// sparkRateLimit mirrors getCodexSparkRateLimit.
func (u *wireUsage) sparkRateLimit() *wireRateLimit {
	if u.SparkRateLimit != nil {
		return u.SparkRateLimit
	}
	if u.Gpt53SparkRateLimit != nil {
		return u.Gpt53SparkRateLimit
	}
	for _, key := range []string{"gpt-5.3-codex-spark", "gpt_5_3_codex_spark", "spark"} {
		if u.ByLimitID != nil && u.ByLimitID[key] != nil {
			return u.ByLimitID[key]
		}
	}
	return findAdditional(u.AdditionalRateLimits, func(id string) bool {
		return strings.Contains(id, "spark") || strings.Contains(id, "5.3-codex-spark")
	})
}

// findAdditional returns the first additional_rate_limits entry whose
// limit_name / metered_feature / id matches.
func findAdditional(entries []map[string]any, match func(id string) bool) *wireRateLimit {
	for _, entry := range entries {
		id := strings.ToLower(firstStr(entry, "limit_name", "metered_feature", "id"))
		if match(id) {
			var rl wireRateLimit
			if err := json.Unmarshal(marshalEntry(entry), &rl); err != nil {
				continue
			}
			return &rl
		}
	}
	return nil
}

func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// marshalEntry re-encodes a decoded additional_rate_limits entry so it can be
// decoded into wireRateLimit without hand-copying every field.
func marshalEntry(entry map[string]any) []byte {
	b, err := json.Marshal(entry)
	if err != nil {
		return []byte("{}")
	}
	return b
}
