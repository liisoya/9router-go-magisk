package chat

import (
	"net/http"
	"sync"
	"sync/atomic"

	"9router/proxy/internal/db"
	"9router/proxy/internal/handlers/shared"
	"9router/proxy/internal/proxy"
)

type comboStickyState struct {
	Index               int
	ConsecutiveUseCount int
	// ServingIndex is the model index currently owning the turn. Mid-turn
	// requests reuse it so a tool-use sequence stays on the same provider,
	// even after Index has advanced for the next turn.
	ServingIndex int
}

// ChatHandler handles /v1/chat/completions (OpenAI) and /v1/messages (Claude) endpoints.
type ChatHandler struct {
	Repo        *db.Repo
	Client      *http.Client
	TokenSaver  *shared.TokenSaverConfig
	stickyMu    sync.Mutex
	stickyState map[string]*comboStickyState
	// prefixHealAt = 上次"展示前缀自愈"的 Unix 秒（0 = 从未）。列表端点每请求只做一次原子读，
	// 超过间隔才真去查库 —— 客户端可能高频轮询 /v1/models，不能每次都写库。
	// 见 prefix_heal.go（理由与安全边界写在 db.HealProviderNodePrefixes 的注释里）。
	prefixHealAt atomic.Int64
}

// Type aliases for shared types
type ModelInfo = shared.ModelInfo
type ConnectionData = shared.ConnectionData
type UsageLogInfo = shared.UsageLogInfo
type streamMetrics = shared.StreamMetrics
type upstreamError = proxy.UpstreamError
