package chat

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"9router/proxy/internal/db"
)

// 用户反馈（2026-09-30，v1.9.5-r1）：清理孤儿数据之后，可用模型里仍然出现
//
//	openai-compatible-chat-63ab874d-5ec5-48b5-9098-f3307545300f/deepseek-flash
//
// 机制（读代码确认，非推断）：kv.customModels 的键是 `<节点ID>|<模型>|llm`（设计如此），
// 列表展示时优先用节点的 `data.prefix` 换成好看的前缀，**取不到就回退成节点 ID**：
//   appendConnectionModels  : 连接 psd.prefix → 注册表别名 → providerID
//   appendLooseCustomModels : 节点 data.prefix → providerID
// 而 `providerNodePrefixMap` 只读 `providerNodes.data.prefix`（该结构连 `name` 字段都没有），
// 所以"节点存在且有人类可读的 name，但 data.prefix 缺失"的历史数据（旧版本 / 导入 / 别的客户端
// 建的节点）会**永久**以内部 ID 出现在模型列表里 —— 它不是孤儿（节点/连接都在），清理按设计
// 不会删它，用户"清理了也没用"正是这个原因。
//
// 这两条断言锁的是**用户可见的模型 ID**（客户端要复制它去调用）：节点有名字时，
// 不得把内部节点 ID 当展示名发布。修复前应为红。
const rawNodeID = "openai-compatible-chat-63ab874d-5ec5-48b5-9098-f3307545300f"

// assertNoInternalAlias 把用户的硬要求写成**不变量**：客户端拿到的模型 ID 里
// 绝不允许出现内部节点 ID（`openai-compatible-chat-<uuid>`）—— 在所有数据形状上统一断言。
func assertNoInternalAlias(t *testing.T, ctx string, ids []string) {
	t.Helper()
	for _, id := range ids {
		head := strings.SplitN(id, "/", 2)[0]
		if db.IsInternalNodeAlias(head) {
			t.Errorf("%s：发布了内部节点 ID（用户可见的模型 ID）：%s", ctx, id)
		}
	}
}

// 悬空连接（节点已被删、连接还在）= 反馈里那份数据的另一种可能形状。
// 契约（2026-09-30 用户要求："旧的 ID 彻底不再出现，但不能简单删除"）：
//   ① 模型**仍然在列表里**（绝不能因为"像内部 ID"就被丢掉）；
//   ② 它显示为**正常名字**（`<连接名>/<模型>`）；
//   ③ 而且这个正常名字**真能路由** —— 自愈按连接上的 provider 把节点补回来了，
//      所以它不是一个"能看不能用"的假别名（这正是必须补节点、而不能只改显示的原因：
//      路由只认节点前缀，见 resolvePrefixProvider）。
func TestHandleModels_DanglingConnection_GetsNormalNameAndStillRoutes(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	connData, _ := json.Marshal(map[string]any{
		"apiKey":               "sk-x",
		"providerSpecificData": map[string]any{"baseUrl": "https://amd.example.com/v1", "apiType": "chat"},
	})
	if _, err := database.Exec(
		`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		 ('conn-dangling', ?, 'apikey', 'AMD', 1, 1, ?, '2026-09-30T00:00:00Z', '2026-09-30T00:00:00Z')`,
		rawNodeID, string(connData)); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	val, _ := json.Marshal(map[string]any{
		"id": "deepseek-flash", "name": "deepseek-flash",
		"providerAlias": rawNodeID, "type": "llm",
	})
	if _, err := database.Exec(
		`INSERT INTO kv (scope, key, value) VALUES ('customModels', ?, ?)`,
		rawNodeID+"|deepseek-flash|llm", string(val)); err != nil {
		t.Fatalf("seed kv: %v", err)
	}

	handler := NewChatHandler(db.NewRepo(database))
	want := "AMD/deepseek-flash"
	for _, q := range []string{"", "?all=1"} {
		ids := fetchModels(t, handler, q).ids()
		assertNoInternalAlias(t, q, ids)
		if !fetchModels(t, handler, q).idSet()[want] {
			t.Errorf("%q: 模型丢了（要求是「改成正常名字」，不是删除）。实际：%v", q, ids)
		}
	}
	// ③ 正常名字必须真能路由：解析出来应指回这个 provider 与这条连接
	info, err := handler.resolveModel(want)
	if err != nil {
		t.Fatalf("resolveModel(%q): %v（正常名字必须可路由，否则只是好看的假别名）", want, err)
	}
	if info.Provider != rawNodeID || info.ConnectionID != "conn-dangling" {
		t.Errorf("resolveModel(%q) = provider %q / conn %q，期望 %q / conn-dangling",
			want, info.Provider, info.ConnectionID, rawNodeID)
	}
}

// 连**连接名字**都没有的悬空连接：无从命名 → 宁可这条不发布，也绝不发布内部节点 ID。
// （自愈会尽力，但"没有名字"是它唯一救不了的情况；这里锁住此时的兜底行为。）
func TestHandleModels_NamelessDanglingConnection_NotPublished(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	if _, err := database.Exec(
		`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		 ('conn-noname', ?, 'apikey', '', 1, 1, '{"apiKey":"sk-x"}', '2026-09-30T00:00:00Z', '2026-09-30T00:00:00Z')`,
		rawNodeID); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	val, _ := json.Marshal(map[string]any{
		"id": "deepseek-flash", "name": "deepseek-flash",
		"providerAlias": rawNodeID, "type": "llm",
	})
	if _, err := database.Exec(
		`INSERT INTO kv (scope, key, value) VALUES ('customModels', ?, ?)`,
		rawNodeID+"|deepseek-flash|llm", string(val)); err != nil {
		t.Fatalf("seed kv: %v", err)
	}

	handler := NewChatHandler(db.NewRepo(database))
	for _, q := range []string{"", "?all=1", "?connected=1"} {
		ids := fetchModels(t, handler, q).ids()
		// 核心不变量：内部 ID 绝不出现（静态目录里本来就有别的 deepseek-flash，不能按子串判）
		assertNoInternalAlias(t, q, ids)
		for _, id := range ids {
			if strings.HasPrefix(id, rawNodeID+"/") {
				t.Errorf("%q: 无从命名的悬空模型被发布了（应宁可不出）：%s", q, id)
			}
		}
	}
}

// 守卫：健康数据（节点有 prefix）行为与修复前一致 —— 列表里就是 `<前缀>/<模型>`。
func TestHandleModels_HealthyNodeKeepsItsPrefix(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	if _, err := database.Exec(
		`INSERT INTO providerNodes (id, type, name, data, createdAt, updatedAt) VALUES
		 (?, 'openai-compatible', 'AMD', '{"prefix":"AMD","apiType":"chat"}', '2026-09-30T00:00:00Z', '2026-09-30T00:00:00Z')`,
		rawNodeID); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	val, _ := json.Marshal(map[string]any{
		"id": "deepseek-flash", "name": "deepseek-flash",
		"providerAlias": rawNodeID, "type": "llm",
	})
	if _, err := database.Exec(
		`INSERT INTO kv (scope, key, value) VALUES ('customModels', ?, ?)`,
		rawNodeID+"|deepseek-flash|llm", string(val)); err != nil {
		t.Fatalf("seed kv: %v", err)
	}

	handler := NewChatHandler(db.NewRepo(database))
	if !fetchModels(t, handler, "?all=1").idSet()["AMD/deepseek-flash"] {
		t.Errorf("健康节点的模型没按前缀列出（回归）：%v", fetchModels(t, handler, "?all=1").ids())
	}
}

func TestHandleModels_NoRawNodeID_WithConnection(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	// 节点：有人类可读的 name（"AMD"），但 data 里没有 prefix —— 历史数据形状
	nodeData := `{"apiType":"chat","baseUrl":"https://example.com/v1"}`
	if _, err := database.Exec(
		`INSERT INTO providerNodes (id, type, name, data, createdAt, updatedAt) VALUES
		 (?, 'openai-compatible', 'AMD', ?, '2026-09-30T00:00:00Z', '2026-09-30T00:00:00Z')`,
		rawNodeID, nodeData); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	// 连接：同样没有 prefix（psd 里也没有）
	connData, _ := json.Marshal(map[string]any{"apiKey": "sk-x"})
	if _, err := database.Exec(
		`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		 ('conn-amd', ?, 'apikey', 'AMD', 1, 1, ?, '2026-09-30T00:00:00Z', '2026-09-30T00:00:00Z')`,
		rawNodeID, string(connData)); err != nil {
		t.Fatalf("seed connection: %v", err)
	}
	// 自定义模型行（键 = 节点ID|模型|llm，与真机一致）
	val, _ := json.Marshal(map[string]any{
		"id": "deepseek-flash", "name": "deepseek-flash",
		"providerAlias": rawNodeID, "type": "llm",
	})
	if _, err := database.Exec(
		`INSERT INTO kv (scope, key, value) VALUES ('customModels', ?, ?)`,
		rawNodeID+"|deepseek-flash|llm", string(val)); err != nil {
		t.Fatalf("seed kv: %v", err)
	}

	handler := NewChatHandler(db.NewRepo(database))
	for _, q := range []string{"", "?all=1", "?connected=1"} {
		for _, id := range fetchModels(t, handler, q).ids() {
			if strings.HasPrefix(id, "openai-compatible-chat-") {
				t.Errorf("%q 发布了内部节点 ID（用户可见的模型 ID）：%s", q, id)
			}
		}
	}
}

func TestHandleModels_NoRawNodeID_NoConnection(t *testing.T) {
	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	nodeData := `{"apiType":"chat","baseUrl":"https://example.com/v1"}`
	if _, err := database.Exec(
		`INSERT INTO providerNodes (id, type, name, data, createdAt, updatedAt) VALUES
		 (?, 'openai-compatible', 'AMD', ?, '2026-09-30T00:00:00Z', '2026-09-30T00:00:00Z')`,
		rawNodeID, nodeData); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	val, _ := json.Marshal(map[string]any{
		"id": "识图模式", "name": "识图模式",
		"providerAlias": rawNodeID, "type": "llm",
	})
	if _, err := database.Exec(
		`INSERT INTO kv (scope, key, value) VALUES ('customModels', ?, ?)`,
		rawNodeID+"|识图模式|llm", string(val)); err != nil {
		t.Fatalf("seed kv: %v", err)
	}

	handler := NewChatHandler(db.NewRepo(database))
	for _, q := range []string{"", "?all=1"} {
		for _, id := range fetchModels(t, handler, q).ids() {
			if strings.HasPrefix(id, "openai-compatible-chat-") {
				t.Errorf("%q 发布了内部节点 ID（用户可见的模型 ID）：%s", q, id)
			}
		}
	}
}
