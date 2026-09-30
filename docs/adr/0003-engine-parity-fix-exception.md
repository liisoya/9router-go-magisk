# ADR-0003: 引擎 parity 缺陷允许定点修复（附上游 PR 义务）

日期：2026-09-25 ｜ 状态：已接受（修订 ADR-0002）

## 背景

ADR-0002 规定"上游零魔改"。但实践中发现上游 Go 移植存在**功能缺失级 parity
bug**——例如 codebuddy-cn 执行器缺少 Node 版的 agent 系统提示词清洗逻辑，
导致真实客户端（CodeBuddy CLI）请求被腾讯渠道校验拦截（HTTP 400 code 11128
"Illegal API invocation from an unapproved channel"），核心供应商完全不可用。
此类问题模块层无法绕过（请求体转换发生在引擎内部）。

## 决策

对**阻断核心功能**的上游 parity 缺陷，允许直接修改引擎源码，但必须：

1. **单点、最小化**：修复收敛在最小函数范围，不做顺手重构
2. **补丁存档**：`git diff` 存入 `tools/patches/<name>.patch`，上游同步后
   可一键重放（`git apply`）
3. **上游 PR 义务**：向 `luqman-v1/9router-go` 提交 PR；上游合并后删除
   本地补丁回归零魔改
4. **ADR 登记**：每个引擎修复在本文件追加记录

## 已应用的引擎修复

| 日期 | 文件 | 问题 | 补丁存档 | 上游 PR |
|---|---|---|---|---|
| 2026-09-25 | `internal/proxy/executor/codebuddy.go` | 缺少 agent 系统提示词清洗 → codebuddy-cn 全部请求被腾讯 11128 拦截 | `tools/patches/codebuddy-cn-agent-prompt-sanitizer.patch` | 待提交 |
| 2026-09-25 | `internal/handlers/dashboard/settings.go` | 内嵌 Svelte 前端以 multipart/form-data 上传备份（`file` 字段、无密码、凭 admin 会话），后端却按 Node 时代的 JSON+password 解析 → Dashboard 导入数据库必然 400 "Invalid database payload"（新设备首装导入被阻断）。修复：multipart 分支解析 `file` 字段；admin 会话/CLI token/密码三选一授权（该路径中间件本就强制 admin 会话）。Node JSON 流保持不变 | **已撤回**（Phase 20 把前端改回上游 JSON+password 形状后该分支已无调用方；Phase 23 随 marge 一并撤掉） | 无需（与上游一致） |
| 2026-09-26 | `internal/handlers/router.go` | `/api/models/test`（仪表盘模型测试）被挂在 RequireApiKey 组内——Node 原版它是 dashboard 内部端点（admin 会话语义，`pingModelByKind`）。备份导入清空 apiKeys 表后，仪表盘所有模型测试在引擎门口 401 "Invalid API key."（请求未达上游）。修复：移入 RequireDashboardAuth 组（admin 会话 / CLI token / API key 三选一），仪表盘从此不依赖 apiKeys 表 | **已由上游吸收**（v1.9.2 `1865f78`，本地补丁已撤） | 已完成 |
| 2026-09-26 | `internal/handlers/router.go` | `media.HandleWebFetch`（网页抓取）**已实现但从未挂载** → 内嵌 Dashboard 的媒体面板按 `/v1/web/fetch` 调用时必 404（UI 调用 parity 巡检抓到；真机 `GET /v1/web/fetch` 由 404 → 405 验证已挂载）。修复：显式双注册 `/web/fetch` + `/v1/web/fetch`（与 `/v1/models` 同形，1 行 + 1 行） | `tools/patches/media-web-fetch-route.patch` | 待提交（**v1.9.3 复核：上游仍未挂载 → 保留**） |
| 2026-09-26 | `internal/handlers/chat/chat.go` | `ChatHandler.HandleHealth`（3 行健康检查）**从未被任何代码引用** —— 引擎的 `/health` 实际由 `router.go` 的内联 `healthHandler`（带 CORS 头）提供；留着会让人误以为 `/health` 由它提供。删除（DEADH 巡检抓到） | **已撤（2026-09-27）**：上游 v1.9.3 自己删掉了该函数（`chat.go` 搬 models 目录时一并消失）→ 已吸收，补丁存档删除 | 已完成 |
| 2026-09-26 | `internal/handlers/router.go` + `internal/handlers/sso/sso.go` | ① 内嵌 Dashboard 的浏览器侧可达性探测 `GET /api/health`：上游 Node 版公开且带 `Access-Control-Allow-Origin: *`，Go 版只有**没有 CORS 头**的 `/health` → 跨域探测永远失败；② SSO 登录回调 `/api/auth/oidc/callback`、`/api/auth/saml/acs` **从未注册** → IdP 跳回打到 404，被误判成"配置写错了"。修复：抽出 `healthHandler` 双注册 + 两个回调端点诚实返回 501 | 无独立补丁存档（改动落在 `router.go` 公开路由区与 `sso.go`，见 `AGENT-CONVENTIONS §10.2`） | 待提交（v1.9.3 复核：上游仍无 `/api/health` 注册 → 保留） |
| 2026-09-30 | `internal/db/heal.go`(新) + `internal/handlers/chat/{models_list,prefix_heal,chat,types}.go` + `internal/app/database.go` + `internal/db/repos.go` | **模型列表把内部节点 ID 当模型 ID 发布**（用户反馈 `openai-compatible-chat-<uuid>/deepseek-flash`）：`kv.customModels` 的键按节点 ID 存（设计如此），列表发布 `<前缀>/<模型>` 时前缀取自节点 `data.prefix`（连接路径取连接前缀），**取不到就回退成节点 ID**；而"有 name 没 prefix"的历史数据（旧版本 / 恢复备份 / 别的客户端建的节点）**不是孤儿**（节点或连接还在）→ 清理孤儿按设计不会碰它 → 用户"清理了也没用"。这类历史数据还会因 `DeleteProviderNode` 当年"先删节点、再删连接（错误被丢弃）"的半状态而**持续产生**。修复：①幂等自愈补回缺失前缀（只补空 / 从 name 派生 / 冲突加后缀 / 单事务 / 不动 updatedAt），启动与读路径（节流 60s）各一次；②悬空连接按连接上的 provider **补回节点**（路由只认节点前缀 → 只改显示名会得到"能看不能用"的假别名）；③内部 ID 形状且解析不出名字 → **不发布**（任何模式都不再泄漏）；④删节点改单事务 + 不再吞错 | `tools/patches/provider-node-prefix-heal.patch` | **待提交**（这是上游同样存在的缺陷，值得上游 PR） |
| 2026-09-30 | `web/src/lib/db-backup.ts` + `web/src/components/ProfileSettingsView.svelte` | **备份/恢复复核**（同步 v1.9.5 时取了我们的文件，事后逐条比对上游 #32/#34 发现）：缺 ①(b) 取消不生效 —— `Modal` 的 Esc / 遮罩 / ✕ 全接 `onClose`，而原 `closeDbAuth` 只清状态 → **导入途中按 Esc，POST 照跑、跑完覆盖数据库**；②(a) 中间件 `handlerutil.WriteJSONError` 写的是**嵌套** envelope `{"error":{"message":…}}`，旧的 `responseErrorMessage` 只认扁平串 → 鉴权拒绝的真实原因被吞成通用提示。修复：在途守卫 `dbRequestInFlight()`（`closeDbAuth`/`openDbAuth` 都在它面前止步）+ `responseErrorMessage` 递归解包（非 JSON / 空壳仍回落 fallback）；顺带 backport 上游 `851070e`（界面里显示的上游默认密码 `Mantep210` → 本文档值 `123456`，该提交晚于 v1.9.5，不属本次同步范围） | `tools/patches/dashboard-backup-dismiss-guard.patch` | 待提交（`851070e` 部分上游已有，其余待上游吸收） |

## 同步裁决记录

**2026-09-27 · v1.9.2 → v1.9.3**

- 上游本版规模：67 文件 / +8278 −809；我们改过的文件里**只有 4 个双方都动过**
  （`chat.go`、`router.go`、`codebuddy.go`、`web/src/api/client.ts`），合并**零文本冲突**。
- **撤**：`codebuddy-cn-agent-prompt-sanitizer`（上游 shaping 丢弃全部 `system`/`developer` 并前置固定
  prompt，且插在清洗**之后**执行 → 清洗恒为 no-op；留着是死代码）；`remove-dead-handlehealth`
  （上游已删该函数）。
- **留**：`media-web-fetch-route`（上游仍未挂载 `HandleWebFetch`）、`/api/health` + SSO 501。
- **上游本版行为变化（非我们的补丁，但影响模块假设）**：Go 侧新增 `db.EnsureCoreSchema`
  （`internal/db/schema.go`，由 `internal/app/database.go` 接线）**在启动时幂等补齐 11 张核心表与缺列、并
  seed `_meta`/`settings`** —— 空白 `DATA_DIR` 从此是受支持的启动路径。模块的
  `module/etc/schema.sql` 仍是安装期的建库来源，两者不冲突（SCHEMA 门禁继续绿）。
- 端点 parity 棘轮随本版**收紧 135 → 131**（上游补上的 4 条 Kiro 路由缺口消失）。

**2026-09-27 · v1.9.3 → v1.9.4**

- 上游本版规模：117 文件 / +8766 −1386；我们改过的文件里只有 3 个双方都动过（`chat.go`、
  `router.go`、`web/src/api/client.ts`），合并**零文本冲突**。
- **留**：`media-web-fetch-route`（上游仍未挂载 `HandleWebFetch`）、`/api/health` + SSO 501
  （上游仍无注册）。`codebuddy.go` 本版上游未动，shaping 维持上游实现（我方清洗已在 v1.9.3 撤除）。
- 复核：`HandleHealth` 未回归；登录态键未回潮（字面量仍只在 `web/src/lib/session*`）。
- schema：上游只把 Go-only 列的回填提取为 `EnsureAdditiveColumns`，表/列无变化 →
  `module/etc/schema.sql` **无需重生成**（SCHEMA 门禁继续绿）。
- 端点 parity 棘轮：无新增缺口，基线 131 条不变。

