# AGENT-CONVENTIONS.md — 本模块的工程契约（给未来的 agent 与新协作者）

> **读法**：先读本文件 → 再读 `CONTEXT.md`（术语）→ 再读 `docs/TESTING.md`（门禁台账）。
> 本文件是**我们（模块层）独有**的文件，上游仓库没有它；不要把它写成上游 `AGENTS.md` 的副本。
> 上游 `AGENTS.md` / `CLAUDE.md` / `ARCHITECTURE.md` 是**引擎侧**的规范；与本文件冲突时以本文件为准，
> 本文件又以「当前代码与产物的实际行为」为准（见 §1）。

日期：2026-09-26 ｜ 状态：生效（建立者：维护者与 agent 的 grilling 共识，见 `docs/FIXPLAN.md` Phase 25–26）

---

## 1. 事实优先级与旧文档处理

**优先级：当前代码/产物 > 本契约 > 其它文档。**

- 旧文档（仓库里的历史章节、`docs/superpowers/**`、上游文档）记录的是**当时的构想**，不是永久约束。
  它与现状冲突时，按 A6 政策收口：
  1. **我们独有的文件**里的错误陈述 → 直接改对；必要时就地留一行标记
     `<!-- 2026-09-26 修正：原写 X，见 §10 -->`；
  2. **上游也有的文件**里的陈述（改了会在下次合并冲突）→ **不改上游文件**，登记到 §10「已知差异」；
  3. 冗余的历史描述 → 删除，只在 `docs/TESTING.md` 的「变更记录」留一行标记。
- **禁止在仓库里同时保留两句相互矛盾的话** —— 这是本契约存在的首要原因。

## 2. 架构不变量（seam 与单一所有者）

1. **生命周期**（"服务该不该在跑"）= `module/lib/lifecycle.sh` 唯一所有者；其它文件只表达意图（ADR-0005）
2. **日志策略**（路径/上限/轮转）= `module/lib/log.sh` 唯一所有者（ADR-0006）
3. **运维动作唯一入口** = `module/lib/ops.sh`；WebUI、`action.sh`、门禁脚本都调它，禁止内联 shell 逻辑
4. **安装唯一入口** = `ops.sh install-engine` / `ops.sh install-module`（ADR-0007）
5. **前端请求形状唯一来源** = `module/webroot/parsers.js`、`module/webroot/bridge.js` 的命令构造器、
   `web/src/lib/*.ts`（纯函数，可离线断言；禁止把请求形状散在组件里）
6. **状态必须与事实一致**：写状态/版本前先验证（`engine=up` 才写 `engine-version`；面板显示必须等于
   `/version` 自报；`.bak` 只记录已验证可用的那一份）
7. **装前门禁 fail-closed**：下载（HTTP 层）→ 文件本体（体积 + ELF 魔数）→ 校验和（取不到即拒绝）；
   任一层不过就中止，且**不碰**现有二进制
8. **门禁的棘轮语义唯一所有者** = `tools/ratchet.py`（基线／豁免／只拦新增／收紧）；scanner 只提供
   `gaps: dict[key, 说明]`，禁止在某个门禁脚本里另写一套基线比较与输出（2026-09-26 收口）
9. **生命周期状态词表唯一所有者** = `module/webroot/parsers.js` 的 `LIFECYCLE_STATES`（词 → 文案/严重度）；
   `life_state`（`module/lib/lifecycle.sh`）是唯一 emit 方。两侧由 `contract-keys.test.js` **双向**缝死：
   shell 会 emit 而表里没有 → 红；表里有幽灵词（shell 永不会 emit）→ 红。加意图态只改这两处
10. **"先门禁后动作"的顺序是数据**，不是装配流程里的隐式约定：更新/清理的步骤顺序与前置判据写成
   `ENGINE_UPDATE_PLAN` / `MODULE_UPDATE_PLAN` / `ORPHAN_CLEAN_PLAN`，由 `parsers.js planSteps` 求值
   （未给 fact 的门禁 = 未通过，默认拒绝）。app.js 只按求值结果执行，顺序不变量在离线有断言
   （ADR-0007 的核心不变量由此从"只能真机验"变成"离线可验"）
11. **dashboard 测试路由表必须 ⊆ 生产路由表**（`TestDashboardRouteTables_TestTableIsSubsetOfProduction`）：
   测试 seam 是 `internal/handlers/dashboard/routes.go` 的 `RegisterRoutes`，生产是 `router.go` 的
   `SetupDashboardRoutes`。往测试 seam 加一条生产没有的路径 → 测试会在**假路线**上变绿，门禁必须红。
   结构收敛（两侧共用一份挂载清单）属上游 `internal/**`，按 ADR-0003 只加门禁、不做顺手重构
12. **`__MOD_ID__` 注入唯一所有者** = `tools/inject-mod-id.sh`（`build.sh` 打包与
   `tools/deploy-device.sh` 直推都调它）。注入对象是**全树所有含占位符的文件**，不维护文件清单
   （清单本身正是会漂移的东西）；MOD_ID 唯一来源 = `module/module.prop` 的 `id=`；零残留由注入器
   自己断言。**禁止**在调用方再写一遍 `sed` 注入或残留检查
13. **「什么算一个引擎」的规则只在 `parsers.js` 声明**（`ELF_MAGIC` / `ENGINE_MIN_BYTES`）；
   `ops.sh engine_src_ok` 是执行前复核，不是第二份判据。两侧由
   `module/webroot/test/engine-spec-contract.test.js` 缝住（常量、魔数字面量、检查项存在性、
   边界语义）。改规则 = 改 `parsers.js` + 按门禁同步 shell 常量
14. **「等就绪 / 等消失」唯一所有者** = `module/lib/wait.sh`（`wait_for` / `wait_gone` /
   `wait_pid_gone`，零依赖可离线测）。生产（`lifecycle.sh` / `watchdog.sh`）与真机门禁
   （`tools/device/test-lifecycle.sh`）都必须用它 —— **禁止**再写 `while + sleep` 轮询：
   ADR-0004 的"固定 3s 误报拉起失败"正是四处各写一份的代价（当时只修了 watchdog 那一份）
15. **登录态唯一所有者** = `web/src/lib/session.ts`（`AUTH_FLAG_KEY` / `API_KEY_STORAGE_KEY` /
   `isAuthed` / `markAuthed` / `clearAuthed` / `clearAll` / `getStoredApiKey` / `setStoredApiKey`，
   存储注入 = 纯函数可离线测）。**禁止**在别处出现 `'9router_auth'` / `'9router_key'` 字面量或
   直接摸 `localStorage`：`session.test.ts` 里的防回潮门禁会扫全树并红。两种"登出"是**有意区分**的：
   `clearAuthed`（只清标记，保留 API key）vs `clearAll`（连 key 一起清，401 后清理过期凭据）
8. **cgroup 脱组**：由 WebUI（`ksu.exec`）启动的进程必须迁出应用 cgroup，否则会随管理器应用被系统清理而连坐（ADR-0004）

## 3. 所有权地图（改哪里会与上游冲突）

| 归属 | 文件 | 改动代价 |
|---|---|---|
| **上游共享** | `AGENTS.md` `CLAUDE.md` `ARCHITECTURE.md` `COMPARISON.md` `README.md` `ROADMAP.md` `TECHNICAL_DEBT.md` `CHANGELOG.md` `DATABASE.md` `Makefile` `VERSION` `version.json` `web/**` `internal/**` `cmd/**` `docs/BUILD_DASHBOARD.md` `docs/DASHBOARD_PROVIDER_PARITY.md` `docs/superpowers/**` | 改动 = **预期下次合并冲突**；必须登记 §10 |
| **我们独有** | `module/**` `tools/**` `build.sh` `update.json` `CONTEXT.md` `MAGISK.md` `AGENT-CONVENTIONS.md` `docs/TESTING.md` `docs/FIXPLAN.md` `docs/adr/**` | 可自由改（仍受 §4 约束） |

> 新增文档时优先取**上游没有的名字**（如本文件、`docs/TESTING.md`），从源头避开冲突。

## 4. 变更映射（改 A 必做 B）

| 改了什么 | 必须同时做 |
|---|---|
| `module/lib/*.sh` 的运维动作或状态 | 相关真机 `T*` 断言；台账登记 |
| 前端请求形状（`fetch`/`request`/`KB.ops`/`KB.fetch` 字面量） | `parsers.js`/`bridge.js`/`web/src/lib/*.ts` 纯函数用例；`check-ui-parity` 基线 |
| 引擎路由/端点、补丁增撤 | `check-parity`；`docs/adr/0003` 登记表 |
| schema 相关 | `python3 tools/gen-schema.py --check` |
| 任何 bug 修复 | 复现用例（沿用上游 `AGENTS.md §5.0` 的硬规则）+ 台账登记 |
| 新增/删除/修改门禁断言 | `docs/TESTING.md` 台账 + 「变更记录」一行 |
| 版本/发布物料 | `build.sh` 七步 + `update.json` 同步 |
| 架构级取舍（难回退 + 反直觉 + 真实取舍） | 写 ADR（见 §6） |

## 5. 门禁与档位

- **唯一入口**：`tools/check.sh`，档位 `--offline` / `--device` / `--parity` / `--all`。
- 各档含义与断言清单见 `docs/TESTING.md`；**禁止在别处再写一份测试清单**（会漂移）。
- **缺前置**（无真机、无 `../9router` 参照树、无 bun/node/go/python3）→ 打印 `SKIP` 摘要并退出 0；
  `--require-device` / `--require-parity` 为严格模式（缺前置即失败），供将来 CI 使用。
- `build.sh` 复用 `tools/check.sh --offline`；发布前 = `check.sh --all` 全绿（或有台账记录的 SKIP）+ `build.sh` 七步绿。
- **门禁 = 会红会拦**；不能拦人的检查可以写进台账，但不许叫门禁。

## 6. ADR 触发条件与格式

三个条件**同时**满足才写 ADR：① 难回退（改主意代价大）② 不看背景会觉得反直觉 ③ 是真实取舍的结果。
格式：`# ADR-XXXX: 标题` ＋ `日期 ｜ 状态` ＋ `## 背景 / ## 决策 / ## 后果`（可选「被否决的替代」）。
拿不准时：写进 `docs/TESTING.md` 或本文件，而不是滥发 ADR。

## 7. 上游同步流程（引擎发新版时）

1. `git fetch origin --tags`，看 `git log --oneline v<旧>..v<新>` + `git diff --stat v<旧>..v<新>`（**以本地 diff 为准**，不只读 release notes）
2. 逐条裁决我们的补丁：被吸收 → 撤（删 `tools/patches/*` + 更新 ADR-0003 表）；未吸收 → 保留
3. 冲突处理：上游改过的共享文件按 §10 清单核对；合并后**必须**复跑对应断言
4. `python3 tools/gen-schema.py --check`（上游 `DATABASE.md` 变了就按 §1 收口）
5. `python3 tools/check-parity.py`（新缺口必红；逐条裁决后 `--write-baseline` 收紧）
6. `bash build.sh` → 真机 `ops.sh install-module`（用我们自己的入口）→ `tools/check.sh --all`
7. 结论写进 `docs/FIXPLAN.md` 新 Phase（含撤/留补丁、实证缺口、待决策）

## 8. 命名与语言

- 文档与注释用中文；代码标识符、提交信息用英文动词短语（`fix(module): …` / `feat(web): …` / `chore(parity): …`）
- **术语以 `CONTEXT.md` 为准**，禁止同义替换（"门禁"不要与"检查/校验"混用；"棘轮"不要写成"巡检"）
- 新文件名避免与上游同名（见 §3）

## 9. 提交与记录

- 提交粒度 = 一个可描述的意图；提交信息写清**为什么**（不要只写"更新"）
- 触碰测试/门禁的提交 → `docs/TESTING.md`「变更记录」加一行（日期｜动作｜对象 ID｜原因｜提交号）
- **不为发版而发版**：本地测完确认可用再发布；推远端/打 tag 由维护者决定

## 10. 与上游文档的已知差异 ／ 已改动的共享文件

### 10.1 已知差异（我们**有意**不同，理由在此；不改上游文件）

| 主题 | 上游文档说法 | 我们的做法 | 理由 |
|---|---|---|---|
| 首装密码 | `docs/BUILD_DASHBOARD.md`：*Do not ship a known initial password* | 固定 `123456`，首启提示改密，登录后可自行修改 | Android 模块首装要在 KernelSU/WebUIX 内登录，随机密码需要额外告知渠道；这是显式的便利/安全取舍 |
| 前端测试约定 | `AGENTS.md §5.0`：模块 JS 用 `node --test` | 模块 WebUI 用 `node --test`；引擎内嵌 Dashboard（`web/**`）用 `bun:test` | 两个不同运行时/不同测试对象；bun 已是前端构建工具链 |
| 前端"没有测试" | `web/README.md`、`docs/BUILD_DASHBOARD.md` 称前端无测试 | `web/src` 已有 10 个文件 / 82 个用例（`bun:test`） | 上游文档滞后于代码 |
| 上游参照树位置 | `AGENTS.md`/`CLAUDE.md`/`scripts/` 写 macOS 绝对路径 | `tools/check-parity.py` 默认 `../9router`，可用 `UPSTREAM=` 覆盖 | 绝对路径不可移植 |
| 端口绑定 | `MAGISK.md`（旧版）曾写"仅绑定 loopback" | 引擎监听 `0.0.0.0:<port>`（模块按局域网使用） | 模块 WebUI 要显示 `http://<设备IP>:<port>`；安全由密码 + 可选 `requireLogin` 承担 |
| DNS 自动优选 | 旧路线图列为"可选功能" | 明确不做（`CONTEXT.md`「刻意不做」） | 与上游优选策略冲突风险 > 收益 |

### 10.2 已改动的共享文件（每次合并按此核对）

| 文件 | 我们的改动 | 合并时怎么办 |
|---|---|---|
| `internal/handlers/router.go` | ① ADR-0003 定点补丁：`/api/models/test` 鉴权 —— **上游 v1.9.2 已吸收，本地补丁已撤**；② 2026-09-26 挂载 `/web/fetch` + `/v1/web/fetch`（`HandleWebFetch` 此前从未挂载 → Dashboard 网页抓取必 404；补丁存档 `tools/patches/media-web-fetch-route.patch`） | 取上游后复核：`TestSetupServerRouter_ModelTestSessionAuth` 仍在跑、两条 web/fetch 路由仍在 |
| `internal/handlers/chat/chat.go` | 删除从未被引用的 `HandleHealth`（`/health` 由 router.go 的内联 healthHandler 提供）；补丁存档 `tools/patches/remove-dead-handlehealth.patch` | 取上游后确认该函数未回归（若回归，DEADH 会红） |
| `internal/proxy/executor/codebuddy.go` | codebuddy-cn agent 提示词清洗（ADR-0003，**待上游吸收**） | 保留；补丁存档 `tools/patches/codebuddy-cn-agent-prompt-sanitizer.patch`，上游合并后撤 |
| `internal/handlers/dashboard/settings_test.go` | 回插 `TestHandleExportDatabase_AcceptsPasswordHeader`（锁 `x-9r-password` 契约） | 合并后确认该测试仍在 |
| `web/src/components/ProfileSettingsView.svelte` | 备份导出/导入的密码弹层与请求形状（Phase 20） | 保留我们的交互，其余取上游 |
| `COMPARISON.md` | §0 端点 parity 巡检节（机器可验） | 与上游 statuses 图例并存，不删除任何一侧 |

> 每次合并后，**先看本表，再跑 `tools/check.sh --all`**。新增对共享文件的改动必须追加到本表。
