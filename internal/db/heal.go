package db

import (
	json "encoding/json/v2"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// HealResult 报告这次修复动了什么。全 0 = 无事可做（幂等运行的正常结果）。
type HealResult struct {
	Nodes        int // 补上 data.prefix 的节点数
	Connections  int // 补上 providerSpecificData.prefix 的连接数
	Resurrected  int // 按悬空连接补回来的节点数（节点被删、连接还在）
}

// HealProviderNodePrefixes 给"有 name 但没有 prefix"的 providerNode 补回展示前缀，
// 并把它同步进这些节点名下连接的 providerSpecificData.prefix。
//
// 为什么需要它（2026-09-30 用户反馈，v1.9.5-r1）：
//
//	可用模型里出现 `openai-compatible-chat-<uuid>/deepseek-flash` —— 那是**内部节点 ID**。
//	kv.customModels 的键是 `<节点ID>|<模型>|llm`（设计如此），而列表发布时用
//	"节点 data.prefix（连接路径则是 providerSpecificData.prefix）"把它换成人类可读前缀；
//	**取不到就回退成节点 ID**。历史数据（旧版本建的节点 / 恢复的备份 / 别的客户端建的节点）
//	可能只有 name 没有 prefix，于是这类模型**永久**以内部 ID 出现在列表里；而它们**不是孤儿**
//	（节点与连接都在），清理孤儿按设计永远不会删它 —— 用户"清理了也没用"正是这个原因。
//
// 为什么改数据而不是只改显示：这个前缀**就是客户端要填的模型 ID**（路由按前缀解析，
// 见 internal/handlers/chat/resolution_test.go）。只改显示会出现"列表写着 A、调用必须用 B"。
// 补上前缀后 `<name>/<model>` 可路由，而旧的 `openai-compatible-chat-<uuid>/<model>` 仍然可路由
// （provider 仍是那个 ID）→ 两边都能用，不破坏已有客户端。
//
// 安全边界（"别产生新问题"）：
//   - **现有非空 prefix 一律不覆盖**（只补空）；
//   - name 为空的节点跳过（无从推断，宁缺勿造）；
//   - 与前缀冲突时加 `-2`/`-3`…（避免两个节点共用一个前缀导致路由歧义）；
//   - 派生前缀里不含 `/`（会破坏 `<prefix>/<model>` 的语法）与空白（折叠成 `-`）；
//   - 全部改动在一个事务里，要么都成要么都不成；
//   - **不动 updatedAt**：那是"用户改过这个 provider"的语义，修复不算用户编辑。
//   - 幂等：第二次调用返回 0/0 且不写任何一行。
func (r *Repo) HealProviderNodePrefixes() (HealResult, error) {
	var res HealResult
	if r == nil || r.db == nil {
		return res, fmt.Errorf("heal provider node prefixes: nil repo")
	}

	type nodeRow struct {
		id, name, data string
	}
	rows, err := r.db.Query("SELECT id, COALESCE(name, ''), COALESCE(data, '') FROM providerNodes")
	if err != nil {
		return res, fmt.Errorf("heal provider node prefixes: read nodes: %w", err)
	}
	var nodes []nodeRow
	used := map[string]bool{}
	for rows.Next() {
		var n nodeRow
		if err := rows.Scan(&n.id, &n.name, &n.data); err != nil {
			_ = rows.Close()
			return res, fmt.Errorf("heal provider node prefixes: scan node: %w", err)
		}
		nodes = append(nodes, n)
		if p := nodeDataPrefix(n.data); p != "" {
			used[p] = true
		}
	}
	if err := rows.Close(); err != nil {
		return res, fmt.Errorf("heal provider node prefixes: close nodes: %w", err)
	}

	// 先算出每个节点**应有的**前缀（只读阶段，无副作用）——冲突在这里就解开。
	type plan struct {
		id     string
		data   string
		prefix string
	}
	var plans []plan
	for _, n := range nodes {
		if nodeDataPrefix(n.data) != "" {
			continue // 已有非空前缀：绝不覆盖
		}
		cand := derivePrefixFromName(n.name)
		if cand == "" {
			continue // 没有可推断的名字：跳过（宁缺勿造）
		}
		base := cand
		for i := 2; used[cand] && i <= 99; i++ {
			cand = fmt.Sprintf("%s-%d", base, i)
		}
		if used[cand] {
			continue // 极端冲突：放弃这个节点，不冒险
		}
		used[cand] = true
		plans = append(plans, plan{id: n.id, data: n.data, prefix: cand})
	}

	// 已有前缀的节点也要参与"连接同步"（它们的连接可能同样缺 prefix → 连接路径会发布内部 ID）
	known := map[string]string{}
	for _, n := range nodes {
		if p := nodeDataPrefix(n.data); p != "" {
			known[n.id] = p
		}
	}
	for _, p := range plans {
		known[p.id] = p.prefix
	}

	// 连接列表**先读**（只读，不开事务）：它是"是否需要干活"的一部分 ——
	// 悬空连接（节点被删）的修复恰恰发生在"一个节点都没有"的库里，早退条件漏掉它就等于永远修不了。
	type connRow struct {
		id, provider, name, data string
	}
	connQuery, err := r.db.Query(
		`SELECT id, provider, COALESCE(name, ''), COALESCE(data, '') FROM providerConnections`)
	if err != nil {
		return res, fmt.Errorf("heal provider node prefixes: read connections: %w", err)
	}
	var connList []connRow
	for connQuery.Next() {
		var c connRow
		if err := connQuery.Scan(&c.id, &c.provider, &c.name, &c.data); err != nil {
			_ = connQuery.Close()
			return res, fmt.Errorf("heal provider node prefixes: scan connection: %w", err)
		}
		connList = append(connList, c)
	}
	if err := connQuery.Close(); err != nil {
		return res, fmt.Errorf("heal provider node prefixes: close connections: %w", err)
	}

	if len(plans) == 0 && len(known) == 0 && len(connList) == 0 {
		return res, nil
	}

	tx, err := r.db.Begin()
	if err != nil {
		return res, fmt.Errorf("heal provider node prefixes: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, p := range plans {
		m := nodeDataMap(p.data)
		m["prefix"] = p.prefix
		blob, err := json.Marshal(m)
		if err != nil {
			return res, fmt.Errorf("heal provider node prefixes: marshal node %s: %w", p.id, err)
		}
		if _, err := tx.Exec("UPDATE providerNodes SET data = ? WHERE id = ?", string(blob), p.id); err != nil {
			return res, fmt.Errorf("heal provider node prefixes: update node %s: %w", p.id, err)
		}
		res.Nodes++
	}

	now := time.Now().UTC().Format(time.RFC3339)
	for _, c := range connList {
		prefix := known[c.provider]
		if prefix == "" {
			// ── 悬空连接：节点被删、连接还在（provider 是**内部节点 ID** 形状）──
			// 必须把节点补回来，**不能只换显示名**：路由只认节点前缀
			// （resolvePrefixProvider → GetProviderNodeByPrefix），换出来的假名字能看不能用，
			// 比内部 ID 更糟。补回节点后 `<名字>/<模型>` 既显示正常、也真能路由。
			if !IsInternalNodeAlias(c.provider) {
				continue // 内置 provider（openrouter / oc / qd…）：本来就没有节点，绝不猜
			}
			name := strings.TrimSpace(c.name)
			if name == "" {
				continue // 连名字都没有：宁可不发布（列表侧会把它藏起来），也不编一个假名
			}
			p := derivePrefixFromName(name)
			if p == "" {
				continue
			}
			base := p
			for i := 2; used[p] && i <= 99; i++ {
				p = fmt.Sprintf("%s-%d", base, i)
			}
			if used[p] {
				continue
			}
			used[p] = true
			nd := map[string]any{"prefix": p}
			if at := psdStringOf(c.data, "apiType"); at != "" {
				nd["apiType"] = at
			} else {
				nd["apiType"] = "chat"
			}
			if bu := psdStringOf(c.data, "baseUrl"); bu != "" {
				nd["baseUrl"] = bu
			}
			blob, err := json.Marshal(nd)
			if err != nil {
				return res, fmt.Errorf("heal provider node prefixes: marshal resurrected node %s: %w", c.provider, err)
			}
			if _, err := tx.Exec(
				`INSERT INTO providerNodes (id, type, name, data, createdAt, updatedAt) VALUES (?, ?, ?, ?, ?, ?)`,
				c.provider, nodeTypeForID(c.provider), name, string(blob), now, now); err != nil {
				return res, fmt.Errorf("heal provider node prefixes: resurrect node %s: %w", c.provider, err)
			}
			known[c.provider] = p
			prefix = p
			res.Resurrected++
		}
		m := connDataMap(c.data)
		psd, _ := m["providerSpecificData"].(map[string]any)
		if psd == nil {
			psd = map[string]any{}
		}
		if s, _ := psd["prefix"].(string); strings.TrimSpace(s) != "" {
			continue // 已有非空前缀：绝不覆盖
		}
		psd["prefix"] = prefix
		m["providerSpecificData"] = psd
		blob, err := json.Marshal(m)
		if err != nil {
			return res, fmt.Errorf("heal provider node prefixes: marshal connection %s: %w", c.id, err)
		}
		if _, err := tx.Exec("UPDATE providerConnections SET data = ? WHERE id = ?", string(blob), c.id); err != nil {
			return res, fmt.Errorf("heal provider node prefixes: update connection %s: %w", c.id, err)
		}
		res.Connections++
	}

	if err := tx.Commit(); err != nil {
		return res, fmt.Errorf("heal provider node prefixes: commit: %w", err)
	}
	return res, nil
}

// nodeDataPrefix 取出 providerNodes.data.prefix（空/非法 JSON → ""）。
func nodeDataPrefix(raw string) string {
	if raw == "" {
		return ""
	}
	var d struct {
		Prefix string `json:"prefix"`
	}
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return ""
	}
	return strings.TrimSpace(d.Prefix)
}

// nodeDataMap 把节点 data 解成可改写的 map（保留未知字段）。
func nodeDataMap(raw string) map[string]any {
	m := map[string]any{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	return m
}

// connDataMap 把连接 data 解成可改写的 map（保留 apiKey 等未知字段）。
func connDataMap(raw string) map[string]any {
	m := map[string]any{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &m)
	}
	return m
}

// derivePrefixFromName 从节点名字派生一个能当模型 ID 前缀用的字符串：
// 去首尾空白、`/` 换成 `-`（否则破坏 `<prefix>/<model>` 语法）、空白折叠成 `-`。
// 结果为空表示无法从名字推断（调用方跳过，宁缺勿造）。
func derivePrefixFromName(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.Join(strings.Fields(s), "-")
	return strings.Trim(s, "-")
}

// internalNodeAliasRE 是**内部节点 ID 的形状**（自定义 provider 的节点主键）：
// `openai-compatible-chat-<uuid>`。它是实现细节，绝不该作为模型 ID 出现在用户面前。
//
// 唯一所有者：面板侧 (`module/webroot/parsers.js` 的 UUID_ALIAS)、本包（自愈与校验）、
// chat 包（列表发布前的兜底）必须用**同一个形状**，否则会出现"一处认得出、另一处认不出"
// 的静默漏网。面板那份是 JS，无法共享常量，改动时三处一起改。
var internalNodeAliasRE = regexp.MustCompile(
	`^openai-compatible-chat-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// IsInternalNodeAlias 判断别名是不是内部节点 ID 的形状（大小写不敏感）。
func IsInternalNodeAlias(alias string) bool {
	return internalNodeAliasRE.MatchString(alias)
}

// psdStringOf 读连接 data 里 providerSpecificData.<key> 的字符串值（缺失/非串 → ""）。
func psdStringOf(raw, key string) string {
	if raw == "" {
		return ""
	}
	m := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return ""
	}
	psd, _ := m["providerSpecificData"].(map[string]any)
	if psd == nil {
		return ""
	}
	s, _ := psd[key].(string)
	return strings.TrimSpace(s)
}

// nodeTypeForID 从节点 ID 推出它的 type 列取值（与创建接口的命名约定一致）。
func nodeTypeForID(id string) string {
	if strings.HasPrefix(id, "anthropic-compatible-") {
		return "anthropic-compatible"
	}
	return "openai-compatible"
}
