# TESTING.md — 门禁台账（模块层）

> 本文件是**测试与门禁的唯一台账**：测什么、怎么跑、失败意味着什么、以及**变更记录**。
> 规则见 `AGENT-CONVENTIONS.md §4/§5`：新增/修改/删除任何断言都必须在这里登记，并追加「变更记录」一行。
> 术语：**门禁（gate）**= 会红会拦；**档位（tier）**= 跑哪些门禁的一组；**棘轮（ratchet）**= 只拦新增缺口。

## 1. 怎么跑

```bash
tools/check.sh --offline     # 离线档：不需要真机 / 外网 / 上游参照树
tools/check.sh --device      # 真机档：adb + root，跑 T*/A* 断言
tools/check.sh --parity      # 对照档：需要 ../9router 参照树（UPSTREAM= 可覆盖）
tools/check.sh --all         # 全部（缺前置 → SKIP 摘要，退出 0）
tools/check.sh --all --require-device --require-parity   # 严格模式（缺前置 = 失败）
bash build.sh                # 发布构建：七步，其中第 3 步复用 check.sh --offline
```

## 2. 门禁台账

### 2.1 构建与产物（`build.sh`）

| ID | 断言（测什么） | 命令 / 位置 | 前置 | 失败意味着 |
|---|---|---|---|---|
| BUILD-1 | 前端构建产出 `web/dist/index.html` | `bun install --frozen-lockfile && bun run build`（无 bun 退化 npm） | bun/npm + 外网；`FORCE=1` 强制重建 | 前端构建失败/产物缺失 |
| BUILD-2 | clipboard polyfill 已注入 dist | `python3 tools/patch-clipboard.py web/dist/index.html` → grep 标记 | python3；`CLIPBOARD_PATCH=0` 可关 | KernelSU 浏览器里复制按钮全失效 |
| BUILD-3 | ① 产物含 `x-9r-password` ② 模块层离线回归 | `grep -rq -- 'x-9r-password' web/dist/assets/`；`tools/check.sh --offline` | node；`SKIP_TESTS=1` 跳过 | ① 仪表盘 Download Backup 必 401 ② 见 §2.3 |
| BUILD-4 | schema 无漂移（见 SCHEMA） | `python3 tools/gen-schema.py --check` | python3；`SKIP_SCHEMA_CHECK=1` 跳过（不建议） | 模块建表与上游 schema 漂移（老用户升级永不建新表） |
| BUILD-5 | arm64 引擎可交叉编译并写入版本 | `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build … -o module/bin/9router-go` | go 工具链 + BUILD-1 产物 | 引擎编译失败/无法内嵌前端 |
| BUILD-6 | `__MOD_ID__` 占位符 0 残留 | `sed` 注入后 grep | 无 | 设备上 `CFG.MODDIR` 错 → 面板读不到信息 |
| BUILD-7 | 发布包完整且版本一致 | `verify_zip()`：`unzip -t`、必备文件、`module.prop` 版本一致、webroot 无占位符、引擎含 polyfill | zip/unzip | 发布包损坏/缺件/版本不符 |

### 2.2 离线断言（`tools/check.sh --offline`）

| ID | 断言 | 命令 | 前置 | 失败意味着 |
|---|---|---|---|---|
| JS-SYNTAX | 全部 shell 脚本语法正确 | `sh -n module/**/*.sh tools/**/*.sh` | sh | 设备上脚本直接不可执行（**mksh 与 dash 有差异，真机断言仍是最终判据**） |
| JS-UNIT | 模块 WebUI 纯函数回归 **46 例** | `node --test module/webroot/test/*.test.js` | node | 解析层/命令构造器/键契约回归 |
| BUN-UNIT | 引擎 Dashboard 纯函数回归 **82 例**（10 文件） | `bun test web/src` | bun | Dashboard 逻辑回归（请求形状、供应商解析、导入导出等） |
| GO-BUILD | 引擎可编译 | `go build ./...` | go | 引擎源码编译失败 |
| GO-TEST | Go 单元测试（排除外网/真机依赖用例） | `go test ./... -skip '<见 §4>'` | go | 引擎侧回归 |
| TSC | Dashboard 类型检查 | `npx tsc -b` | node_modules | 类型错误（构建前提前拦） |
| DEADH | handler / 注册函数是否真的被挂载（定义了却没人调 = 点了必 404） | `python3 tools/check-dead-handlers.py` | python3 | 新增了一条"有实现、有测试、没路由"的代码（`HandleWebFetch`/`RegisterRoutes` 同类） |
| PY-UNIT | 棘轮 module（基线／豁免／只拦新增／收紧）的接口级单测 | `python3 -m unittest discover -s tools -p 'test_*.py'` | python3 | 三个门禁共用的棘轮语义坏了（PARITY/UIPARITY/DEADH 会一起失真） |
| INJECT | `__MOD_ID__` 注入器（全树注入 + 零残留 + 不可读拒绝） | `sh tools/test-inject-mod-id.sh` | sh | 打包/直推两条路径的注入实现漂移（上线后设备上才看到占位符 → 面板取不到信息） |

### 2.3 真机断言（`tools/check.sh --device`）

`tools/device/test-lifecycle.sh` —— 生命周期与安装（修复前 T2/T3/T8/T9/T10 会红）：

| ID | 断言 | 失败意味着 |
|---|---|---|
| T1 | 守护在场（`watchdog=up`） | 引擎死后没人拉起 |
| T2 | `kill -9` 引擎后 40s 内自愈（新 PID + `/health` 200） | 被杀即永久停机 |
| T3 | 在管理器应用 cgroup 内启动仍能脱组（cgroup=`/`） | 会随管理器应用被系统清理而连坐（ADR-0004） |
| T4 | `stop-user` 后状态如实为 `engine=stopped` 且 30s 内不被复活；`start-user` 能恢复 | 用户停服意图不被尊重 |
| T5 | 维护窗口（hold）内不插手、窗口到期后自愈 | 维护期被守护干扰 |
| T6 | 日志轮转生效（`dnsfwd.log` 由 ~440KB 降到上限内） | 24/7 运行把 `/data` 写满 |
| T7 | 承载性 env 6 个键齐全且真的进了引擎进程 | 缺 `SSL_CERT_DIR`/`AUTO_UPDATE` 等 → HTTPS 失败或绕过模块管理（ADR-0006） |
| T8a–d | 装前门禁：9 字节 404 正文被拒 / 现有引擎字节数不变 / `engine-version` 不谎报 / 引擎仍 up | 会把垃圾装成引擎、设备再无可用引擎（ADR-0007） |
| T9 | 面板 `engine_version` == 引擎自报 `/version.currentVersion` | 状态谎报（面板显示旧版本、永远提示有更新） |
| T10a–b | 整包安装跑完无 `syntax error`，装完 `engine=up` | 安装会覆写正在执行的自己而夭折 |

`tools/device/test-dashboard-api.sh` —— 仪表盘 API 功能（撤补丁后的"功能确实可用"证明）：

| ID | 断言 | 失败意味着 |
|---|---|---|
| A1 | `/version` 公开可读（200 + `currentVersion`） | 未登录页面轮询会 401 刷屏 |
| A2 | 本机 CLI token 调 `POST /api/models/test` 非 401 | 仪表盘模型测试被 apiKeys 表绑架（备份导入后必 401） |
| A3a | CLI token 导出备份 → 200 | 备份导出不可用 |
| A3b/A3c | 错误密码 / 无凭据 → 401 | 鉴权判据被削弱 |
| A4 | 对照组：无凭据 `/v1/models` → 401 | 引擎保护被削弱 |
| A5 | 导出载荷含密码/登录状态字段 | 导入备份后无法用"导入数据的密码"登录 |

### 2.4 对照与漂移（`tools/check.sh --parity`）

| ID | 断言 | 命令 | 前置 | 失败意味着 |
|---|---|---|---|---|
| PARITY | 上游端点 parity 棘轮：**新增**缺口即红（存量缺口在基线内不报警） | `python3 tools/check-parity.py` | python3 + `../9router`（`UPSTREAM=`） | 又漏移植了一个上游端点/方法（`--write-baseline` 收紧，`tools/parity-ignore.txt` 豁免须写理由） |
| UIPARITY | UI 调用 ⊆ 已注册端点：Dashboard/WebUI 里 `fetch`/`request`/`KB.*` 与**导航式调用**（`location.href=`/`window.open(`）的字面量路径必须已注册（引擎对已注册端点统一提供 `/v1` 别名，脚本会同时尝试去前缀形态） | `python3 tools/check-ui-parity.py` | python3 | UI 加了一个后端不存在的端点（"将来才会暴露"的那类） |
| SCHEMA | `module/etc/schema.sql` 的表与列与上游 `DATABASE.md` 对齐 | `python3 tools/gen-schema.py --check` | python3（`DATABASE.md` 在仓库内） | schema 漂移；有意超集列必须在 `SUPERSET_COLUMNS` 登记并写代码依据 |

## 3. 单元测试目录（文件 → 覆盖什么，不逐条抄用例名）

| 位置 | 规模 | 覆盖 |
|---|---|---|
| `module/webroot/test/parsers.test.js` | 25 | 解析层：`status/panel` 输出、meminfo/RSS、DNS 探测与评分、上游行归一、孤儿判定、**装前门禁**（404 正文/HTML/探针失败/真品） |
| `module/webroot/test/bridge-commands.test.js` | 18 | 命令构造器：引号转义、base64 写文件、备份/恢复、`download` 必带 `-f`、`sqlSnapshot` 成败判据、`promiseWrap`、`fileSize`/`elfMagic` |
| `module/webroot/test/contract-keys.test.js` | 3 | 键契约：shell `emit` 键集合 ↔ `app.js` 消费键 |
| `web/src/**/*.test.ts`（10 文件） | 82 | Dashboard：请求形状（`db-backup`）、供应商/路由解析、批量添加、代理导入、OAuth 交接、analytics 类型等 |
| Go `./...`（约 28 包） | — | 引擎侧；外网/真机依赖用例见 §4 |

## 4. 已知不绿灯（环境性必红，避免误判为回归）

| 对象 | 现象 | 处理 |
|---|---|---|
| `internal/handlers/media`：`TestHandleAudioVoices_elevenlabs` | 访问 `api.elevenlabs.io` 返回 EOF → 502 | 列入 `go test -skip`（名单在 `tools/check.sh` 的 `go_skip_pattern`） |
| `internal/handlers/chat`：`TestLiveE2E_Cline_SmartCombo`、`TestIntegration_OpenCode_MuseSpark13_ChatCompletions` | 本机存在 `~/.9router` 时会真的打上游 → 失败 | 列入同一 `-skip` 名单；**已在上游 pristine 树（`../9router-go`）复跑，同样失败**（2026-09-26 证据）→ 环境依赖，非本仓回归 |
| 其它 `*_live_*` / `*_e2e_*` 用例 | 需要真实 key 或 `$HOME/.9router/db/data.sqlite` | 多数自带 `t.Skip`（本机有库时才会真跑）；**不扩大排除范围**，出问题先在上游树复跑 |
| 真机档在无设备时 | 全部 `T*`/`A*` | SKIP（退出 0）；严格模式 `--require-device` 才失败 |

## 5. 变更记录

| 日期 | 动作 | 对象 ID | 原因 | 提交 |
|---|---|---|---|---|
| 2026-09-26 | 建账 | 全部 | 建立门禁台账（批 1：契约/术语/台账） | 见本次提交 |
| 2026-09-26 | 新增 | T8a–T8d | 更新引擎把 404 正文装成引擎（Phase 22 / ADR-0007） | b231232 |
| 2026-09-26 | 新增 | T9 | 整包更新后 `engine-version` 谎报（Phase 23.4） | c31d681 |
| 2026-09-26 | 新增 | T10a–T10b | `install-module` 覆写正在执行的自己（Phase 23.7） | bc613c3 |
| 2026-09-26 | 新增 | A1–A5 | 撤补丁后的仪表盘 API 功能门禁，改用 CLI token 免密码（Phase 25.3） | 544c576 |
| 2026-09-26 | 新增 | PARITY | 上游端点 parity 棘轮 + 基线（Phase 21） | cab577c |
| 2026-09-26 | 修改 | SCHEMA | 上游 v1.9.2 起不再文档化索引/部分列 → 校验收敛为"表与列"，登记有依据的超集列（Phase 23.3） | c31d681 |
| 2026-09-26 | 新增 | JS-UNIT（+9） | 装前门禁用例：404 正文/HTML 错误页/探针失败判死、校验和缺失必须拒绝 | b231232 |
| 2026-09-26 | 新增 | BUILD-3① | 构建产物必须含 `x-9r-password`（Phase 20） | 6a98e6d |
| 2026-09-26 | 新增 | UIPARITY | UI 调用 ⊆ 已注册端点（棘轮；首跑冻结 3 条：见下「UI parity 已知缺口」） | 07cd4d6 |
| 2026-09-26 | 修改 | UIPARITY（基线 3 → 2） | 挂载 `/web/fetch` + `/v1/web/fetch`（HandleWebFetch 从未挂载）→ 该缺口消失，收紧基线 | 见本次提交 |
| 2026-09-26 | 修改 | T10a | 匹配面从 `syntax error` 扩大到 `no closing quote` / `bad substitution` / `unexpected`（当日一次安装出现 `ops.sh[291]: no closing quote`，且行号 291 > 文件 284 行 → 当时读的是另一份内容；不可复现，先让门禁能抓到同类签名） | 见本次提交 |
| 2026-09-26 | 新增 | DEADH | handler/注册函数挂载巡检（棘轮）。首跑即抓到 2 条真实案例并已带理由豁免：`chat.HandleHealth`（无引用）、`dashboard.RegisterRoutes`（只有测试引用、无路由） | 见本次提交 |
| 2026-09-26 | 修改 | PARITY 侧代码 | 挂载 `/web/fetch` + `/v1/web/fetch`（ADR-0003 补丁），UIPARITY 基线 3 → 2 | bcf97e2 |
| 2026-09-26 | 修改 | DEADH（豁免 2 → 1，定义 156 → 155） | 删除真死代码 `chat.HandleHealth`；**更正** `dashboard.RegisterRoutes` 为"测试 seam"（12 文件 33 处调用），不是死代码 —— 两侧路由表实测：生产 76 条 / 测试 46 条、「只在测试里」0 条，漂移风险已记为架构候选 | 见本次提交 |
| 2026-09-26 | 修改 | PARITY / UIPARITY / DEADH | 三份重复的棘轮机械结构（清单解析／基线写盘／差值／打印／退出码）上收为 `tools/ratchet.py`（架构候选 1 的深化）；**行为不变**：135 / 2 / 1 豁免、退出码 0/1、红灯自证仍红 | 见本次提交 |
| 2026-09-26 | 新增 | PY-UNIT（11 例） | 棘轮 module 的接口级单测。**首跑即抓到迁移漏洞**：抽掉旧脚本的行形状校验后"格式错误"分支变成死代码（手抖写错的一行会静默成为永远匹配不上的基线项）→ 补回可选 `validate`，三个 scanner 各自传入形状规则 | 见本次提交 |
| 2026-09-26 | 修改 | JS-UNIT（52 → 58 例，+9/-3 重排） | 候选 2：状态词表收成单一所有者 `KP.LIFECYCLE_STATES`（词 → 文案/严重度），app.js 三条 if/else 链只查表；键契约门禁扩成**值枚举双向对齐**（shell emit 的词 ↔ 表里的键，两侧多/少都红）。红灯自证：给 `life_state` 加 `hibernating` → 门禁精确报错并红 | 见本次提交 |
| 2026-09-26 | 新增 | JS-UNIT（+6 例） | 候选 3：`ENGINE_UPDATE_PLAN` / `MODULE_UPDATE_PLAN` / `ORPHAN_CLEAN_PLAN` + `planSteps` 求值器，把"先门禁后动作"的顺序变成**可离线断言的数据**（任一引擎门禁未过 → install 不可达；没给 fact 的门禁默认拒绝；plan 结构断言"install 前必须有两个门禁"）。app.js 不再手写顺序判断 | 见本次提交 |
| 2026-09-26 | 新增 | GO-TEST（+1 例） | 候选 4：`TestDashboardRouteTables_TestTableIsSubsetOfProduction` —— 把"dashboard 测试路由表 ⊆ 生产路由表"从"靠运气不漂移"变成结构断言（生产 334 / 测试 46 / 只在测试里注册 0 条）。红灯自证：往测试 seam 加 `GET /api/zz-fake-route` → 门禁精确报出该路径并红 | 见本次提交 |
| 2026-09-26 | 新增 | INJECT（8 例） | 候选 7：`tools/inject-mod-id.sh` 收成注入唯一实现（**不维护文件清单**：注入对象 = 全树所有含占位符的文件；MOD_ID 唯一来源 = module.prop；自己断言零残留；不可读文件一律拒绝）。build.sh 的 2 文件 sed 循环与 deploy-device.sh 的 4 文件 sed 一并撤掉 | 见本次提交 |
| 2026-09-26 | 新增 | JS-SYNTAX / GO-BUILD / GO-TEST / TSC / BUN-UNIT | 由 `tools/check.sh` 统一编排（离线档） | f509e85 |
| 2026-09-26 | 新增 | 变更映射自检 | `tools/check.sh` 末尾提醒"改了 A 没改 B"（只提醒不拦，规则见契约 §4） | f509e85 |

## 6. UI parity 已知缺口（基线与理由）

| 缺口 | 证据 | 结论 |
|---|---|---|
| `/api/auth/oidc/start`、`/api/auth/saml/start`（`LoginView.svelte`） | 上游 Go 版只实现 SSO 配置测试，登录流程整体未实现（回调端点由我们补为 501，起点仍 404） | 已知、有意：**不做 SSO 登录**（`AGENT-CONVENTIONS.md §10`）。若将来要做，起点与回调一起补 |
| `/v1/web/fetch`（`MediaProviderDetail.svelte`） | `media.HandleWebFetch` **已实现但从未挂载**（`grep HandleWebFetch` 只有定义）；真机探测 `/web/fetch` 与 `/v1/web/fetch` 原为 404 | **已修（2026-09-26）**：显式双注册（ADR-0003 补丁 `tools/patches/media-web-fetch-route.patch`）；真机 `GET` 由 404 → **405**（路由存在）→ 基线收紧为 2 条 |

