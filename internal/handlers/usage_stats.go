package handlers

import (
	"9router/proxy/internal/db"
	"9router/proxy/internal/handlerutil"
	"9router/proxy/internal/translator"
	"9router/proxy/internal/usagetracker"
	json "encoding/json/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ProviderUsageItem struct {
	Requests         int     `json:"requests"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	Cost             float64 `json:"cost"`
}

type ModelUsageItem struct {
	Requests         int     `json:"requests"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	Cost             float64 `json:"cost"`
	RawModel         string  `json:"rawModel"`
	Provider         string  `json:"provider"`
	LastUsed         string  `json:"lastUsed"`
}

type AccountUsageItem struct {
	Requests         int     `json:"requests"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	Cost             float64 `json:"cost"`
	RawModel         string  `json:"rawModel"`
	Provider         string  `json:"provider"`
	ConnectionID     string  `json:"connectionId"`
	AccountName      string  `json:"accountName"`
	LastUsed         string  `json:"lastUsed"`
}

type ApiKeyUsageItem struct {
	Requests         int     `json:"requests"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	Cost             float64 `json:"cost"`
	RawModel         string  `json:"rawModel"`
	Provider         string  `json:"provider"`
	ApiKeyMasked     string  `json:"apiKeyMasked,omitempty"`
	KeyName          string  `json:"keyName"`
	ApiKeyKey        string  `json:"apiKeyKey"`
	LastUsed         string  `json:"lastUsed"`
}

type EndpointUsageItem struct {
	Requests         int     `json:"requests"`
	PromptTokens     int64   `json:"promptTokens"`
	CompletionTokens int64   `json:"completionTokens"`
	CachedTokens     int64   `json:"cachedTokens"`
	Cost             float64 `json:"cost"`
	Endpoint         string  `json:"endpoint"`
	RawModel         string  `json:"rawModel"`
	Provider         string  `json:"provider"`
	LastUsed         string  `json:"lastUsed"`
}

type UsageStatsResponse struct {
	TotalRequests         int                          `json:"totalRequests"`
	TotalPromptTokens     int64                        `json:"totalPromptTokens"`
	TotalCompletionTokens int64                        `json:"totalCompletionTokens"`
	TotalCachedTokens     int64                        `json:"totalCachedTokens"`
	TotalCost             float64                      `json:"totalCost"`
	ByProvider            map[string]ProviderUsageItem `json:"byProvider"`
	ByModel               map[string]ModelUsageItem    `json:"byModel"`
	ByAccount             map[string]AccountUsageItem  `json:"byAccount"`
	ByApiKey              map[string]ApiKeyUsageItem   `json:"byApiKey"`
	ByEndpoint            map[string]EndpointUsageItem `json:"byEndpoint"`
	ActiveRequests        []usagetracker.ActiveRequest `json:"activeRequests"`
	RecentRequests        []usagetracker.RecentRequest `json:"recentRequests"`
	ErrorProvider         string                       `json:"errorProvider"`
	Pending               usagetracker.PendingState    `json:"pending"`
}

// HandleUsageStats returns aggregated stats for the specified period ("today", "24h", "7d", "30d", "60d").
func HandleUsageStats(repo *db.Repo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		period := r.URL.Query().Get("period")
		if period == "" {
			period = "today"
		}

		tracker := usagetracker.GetTracker()
		activeState := tracker.GetActiveState(repo)

		resp := UsageStatsResponse{
			ByProvider:     make(map[string]ProviderUsageItem),
			ByModel:        make(map[string]ModelUsageItem),
			ByAccount:      make(map[string]AccountUsageItem),
			ByApiKey:       make(map[string]ApiKeyUsageItem),
			ByEndpoint:     make(map[string]EndpointUsageItem),
			ActiveRequests: activeState.ActiveRequests,
			Pending:        activeState.Pending,
			ErrorProvider:  activeState.ErrorProvider,
		}

		// Load connection mapping for friendly account names
		connMap := make(map[string]string)
		if conns, err := repo.GetProviderConnections("", true); err == nil {
			for _, c := range conns {
				name := c.ID
				if c.Name != nil && *c.Name != "" {
					name = *c.Name
				} else if c.Email != nil && *c.Email != "" {
					name = *c.Email
				}
				connMap[c.ID] = name
			}
		}

		// Load provider nodes for custom reverse-proxy display names
		nodeNameMap := make(map[string]string)
		if nodes, err := repo.GetProviderNodes(); err == nil {
			for _, n := range nodes {
				if n.ID != "" && n.Name != nil && *n.Name != "" {
					nodeNameMap[n.ID] = *n.Name
				}
			}
		}

		useDailySummary := period != "today" && period != "24h"

		if useDailySummary {
			daysLimit := 7
			if period == "30d" {
				daysLimit = 30
			} else if period == "60d" {
				daysLimit = 60
			} else if period == "all" {
				daysLimit = 365
			}

			dailyRows, err := repo.GetUsageDailyRecent(daysLimit)
			if err == nil {
				for _, rowJSON := range dailyRows {
					var dayData map[string]any
					if err := json.Unmarshal([]byte(rowJSON), &dayData); err != nil {
						continue
					}

					// byProvider
					if bp, ok := dayData["byProvider"].(map[string]any); ok {
						for prov, pVal := range bp {
							if pm, ok := pVal.(map[string]any); ok {
								cur := resp.ByProvider[prov]
								cur.Requests += getMapInt(pm, "requests")
								cur.PromptTokens += getMapInt64(pm, "promptTokens")
								cur.CompletionTokens += getMapInt64(pm, "completionTokens")
								cur.CachedTokens += getMapInt64(pm, "cachedTokens")
								cur.Cost += getMapFloat(pm, "cost")
								resp.ByProvider[prov] = cur
							}
						}
					}

					// byModel
					if bm, ok := dayData["byModel"].(map[string]any); ok {
						for mk, mVal := range bm {
							if mm, ok := mVal.(map[string]any); ok {
								rawModel, _ := mm["rawModel"].(string)
								prov, _ := mm["provider"].(string)
								if rawModel == "" {
									parts := strings.Split(mk, "|")
									rawModel = parts[0]
									if len(parts) > 1 && prov == "" {
										prov = parts[1]
									}
								}
								statsKey := rawModel
								if prov != "" {
									statsKey = rawModel + " (" + prov + ")"
								}
								displayName := prov
								if dn, ok := nodeNameMap[prov]; ok && dn != "" {
									displayName = dn
								}

								cur := resp.ByModel[statsKey]
								cur.RawModel = rawModel
								cur.Provider = displayName
								cur.Requests += getMapInt(mm, "requests")
								cur.PromptTokens += getMapInt64(mm, "promptTokens")
								cur.CompletionTokens += getMapInt64(mm, "completionTokens")
								cur.CachedTokens += getMapInt64(mm, "cachedTokens")
								cur.Cost += getMapFloat(mm, "cost")
								resp.ByModel[statsKey] = cur
							}
						}
					}

					// byAccount
					if ba, ok := dayData["byAccount"].(map[string]any); ok {
						for connID, aVal := range ba {
							if am, ok := aVal.(map[string]any); ok {
								rawModel, _ := am["rawModel"].(string)
								prov, _ := am["provider"].(string)
								accName := connMap[connID]
								if accName == "" {
									if len(connID) > 8 {
										accName = "Account " + connID[:8] + "..."
									} else {
										accName = "Account " + connID
									}
								}
								accountKey := rawModel + " (" + prov + " - " + accName + ")"
								displayName := prov
								if dn, ok := nodeNameMap[prov]; ok && dn != "" {
									displayName = dn
								}

								cur := resp.ByAccount[accountKey]
								cur.RawModel = rawModel
								cur.Provider = displayName
								cur.ConnectionID = connID
								cur.AccountName = accName
								cur.Requests += getMapInt(am, "requests")
								cur.PromptTokens += getMapInt64(am, "promptTokens")
								cur.CompletionTokens += getMapInt64(am, "completionTokens")
								cur.CachedTokens += getMapInt64(am, "cachedTokens")
								cur.Cost += getMapFloat(am, "cost")
								resp.ByAccount[accountKey] = cur
							}
						}
					}
				}
			}
		} else {
			// Today or 24h: query usageHistory directly
			var cutoff string
			now := time.Now().UTC()
			if period == "today" {
				startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
				cutoff = startOfDay.Format(time.RFC3339)
			} else {
				cutoff = now.Add(-24 * time.Hour).Format(time.RFC3339)
			}

			histRows, err := repo.GetUsageHistorySince(cutoff)
			if err == nil {
				for _, r := range histRows {
					promptTok := int64(r.PromptTokens)
					complTok := int64(r.CompletionTokens)
					cachedTok := int64(translator.CachedTokensFromJSON([]byte(r.Tokens)))
					entryCost := r.Cost

					provName := r.Provider
					provDisplayName := provName
					if dn, ok := nodeNameMap[provName]; ok && dn != "" {
						provDisplayName = dn
					}

					// byProvider
					if provName != "" {
						p := resp.ByProvider[provName]
						p.Requests++
						p.PromptTokens += promptTok
						p.CompletionTokens += complTok
						p.CachedTokens += cachedTok
						p.Cost += entryCost
						resp.ByProvider[provName] = p
					}

					// byModel
					modelKey := r.Model
					if provName != "" {
						modelKey = r.Model + " (" + provName + ")"
					}
					m := resp.ByModel[modelKey]
					m.RawModel = r.Model
					m.Provider = provDisplayName
					m.Requests++
					m.PromptTokens += promptTok
					m.CompletionTokens += complTok
					m.CachedTokens += cachedTok
					m.Cost += entryCost
					if r.Timestamp > m.LastUsed {
						m.LastUsed = r.Timestamp
					}
					resp.ByModel[modelKey] = m

					// byAccount
					if r.ConnectionID != "" {
						accName := connMap[r.ConnectionID]
						if accName == "" {
							if len(r.ConnectionID) > 8 {
								accName = "Account " + r.ConnectionID[:8] + "..."
							} else {
								accName = "Account " + r.ConnectionID
							}
						}
						accKey := r.Model + " (" + provName + " - " + accName + ")"
						a := resp.ByAccount[accKey]
						a.RawModel = r.Model
						a.Provider = provDisplayName
						a.ConnectionID = r.ConnectionID
						a.AccountName = accName
						a.Requests++
						a.PromptTokens += promptTok
						a.CompletionTokens += complTok
						a.CachedTokens += cachedTok
						a.Cost += entryCost
						if r.Timestamp > a.LastUsed {
							a.LastUsed = r.Timestamp
						}
						resp.ByAccount[accKey] = a
					}
				}
			}
		}

		// Calculate total aggregates from byProvider
		for _, p := range resp.ByProvider {
			resp.TotalRequests += p.Requests
			resp.TotalPromptTokens += p.PromptTokens
			resp.TotalCompletionTokens += p.CompletionTokens
			resp.TotalCachedTokens += p.CachedTokens
			resp.TotalCost += p.Cost
		}

		// Build recent requests list (20 deduped from usageHistory)
		if recentHistory, err := repo.GetRecentUsageHistory(60); err == nil {
			var dedupedRecent []usagetracker.RecentRequest
			seen := make(map[string]bool)
			for _, rh := range recentHistory {
				if rh.PromptTokens == 0 && rh.CompletionTokens == 0 {
					continue
				}
				min := ""
				if len(rh.Timestamp) >= 16 {
					min = rh.Timestamp[:16]
				}
				k := rh.Model + "|" + rh.Provider + "|" + strconv.Itoa(rh.PromptTokens) + "|" + strconv.Itoa(rh.CompletionTokens) + "|" + min
				if seen[k] {
					continue
				}
				seen[k] = true

				cachedTok := int(translator.CachedTokensFromJSON([]byte(rh.Tokens)))
				status := "ok"
				if rh.Status != "success" && rh.Status != "ok" && rh.Status != "" {
					status = rh.Status
				}

				dedupedRecent = append(dedupedRecent, usagetracker.RecentRequest{
					Timestamp:        rh.Timestamp,
					Model:            rh.Model,
					Provider:         rh.Provider,
					PromptTokens:     rh.PromptTokens,
					CompletionTokens: rh.CompletionTokens,
					CachedTokens:     cachedTok,
					Status:           status,
				})
				if len(dedupedRecent) >= 20 {
					break
				}
			}
			resp.RecentRequests = dedupedRecent
		}

		handlerutil.WriteJSON(w, http.StatusOK, resp)
	}
}

// HandleRequestDetails returns paged request detail objects for the Details tab.
func HandleRequestDetails(repo *db.Repo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 50
		offset := 0
		if lStr := r.URL.Query().Get("limit"); lStr != "" {
			if parsed, err := strconv.Atoi(lStr); err == nil && parsed > 0 && parsed <= 100 {
				limit = parsed
			}
		}
		if oStr := r.URL.Query().Get("offset"); oStr != "" {
			if parsed, err := strconv.Atoi(oStr); err == nil && parsed >= 0 {
				offset = parsed
			}
		}

		rawJSONs, total, err := repo.GetRequestDetailsPaged(limit, offset)
		if err != nil {
			handlerutil.WriteJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		details := make([]any, 0, len(rawJSONs))
		for _, raw := range rawJSONs {
			var item map[string]any
			if err := json.Unmarshal([]byte(raw), &item); err != nil {
				continue
			}
			if tokens, ok := item["tokens"].(map[string]any); ok {
				rawTokens, marshalErr := json.Marshal(tokens)
				if marshalErr == nil {
					tokens["cached_tokens"] = float64(translator.CachedTokensFromJSON(rawTokens))
				}
			}
			details = append(details, item)
		}

		handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
			"details": details,
			"total":   total,
			"limit":   limit,
			"offset":  offset,
		})
	}
}

// Helper functions for map extraction
func getMapInt(m map[string]any, key string) int {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

func getMapInt64(m map[string]any, key string) int64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int64:
			return n
		case int:
			return int64(n)
		}
	}
	return 0
}

func getMapFloat(m map[string]any, key string) float64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		}
	}
	return 0
}
