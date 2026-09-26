package handlers

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"9router/proxy/internal/db"
	"9router/proxy/internal/handlers/dashboard"
)

// walkRoutes 收集路由器上所有 (方法, 路径) 组合。路径用 chi 的 pattern 形态
// （`/api/connections/{id}`），两侧同源，可直接比较。
func walkRoutes(t *testing.T, r chi.Router) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler,
		_ ...func(http.Handler) http.Handler) error {
		out[method+" "+route] = true
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk 失败：%v", err)
	}
	return out
}

// 候选 4｜dashboard 有两张路由表（架构评审 2026-09-26 实测）：
//   - 生产：`SetupDashboardRoutes`（internal/handlers/router.go）inline 注册，76 条；
//   - 测试：`dashboard.RegisterRoutes`（internal/handlers/dashboard/routes.go，**上游文件**），46 条，
//     被 12 个测试文件的 33 处调用（`setupTestRouter` 是唯一 seam）。
//
// 今天还没有假绿（"只在测试里注册" 0 条），但那是**靠运气**：往 routes.go 里加一条生产没有的
// 路径，测试会在假路线上变绿，而 UIPARITY / DEADH 都看不出来（它们只问"有没有被挂上"）。
// 本门禁把"测试表 ⊆ 生产表"变成结构断言 —— 往测试 seam 里加一条生产不存在的路径立刻红。
//
// 为什么只加门禁、不合并两张表：`internal/**` 属上游共享（AGENT-CONVENTIONS §3/§10.2、
// ADR-0003「单点最小化、不做顺手重构」）。结构收敛（两侧共用一份挂载清单）留给上游 PR。
func TestDashboardRouteTables_TestTableIsSubsetOfProduction(t *testing.T) {
	t.Setenv("JWT_SECRET", "route-table-secret")
	t.Setenv("DATA_DIR", t.TempDir())
	database, cleanup := setupTestDB(t)
	defer cleanup()
	repo := db.NewRepo(database)

	// 生产表：整个 server router（含中间件分组）—— 比只 walk SetupDashboardRoutes 更忠实，
	// 因为门禁要回答的是"这条路径在生产**真的可达**吗"。
	prod := chi.NewRouter()
	SetupServerRouter(prod, repo, nil)
	prodRoutes := walkRoutes(t, prod)

	// 测试表：dashboard 测试用的那个 seam
	testR := chi.NewRouter()
	dashboard.RegisterRoutes(testR, dashboard.NewDashboardHandler(repo))
	testRoutes := walkRoutes(t, testR)

	var missing []string
	for route := range testRoutes {
		if !prodRoutes[route] {
			missing = append(missing, route)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("dashboard 测试路由表里有 %d 条在生产路由表里不存在 —— 测试会在**假路线**上变绿：\n  %s\n"+
			"要么把它们挂进 SetupDashboardRoutes（internal/handlers/router.go），"+
			"要么从 internal/handlers/dashboard/routes.go 撤掉。", len(missing), strings.Join(missing, "\n  "))
	}
	t.Logf("生产 %d 条 / 测试 %d 条；测试表 ⊆ 生产表 ✓（只在测试里注册的：%d 条）",
		len(prodRoutes), len(testRoutes), len(missing))
}
