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
| JS-UNIT | 模块 WebUI 回归（当前 **128 例 / 10 文件**；文件清单由 **glob 全量**展开，不手写文件名；**加载哪些脚本、按什么顺序，由 `index.html` 的 `<script src>` 清单派生**）。含**装配层冒烟**（在桩 DOM + 桩 `ksu.exec` 里按清单顺序跑真实面板脚本）与 **DOMID 契约**（见 §3） | `node --test module/webroot/test/*.test.js` | node | 解析层/命令构造器/键契约/上游地址契约/DOM id 契约回归，以及装配层"跑到底"的回归。**为什么必须 glob + 清单派生**：此前手写文件名，`engine-spec-contract.test.js` 成了「存在、能被跑、但唯一入口从不跑它」的门禁孤儿（2026-09-27 修正）；把扫描目标按文件名写死时，面板拆成多文件后新增页面会**静默失去覆盖**（2026-09-29 修正） |
| BUN-UNIT | 引擎 Dashboard 纯函数回归 **82 例**（10 文件） | `bun test web/src` | bun | Dashboard 逻辑回归（请求形状、供应商解析、导入导出等） |
| JSTYPES | 面板脚本类型闸（**棘轮两层**：`checkJs:false` → 只有顶部写了 `// @ts-check` 的文件被检查；严格度取"零注释成本"档）。拦的是 TS2304 找不到名字 / TS2554 参数个数 / TS2339·TS2551 属性拼错 / TS2300·TS2451 **跨文件顶层重名**（经典脚本共享一个词法作用域，重名 = 整页 SyntaxError） | `web/node_modules/.bin/tsc -p module/webroot/tsconfig.json` | `web/node_modules`（缺则 SKIP） | 面板在真机上"整页失效 / 点按钮报 undefined"（2026-09-27 事故：收编常量时漏改一处裸引用 → 面板只显示 localStorage 旧快照）。**为什么不直接上 strict**：先量过后定档 —— 仅 `parsers.js` 在 strict 下就有 41 条，**全是** `noImplicitAny`（缺 JSDoc 参数标注）、没有一条真 bug；一次补几百处标注会把"拦 bug"变成"补注释" |
| GO-BUILD | 引擎可编译 | `go build ./...` | go | 引擎源码编译失败 |
| GO-TEST | Go 单元测试（排除外网/真机依赖用例） | `go test ./... -skip '<见 §4>'` | go | 引擎侧回归 |
| TSC | Dashboard 类型检查 | `npx tsc -b` | node_modules | 类型错误（构建前提前拦） |
| DEADH | handler / 注册函数是否真的被挂载（定义了却没人调 = 点了必 404） | `python3 tools/check-dead-handlers.py` | python3 | 新增了一条"有实现、有测试、没路由"的代码（`HandleWebFetch`/`RegisterRoutes` 同类） |
| PY-UNIT | 棘轮 module（基线／豁免／只拦新增／收紧）的接口级单测 | `python3 -m unittest discover -s tools -p 'test_*.py'` | python3 | 三个门禁共用的棘轮语义坏了（PARITY/UIPARITY/DEADH 会一起失真） |
| INJECT | `__MOD_ID__` 注入器（全树注入 + 零残留 + 不可读拒绝） | `sh tools/test-inject-mod-id.sh` | sh | 打包/直推两条路径的注入实现漂移（上线后设备上才看到占位符 → 面板取不到信息） |
| WAIT | 「等就绪 / 等消失」原语（`lib/wait.sh`：零等待/如实失败/非法次数拒绝/pid 消失判定） | `sh tools/test-wait-lib.sh` | sh | 轮询语义坏了 → 会谎报"拉起成功"或白等满超时（ADR-0004 那类误报） |
| LIFECYCLE | `lib/lifecycle.sh` 可离线断言的部分（当前 **89 通过 / 1 跳过**；含 **L12 `life_restart_all`**：停 → 引擎 → DNS 三件齐备且有序 —— 无守护分支曾漏掉 DNS，导致域名解析静默全挂）：`life_boot` **不丢弃**守护启动判据且失败重试一次；守护身份按 cmdline 认（pidfile 号被无关进程复用时必须判「不在」）；`life_oom_protect` / `life_rss_kb` 的参数护栏（非法参数绝不写 `/proc`）；**热路径改写后的语义锁定**（L7：活/已退出/空/缺/非数字/**僵尸态**/多行 pidfile，且失败时**完全静默** —— 真机事故：重定向顺序写反会让 `can't open /proc/<pid>/stat` 混进 `panel` 的键值输出）；**退出原因解码**（L9：128+N → 信号名）；**通知安全性**（L10：pidfile 陈旧时绝不向非守护进程发 USR1 —— 默认动作是终止，会误杀无辜进程）；**内存策略用秒记账**（L8）；**意图先于动作的顺序**（L11：断言"动作发生那一刻意图是否已可见"）；**承载性 env 单一来源**（L13：`life_carrier_env_keys` 是唯一声明，写出物与内联兜底的**键集合必须逐键一致** —— 此前是"清单 + 六行 echo + 内联 export"三份并行实现，漂移离线看不见，只有真机 T7 兜）；**父子关系**（L22：守护必须经 `wd_ensure` **直调** ensure —— `$()` 会把引擎孤儿化、CHLD 永久不可达，见 Phase 39） | `sh tools/test-lifecycle-lib.sh` | sh | 开机守护起不来却**日志无痕**（2026-09-29 真机事故：`life_boot` 把 `start-failed` 丢进 `/dev/null`）；把无关进程当成守护 → 面板永久误报 `up` 且永不拉起；僵尸态被当成"活着" → **引擎崩了却永不拉起**；误向无关进程发 USR1 → **杀掉无辜进程**；"先停后写意图" → 守护把用户刚停掉的服务复活（Phase 33.14）；`$()` 调 ensure → 引擎被 init 收养 → 自愈退化成最长 60s、死因恒假 127（Phase 39） |
| CHECKFLAGS | 门禁入口自身：档位选择（无参/单档/全档）+ **严格模式必须同时选档** + 无设备时「非严格 SKIP / 严格必红」的对照（并且"跑不起来"不算"拦住了"） | `sh tools/test-check-flags.sh` | sh | 严格模式空转 → CI 报成功却一条 `T*`/`A*`/parity 都没跑（2026-09-29 走查 A1，最高级别假绿） |
| WATCHDOG | 守护判定核（`watchdog.sh` 以 `_WD_LIB_ONLY=1` 可 source，当前 **18 例**）：去抖（事件确认立刻动手 / 无事件须连续两次 / 单次不动手）、S3 记账（只有 started 才记归属——running/空词不记）、S4 位保持（有在管子进程不清 CHLD 位）、`wd_bring_up`（拉起→等就绪→记账→锚点→日志，S3 纪律唯一实现；running 不记、不就绪**如实失败**；判定词经 `wd_ensure`/`LIFE_ENSURE_VERDICT` 文件消费——W4 桩同步走 `life_verdict_emit`）、死因取证两分支（pid 消失 vs 号被复用——后者是"面板谎报 up"的形状）、A2 顺序不变量（确认-退出先于归属校正，行号断言）、**W7 事实不丢弃**（不在管辖分支不得清 `confirmed_*`，红灯自证：注入清零行精确变红） | `sh tools/test-watchdog-decision.sh` | sh | 去抖/记账/位保持规则被改坏 → 伪退出码死因、死因丢失退化成 2×60s 轮询、单次判死抢跑误杀（此前 336 行主循环离线零覆盖，2026-10-01 架构走查候选 3）；hold 期间确认的死亡被 else 分支丢弃 → hold 到期后复活多等两轮（Phase 39） |
| OPSSTATUS | `ops.sh` 的单行契约（`status`/`panel` 各一行、token 全为 `k=v`）+ 键访问器 `get <key>`（行首键/中间键/多键/缺键/非法键名）+ `action.sh` 端到端值与 `status` 对齐 | `sh tools/test-ops-get.sh` | sh | 管理器「操作」按钮状态全空、端口串成整行残余（2026-09-29 走查 A4：`action.sh` 自己重写了"怎么解析这一行"） |
| INSTALLGATE | 装包**门禁先于动作**：坏包/截断包必须被挡在 `life_stop_all` 之前（断言 pidfile 仍在 = 服务没被停）、文件缺失仍回 `no-src`；成功路径用"门禁行早于 stop_all"的顺序断言兜住（测试里绝不真装包） | `sh tools/test-install-gate.sh` | sh | 装个坏包先把引擎/DNS 停掉，守护未武装时服务**永不回来**（2026-09-29 第二轮诊断 I1） |
| INSTALLFLOW | **install-engine / install-module 的成功与回滚全路径**（当前 **30 例**）：把 `MODDIR` 指到临时目录 + `OPS_LIB_ONLY=1` 只加载定义，在真实文件系统上重放 —— 成功路径（引擎起来**才**写版本、**才**刷 `.bak`、清 `.prev`）、回滚路径（起不来 → 二进制回滚且**绝不谎报版本**）、门禁（太小/非 ELF/缺源 → 绝不碰现有二进制；当前二进制不合格时**不把它当回滚点**）、装包（`module.prop` 换新、包内引擎版本落盘、留档）、**引擎保留**（S6：包内基线旧于在跑引擎 → 不降级 + 记账走 `--keep`；在跑更旧 → 包内为准） | `sh tools/test-install-flow.sh` | sh | 安装是唯一"做错就变砖"的动作，而这两条分支过去**零测试**（`MODDIR` 写死 → 只能真写设备）；顺序保障只有 grep 行号，重排 `ops.sh` 会静默撤掉护栏（2026-09-30 架构扫描 C2）；整包更新把面板更新的引擎悄悄降级（Phase 40） |

### 2.3 真机断言（`tools/check.sh --device`）

`tools/device/test-lifecycle.sh` —— 生命周期与安装（修复前 T2/T3/T8/T9/T10/T13 会红）：

| ID | 断言 | 失败意味着 |
|---|---|---|
| T1 | 守护在场（`watchdog=up`） | 引擎死后没人拉起 |
| T2 | `kill -9` 引擎后 40s 内自愈（新 PID + `/health` 200）。**判据守的是"必须自愈"，不是"必须多快"** —— 事件驱动（Phase 33.13）后实测约 **1.2s**，40s 仍作为上限 | 被杀即永久停机 |
| T3 | 在管理器应用 cgroup 内启动仍能脱组（cgroup=`/`） | 会随管理器应用被系统清理而连坐（ADR-0004） |
| T4 | `stop-user` 后状态如实为 `engine=stopped` 且 30s 内不被复活；`start-user` 能恢复 | 用户停服意图不被尊重 |
| T5 | 维护窗口（hold）内不插手、窗口到期后自愈 | 维护期被守护干扰 |
| T6 | 日志轮转生效（`dnsfwd.log` 由 ~440KB 降到上限内） | 24/7 运行把 `/data` 写满 |
| T7 | 承载性 env 6 个键齐全且真的进了引擎进程 | 缺 `SSL_CERT_DIR`/`AUTO_UPDATE` 等 → HTTPS 失败或绕过模块管理（ADR-0006） |
| T8a–d | 装前门禁：9 字节 404 正文被拒 / 现有引擎字节数不变 / `engine-version` 不谎报 / 引擎仍 up | 会把垃圾装成引擎、设备再无可用引擎（ADR-0007） |
| T9 | 面板 `engine_version` == 引擎自报 `/version.currentVersion` | 状态谎报（面板显示旧版本、永远提示有更新） |
| T10a–b | 整包安装跑完无 `syntax error`，装完 `engine=up` | 安装会覆写正在执行的自己而夭折 |
| T11a–c | 「等就绪/等消失」原语本身（`wait_for` 真/假两判、次数非法即拒绝、`wait_gone` 真消失且不谎报） | 轮询语义坏了 → 会谎报"拉起成功"或白等满超时 |
| T12a–e | 运行期引擎版本自愈 + 来源自检：整包更新后（记录落后）自愈 / 自检字段如实（来源=包内·刚自愈）/ 稳态不重复自愈（来源=运行期记录）/ 同版本重装（靠 mtime）自愈 / 运行期更新不被包内旧值覆盖 / 文件缺失从包内补齐 | 面板谎报旧版本 → 用户看到「假更新」（引擎其实已是新的）；或自检字段说谎 |
| T13a–b | 引擎更新链路（**只读**）：用设备自己的加速节点走「版本清单 → `SHA256SUMS.txt` → arm64 资产」，断言 sha256 与 `SHA256SUMS` 一致、且过 `engine_src_ok` 的体积+ELF 判据。**不执行 install-engine、不改引擎版本**（引擎已是最新时同样能跑） | 地址契约坏了（tag 缺 v → 404；`fetch` 缺 `-L` → 302 空正文）→ 面板点下载必失败（2026-09-27 用户实测） |
| T14a–e | 守护「必然在跑」三要素：`oom_score_adj=-1000`（**显式**策略，不再靠从启动者继承）/ 身份可按 cmdline 认出 / 面板 `watchdog=up` 不误判 / 守护日志有**引导证据行**（区分「没被执行」与「在 source 里就死」）/ 开机判据 `boot: watchdog=` 已入日志（未重启过则 SKIP，不假绿） | 守护被杀或起不来 → 整机失去自愈能力（引擎任何死因都不会再被拉起 = 用户"跑一段时间就挂，要手动开"）；OOM 保护若靠继承，换个启动路径就变成可杀 |
| T15 | 快路径前提校正：**停服 → 启服**（新引擎不再是守护子进程）→ `kill -9` → 要求 **≤30s** 自愈（修好走 10s 兜底 ≈20s；没修则退回 60s 长周期、最多 2×60=120s）。实测 13s | `eng_ours` 陈旧 → 收不到 CHLD 且停在长周期 → 自愈从秒级退化成最多 60s（2026-09-29 走查 A2，本轮新代码的洞） |

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
| `module/webroot/test/parsers.test.js` | 51 | 解析层：`status/panel` 输出、meminfo/RSS、DNS 探测与评分、上游行归一、孤儿判定、**装前门禁**（404 正文/HTML/探针失败/真品）、「先门禁后动作」计划求值、**批量测速输出解析**（按行内索引还原、失败不冒充、**失败原因透出**） |
| `module/webroot/test/bridge-commands.test.js` | 27 | 命令构造器：引号转义、base64 写文件、备份/恢复、`download` 必带 `-f`、`fetch` 必带 `-L`、`sqlSnapshot` 成败判据、`promiseWrap`、`fileSize`/`elfMagic`、**`curlTimingBatch`**（并发在 shell 内部 + 每节点独立文件 + `-w` 契约） |
| `module/webroot/test/contract-keys.test.js` | 6 | 键契约：shell `emit` 键集合 ↔ **全部面板脚本**的消费键（扫描目标从 `index.html` 清单派生，不按文件名写死）；状态词值枚举双向对齐 |
| `module/webroot/test/engine-spec-contract.test.js` | 5 | 「什么算一个引擎」两侧缝死（常量/魔数字面量/检查项存在性/边界语义） |
| `module/webroot/test/upstream.test.js` | 14 | 上游 release 地址契约：tag 缺 v 必须补（原故障复现）、资产与校验和同 tag、加速前缀只加 GitHub 域、`SHA256SUMS` 整词解析、与 `checksumGate` 的 fail-closed 联动、**两道回潮扫描**（不许再手写 release 地址；`upstream.js` 拥有的名字必须带 `KU.` 前缀，扫描目标同样从清单派生）+ 清单顺序断言（`upstream.js` 必须是清单里第一个） |
| `module/webroot/test/app-wiring.test.js` | 4 | **装配层冒烟**：在桩 DOM + 桩 `ksu.exec`（cb3 形态 + 命中形态缓存）里按清单顺序跑真实面板脚本 —— ① 初始化链无 unhandledRejection、`renderPanel` **后半段**与链尾 `renderAccelCur` 都渲染到了（2026-09-27「面板读不出来」事故的回归）、② 每个 `btn-*` 都真的绑上了处理函数、③ **清单双向闭合**：声明的脚本必须存在（漏推送），存在的 `.js` 必须被声明（死文件） |
| `module/webroot/test/gate-flows.test.js` | 4 | 「先门禁后动作」的**流程级**回归：DNS 优选全链可达（探测→Top5→备份→写配置→热重载）、引擎更新全链可达（下载→体积/ELF→SHA256→install-engine）、门禁不过时 install 不可达、**测速结束自动选中最快节点**（A5） |
| `module/webroot/test/orphan-scan.test.js` | 4 | 孤儿扫描/清理流程级回归：健康扫描必须列出孤儿且**不显示警告**（`planSteps` 部分求值假拦的回归）、不可信扫描必须中止判定、快照 SQL 不得翻倍引号、复查读失败必须中止删除 |
| `module/webroot/test/lib/app-harness.js` | — | **桩具唯一一份**（此前 app-wiring / orphan-scan 各复制一份）：宿主全局 + 最小 DOM + **按 realm** 建 exec 桩（回调名在「发起该命令的那个 realm」解析）+ 按 `index.html` 清单顺序加载脚本 + `recordRejections()` |
| `module/webroot/test/dom-id-contract.test.js` | 4 | **DOM id 契约（三个方向）**：① 脚本请求的每个 id 都必须在 `index.html` 里存在（拼错 → 真机整页死）② `index.html` 里每个 `btn-*` 都必须被绑定（死按钮 —— 这是 app-wiring 那条的**另一半**：桩会为拼错的 id 凭空造元素并绑上，所以只有这条能发现"HTML 里没那个按钮"）③ nav 的 `data-page` 必须指向存在的 id。加 ④ 非空转自检（两侧解析结果都必须有量）。**为什么需要**：桩 DOM 的 `getElementById` 永远返回元素 → 这一类错误离线完全看不见 |
| `web/src/**/*.test.ts`（10 文件） | 91 | Dashboard：请求形状（`db-backup`）、供应商/路由解析、批量添加、代理导入、OAuth 交接、登录态收敛、analytics 类型等 |
| Go `./...`（约 28 包） | — | 引擎侧；外网/真机依赖用例见 §4 |

## 4. 已知不绿灯（环境性必红，避免误判为回归）

| 对象 | 现象 | 处理 |
|---|---|---|
| `internal/handlers/media`：`TestHandleAudioVoices_elevenlabs` | 访问 `api.elevenlabs.io` 返回 EOF → 502 | 列入 `go test -skip`（名单在 `tools/check.sh` 的 `go_skip_pattern`） |
| `internal/handlers/chat`：`TestLiveE2E_Cline_SmartCombo`、`TestIntegration_OpenCode_MuseSpark13_ChatCompletions` | 本机存在 `~/.9router` 时会真的打上游 → 失败 | 列入同一 `-skip` 名单；**已在上游 pristine 树（`../9router-go`）复跑，同样失败**（2026-09-26 证据）→ 环境依赖，非本仓回归 |
| 其它 `*_live_*` / `*_e2e_*` 用例 | 需要真实 key 或 `$HOME/.9router/db/data.sqlite` | 多数自带 `t.Skip`（本机有库时才会真跑）；**不扩大排除范围**，出问题先在上游树复跑 |
| `GO-BUILD` / `GO-TEST` | `dial tcp … proxy.golang.org:443: i/o timeout`（本机直连不到 Go 官方代理，尤其上游同步带来新依赖时） | **不是回归**：用国内镜像重跑 —— `GOPROXY=https://goproxy.cn,direct bash tools/check.sh --offline`（实测可用；`ALL_PROXY` 对 Go 无效，Go 只认 `HTTP(S)_PROXY`） |
| 真机档在无设备时 | 全部 `T*`/`A*` | SKIP（退出 0）；严格模式 `--require-device` 才失败 |
| 真机 `T13` | 需要**外网 + 设备上选中的加速节点可达**（会真的下载 ~25MB 到 `/data/local/tmp` 再删掉） | 前置不可达时如实打印 `T13 跳过：…` 并**不计入通过**；这是真机档唯一的外网依赖，别把它当成"网络抖动 = 回归" |

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
| 2026-09-29 | 新增 | LIFECYCLE | 开机守护起不来却日志无痕（`life_boot` 丢弃判据）；守护身份校验与 OOM 策略参数护栏 | 见本次提交 |
| 2026-09-29 | 修改 | LIFECYCLE（19 → 55） | 守护事件驱动的前置：热路径改写（内建 `read`/僵尸判定/失败静默）、退出原因解码、USR1 通知安全性、内存策略改秒记账、**意图先于动作**的顺序断言（Phase 33.11–33.14）。红灯自证：L11 在前置顺序写反时精确变红 | 见本次提交 |
| 2026-09-29 | 新增 | CHECKFLAGS / OPSSTATUS | 架构走查 A1（严格模式空转 —— 门禁报成功却一条都跑，最高级别假绿）与 A4（`action.sh` 自己重写了"怎么解析 ops.sh 的单行 status"） | 见本次提交 |
| 2026-09-29 | 新增 | T15（真机） | 架构走查 A2：`eng_ours` 陈旧 → 自愈退化成 60s 长周期（修复后实测 13s） | 见本次提交 |
| 2026-09-29 | 修改 | JS-UNIT（117 → 123，+1 文件）/ LIFECYCLE（55 → 56） | 架构走查 A5/A6 行为用例（谎报成功 / 丢回滚点 —— 断言命令流与文案双向对照）、B5 属性转义源码扫描、A3 组合断言 L12（停 → 引擎 → DNS 齐备） | 见本次提交 |
| 2026-09-29 | 新增 | INSTALLGATE | 第二轮诊断 I1：装包门禁必须早于停服务（坏包曾把引擎/DNS 停掉就走） | 见本次提交 |
| 2026-09-29 | 修改 | JS-UNIT（123 → 128）/ LIFECYCLE（+跳过计数） | 第二轮诊断 I2–I6 的行为与源码断言（startSvc 诚实度、reload 失败必报、`.prev` 刷新顺序、清选中不得假故障）；I7：L7g 由"跳过记成 PASS"改为 `skp` + 汇总打印跳过数（**跳过要看得见**） | 见本次提交 |
| 2026-09-29 | 修改 | 真机档入口（`tools/check.sh`） | **跑前/跑后各直推一次当前源码**：T10 会把设备 `lib/` 换成 `$DATA_DIR/last-module.zip`（旧包），否则**下一次**真机档在旧代码上跑 → `T4` 假红（Phase 33.16 实测） | 见本次提交 |
| 2026-09-29 | 新增 | T14a–T14e | 守护「必然在跑」三要素：显式 OOM 保护 / 身份可认 / 引导证据行（Phase 33） | 见本次提交 |
| 2026-09-29 | 修改 | JS-UNIT | 用例 86→**108**；加载目标由 `index.html` 清单派生（面板拆分后按文件名扫描会静默失去覆盖）；新增清单双向闭合断言 | 见本次提交 |
| 2026-10-01 | 新增 | WATCHDOG（16 例，离线档） | 架构走查候选 3：守护决策核纯函数化（`_WD_LIB_ONLY` 库模式）——去抖/S3 记账/S4 位保持/死因取证两分支/A2 顺序不变量首次可离线红绿；真机档回归：kill -9 引擎后 1s 内 CHLD 确认 → 事件确认拉起 → 取证（新判定核全链） | 见本次提交 |
| 2026-10-01 | 新增 | JS-UNIT（147 → 154，+回执词门禁）/ 修改 LIFECYCLE（+L21） | 架构走查候选 1/2：`ACTION_WORDS` + `actionOk`（动作回执词唯一所有者，7 处手抄 expect 收口；contract-keys 双向门禁对齐 shell emit）；`life_settle_report`（三态词编排唯一实现）+ `life_wait_engine_ready`（时限一处）+ L21 单一家庭断言；另候选 4/5：bridge `remove` 自报 rm-ok、`life_ensure_stack` 拉回清单唯一化 | 见本次提交 |
| 2026-10-02 | 修改 | LIFECYCLE（+L22，59 → 89 例）/ WATCHDOG（16 → 18，+W7）/ JS-UNIT（contract emit 通道） | **真机 T5 红灯挖出守护事件驱动的结构性失效**：`wd_bring_up` 经 `$()` 调 `life_ensure_engine` → 引擎被 init 收养（PPID=1，真机实锤）→ CHLD 对引擎死亡永不可达 + `wait` 恒假 127 + `eng_ours` 非空使守护睡 60s 长周期。修复：`wd_ensure` 直调（判定词经 `LIFE_ENSURE_VERDICT` 文件带回；contract-keys 的 emit 扫描同步认 `life_verdict_emit`）；W7 锁"else 分支不清 `confirmed_*`"。红灯自证：L22d（注入 `$()` 即红）/ W7（注入清零即红）。真机复验：kill → CHLD 同秒确认、死因取证首次拿到真实 137、T2 拉起 1s、T* 37/37 全绿。**教训**：Phase 33.13 的"1.2s 恢复"从未被父进程身份佐证——事件是否可达要以 PPID 为证，不能以日志里"事件确认"的字样为证（那是拉起时 setsid 中间进程退出留下的陈旧标志位） | 见本次提交 |
| 2026-10-02 | 新增 | INSTALLFLOW（24 → 30，+S6）/ T10c（真机） | **两条更新通道不打架**（Phase 40 用户决策）：包内引擎基线旧于在跑引擎时整包更新不降级（install-module 保留 + customize.sh 同语义 + 记账 `--keep`）；T10 前快照/被滚回则 install-engine 恢复/断言自报==快照。红灯自证：禁用保留分支 → S6b/c 精确变红。真机 T10c 首跑即真实触发恢复，T\* 38/38 全绿 | 见本次提交 |
| 2026-09-29 | 修改 | BUILD-7 | `verify_zip` 增加「`index.html` 声明的每个脚本都必须在包内」（Phase 27.6 漏推事故的打包侧孪生断言） | 见本次提交 |
| 2026-09-29 | 修改 | T10 | 明确副作用：本步会把设备 `lib/` 换成包内版本 → 开发直推态失效，需重跑 `tools/deploy-device.sh`（否则下一轮 T14 会在旧代码上跑） | 见本次提交 |
| 2026-09-29 | 新增 | JSTYPES | 面板类型闸（棘轮两层：`// @ts-check` 逐文件点亮 + 零注释成本档；红灯自证：插一个裸引用 → TS2304） | 见本次提交 |
| 2026-09-29 | 修改 | BUILD-7 | 纯净发布：`webroot/types/*` 与 `webroot/tsconfig.json` 不许进发布包（类型闸开发专用） | 见本次提交 |
| 2026-09-29 | 新增 | JS-UNIT +4（DOMID） | DOM id 契约三方向（JS→HTML / HTML→JS 死按钮 / nav data-page）—— 补"桩 DOM 永远返回元素"的盲区；红灯自证：三个方向各注入一个错误 → 精确 3 红（Phase 32.7） | 见本次提交 |
| 2026-09-29 | 新增 | JS-UNIT +4（A5 批量测速） | `parseCurlTimings`（索引还原/失败不冒充）+ `curlTimingBatch`（并发在 shell 内部/每节点独立文件/`-w` 契约）；真机端到端验证过（Phase 32.8） | 见本次提交 |
| 2026-09-29 | 新增 | JS-UNIT +2（A5 提速） | `--connect-timeout 3`（失败节点 5.03s→3.00s）+ 失败原因透出 + **测速即选中**；真机整批 4 节点从 ~5.3s 降到 **2s**。新用例第一次跑就抓到桩的顺序缺陷（写 `github-accel` 的命令含同名字符串，被读分支吞掉） | 见本次提交 |
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
| 2026-09-26 | 新增 | JS-UNIT（58 → 63 例） | 候选 5：`engine-spec-contract.test.js` 把「什么算一个引擎」两侧缝死（常量两种写法不许漂、魔数字面量逐字相同、两项检查的存在性、边界语义 `-ge` ↔ `<`）。红灯自证：ops.sh 下限改 4MB → 精确报出「前端放行、后端拒绝」并红。ADR-0007 补第 7 条澄清「判据 vs 执行 ≠ 抄两遍」 | 见本次提交 |
| 2026-09-26 | 新增 | WAIT（离线 10 例）+ 真机 T9（6 例） | 候选 6：`module/lib/wait.sh` 收成「等就绪/等消失」唯一实现（`wait_for` / `wait_gone` / `wait_pid_gone`；先判定再睡觉、次数非法即拒绝、僵尸态算已退出、谓词在当前 shell 执行）。**五处** ad-hoc 轮询全部收敛（watchdog 拉起等待、life_stop_all、life_restart_engine、life_wd_start、真机门禁 3 处）；`life_pid_gone` 保留名字但实现改为委派。附带收益：轮询语义第一次能离线验（此前只能上真机） | 见本次提交 |
| 2026-09-26 | 新增 | 真机 T12（4 例） | 用户报障「模块更新后界面显示已是最新、概览仍是旧版本 → 假更新」：根因是整包更新**不跑我们的代码**（WebUI 按钮由设备上那份旧 `ops.sh` 执行；管理器在线更新一个字节模块代码都不跑）→ 运行期 `engine-version` 停在上一个版本，面板据此谎报。新增 `ops.sh engine_version_sync`（`cmd_status` 入口读取时自愈；主判据 = `engine-version-code` 记录，mtime 仅作补充）。**T12a 首跑即抓到我实现里的真缺陷**：mksh 的 `-nt` 只到秒精度，同一秒内"先写文件再 touch"判不出来 → 改为不依赖精度的记录判据（T12a2 专门盯 mtime 那条补充路径） | 见本次提交 |
| 2026-09-26 | 新增 | JS-UNIT（67 → 69 例）+ 真机 T12 扩到 a–e | 概览页「引擎版本」旁新增**来源自检**（运行期记录 / 包内 · 刚自愈 / 无来源），由 `ops.sh` 的 `engine_ver_src` + `engine_ver_healed` 两个字段驱动、`parsers.js engineVersionSourceLabel` 出文案。T12 相应扩展：自检字段必须如实（T12a2 来源=包内·刚自愈、T12b 稳态不重复自愈）—— **写成"一次 panel 快照取多字段"**，因为每次 panel 都是新进程，"刚自愈"只在那一次调用里为 1（分多次取会读到第二次的 0） | 见本次提交 |
| 2026-09-26 | 新增 | JS-UNIT（63 → 67 例） | DNS 优选纳入计划化（`DNS_OPTIMIZE_PLAN`：无可用上游不得改写 / 回滚点不可用不得改写 / write 与 reload 在两道门禁之后）；**顺手修掉一个"永远 true"**：`KB.backupOnce` 原先 `...; true` 恒返回真，挂在它上面的门禁形同虚设 —— 现按 shell 回的 ok/exists/no-src/fail 诚实返回（三个调用点都忽略返回值，无行为回归） | 见本次提交 |
| 2026-09-26 | 新增 | BUN-UNIT（83 → 91 例） | 候选 8：`web/src/lib/session.ts` 收成登录态唯一所有者（存储注入 → 纯函数；两种"登出"有意区分：`clearAuthed` 只清标记 / `clearAll` 连 key 一起清）。7 处调用点收敛（client.ts ×4、LoginView、App.svelte、EndpointView、AnalyticsView、client.test.ts）。含**防回潮门禁**：全树扫描 `'9router_auth'` / `'9router_key'` 字面量，只允许出现在 `lib/session*` | 见本次提交 |
| 2026-09-26 | 新增 | JS-SYNTAX / GO-BUILD / GO-TEST / TSC / BUN-UNIT | 由 `tools/check.sh` 统一编排（离线档） | f509e85 |
| 2026-09-26 | 新增 | 变更映射自检 | `tools/check.sh` 末尾提醒"改了 A 没改 B"（只提醒不拦，规则见契约 §4） | f509e85 |
| 2026-09-27 | 修改 | JS-UNIT（清单 → glob 全量，46 → 83 例） | 用户报障「更新引擎点下载必失败」定位时发现：`engine-spec-contract.test.js`（候选 5 的门禁）**从未被唯一入口执行过** —— 清单手写必然漏，改为 glob 展开；台账例数与 §3 目录同步为实测值 | 见本次提交 |
| 2026-09-27 | 新增 | JS-UNIT（+14 例：`upstream.test.js` 13 + `bridge-commands` 1） | Phase 27：上游 release 地址契约（tag 缺 v → 404）与 `fetch` 缺 `-L`（302 空正文）两条通道缺陷的回归。含**回潮扫描**（`app.js` 不许再手写 release 地址）。**红灯自证**：回退三处修复 → 7 例精确变红 | 见本次提交 |
| 2026-09-27 | 新增 | 真机 T13（a–b） | Phase 27：引擎更新链路的只读真机门禁（清单 → SHA256SUMS → arm64 资产 → 摘要/ELF），不安装不改版本；前置不可达即 SKIP | 见本次提交 |
| 2026-09-27 | 修改 | 真机档前置 | T13 引入真机档唯一的**外网依赖**，§4 已登记（不可达 = SKIP，不是回归） | 见本次提交 |
| 2026-09-27 | 修改 | PARITY（基线 135 → 131） | 上游同步 v1.9.3：4 条 Kiro OAuth 路由缺口被上游补齐 → 按棘轮语义 `--write-baseline` 收紧。**附带**：基线文件被当前生成器去掉了方法名对齐空格（格式归一），所以 diff 看起来很宽 —— `git diff --ignore-all-space` 只有 1 增 5 删，可据此复核 | 见本次提交 |
| 2026-09-27 | 修改 | GO-BUILD / GO-TEST 前置 | 上游 v1.9.3 带来新依赖 `golang.org/x/sync`，本机 `proxy.golang.org` 直连超时 → §4 登记 `GOPROXY=https://goproxy.cn,direct`（`ALL_PROXY` 对 Go 无效） | 见本次提交 |
| 2026-09-27 | 新增 | JS-UNIT（83 → 86 例，+`app-wiring.test.js`，`upstream` +1） | Phase 28：把 URL 常量收编进 `upstream.js` 时漏改一处裸引用，面板整个读不出来 —— **纯函数用例拦不住装配层缺陷**。新增「跑真实 `app.js`」的冒烟门禁 + 「`upstream.js` 拥有的名字必须带 `KU.` 前缀」的静态扫描（清单从 return 对象现取）。红灯自证：两处都精确变红 | 见本次提交 |
| 2026-10-03 | 新增 | GO-TEST（`internal/constants` +3 例、`internal/proxy` +4 例） | 真机事故：到 codebuddy.ai 的 TLS 握手间歇性 stall，`TLSHandshakeTimeout: 10s` 把每次卡死原样报成 502，而 `retryTransientUpstream` 对传输层错误**零重试**（`fallback.go` 也不换号）→ 10.105s 的 502 直达客户端。改动：超时 10s→3s（可由 `HTTP_TLS_HANDSHAKE_TIMEOUT` 覆盖）+ 连接层失败重拨。**可重试的语义依据**：握手未完成时无应用数据发出，重拨无重复计费风险 —— 这条写进 `IsTransientTransportError` 注释，避免后人误以为与 502 同类。红灯自证：`TestIsTransientTransportError_ClassifiesConnectionFailures` 锁住三个排除项（代理失败 / 客户端已断开 / DNS 失败）不被重试，`TestDoRequest_DoesNotReDialProxyFailures` 锁住 operator 指派代理仍显式失败。**已知无关抖动**：`internal/fetchgate` 的 `TestGateAcquire_SpacesConcurrentCallers` 在 `go test ./...` 并发负载下偶发（单独连跑 5 次全绿），与本改动无关，未处理 | 见本次提交 |
| 2026-10-03 | 修改 | LIFECYCLE（新增 L23a–L23c） | 真机事故：默认 DNS 兜底第三条是 `nameserver 1.1.1.1`，它给 `www.codebuddy.ai` 返回 43.17x 段（两条国内腿返回的是同一个 43.160.158.125，不同批次），而这批节点从 CMCC 宽带出去 TLS 握手反复卡 16–20s，撞上 10s 硬超时即 502。`dnsfwd` 只在**查询**失败时切腿，不会在"解析成功但节点握手差"时切，所以这条腿是纯负债。已从 `life_prep` 默认集删除，保留两条已验证的国内明文腿；要加更多上游走面板 DNS 优选（它会实测 RTT 与可用率，见 `page-dns.js` 的候选池）。**L23 打在行为上**：真调 `life_prep` 生成一份再读内容，不 grep 源码。红灯自证：把 `nameserver 1.1.1.1` 放回默认集 → L23b 精确变红。**踩到的坑**：`LIFE_UPSTREAMS` 是 source 时按 `DATA_DIR` 展开的派生量（`lifecycle.sh:35`），断言里只改 `DATA_DIR` 不够、必须与 L13 同样显式重设派生路径 —— 第一次写时漏了这步，L23a 假红 | 见本次提交 |

## 6. UI parity 已知缺口（基线与理由）

| 缺口 | 证据 | 结论 |
|---|---|---|
| `/api/auth/oidc/start`、`/api/auth/saml/start`（`LoginView.svelte`） | 上游 Go 版只实现 SSO 配置测试，登录流程整体未实现（回调端点由我们补为 501，起点仍 404） | 已知、有意：**不做 SSO 登录**（`AGENT-CONVENTIONS.md §10`）。若将来要做，起点与回调一起补 |
| `/v1/web/fetch`（`MediaProviderDetail.svelte`） | `media.HandleWebFetch` **已实现但从未挂载**（`grep HandleWebFetch` 只有定义）；真机探测 `/web/fetch` 与 `/v1/web/fetch` 原为 404 | **已修（2026-09-26）**：显式双注册（ADR-0003 补丁 `tools/patches/media-web-fetch-route.patch`）；真机 `GET` 由 404 → **405**（路由存在）→ 基线收紧为 2 条 |

