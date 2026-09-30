package chat

import (
	"time"

	"9router/proxy/internal/db"
	"9router/proxy/internal/log"
)

// prefixHealEvery 是"展示前缀自愈"的最小间隔。客户端可能高频轮询 /v1/models，
// 每次都查库/写库不行；自愈是幂等的，慢一拍（≤1 分钟）没有任何影响。
const prefixHealEvery = 60 * time.Second

// isInternalNodeAlias 判断别名是不是**内部节点 ID 的形状**（`openai-compatible-chat-<uuid>`）。
// 形状判定的唯一所有者在 internal/db（IsInternalNodeAlias），这里只是本包的短名字，
// 免得调用点散落一个自己写的正则（那样迟早出现"一处认得出、另一处漏网"）。
func isInternalNodeAlias(alias string) bool { return db.IsInternalNodeAlias(alias) }

// healCustomModelPrefixes 幂等地给"有名字但没前缀"的节点/连接补回展示前缀。
//
// 为什么放在读路径：这类历史数据（旧版本建的节点 / 恢复的备份 / 别的客户端建的节点）不会
// 自己好起来，而用户看到的正是模型列表 —— 读完自愈比"等下次重启"更符合直觉。
// 实现与安全边界（只补空、冲突解、事务、幂等）全在 db.HealProviderNodePrefixes 的注释里。
func (h *ChatHandler) healCustomModelPrefixes() {
	if h == nil || h.Repo == nil {
		return
	}
	now := time.Now().Unix()
	last := h.prefixHealAt.Load()
	if last != 0 && now-last < int64(prefixHealEvery/time.Second) {
		return
	}
	// CAS 失败 = 另一个请求刚好在这中间开始自愈了：直接返回，不重复查库。
	if !h.prefixHealAt.CompareAndSwap(last, now) {
		return
	}
	res, err := h.Repo.HealProviderNodePrefixes()
	if err != nil {
		log.Warn("db", "展示前缀自愈失败（不影响模型列表返回）", "error", err)
		return
	}
	if res.Nodes > 0 || res.Connections > 0 || res.Resurrected > 0 {
		log.Info("db", "展示前缀自愈：补回缺失的展示前缀（历史数据缺 prefix 会让模型以内部节点 ID 出现）",
			"nodes", res.Nodes, "connections", res.Connections, "resurrected", res.Resurrected)
	}
}
