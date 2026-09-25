# 修复计划 · 2026-09-25（修补循环终结计划）

> 背景：模块管理面板经历"哨兵累积 → 形态探测 → 单行补偿 → 诊断探针 → chmod 兜底 → panel 批量"
> 一连串窄修补，可靠性差。三轴评审（Standards / Spec / Architecture）定位出病根：
> **同一语义多处复制**——45 处内联拼 shell、4 处内联 kill→重启、3 层 promise 补偿。
> 本文档按依赖顺序追踪修复，每项含验收命令（真机检测）。

## 状态图例

- [ ] 待办 · [~] 进行中 · [x] 完成（含验收日期）

## Phase 0 · 根因止血（立即项）

- [x] **0.1 模块自更新通道 chmod 漏 lib/**（同一 bug 从第二条更新通道复发）
  - 修复：`app.js` modUpdate 的 `chmod 0755 *.sh bin/*` → `*.sh lib/*.sh bin/*`
  - 验收：真机走一次 WebUI 模块更新（下个版本发布时），更新后 `ls -l $MODDIR/lib/ops.sh` 为 `-rwxr-xr-x`
- [x] **0.2 sqlite 读失败冒充 0 → 误判全新安装 → 自动写库**（ops.sh + checkFactoryKey 双侧护栏）
  - 修复：`cmd_status` 读失败输出 `factory_key=err`；`checkFactoryKey` 遇非数字显示"状态未知"，绝不自动 seed；`cmd_seed_key` 读失败/写失败如实返回 `error`
  - 验收：`adb shell su -c 'F=/data/adb/9router-go/db/data.sqlite; /data/adb/modules/ninerouter-go/bin/sqlite3 $F "BEGIN EXCLUSIVE; SELECT 1;" & sleep 0.2; /data/adb/modules/ninerouter-go/lib/ops.sh status'` → `factory_key=err`，面板显示"状态未知"且无自动写入
- [x] 0.3 语法与回归：`sh -n ops.sh` 通过、parsers 17/17 通过（2026-09-25）

## Phase 1 · 生命周期收回 ops.sh seam（架构候选 #2，Strong）✅ 2026-09-25

- [x] **1.1 ops.sh 新增 `stop-all` / `restart-engine` 子命令**（kill→rm pidfile→sh service.sh 唯一实现；restart-engine 内置 20s 等待，调用即知结果；不触碰 dns-disabled 用户开关）
- [x] **1.2 app.js 4 处内联生命周期 shell 全部删除**，改调 KB.ops（restartAll / savePort / engUpdate / modUpdate；engUpdate/modUpdate 增加替换失败报错）
- [x] **1.3【计划外·重要发现】mksh 参数展开 `|` 运算符 bug**
  - 现象：mksh 里 `${v%%|*}` 把整个值删空（`|` 是模式"或"运算符），Linux sh/bash 里是字面量——SQLite 计数被静默删空、factory_key 恒显示 0/空
  - 修复：转义 `\|`（两种 shell 行为一致，设备实测）；全模块 grep 无其他同类隐患
  - 教训沉淀：**凡是 Linux 上写、Android 上跑的 shell 模式匹配，`|` 必须转义**
- [x] 验收（真机 2026-09-25）：`stop-all` → `stopped`；`restart-engine` → `engine=up`（20s 内）；`panel` 全量正常，`factory_key=1 apikeys_total=1`（真实值首次浮出）；`engine_version=` 留空按 Q4(b) 决策显示"未知"

## Phase 2 · 命名桥操作层（架构候选 #1，Strong）✅ 2026-09-25

- [x] **2.1 bridge.js 命名操作层落地**：readFile / writeFile（base64 内容通道 + 原子落盘）/ appendLine / remove / backupOnce / restoreBackup / probeDns / curlTiming / fetch / download / sha256 / zipList / sqlSnapshot；`shq` 成为引号转义唯一出口；构造器以 `_cmds` 导出（纯函数）。回调解累积 `appendChunk` 去重（sentinelExec/probe 共用）
- [x] **2.2 seam 扩展（ops.sh）**：新增 `reload-dns` / `install-engine <file>` / `install-module <zip>`（备份→替换→权限兜底含 lib/→清理→重启，唯一入口）；app.js 的 engUpdate/modUpdate 安装段全部下沉
- [x] **2.3 app.js 迁移完成**：KB.sh 调用点 34 → **2**（仅剩诊断探针 `id`/`ls`，属豁免项）；`cat ACCEL_SEL` 五连读消除；scanCred 由 N+1 次 SQL 合并为 1 次；修复 addAccel strip 单引号破坏含引号 URL 的 bug（URL 现在完整保留）
- [x] **2.4 命令构造器离线测试**：`test/bridge-commands.test.js` 12 项（转义/base64 通道/原子落盘/哨兵/MODDIR 注入），合计 **29/29 通过**
- [x] 验收（真机 2026-09-25）：`reload-dns` → `reloaded`；`install-module /nonexistent.zip` → `no-src`（护栏）；设备 base64 编解码含中文 UTF-8 往返正常；panel 全量正常
- [ ] 待真机功能回归：WebUI 全功能过一遍 §验收矩阵（需重进面板加载新 app.js/bridge.js）

## Phase 3 · withBusy + 状态收敛（架构候选 #4）✅ 2026-09-25

- [x] **3.1 `withBusy(btn, busyLabel, fn)` 包装器**：busy 态 / 异常 console.error + toast / finally 必然恢复按钮；全部 16 个用户入口（重启/端口/DNS 保存/优选/回滚/探测/恢复/孤儿扫描清理/凭据扫描/测速/引擎与模块检查更新/补 key）统一接入
- [x] **3.2 全局态收敛**：`window._modUrl/_modUpdate/_engLatest` + `orphanAliases` → 单一 `state` 对象；`state.moduleVersion` 免 DOM 反解
- [x] **3.3 死代码清除（deletion test）**：`setBind`/`setBindUI`/`restartDns`/`BIND_FILE`/`ENG_PID`/`DNS_PID`——index.html 无绑定范围按钮，相关 JS 全部失联（修补循环又一实证：UI 早已删除、逻辑残留）
- [x] 验收：`node --check` 通过、29/29 测试通过、已部署真机

## Phase 4 · promise 降级补偿单层化（架构候选 #3）✅ 2026-09-25（范围按 YAGNI 收窄）

- [x] **4.1 bridge.js promise 形态统一 base64 包裹**（`_cmds.promiseWrap`）：多行输出（SQL 结果、dnsfwd 探测、诊断）在"只剩末行"的管理器实现上不再失真——环境补偿从此只在 bridge 一层
- [x] **4.2 保留 ops.sh panel 单行 + upstreams_b64**（范围调整说明）：panel 单行通路已经实测稳定且高效；改回多行需 parser/panel/UI 三处联动重验，收益为零。补偿的"唯一出口"已由 4.1 达成，删除既有单行化反而是为删而删
- [x] 验收：promiseWrap 离线测试通过（30/30）；真机 panel/reload-dns 冒烟正常

## Phase 5 · Standards/Spec 杂项收口 ✅ 2026-09-25

- [x] 5.1 ops.sh 子命令清单单一来源（`USAGE` 变量，兜底分支复用）
- [x] 5.2 MAGISK.md 契约已同步（Phase 1 内完成：panel 等子命令 + 123456 密码如实入档）
- [x] 5.3 ops.sh `read_bind()` 原语抽取（status/start-dns 去重）
- [x] 5.4 诊断探针走 `KB.ops('status')`；ACCEL_SEL 五连读消除（Phase 2）；`modVersion()` DOM 反解 → `state.moduleVersion`（Phase 3）
- [x] 5.5 engine_version 停止伪造，留空显示"未知"（Q4b，Phase 1 完成）
- [x] 5.6 bridge.js `appendChunk` 去重；`callForm` 未知形态显式抛错（Phase 2/4）

## Phase 6 · 引擎定点修复：Dashboard 导入数据库 400（ADR-0003 流程）✅ 2026-09-25

- [x] **6.1 根因**：Go 引擎内嵌 Svelte 前端以 multipart/form-data 上传备份（`file` 字段、无密码、凭 admin 会话），后端 `HandleImportDatabase` 按 Node 时代的 JSON+password 解析 → Dashboard 导入必然 400 "Invalid database payload"。旧设备可用是因为老版 Node Dashboard 发的就是 JSON——新设备首次用引擎自带 UI 时暴露
- [x] **6.2 修复（单函数范围）**：`HandleImportDatabase` 识别 multipart 分支，解析 `file` 字段；授权三选一（admin 会话 / CLI token / 密码表单字段，中间件本就强制 admin 会话，handler 级为纵深防御）；Node JSON 流保持不变
- [x] **6.3 回归测试**：`TestHandleImportDatabase_Multipart`（401 无凭据 / 400 缺 file / 200 落库三段断言）✓；全包 `go test` ✓
- [x] **6.4 流程合规（ADR-0003）**：补丁存档 `tools/patches/dashboard-import-multipart.patch`；ADR-0003 表格登记；上游 PR 义务待提交；上游参照树（`9router-go/`）已恢复 pristine
- [x] **6.5 部署与真机验证**：交叉编译 arm64 → `ops.sh install-engine` 安装（seam 实战首秀，engine=up）→ 副本库实例端到端验证：登录 → multipart 导入 **200** → 数据真实落库。修复前同请求为 400

## Phase 7 · dnsfwd 关闭语义真伪排查 + 孤儿进程修复 ✅ 2026-09-25

- [x] **7.1 问题定性**（用户问："关闭后能否自动识别手机已有 DNS？开关是假开关吗？"）：
  - 引擎是纯 Go 静态二进制，域名解析**只认 127.0.0.1:53**（/etc/resolv.conf 在 Android 上不存在，实测；net.dns1 系统属性 Go 不读）——引擎不会"自动识别"设备的其他 DNS 配置
  - 关闭后能正常工作的唯一条件：用户自己的 DNS 服务监听 **127.0.0.1:53**（此时 start-dns 的让路逻辑也会正确让位）
  - 实测抓到真 bug：`dns=disabled` 但有**孤儿 dnsfwd（PID 13960）**仍占 :53——stop-dns 只信 pidfile，pidfile 失联即杀不掉，"关闭"变假关闭；此前引擎能解析正是靠这个孤儿
- [x] **7.2 修复（ops.sh）**：`kill_our_dnsfwd` 原语 = pidfile 精确杀 + `pgrep -f <完整二进制路径>` 兜底（只匹配自己的 dnsfwd/引擎，绝不误杀第三方 DNS 服务）；`stop-dns`/`stop-all` 全部接入
- [x] **7.3 面板诚实化（app.js）**：关闭后实时探测 :53——有服务接管 → 告知"引擎解析由它接管 ✓"；无服务 → 明确警告"引擎域名解析会失败，模型将无法连接"，替代原先言过其实的"引擎将改用设备已有 DNS 方案"
- [x] **7.4 真机验证**：孤儿 13960 清除 ✓；:53 无监听确认 ✓（用户自有 DNS 服务绑上 :53 后引擎解析即恢复；否则需重新开启 dnsfwd）

## Phase 8 · 第三方 DNS 自动让路 + 开机宽限窗口 ✅ 2026-09-25

- [x] **8.1 机制确认**：`start-dns` 本就探测 `:53` 被占即让路（面板显示"已让路"，引擎解析由已有服务接管）——用户问的"检测到已有 DNS 就不开启内置"核心已存在
- [x] **8.2 时序缺口修复**：Magisk 服务早于普通 App 启动，第三方 DNS 服务晚到会抢不到端口。`start-dns` 增加 **5 秒让位宽限窗口**（空闲时复查一次再绑定；残余竞态已在 MAGISK.md 如实说明，兜底方案：面板关闭 dnsfwd 再启动自己的服务）
- [x] **8.3 真机验证（四路径）**：disabled（用户开关尊重）✓ / yielded（第三方占用不启动）✓ / started（空闲+宽限窗口后绑定）✓ / 恢复用户关闭状态 ✓

## Phase 9 · 面板首屏性能优化（3-5s → 目标秒出）✅ 2026-09-25

- [x] **9.1 根因定位**：adb 实测 `ops.sh panel` 仅 0.23s——瓶颈不在 shell 而在 WebUI 桥接层：
  ① 形态探测**串行**且每次开页重来（cb3 等 4s → cb2 等 4s，降级形态的管理器上探测即 8s）
  ② 探测结果从不缓存
  ③ 首屏必须等完整一轮 shell 才渲染，无快照
- [x] **9.2 修复（bridge.js）**：探测结果持久缓存 localStorage（一次探测终身复用）；cb3/cb2 **并行**探测（上限 4s→2.5s）；缓存形态失联时（exec timeout）自动清缓存重探测并重试一次
- [x] **9.3 修复（app.js）**：panel 快照持久缓存——打开页面**先渲染上次状态（秒出）**，panel 后台刷新后覆盖；实时数据缺 port 才触发诊断区
- [x] **9.4 明确不做**：panel 内部 3 个 awk 合并为 1（shell 侧实测仅 0.23s，非瓶颈，不值得为省 2 个 spawn 牺牲可读性）
- [ ] **待用户验收**：重进面板两次——第二次应秒出；console 里 `[9r-panel] panel 耗时` 可反馈真实数值

## Phase 10 · 模型测试 401 根治 + 修复流程硬化 ✅ 2026-09-26

- [x] **10.1 根因**（日志实锤：19 条 `POST /api/models/test status=401`，耗时 ~0.44ms，未达上游）：`/api/models/test` 被挂在 RequireApiKey 组内，备份导入清空 apiKeys 表 → 仪表盘模型测试全军覆没。Node 原版该端点是 dashboard 内部端点（admin 会话语义）——**此分歧为 Go 移植自行引入**（parity 缺陷，ADR-0003 范畴）
- [x] **10.2 坦白记录**：此问题此前被"修"过一次——只在 `webroot/index.html` 写了提示文字，无结构修复，一天后复发。文档式修复 ≠ 修复
- [x] **10.3 修复（Q1a，单点）**：`/api/models/test` 移入 RequireDashboardAuth 组（admin 会话 / CLI token / API key 三选一）；仪表盘从此不依赖 apiKeys 表，**"导入毁 key → 测试全挂"这一整类问题被消灭**
- [x] **10.4 回归测试（Q3a）**：`TestSetupServerRouter_ModelTestSessionAuth`（空 apiKeys 表 + 会话 cookie 必须越过鉴权 + 无凭据错误体不得是 apiKeys 语义）✓
- [x] **10.5 Q2(a) 结构性达成说明**：原定"导入后补出厂 key"——Q1 落地后仪表盘不再依赖 apiKeys 表，该护栏被结构性取代；出厂 key 的补入保留面板"补入出厂 key"按钮（本次已实测可用）
- [x] **10.6 计划外真 bug（用户点击按钮暴露）**：`cmd_seed_key` 的 `[^0-9]` 在 mksh/dash 里 `^` 是**字面量**（类= ^+数字），"0" 被误判为读取失败 → 永远 error。已改 `[!0-9]` 并真机验证 `seeded`。教训与 `|` bug 同族，已写入 AGENTS.md §5.0
- [x] **10.7 流程硬化（Q3a）**：AGENTS.md 新增 **§5.0 Regression-Test-or-It-Didn't-Happen**（硬规则）：修复必须附复现原失败的回归测试才能标 ✅；FIXPLAN 各 Phase 自此含测试条目
- [x] **10.8 验证**：`go test ./internal/handlers/ ./internal/middleware/` 全绿；补丁存档 `tools/patches/models-test-dashboard-auth.patch`；ADR-0003 登记；副本库端到端：apiKeys 清空 + 模型测试 → `400 Model required`（到达 handler）✓，对照组 `/v1/models` 仍 401（保护未削弱）✓
- [ ] **待用户验收**：Dashboard 刷新 → Providers → 逐模型测试（应不再出现 Invalid API key.；若个别模型仍报错，那是上游/凭据层的真实问题，日志可见上游响应）

## Phase 11 · 版本显示与更新误报根治 ✅ 2026-09-26

- [x] **11.1 引擎版本"未知"**：Q4(b) 去掉伪造后缺真实来源。补齐来源链：构建期 `build.sh` 写 `etc/engine-version`（上游 VERSION）→ `install-engine <file> [ver]` 运行期更新时覆写 `$DATA_DIR/engine-version` → `engine_version()` 原语按优先级读取。**当前设备已写 1.9.1**，概览/引擎更新不再显示"未知"
- [x] **11.2 引擎更新比对语义**：`engCheck` 曾拿模块版本冒充引擎当前版本（1.9.1 碰巧撞对）——改用 `state.engineVersion`（panel 真实字段）；版本未知时如实提示并让用户自行判断，不盲启更新按钮
- [x] **11.3 模块更新误报"有更新"**：`modCheck` 从 `v1.9.1-r1` 正则提取 `r1` 当 versionCode（=1）与远端 109010 比较 → 永远误报。改用 `module.prop` 真实 `versionCode` 字段（panel 新增 `versioncode=` token）→ 当前 109010 vs 远端 109010 = 已是最新 ✓
- [x] **11.4 回归验证**：30/30 离线测试 + 真机 panel 输出 `module_version=v1.9.1-r1 versioncode=109010 engine_version=1.9.1`（设备证据，AGENTS §5.0(4)）；`install-engine` 带版本参数路径真机待下次引擎更新自然验证
- [x] **11.5 架构一致**：三处症状同源——"版本信息没有唯一真源"。现在版本链为：构建期（etc/engine-version / module.prop versionCode）→ 运行期（$DATA_DIR/engine-version）→ panel 单命令 → UI，每级单一来源

## Phase 12 · 出厂 key UI 移除（用户无感化）✅ 2026-09-26

- [x] **12.1 保障链确认**：新装 → service.sh 开机自动补入（表空时）✓；导入 → 仪表盘走会话鉴权不依赖 apiKeys 表（Phase 10）✓；唯一不自动 = 用户**主动删除**不复活——安全特性（出厂 key 是公开常量，删除是合理加固，自动复活为安全退化）
- [x] **12.2 UI 移除**：index.html 出厂 key 卡片（含旧"文档式修复"提示文字）与 app.js `checkFactoryKey`/`btn-fix-key` 全部删除，零残留引用（grep 0）；`ops.sh seed-key` 子命令保留（service.sh 依赖）
- [x] **12.3 残余路径**：外部 CLI 需要默认 key 时在 Dashboard keys 页手动添加（正常用户流程）
- [x] 验证：30/30 测试 ✓、node --check ✓、已部署 ✓

## Phase 13 · 概览页"服务地址"卡片 ✅ 2026-09-26

- [x] **13.1 数据源**：`ops.sh` 新增 `lan_ips()`（全局作用域 IPv4，排除 127.*，最多 3 个，`|` 连接进 panel 单行）；引擎监听 `:PORT` 全接口已核实（`internal/app/server.go:50`，模块未设 HOST）→ 局域网地址真实可用
- [x] **13.2 UI**：概览页新增"服务地址"卡片——本机 `http://127.0.0.1:port` + 局域网 `http://<ip>:port` 每行一个；点地址复制（clipboard API + execCommand 兜底），点链接浏览器打开（首次登录用 dashboard 密码）
- [x] **13.3 验证**：真机 panel 输出 `lan_ip=192.168.10.14` ✓；30/30 测试 ✓；已部署（ops.sh/app.js/index.html 三件套）
- [ ] 待用户验收：重进面板查看卡片显示与复制/打开行为

## Phase 14 · 部署流程事故修复 + 部署脚本化 ✅ 2026-09-26

- [x] **14.1 事故**：面板"获取不到信息"复发——诊断显示 `ls /data/adb/modules/__MOD_ID__/lib/: No such file or directory`。根因是**部署失误而非代码**：手动 adb push 仓库源文件绕过了 build.sh 第 6 步的 `__MOD_ID__` 占位符注入 → CFG.MODDIR 错误 → ops.sh 路径全错
- [x] **14.2 即时修复**：带 sed 注入重新部署 index.html，panel 恢复（engine=up，版本/lan_ip 齐全）
- [x] **14.3 结构修复（Q3a 精神）**：`tools/deploy-device.sh` —— 把注入→推送→权限→自检固化为一条命令，占位符残留即失败；发布仍走 build.sh 完整打包。手工 push 的问题类别（忘步骤）从此无入口
- [x] **14.4 验证**：脚本完整跑通（2246E2F9），注入 0 残留 + panel 自检通过 ✓

## Phase 15 · v1.9.1-r2 发布 ✅ 2026-09-26

- [x] **15.1 发布前诊断扫描**：模块测试 30/30 ✓、全部 POSIX 脚本 `sh -n` ✓、JS `node --check` ✓、引擎 `go build` ✓
- [x] **15.2 差分排查 3 个失败测试**（发布闸门红 → 查明非阻塞）：`TestLiveE2E_Cline_SmartCombo`（deepseek 上游已改模型名，返回真实 request_id）、`TestIntegration_OpenCode_MuseSpark13`（同上）、`TestHandleAudioVoices_elevenlabs`（本网络 EOF 超时）——`git stash` 差分证明**改动前同样失败**，属环境依赖的 live 测试，与本次发布内容无关。建议后续为 Live 测试加 `-short`/build-tag 跳过机制（未纳入本版）
- [x] **15.3 版本 bump**：module.prop `version=v1.9.1-r2`、`versionCode=109011`；update.json 同步（zipUrl 指向 v1.9.1-r2 release 资产）
- [x] **15.4 完整构建**：build.sh 7 步全绿（web 测试 17/17、schema 漂移断言 ✓、引擎 arm64 编译含全部补丁、MODID 注入、verify_zip ✓）→ **`dist/9router-go-1.9.1-r2-magisk.zip` (15M)**
- [ ] **待用户发布**：① GitHub 创建 release `v1.9.1-r2` 并上传 zip；② 提交并推送 `update.json`（旧版设备靠它检测更新）；③ git 提交本次全部变更（等用户明确指示）

## 验收矩阵（每 Phase 完成后真机过一遍）

| 功能 | 操作 | 期望 |
|---|---|---|
| 概览 | 重进模块管理页 | 秒出，状态/内存/RSS 齐全 |
| 重启引擎+DNS | 按钮点击 | 引擎 up，DNS up |
| 端口修改 | 改端口→确认 | 引擎在新端口重启 |
| DNS 保存/优选/回滚 | 各按钮 | 配置写入 + 热重载 |
| 孤儿扫描/清理 | 按钮点击 | 扫描正常，误判护栏生效 |
| 引擎/模块更新检查 | 按钮点击 | 版本比对正确 |
| 新设备装机 | 刷 zip | 面板秒出（chmod 兜底完整） |

## Grilling 决策记录

**2026-09-25 · 第一轮**（用户答复：基本都按推荐）
- Q1 拉起策略 → **(a)** `restart-engine` 复用 service.sh，不造第二份启动实现
- Q2 等待语义 → **(a)** restart-engine 内置 20s 轮询，调用即知 `engine=up/down`，WebUI 与 action.sh 共用
- Q3 初始密码 → **(b)** 保持 123456 不随机生成，MAGISK.md 已如实更新（用户可自行在 Dashboard 改密码）
- Q4 engine_version → **(b)** 停止伪造，拿不到显示"未知"
- Q5 Phase 2 迁移 → **(a)** 渐进：先加命名操作，按类迁移，每类真机验收后再删旧路径

**计划外发现（Phase 1 验收中）**：mksh 参数展开模式 `|` 为"或"运算符的跨 shell 差异 bug，已修复并沉淀为教训（见 1.3）
