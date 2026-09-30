package db

import (
	json "encoding/json/v2"
	"strings"
	"testing"
)

// healTestDB 建一个带真实 core schema 的临时库（heal 要碰 providerNodes/providerConnections/kv）。
func healTestDB(t *testing.T) (*Repo, func()) {
	t.Helper()
	database, cleanup := setupTestDB(t)
	if err := EnsureCoreSchema(database); err != nil {
		cleanup()
		t.Fatalf("EnsureCoreSchema: %v", err)
	}
	return NewRepo(database), cleanup
}

func seedHealNode(t *testing.T, r *Repo, id, name, data string) {
	t.Helper()
	if _, err := r.db.Exec(
		`INSERT INTO providerNodes (id, type, name, data, createdAt, updatedAt) VALUES
		 (?, 'openai-compatible', ?, ?, '2026-09-30T00:00:00Z', '2026-09-30T00:00:00Z')`,
		id, name, data); err != nil {
		t.Fatalf("seed node %s: %v", id, err)
	}
}

func seedHealConn(t *testing.T, r *Repo, id, provider, data string) {
	t.Helper()
	seedHealConnNamed(t, r, id, provider, "conn", data)
}

// seedHealConnNamed 允许指定连接名（悬空连接的自愈要用它派生前缀；空名 = 无从命名的情形）。
func seedHealConnNamed(t *testing.T, r *Repo, id, provider, name, data string) {
	t.Helper()
	if _, err := r.db.Exec(
		`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		 (?, ?, 'apikey', ?, 1, 1, ?, '2026-09-30T00:00:00Z', '2026-09-30T00:00:00Z')`,
		id, provider, name, data); err != nil {
		t.Fatalf("seed connection %s: %v", id, err)
	}
}

func nodePrefixOf(t *testing.T, r *Repo, id string) string {
	t.Helper()
	var data string
	if err := r.db.QueryRow(`SELECT COALESCE(data,'') FROM providerNodes WHERE id = ?`, id).Scan(&data); err != nil {
		t.Fatalf("read node %s: %v", id, err)
	}
	return nodeDataPrefix(data)
}

func connDataOf(t *testing.T, r *Repo, id string) map[string]any {
	t.Helper()
	var data string
	if err := r.db.QueryRow(`SELECT COALESCE(data,'') FROM providerConnections WHERE id = ?`, id).Scan(&data); err != nil {
		t.Fatalf("read connection %s: %v", id, err)
	}
	m := map[string]any{}
	_ = json.Unmarshal([]byte(data), &m)
	return m
}

func psdPrefixOf(m map[string]any) string {
	psd, _ := m["providerSpecificData"].(map[string]any)
	if psd == nil {
		return ""
	}
	s, _ := psd["prefix"].(string)
	return s
}

// 用户反馈的形状：节点有 name、没有 prefix → 补上前缀（否则模型以内部节点 ID 出现在列表里）。
func TestHealProviderNodePrefixes_FillsMissingPrefixFromName(t *testing.T) {
	r, cleanup := healTestDB(t)
	defer cleanup()

	seedHealNode(t, r, "node-a", "AMD", `{"apiType":"chat","baseUrl":"https://amd.example.com/v1"}`)
	seedHealConn(t, r, "conn-a", "node-a", `{"apiKey":"sk-secret"}`)

	res, err := r.HealProviderNodePrefixes()
	if err != nil {
		t.Fatalf("heal: %v", err)
	}
	if res.Nodes != 1 || res.Connections != 1 {
		t.Fatalf("expected 1 node + 1 connection healed, got %+v", res)
	}
	if got := nodePrefixOf(t, r, "node-a"); got != "AMD" {
		t.Errorf("node prefix = %q, want %q", got, "AMD")
	}
	m := connDataOf(t, r, "conn-a")
	if got := psdPrefixOf(m); got != "AMD" {
		t.Errorf("connection psd.prefix = %q, want %q", got, "AMD")
	}
	// **绝不能丢字段**：连接里的 apiKey 必须原样还在（json 往返最容易在这里出事）
	if m["apiKey"] != "sk-secret" {
		t.Errorf("apiKey 被改坏了：%v", m["apiKey"])
	}
}

// 已有非空前缀：一个字都不能改（"只补空，不覆盖"）。
func TestHealProviderNodePrefixes_NeverOverwritesExistingPrefix(t *testing.T) {
	r, cleanup := healTestDB(t)
	defer cleanup()

	seedHealNode(t, r, "node-a", "别的名字", `{"prefix":"Keep","apiType":"chat"}`)
	seedHealConn(t, r, "conn-a", "node-a", `{"apiKey":"k","providerSpecificData":{"prefix":"KeepConn"}}`)

	res, err := r.HealProviderNodePrefixes()
	if err != nil {
		t.Fatalf("heal: %v", err)
	}
	if res.Nodes != 0 || res.Connections != 0 {
		t.Fatalf("不该改任何东西，却报了 %+v", res)
	}
	if got := nodePrefixOf(t, r, "node-a"); got != "Keep" {
		t.Errorf("node prefix 被覆盖成 %q", got)
	}
	if got := psdPrefixOf(connDataOf(t, r, "conn-a")); got != "KeepConn" {
		t.Errorf("connection psd.prefix 被覆盖成 %q", got)
	}
}

// 幂等：第二次运行什么都不改（0/0），数据也不变 —— 读路径每 60s 会调一次，必须便宜且无副作用。
func TestHealProviderNodePrefixes_Idempotent(t *testing.T) {
	r, cleanup := healTestDB(t)
	defer cleanup()

	seedHealNode(t, r, "node-a", "AMD", `{"apiType":"chat"}`)
	seedHealConn(t, r, "conn-a", "node-a", `{"apiKey":"k"}`)

	if res, err := r.HealProviderNodePrefixes(); err != nil || res.Nodes != 1 {
		t.Fatalf("first heal: %+v / %v", res, err)
	}
	after := nodePrefixOf(t, r, "node-a")
	res, err := r.HealProviderNodePrefixes()
	if err != nil {
		t.Fatalf("second heal: %v", err)
	}
	if res.Nodes != 0 || res.Connections != 0 {
		t.Errorf("第二次不该改动任何行，却报了 %+v", res)
	}
	if got := nodePrefixOf(t, r, "node-a"); got != after {
		t.Errorf("第二次运行改了前缀：%q → %q", after, got)
	}
}

// 同名冲突：必须解成不同前缀（两个节点共用一个前缀会让路由歧义 → 比丑名字更糟）。
func TestHealProviderNodePrefixes_CollisionGetsSuffix(t *testing.T) {
	r, cleanup := healTestDB(t)
	defer cleanup()

	seedHealNode(t, r, "node-a", "Same", `{}`)
	seedHealNode(t, r, "node-b", "Same", `{}`)

	res, err := r.HealProviderNodePrefixes()
	if err != nil {
		t.Fatalf("heal: %v", err)
	}
	if res.Nodes != 2 {
		t.Fatalf("expected 2 nodes healed, got %+v", res)
	}
	a, b := nodePrefixOf(t, r, "node-a"), nodePrefixOf(t, r, "node-b")
	if a == b {
		t.Fatalf("两个节点拿到同一个前缀 %q（会造成路由歧义）", a)
	}
	if a != "Same" || b != "Same-2" {
		t.Errorf("前缀 = %q / %q, want Same / Same-2", a, b)
	}
	// 也不能撞到**已存在**的前缀
	seedHealNode(t, r, "node-c", "Same-2", `{}`)
	if _, err := r.HealProviderNodePrefixes(); err != nil {
		t.Fatalf("heal: %v", err)
	}
	if got := nodePrefixOf(t, r, "node-c"); got == "Same-2" {
		t.Errorf("新节点前缀撞上了已存在的 %q", got)
	}
}

// 没有 name：跳过（宁缺勿造 —— 编不出前缀就不编，绝不猜）。
func TestHealProviderNodePrefixes_SkipsNodeWithoutName(t *testing.T) {
	r, cleanup := healTestDB(t)
	defer cleanup()

	seedHealNode(t, r, "node-a", "", `{"apiType":"chat"}`)
	seedHealNode(t, r, "node-b", "   ", `{"apiType":"chat"}`)

	res, err := r.HealProviderNodePrefixes()
	if err != nil {
		t.Fatalf("heal: %v", err)
	}
	if res.Nodes != 0 {
		t.Errorf("没有名字却补了前缀：%+v", res)
	}
	if got := nodePrefixOf(t, r, "node-a"); got != "" {
		t.Errorf("凭空造了前缀 %q", got)
	}
}

// 名字里的 `/` 与空白必须被规整：`/` 会破坏 `<prefix>/<model>` 的语法。
func TestHealProviderNodePrefixes_SanitizesDerivedPrefix(t *testing.T) {
	r, cleanup := healTestDB(t)
	defer cleanup()

	seedHealNode(t, r, "node-a", " a/b  c ", `{}`)
	if _, err := r.HealProviderNodePrefixes(); err != nil {
		t.Fatalf("heal: %v", err)
	}
	got := nodePrefixOf(t, r, "node-a")
	if strings.Contains(got, "/") || strings.Contains(got, " ") {
		t.Errorf("前缀 %q 含 / 或空格（会破坏模型 ID 语法）", got)
	}
	if got != "a-b-c" {
		t.Errorf("前缀 = %q, want a-b-c", got)
	}
}

// 内置 provider（无节点）的连接：不猜、不写（合法数据，绝不能被自愈碰到）。
func TestHealProviderNodePrefixes_LeavesBuiltinConnectionsAlone(t *testing.T) {
	r, cleanup := healTestDB(t)
	defer cleanup()

	seedHealConn(t, r, "conn-openrouter", "openrouter", `{"apiKey":"k"}`)

	res, err := r.HealProviderNodePrefixes()
	if err != nil {
		t.Fatalf("heal: %v", err)
	}
	if res.Nodes != 0 || res.Connections != 0 {
		t.Errorf("碰到了无节点的内置 provider 连接：%+v", res)
	}
	if got := psdPrefixOf(connDataOf(t, r, "conn-openrouter")); got != "" {
		t.Errorf("给内置 provider 编了个前缀 %q（内置 provider 不该有节点前缀）", got)
	}
}

// 悬空连接（节点已删、连接还在）→ 把节点按连接上的信息补回来：
// 只有这样 `<名字>/<模型>` 才是**可路由**的（路由只认节点前缀），而不是一个好看的假别名。
func TestHealProviderNodePrefixes_ResurrectsNodeForDanglingConnection(t *testing.T) {
	r, cleanup := healTestDB(t)
	defer cleanup()

	const dangling = "openai-compatible-chat-b5b35395-3025-4810-90cd-5473475261b8"
	seedHealConnNamed(t, r, "conn-x", dangling, "AMD",
		`{"apiKey":"k","providerSpecificData":{"apiType":"chat","baseUrl":"https://x.example.com/v1"}}`)

	res, err := r.HealProviderNodePrefixes()
	if err != nil {
		t.Fatalf("heal: %v", err)
	}
	if res.Resurrected != 1 {
		t.Fatalf("expected 1 resurrected node, got %+v", res)
	}
	// 节点以**原来的 ID** 回来了（连接的 provider 指向它），并且带上了名字派生的前缀
	if got := nodePrefixOf(t, r, dangling); got != "AMD" {
		t.Errorf("resurrected node prefix = %q, want %q（用连接名派生）", got, "AMD")
	}
	var name, nodeType, baseURL string
	if err := r.db.QueryRow(
		`SELECT name, type, COALESCE(json_extract(data,'$.baseUrl'),'') FROM providerNodes WHERE id = ?`,
		dangling).Scan(&name, &nodeType, &baseURL); err != nil {
		t.Fatalf("read resurrected node: %v", err)
	}
	if name != "AMD" || nodeType != "openai-compatible" {
		t.Errorf("resurrected node name/type = %q/%q", name, nodeType)
	}
	if baseURL != "https://x.example.com/v1" {
		t.Errorf("resurrected node baseUrl = %q（应从连接的 providerSpecificData 继承）", baseURL)
	}
	// 连接上的前缀同步补上（列表的连接路径也用它）
	if got := psdPrefixOf(connDataOf(t, r, "conn-x")); got != "AMD" {
		t.Errorf("connection psd.prefix = %q, want AMD", got)
	}
	// 幂等：第二次不再补
	res2, err := r.HealProviderNodePrefixes()
	if err != nil {
		t.Fatalf("second heal: %v", err)
	}
	if res2.Resurrected != 0 || res2.Nodes != 0 || res2.Connections != 0 {
		t.Errorf("第二次不该再动任何东西：%+v", res2)
	}
}

// 连名字都没有的悬空连接：宁可不补（列表侧会把它藏起来），也绝不编一个假名字。
func TestHealProviderNodePrefixes_DoesNotResurrectWithoutName(t *testing.T) {
	r, cleanup := healTestDB(t)
	defer cleanup()

	const dangling = "openai-compatible-chat-deadbeef-0000-4000-8000-000000000009"
	seedHealConnNamed(t, r, "conn-noname", dangling, "", `{"apiKey":"k"}`)

	res, err := r.HealProviderNodePrefixes()
	if err != nil {
		t.Fatalf("heal: %v", err)
	}
	if res.Resurrected != 0 {
		t.Errorf("没有名字却补了节点：%+v", res)
	}
	var n int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM providerNodes WHERE id = ?`, dangling).Scan(&n); err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if n != 0 {
		t.Errorf("凭空造了一个节点（应该宁缺勿造）")
	}
}

// 预防：删节点必须**原子**（节点 + 它的连接），不能留下"节点没了、连接还在"的半状态 ——
// 那正是内部 ID 出现在可用模型里的源头（2026-09-30）。
func TestDeleteProviderNode_IsAtomicAndRemovesConnections(t *testing.T) {
	r, cleanup := healTestDB(t)
	defer cleanup()

	const node = "openai-compatible-chat-069fdcc2-f29b-41da-b3b9-11f4702667ac"
	const other = "openai-compatible-chat-14be83e7-a41e-46af-8070-9c80a83ef1e5"
	seedHealNode(t, r, node, "待删", `{"prefix":"Del"}`)
	seedHealConn(t, r, "conn-del", node, `{"apiKey":"k"}`)
	seedHealNode(t, r, other, "别动", `{"prefix":"Keep"}`)
	seedHealConn(t, r, "conn-keep", other, `{"apiKey":"k"}`)

	if err := r.DeleteProviderNode(node); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM providerNodes WHERE id = ?`,
	} {
		var n int
		if err := r.db.QueryRow(q, node).Scan(&n); err != nil || n != 0 {
			t.Errorf("节点没删干净：n=%d err=%v", n, err)
		}
	}
	var conns int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM providerConnections WHERE provider = ?`, node).Scan(&conns); err != nil {
		t.Fatalf("count connections: %v", err)
	}
	if conns != 0 {
		t.Errorf("连接没跟着删（留下半状态 = 内部 ID 会重新出现在模型列表里）：%d 条", conns)
	}
	// 别的 provider 一根毫毛都不能动
	var kept int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM providerConnections WHERE provider = ?`, other).Scan(&kept); err != nil {
		t.Fatalf("count kept: %v", err)
	}
	if kept != 1 {
		t.Errorf("误删了别的 provider 的连接：%d", kept)
	}
}
