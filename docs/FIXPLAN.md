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

## Phase 16 · 生命周期根治：cgroup 脱组 + 守护 + 三处"谎报成功" ✅ 2026-09-26

> 用户症状：「跑一段时间后引擎未运行、Dashboard 打不开，手动重启才恢复」（两台设备同样反馈）。
> 用户追问：「是系统杀后台，还是模块内部 BUG？会不会占用高时自己把自己杀掉？」

- [x] **16.1 根因（真机取证，非推断）**：
  - 引擎与 `dnsfwd` **同时静默消失**（无退出行、无 panic、无 OOM、设备未重启）；
  - `dumpsys activity exit-info me.weishu.kernelsu`：`11:35:23 reason=10 USER REQUESTED / LockScreenClean`（同刻另有 2 个 App 被清）；
  - `ksu.exec`（模块 WebUI）派生的 `sh` 是**管理器应用的子进程**，cgroup `0::/uid_10235/pid_X`（实测）；`setsid` 只换会话**不换 cgroup**（实测：把 setsid 进程放进应用 cgroup → `am force-stop` 后同死）；
  - 旧模块**无守护** → 一死即永久停机。**排除**"内存超限自杀"：全仓非测试代码只有启动期 `log.Fatal/os.Exit` 与自更新 `RestartSelf`（先拉起新进程再退旧），面板 200/300MB 只是 UI 上色阈值。
- [x] **16.2 修复 A（脱组）**：启动收敛到 `ops.sh start-engine`（唯一实现），起来后 `cgroup_escape`（root 写 cgroup 根 `cgroup.procs`）；`start-dns` 同办。失败不阻塞启动（守护兜底）但如实写日志。
- [x] **16.3 修复 B（守护）**：新增 `module/lib/watchdog.sh` —— 只判"pid 在不在"（不做健康度判据）、连续两次判死才动手、`watchdog-hold` 维护窗口让路、`watchdog.req` 请求优先于 hold、只由 `service.sh` 在开机路径武装（`watchdog-armed` 是闸）；`restart-engine` 有守护时**委托守护**执行停止+启动（ADR-0004）。
- [x] **16.4 可观测**：`ops.sh status/panel` 增 `watchdog=up|down|stale` + `watchdog_pid`；概览页新增「生命周期守护」行。
- [x] **16.5 回归测试（AGENTS §5.0）**：`tools/device/test-lifecycle.sh` —— T1 守护在场 / T2 `kill -9` 后 40s 内自愈（新 PID + `/health` 200）/ T3 在管理器应用 cgroup 里启动仍脱组。**修复前 T2、T3 必红**；真机 **3/3 绿**（T2 新 pid cgroup=/ health=200；T3 脱组 cgroup=/）。
- [x] **16.6 计划外真 bug（数据安全级）：`sqlSnapshot` 假成功**
  - 现场证据：`backups/kv-before-orphan-clean-2026-09-26T03-16-05.sql` 是 **0 字节**，而界面报"删除前快照已存"；
  - 复现（真机）：`sqlite3 db ".mode insert kv" "<坏SQL>" > out` → **rc=1 size=0**（`>` 已建出空文件）；旧实现 `sqlSnapshot()` 只判 `!r.err`（exec 层错误）→ 成功；
  - 修复：命令自带 `[ -s ]` 判据 + `snap-ok/snap-fail` 哨兵 + 失败删空文件；JS 只认 `snap-ok`；
  - 回归：`test/bridge-commands.test.js` 两条（差分验证：修复前 3 红 → 修复后 33/33 绿）。
- [x] **16.7 计划外真 bug：守护把"让路"当"死亡"空转**（本次新增代码自身缺陷）
  - `:53` 被第三方占用（yielded）时，守护每 ~16s 判死一次 → `start-dns` 回 yielded → 往 `dnsfwd.log` 追加一行，永不停止；
  - 修复：`port53-busy=1` 视为健康；隔离差分夹具（独立 DATA_DIR + 伪造引擎 pidfile）实测 **修复前 22s 内判死 5 次 / 修复后 0 次**。
- [x] **16.8 计划外真 bug（4 处，均带证据）**：
  - `pgrep -f` 裸子串匹配：引擎按整条命令行锚定（`^...$`）；dnsfwd 只匹配守护形态（`-b `）—— 否则会误杀面板正在跑的 `dnsfwd -P` 探测进程；
  - pidfile 杀进程无身份校验（PID 复用会误杀无关进程）→ 新增 `pid_is_exe`（`readlink /proc/pid/exe`，容忍 ` (deleted)`）；
  - `uninstall.sh` 硬编码模块路径 + 只按 pidfile 停进程（卸载后可能留占 `:53` 的孤儿）→ 从 `$0` 推导 + 路径兜底；
  - `cmd_prep_db` 无条件 `echo ok`（schema/autoUpdate 压回失败都谎报成功）→ 改 `ok|degraded` 并写日志；`restart-engine` 与守护 pidfile 落盘的竞态（会退回调用者 cgroup 启动）→ `watchdog-start` 等 pidfile 就位。
- [x] **16.9 发布物料**：`module.prop` → `v1.9.1-r3` / `109012`；`update.json` 同步；`build.sh` verify_zip 增 `lib/watchdog.sh` 断言；`tools/deploy-device.sh` 推送 lib/{ops,watchdog}.sh + service.sh；ADR-0004 + MAGISK.md 生命周期章节。
- [ ] **待用户发布**：① GitHub release `v1.9.1-r3` + 上传 zip；② 推送 `update.json`；③ git 提交（等用户明确指示）
- [ ] **待真机验收**：面板概览出现「生命周期守护 运行中」；升级 r3 后长时间运行不再变"未运行"

## Phase 17 · C1 生命周期收成一个 module（架构体检 C1）✅ 2026-09-26

> 体检结论：守护引入了 4 个状态文件 + 优先级规则，由 3 个 module 各自读写，**没有所有者**；
> 已经因此出过两次真 bug（撤销用户的"关守护"意图、`watchdog-start` 与 pidfile 落盘的竞态）。

- [x] **17.1 新增 `module/lib/lifecycle.sh`**（可 source、零副作用）：状态文件
  （`watchdog-armed/-hold/-req/-off`、`service-off`、三个 pidfile）全部成为它的实现细节；
  接口按意图设计（`life_boot` / `life_stop_user` / `life_start_user` / `life_restart_engine` /
  `life_ensure_engine|dns` / `life_wd_should_supervise` / `life_state`）。ADR-0005 登记。
- [x] **17.2 `ops.sh` 退为配置与编排**：数据（schema/端口/密码/出厂 key）+ 状态聚合 +
  安装编排；**对外子命令集合不变**（action.sh / WebUI / 门禁零改动），新增 `stop-user`/`start-user`。
- [x] **17.3 `watchdog.sh` 只做轮询/去抖/记日志**：三处内联状态判断 → `life_wd_should_supervise`
  + 两个健康谓词；`dnsfwd` 的"让路 = 正常稳态"语义只在一个谓词里。顺带修掉"固定 3s 判拉起失败"
  的误报（改轮询到 10s，真机见过日志说失败但 2s 后 /health 200）。
- [x] **17.4 用户意图可见可操作**：WebUI 概览新增「启动服务 / 停止服务」；`engine=stopped`
  与 `engine=down` 分开显示（前者是用户要的，后者才要查）。
- [x] **17.5 回归（AGENTS §5.0）**：`tools/device/test-lifecycle.sh` 扩到 **T4**（停服后 30s 内
  不得被复活 + 显式启动恢复）与 **T5**（维护窗口内不插手 + 到期后必须自愈）。真机 **10/10 绿**。
  红侧基线：修复前 `kill -9` 引擎后 10s 内必被拉起（即 T4 的"意图不被尊重"行为）。
- [x] **17.6 结构副作用**：`uninstall.sh` 正常走 `life_shutdown`，仅保留被明确标注的
  "library 不可读时"兜底（卸载是最后一道保险）；`CONTEXT.md` 新增「生命周期 / 意图」术语；
  `service.sh` 成为纯开机时序（每个动作的语义都在 lifecycle）。
- [x] **17.7 语法与部署链**：全部脚本 `sh -n` 通过；`deploy-device.sh` 改推 `lib/*.sh`；
  `build.sh` verify_zip 增 `lib/{lifecycle,log}.sh` 断言。

## Phase 18 · C2 键契约门禁（架构体检 C2）✅ 2026-09-26

- [x] **18.1 病根**：`ops.sh status/panel` 的接口是"一行 20+ 个 key=value"，键名契约四处手抄
  （shell emit / JS parse / JS consume / 测试 fixture），漂移是**静默**的（`st.watchdog_state`
  这类错拼只得到 undefined，界面安静地空着）。
- [x] **18.2 门禁（源码即契约）**：新增 `module/webroot/test/contract-keys.test.js` ——
  从 shell 的 `cmd_status`/`cmd_panel`/`life_state` 的 **echo 模板段**抽 emit 键，
  从 `app.js` 抽消费键（`st.<key>`），断言：① 消费 ⊆ emit；② emit 的键要么被消费、
  要么在 `EMIT_ONLY` 里逐条写明理由（当前 3 条：`factory_key`/`apikeys_total`/`bind`）。
- [x] **18.3 差分验证**：把 `app.js` 的 `st.watchdog` 改成 `st.watchdog_state` →
  门禁红（"app.js 读了 shell 没输出的键：watchdog_state"）→ 恢复即绿。
- [x] **18.4 接入构建闸门**：`build.sh` 第 3 步从"只跑 parsers"改为 `node --test module/webroot/test/*.test.js`
  （三套：解析层 / 命令构造器 / 键契约），离线 **37/37**。

## Phase 19 · C3+C4 承载性 env 与日志各自收成单一来源 ✅ 2026-09-26

- [x] **19.1 C3 承载性 env**：`$DATA_DIR/runtime.env`（0600，构建器 `life_write_runtime_env`，
  清单 `life_carrier_env_keys` = 6 键）为唯一来源；`life_ensure_engine` 改成
  `set -a; . runtime.env` 加载，生成失败才退回内联并**写日志**。ADR-0006 登记。
- [x] **19.2 C4 日志策略**：新增 `lib/log.sh`（路径 + 上限 + 轮转实现唯一一份）；
  写者只有两种用法（`log_write` / `LOG_*_PATH` 流重定向）；守护每 ~60s `log_rotate_all`。
  之前 `9router.log`（每请求一行）与 `dnsfwd.log` **无任何轮转**。
- [x] **19.3 回归**：门禁新增 **T6**（隔离数据目录实跑轮转：440000B → 4400B）与
  **T7**（承载性 env 键在 `runtime.env` **且真的进了引擎进程** `/proc/<pid>/environ`）。
  真机 **T1–T7 全绿（10 条断言）**。
- [x] **19.4 术语**：`CONTEXT.md` 新增「承载性 env」。

## Phase 20 · Dashboard「Download Backup」401 Invalid password 根治（用户报障）✅ 2026-09-26

> 用户报障：Settings → Download Backup 报 `Invalid password`，但"明明设了密码、也能正常登录"。

- [x] **20.1 定性（不是模块 bug，也不是上游 bug —— 是本 fork 的 Svelte 仪表盘移植缺失）**
  - 上游 spec：`GET /api/settings/database` 要求 `x-9r-password` 头
    （`9router/src/app/api/settings/database/route.js:16`），官方 UI 是**先弹层输密码**再带头发请求
    （`profile/page.js:663-668`，弹层 `:1673-1699`），文件名 `.json`（`:687`）；
  - fork 的 Go handler **与上游 parity**（`internal/handlers/dashboard/settings.go:119`）→ 没有该头即
    `401 {"error":"Invalid password"}`（用户看到的就是这一句；登录态救不了，那是 handler 的独立判据）；
  - fork 的 Svelte 仪表盘用裸 `<a href="/api/settings/database">` 下载（`ProfileSettingsView.svelte:199-211`）
    —— 既不带密码头、也没有输密码的弹层；
  - **红基线**：修复前 `web/dist/assets/` 里 `x-9r-password` 命中 **0**。
- [x] **20.2 修在正确的一层（前端；不动 Go handler → 不需要 ADR-0003 补丁、不欠上游 PR）**
  新增 `web/src/lib/db-backup.ts`（请求形状/文件名/错误文案的纯函数唯一实现）+
  `ProfileSettingsView.svelte` 恢复上游的密码弹层（`Modal` + `Input`，Svelte 5 runes）；
  顺带修掉两处同源偏差：导出文件名 `.sqlite` → `.json`（内容本来就是 JSON）、
  导入改回上游形状（JSON + password，Go 侧 multipart 分支保留为兼容超集）。
- [x] **20.3 回归（AGENTS §5.0）**
  - 前端 `web/src/lib/db-backup.test.ts`（bun:test）4 条：导出必须带密码头 / 导入是 JSON+password /
    文件名 `.json` / 服务端 `{error}` 文案要透出给用户 —— 修复前必红；
  - Go `settings_test.go` 新增 `TestHandleExportDatabase_AcceptsPasswordHeader`
    （错密码 401 / 对密码 200 且导出体非空）✓；
  - 构建期闸门 `build.sh`：`web/dist/assets` 必须含 `x-9r-password`（修复前红，修后绿）。
- [x] **20.4 真机交付**：本地 arm64 交叉编译（go1.27）→ 二进制内嵌前端含密码头（`strings` 命中 2）→
  用模块自己的 `install-engine` 入口装入真机（幂等 + 自动备份 + 重启，`engine=up`）。
- [x] **20.5 计划外真 bug（同轮暴露）：stop→start 竞态**
  - 现象：真机 15:34:44 守护日志写"拉起失败"，下一轮又成功 → `life_stop_all` 原先只 `sleep 1`，
    而引擎收到 SIGTERM 要 drain SSE / 关监听才退，1s 不够 → 新实例 bind 失败；
  - 修：`life_pid_gone`（含僵尸态判定，`kill -0` 对僵尸仍为真会拖满等待）+ `life_stop_all` 轮询等待
    （最多 10s）；守护的 `restart`/`start` 请求补上 `life_ensure_dns`（`stop_all` 把 DNS 也停了，
    同一处必须一起拉回来，别再靠监督分支 10s 后补救）；
  - 验证：连做 3 次 `restart-engine` 全部 `engine=up`，`watchdog.log` 无"拉起失败"。
- [ ] **待用户验收**：Dashboard 硬刷新（新资源带 hash，普通刷新即可）→ Settings → Download Backup →
  弹层输当前密码 → 应下载 `9router-backup-<时间戳>.json`（修复前是 401）

## Phase 21 · 端点 parity 巡检（棘轮）—— 让"移植缺失"不可能悄悄复发 ✅ 2026-09-26

> 起因：用户追问"是不是拉取原仓库出的问题？要不要重新 fork？" —— 结论：不是。
> 本仓与上游（Next.js/React）是**重写移植**关系，没有可 merge 的血缘，重新 clone 修不了任何东西；
> 端点是手写重写的，漏一个既不会编译报错也不会运行报错（Phase 20 的 401 就是这一类）。
> 治法只有一条：把"上游有哪些端点"变成可执行断言。

- [x] **21.1 新增 `tools/check-parity.py`（棘轮 / ratchet）**
  - 上游侧清单：`<上游树>/src/app/api/**/route.js` → 路径 `/api/<目录段>`，方法取
    `export async function GET` 与 `export const GET` 两种写法；
  - 本仓侧清单：`internal/handlers/**/*.go` 的字面量注册，按 `.Route("/前缀", …)` 嵌套推导前缀
    （实测 `internal/handlers/dashboard/routes.go` 用的就是 `r.Route("/api", …)`）；
  - 动态段两侧统一归一（`[id]` / `{id}` / `{id:正则}` → `{}`；`[...slug]` / chi `*` → `{**}`），
    避免参数名不同造成假差异；统计括号深度前先抹掉字符串字面量，避免 `{id}` 干扰前缀栈配对；
  - 棘轮语义：`tools/parity-baseline.txt` 冻结存量缺口（**只拦新增**），
    `tools/parity-ignore.txt` 放**有理由**的豁免（等价实现/产品决策，理由必填、工具校验格式）；
  - 红灯自证：构造假上游 `/api/zz-probe` → `❌ 新增缺口 1 条` + 退出码 1 ✓。
- [x] **21.2 冻结基线**：上游参照 `decolua/9router v0.5.81 (a8c9d380)`；上游 224 条端点 /
  本仓 242 条注册（44 个 Go 文件）/ **已知缺口 141 条**（130 端点 + 11 方法）已入基线。
  聚类：`/api/cli-tools/*` 49、`/api/v1/*` 19、`/api/oauth/*` 10、`/api/pxpipe/*` 9、
  `/api/translator/*` 7，其余零散 —— 详见 `COMPARISON.md §0`。
- [x] **21.3 首轮就抓到两个实证缺口（不是误判）**
  - `GET /api/health` **缺失**：本仓 Dashboard 自己在调它
    （`web/src/components/EndpointView.svelte:212-217` 探测 `tunnelUrl` / `publicUrl` / `tailscaleUrl`），
    而本仓只注册了 `/health` 且**不带** `Access-Control-Allow-Origin: *`
    （`internal/handlers/router.go:300`）→ tunnel / 公共地址可达性探测**永远失败**；
  - `GET /api/auth/oidc/callback` + `POST /api/auth/saml/acs` **未注册**：
    `internal/handlers/sso/sso.go:28-29` 定义并用于拼 `redirectURI` / `acsUrl`，但 router.go 只注册了
    `oidc/test`、`saml/test`、`saml/metadata` → IdP 回调打到 404，SSO 登录走不完。
- [ ] **待决策（用户）**：① `/api/health` 补端点（含 CORS 头）还是把前端改成探 `/health`；
  ② SSO 回调补注册，还是明确"模块不提供 SSO 登录"（配置界面在、回调不通，属半接线）；
  ③ 大块缺口（cli-tools 49 / pxpipe 9 / translator 7）是否排期。
- [ ] **巡检入口**：`python3 tools/check-parity.py`（需 `../9router` 参照树；**不进 build.sh** ——
  参照树不总是存在，不能让它变成构建的硬依赖）

## Phase 22 · 更新引擎把 404 正文装成了引擎（用户报障，P0）✅ 2026-09-26

> 用户报障：「我在手机端更新引擎最后是安装失败，没有实现真的更新引擎」。
> 取证结论比报障更严重：**引擎与面板当时是死的**（不是"还在跑旧版、只是没换成功"）。

- [x] **22.1 现场取证（真机）**
  - `bin/9router-go` = **9 字节**，内容 `Not Found`（HTTP 404 正文）；`bin/9router-go.bak`
    **也是 9 字节** → 第二次尝试把已损坏的当前文件备份了，**设备上再无可用引擎二进制**；
  - `engine-version` = `1.9.2`（**谎报**：版本号在替换后立即写，从不校验引擎是否真的起来）；
  - `ps` 无引擎进程、`curl :20128/version` 空响应；`watchdog.log` 从 16:02:38 起每 21s
    「引擎不在 → 拉起失败」死循环（守护在正确工作，但二进制是垃圾，永远拉不起来）；
  - 加速节点 = `https://github-proxy.memory-echoes.cn/`；GitHub API 确认 v1.9.2 **资产名没变**
    （`9router-go-linux-arm64`，25,559,200 B）→ 404 来自加速节点，不是 URL 拼错。
- [x] **22.2 根因链：4 层都缺校验（任一层拦住都不会出事）**
  1. `module/webroot/bridge.js` 的 `download` 用 `curl -sL`，**没有 `-f`** → 404 退出码仍为 0，
     正文被写出且 `echo dl-ok` → 前端判成"下载成功"；
  2. `module/webroot/app.js` 取不到 `SHA256SUMS.txt` 时**跳过校验继续安装**（fail-open）；
  3. `module/lib/ops.sh cmd_install_engine` 不校验源文件（体积 / ELF 魔数）就 `mv` + `chmod`；
  4. 备份语义是"替换前 cp 当前文件" → 当前文件一旦损坏，备份即被污染。
- [x] **22.3 修复（4 层 + 备份语义）**
  - `bridge.js`：`download` → `curl -fsSL`（HTTP ≥400 即失败）；新增只读探针 `fileSize` / `elfMagic`；
  - `parsers.js`：新增装前门禁纯函数 `engineFileGate(size, magic)`（≥5MB 且 `7f454c46`）与
    `checksumGate(expected, actual)`（**取不到校验和即拒绝**）；
  - `app.js`：下载后先过 `engineFileGate`、再过 `checksumGate`，任一不过立即中止且不碰设备；
  - `ops.sh cmd_install_engine`：先 `engine_src_ok` 门禁（不合格 → `install-rejected-src`，
    **不碰**现有二进制/不停服务/不写版本号）；回滚点只在当前二进制合格时留；**起来后才**写
    `engine-version`；起不来自动回滚并报 `install-failed-rolled-back`；`.bak` 语义改为
    "最后一次已验证可用"（ADR-0007）。
- [x] **22.4 回归（AGENTS §5.0）**
  - 离线 `node --test module/webroot/test/*.test.js` → **46/46**（新增 9 条：404 正文 / HTML 错误页 /
    探针读不到必须判死；校验和缺失必须拒绝；`download` 必须带 `-f`；无 `dl-ok` 必须返回 false）；
  - 真机 `tools/device/test-lifecycle.sh` → **14/14**，其中 T8 直接用 9 字节 `Not Found` 断言：
    T8a 被拒（`install-rejected-src`）/ T8b 现有引擎字节数不变（25,428,128）/
    T8c `engine-version` 不谎报（1.9.1）/ T8d 引擎仍 `up`（门禁没惊动服务）。
- [x] **22.5 现场恢复（止血）**：从本地 `dist/9router-go-1.9.1-r2-magisk.zip` 取出合法 arm64 引擎
  （25,428,128 B，`file` 确认 `ELF 64-bit ... ARM aarch64`，与 zip 内 sha256 一致）→
  `ops.sh install-engine ... 1.9.1` → `engine=up` / `dns=up` / `watchdog=up`、
  `engine-version=1.9.1`（谎报清除）、`/version` 已能应答（返回 401 说明路由在工作）。
- [ ] **待用户验收**：面板「下载并更新引擎」重跑一次应能真正更新；把加速节点换成坏节点时应看到
  「拒绝安装」而不是「装完引擎消失」。

## Phase 23 · 升级上游 v1.9.2：撤补丁 + 修整包更新的版本谎报 + 功能门禁 ✅ 2026-09-26

> 起因：用户报「上游出了 v1.9.2，看是否修了我们的 bug、旧补丁能否撤」，并追加报障
> 「我在手机端更新引擎最后是安装失败，没有实现真的更新引擎」。

- [x] **23.1 上游对照（依据本地 `git diff v1.9.1..v1.9.2`，5 个提交，不只看 release notes）**

  | 我们携带的 | 上游 v1.9.2 | 处置 |
  |---|---|---|
  | `models-test-dashboard-auth`（`/api/models/test` 401） | **已吸收**（`1865f78` 移入 dashboard 鉴权组，且自带 `TestSetupServerRouter_ModelTestSessionAuth`） | 撤补丁（`tools/patches/` 删除，ADR-0003 登记"已由上游吸收"） |
  | `dashboard-import-multipart` | 未做 | 撤（Phase 20 已把前端改回上游 JSON+password 形状，该分支无调用方） |
  | `codebuddy-cn-agent-prompt-sanitizer` | 无对应实现 | 保留（自有功能，上游 PR 义务仍在） |

  白拿的修复：`/version` 系列公开（不再 401 刷屏）、`/sw.js` 与 `/manifest.*` 404、缺失图标、
  前端 `onUnauthorized` 清会话跳登录、`GET /api/keys` 对 dashboard 会话返回完整密钥、媒体多账号轮换。
  我方提的三点上游**都没修**：`/api/health`（上游 Go 仓自己也没注册）、SSO `oidc/callback`+`saml/acs`
  （同样只算地址没注册）、大块缺口（cli-tools 49 / pxpipe 9 / translator 7）。
- [x] **23.2 合并与冲突解决**：三处冲突 —— `internal/handlers/router.go`（取上游，含 models/test 新挂载点）、
  `ProfileSettingsView.svelte`（保留我们的密码弹层与请求形状，其余取上游措辞/`accept=".json"`）、
  `COMPARISON.md`（上游 statuses 图例 + 我们的 §0 parity 节并存）；`settings.go`/`settings_test.go`
  取上游后**回插** `TestHandleExportDatabase_AcceptsPasswordHeader`（锁 `x-9r-password` 契约）。
- [x] **23.3 schema 门禁随上游收敛**：v1.9.2 的 `DATABASE.md` 删掉了索引与部分列（改为指向
  Next.js 的 `src/lib/db/schema.js`），但 `lastUsedAt`/`consecutiveUseCount` 仍被引擎代码真实使用
  （`internal/db/usage.go:67`、`internal/handlers/dashboard/connections.go:257`）。`tools/gen-schema.py`
  改为"**表与列**必须对齐"，并显式登记 `SUPERSET_COLUMNS`（每条必须写代码依据）；索引不再比对，
  但 `--diff` 仍逐条列出供人工核对。
- [x] **23.4 计划外真 bug：整包更新后引擎版本谎报**
  - 现场：`install-module`（KernelSU 的常规升级路径）装了 v1.9.2 引擎，但 `$DATA_DIR/engine-version`
    停在 1.9.1 → 面板显示"当前 1.9.1"并**永远提示有更新**（引擎自报其实是 1.9.2）；
  - 修：`cmd_install_module` 成功后把包内 `$MODDIR/etc/engine-version`（构建期写入，描述刚装进来的
    二进制）同步到运行期文件；包里没有该文件时删除运行期文件（宁可显示"未知"，也不谎报）。
  - 门禁：`tools/device/test-lifecycle.sh` **T9** —— 面板 `engine_version` 必须等于引擎自报
    `/version.currentVersion`（版本一致性不再靠人看）。
- [x] **23.5 撤补丁的功能验证（用户要求"注重检测最后功能可行"）**
  - 上游自带 `TestSetupServerRouter_ModelTestSessionAuth` 通过 ✓（会话 cookie 越过鉴权、错误体不再
    是 apiKeys 语义）——即我们那条补丁的回归已由上游测试覆盖；
  - 离线：`node --test module/webroot/test/*.test.js` **46/46**；`bun test src/lib/db-backup.test.ts` **4/4**；
    `tsc -b && vite build` ✓；`go build ./...` ✓；`go test ./internal/handlers/... ./internal/middleware/...` ✓
    （唯一失败 `TestHandleAudioVoices_elevenlabs` 是沙箱访问 `api.elevenlabs.io` EOF，与本次无关）；
  - 新增 `tools/device/test-dashboard-api.sh`（真机**功能**门禁）：A4 `/version` 公开可读 ✓、
    A3b/A3c 错误/无密码 → 401 ✓；A1/A3a 需要**当前** dashboard 密码（`initial-password` 已不是用户
    密码，第三个参数可传入）。
- [x] **23.6 发布物料**：引擎升级到 v1.9.2 → 按 `build.sh` 的命名规则"大版本更新归 r1" → `v1.9.2-r1` /
  versionCode 109020；`build.sh` 七步全绿，产物 `dist/9router-go-1.9.2-r1-magisk.zip`（15M，verify_zip ✅）；
  真机用模块自己的 `install-module` 装入 → `engine=up`、`engine_version=1.9.2`、`dns=up`、`watchdog=up`，
  设备门禁 **15/15**（T8b 引擎 25,559,200 字节 = v1.9.2 `linux-arm64` 资产大小）。
- [x] **23.7 计划外真 bug：整包安装会覆写正在执行的自己**
  - 现场：`install-module` 执行中报 `ops.sh[193]: syntax error: unexpected ';'`（行号落在 `case`
    块内），安装中途夭折；但同一文件 `sh -n` 通过、两侧都是 200 行 → 不是文件坏了，而是
    `unzip -oq` **原地覆写**了正在被 shell 逐行读取的 `lib/ops.sh`（同 inode + 截断重写）；
  - 修：`cmd_install_module` 改为"解压到 `$DATA_DIR/module-stage` → 用 `mv` 换 inode 落位"
    （换 inode 后执行中的实例读的仍是旧文件；目录必须先搬成 `.new` 再让旧目录让位，
    否则 `mv` 会把新目录塞进同名旧目录里）；
  - 门禁：设备门禁 **T10** —— 用 `last-module.zip` 的副本跑一次 `install-module`，断言输出无
    `syntax error` 且装完 `engine=up`（真机 **17/17** 全绿）。
- [ ] **待用户发布**：tag `v1.9.2-r1` → 上传 zip → 推送 `update.json`
- [ ] **待决策**：`/api/health` 与 SSO 回调是否补（上游同样缺失，可顺手提 PR）；大块缺口是否排期

## Phase 24 · 补 `/api/health` 与 SSO 回调诚实化（端点巡检提出的两个实证缺口）✅ 2026-09-26

> 来源：Phase 21 的端点 parity 巡检首轮抓到的两个缺口；v1.9.2 已确认**上游也没修**
> （上游 Go 仓自己也没注册 `/api/health`；SSO 只实现了"配置测试"，回调从未实现）。

- [x] **24.1 `/api/health`（含 CORS）**：内嵌 Dashboard 的浏览器侧可达性探测
  （`web/src/components/EndpointView.svelte` 的 `clientPingUrl`，探测 tunnel/公网/Tailscale 地址）
  打的一直是 `/api/health`，而本仓只有 `/health` 且**没有** `Access-Control-Allow-Origin: *`
  → 跨域探测必然失败（用户看到"不可达"）。现在两者共用 `healthHandler`，都带 CORS 头。
- [x] **24.2 SSO 回调诚实化**：`oidcCallback` / `samlACSPath` 只被用来拼给 IdP 的地址，
  **回调本身从未实现**（上游同样）→ 注册 `GET /api/auth/oidc/callback`、
  `POST /api/auth/saml/acs` 并明确回 **501** + `sso_login_not_implemented`，
  避免 IdP 跳回来打到 404、被误判成"配置写错了"。
- [x] **24.3 回归**：`internal/handlers/router_test.go` 新增
  `TestSetupServerRouter_HealthAndSsoStubs`（`/health` 与 `/api/health` 都 200 且带 CORS；
  两个 SSO 回调都 501）；`go build ./...` ✓、`go test ./internal/handlers/` ✓。
- [x] **24.4 棘轮收紧**：巡检确认**无新增缺口**，且基线内 6 条已被覆盖（`/api/health`、两个 SSO 回调，
  另 3 条 `/api/translator/console-logs*` 由上游合并带来）→ `--write-baseline` 收紧到 **135 条**。
- [ ] **待决策**：真正的 SSO 登录回调（authorization code 交换 + id_token/断言签名校验 + 会话签发）
  是否要做；若不打算支持登录，建议把设置页的 OIDC/SAML 入口标注为"仅测试连接"。

## Phase 25 · 收尾：清理命令、免密码功能门禁、缺口影响分析 ✅ 2026-09-26

- [x] **25.1 新增 `ops.sh cleanup [--dry-run]`**（用户要求"更新完把旧数据与二进制清掉"）：
  清安装残留（`*.old`/`*.new`/`module-stage`/`engine.prev`）、旧安装包与临时下载
  （`/data/local/tmp/9r-*`、`9router-go-*.zip`）、轮转日志 `.1`、备份快照只留最近 5 份；
  **保留**当前二进制、`.bak` 恢复点、`last-module.zip`（整包回滚 + T10 用）、DB、凭据、配置。
  首次执行：设备释放 ~28MB（两个旧包），本地另清 29MB（两个旧 dist zip）。
- [x] **25.2 修两处本轮自造的副作用**
  - 目录整体换 inode 会连带抹掉"不在包内"的 `bin/9router-go.bak` → 更新前先把它搬进暂存、
    更新成功后再按"已验证可用"刷一次（与 install-engine 同语义）；真机已重建（25,559,200B）；
  - 功能门禁的 CLI token 触发点原用 `/api/auth/status`（现为公开路由，中间件不跑、文件不生成）
    → 改用必过 `RequireDashboardAuth` 的 `/api/settings/database`。
- [x] **25.3 功能门禁改为免密码**：`tools/device/test-dashboard-api.sh` 改用本机 CLI token
  （machine-id + `9r-cli-auth` + cli-secret 推导）而非用户登录密码 —— 用户改密码也不会让门禁失效。
  真机 **7/7**：A1 `/version` 公开 ✓ ｜ A2 CLI token 调 `/api/models/test` → 400（越过鉴权、
  不依赖 apiKeys 表）✓ ｜ A3a CLI token 导出 → 200 ✓ ｜ A3b·A3c 错误与无密码 401 ✓ ｜
  A4 对照组 `/v1/models` 401 ✓ ｜ **A5 导出载荷含密码/登录状态字段**（即"导入后按导入数据的密码登录"）。
- [x] **25.4 缺口影响分析（回答"这些缺口不修会怎样"）**：把 UI（`web/src` + `module/webroot`）
  实际调用的 81 条路径与 135 条基线缺口求交，得到 6 条"路径看起来会碰到"的项，逐条核对：
  - `/api/auth/oidc/start`、`/api/auth/saml/start` —— **真的会 404**，但根因是"SSO 登录整体未实现"
    （上游两端都没实现），不是端点漏移植；
  - `/api/models/custom` DELETE、`/api/models/disabled` POST/DELETE、`/api/oauth/cursor/import` GET、
    `/api/providers` POST —— UI 实际调用的是等价路径（`/custom/{key}` DELETE、`/disabled/{provider}` PUT、
    cursor `POST`、新建走 `/api/connections`），**碰不到**。
  → 结论：135 条缺口里用户可感知的只有 SSO 这一条线；其余是上游 Next 版的别名/扩展形状。
- [x] **25.5 决策**：**SSO 登录不做**（模块面向个人 Android 设备，密码 + API key 已够，上游亦未实现）；
  缺口的推进责任在**上游 Go 移植**（luqman-v1/9router-go），模块侧只保证"UI 不指向缺失端点"
  + "不假装有" + 棘轮巡检（Phase 21）在新缺口出现时报警。
- [x] **25.6 发布节奏**：用户明确"不频繁发版，本地测完确认再发" → 本轮不推任何 tag/远端，
  `v1.9.2-r1` 只作为本地工作版本号（引擎/模块均已升到 1.9.2 基线）。

## Phase 26 · 整包更新后的「假更新」：运行期版本文件读取时自愈（用户报障）✅ 2026-09-26

> 用户报障：「在有线 adb 设备（Nova3Color）上试模块更新，更新并没有成功，而界面显示版本号又是最新的；
> 概览页还是老版本 —— 所以这个更新现在是假更新」。
> 取证结论：**更新是真的，展示是假的**。设备上 `module.prop=v1.9.2-r2 (109021)`、`bin/9router-go` =
> 25,559,200 B（正是 v1.9.2 那份）、引擎 `/version` 自报 `currentVersion=1.9.2`；但
> `$DATA_DIR/engine-version` = **1.9.1**，而 `engine_version()` 优先读它 → 概览显示 1.9.1。

- [x] **26.1 根因：整包更新不跑我们的代码（两条安装路径都躲不掉）**
  - WebUI 的「下载并覆盖安装」是由**当时装在设备上的那份旧 ops.sh** 执行的 —— 修好的安装逻辑
    要等下一次 install 才生效（自更新收敛问题）；
  - 管理器在线更新（updateJson）更是一个字节的模块代码都不跑 → 运行期文件永远不会被同步。
  - Phase 23 已加过"安装后同步运行期版本"（`cp etc/engine-version → DATA/engine-version`），但它只对
    "由含该修复的代码执行的安装"有效 —— 本次更新正是由旧代码执行的，所以没生效（mtime 实测：
    包 20:07 装上，运行期文件还停在当天 **00:24**）。
- [x] **26.2 修复：读取时自愈**（`ops.sh engine_version_sync()`，在 `cmd_status` 入口调用 ——
  status 是面板与 `action.sh` 的唯一数据源，读一次收敛一次）
  - 主判据 = `$DATA_DIR/engine-version-code`：记录"这份引擎版本是在哪个 module versionCode 下写的"；
    记录缺失或与当前 versionCode 不同 → 包被换过（换包必然换 `bin/`）→ 以包内 `etc/engine-version` 为准；
    一致 → 是本模块版本下 `install-engine` 装的 → **不动它**；
  - 补充判据 = 包比运行期文件新（覆盖"同 versionCode 重装"场景）；
  - `install-engine` 与 `install-module` 都写该记录，两条更新路径语义一致。
- [x] **26.3 回归（AGENTS §5.0）**
  - 真机 `T12a/a2/b/c`：整包更新后自愈（0.0.1 → 1.9.2）/ 同版本重装也自愈（mtime 路径）/
    运行期更新不被包内旧值覆盖 / 运行期文件缺失从包内补齐；
  - **T12a 第一次跑就抓到我实现里的真缺陷**：mksh 的 `-nt` 只到秒精度，同一秒内"先写文件再 touch"
    判不出来 → 改成不依赖 mtime 精度的记录判据（mtime 降为补充，T12a2 专门盯它）；
  - 受影响设备实测：部署修复后 `engine_version` **自动**从 1.9.1 回到 1.9.2（无需手工改文件），
    该设备全量门禁 **25/25**（另一台 MI 6X 为 22/22 + 本次新增 T12 亦绿）。
- [x] **26.4 来源自检（用户要求：让"谎报"一眼可见）**：概览页「引擎版本」旁显示来源 ——
  `运行期记录`（常态）/ `包内 · 刚自愈`（本次读取刚收敛）/ `无来源`；由 `ops.sh` emit
  `engine_ver_src` + `engine_ver_healed`、`parsers.js engineVersionSourceLabel` 出文案（可离线测）。
  真机 T12 扩到 **a–e**（含"自检字段必须如实"与"稳态不重复自愈"），另含一条编排教训：
  自检字段必须**一次 panel 快照取多个**，分多次取会读到第二次调用里的 `healed=0`。
- [ ] **待用户验收**：在有线设备上再点一次「检查更新」（应显示已是最新，且概览的引擎版本与远端一致、
  来源显示"运行期记录"）；若哪天又出现不一致，面板会在下一次刷新时自愈并显示"包内 · 刚自愈"。

## Phase 27 · 引擎更新「点下载必失败」：上游 release 地址契约（用户报障）✅ 2026-09-27

> 用户报障：「wifi adb 设备（MI 6X，模块 v1.9.2-r2）测试更新引擎：点『检查更新』能看到上游 1.9.3，
> 点『下载并更新引擎』显示 `❌ 下载失败（HTTP 非 2xx 或网络中断）—— 设备上的引擎未改动`」。
> 取证结论：**下载地址一直是错的**，与安装逻辑、门禁、设备网络都无关。

- [x] **27.1 根因：把裸版本号当成了 release tag**
  - `version.json` 的 `latestVersion` 是 `"1.9.3"`（**不带 v**），而上游 release 的 tag 是 `v1.9.3`
    （其 CI 触发条件就是 `tags: v*`）→ 面板拼出
    `https://github.com/luqman-v1/9router-go/releases/download/1.9.3/9router-go-linux-arm64` → **404**。
  - 实测（本机与真机同 URL，真机走它自己选中的加速节点）：无 v → `404 size=9`（正文就是 `Not Found`）；
    带 v → `302 → 200`（25,821,344B，sha256 `8b52af39…2c309e` 与 `SHA256SUMS.txt` 完全一致）。
    **那 9 字节正文就是 2026-09-26「404 正文被当引擎装上」事故里的同一个东西** —— 当时加的 `-f`
    与两道门禁只把「静默装坏」变成「显式失败」，**下载地址本身从来没对过**（旧版
    `webroot/index.html:728` 就是同一行，不是重构引入的）。
- [x] **27.2 第二个缺陷（还没炸但必炸）：`fetch` 少 `-L`**
  - release 资产地址是 302 跳到 `objects.githubusercontent.com`；`curl -s` 不跟随重定向时只剩空正文。
    真机实测：`SHA256SUMS 无 -L → 302 size=0` ／ `有 -L → 200 size=453`。
  - 后果：tag 修好了也会卡在 `❌ 未取到 SHA256SUMS（拿不到校验和就不装）`。修法：`fetch` 加 `-L`、
    **不加 `-f`** —— 这里要的是「把正文拿回来」，404 正文留给调用方展示（诊断信息），
    `-f` 的职责仍只属于 `download`。
- [x] **27.3 收敛到唯一所有者：新增 `module/webroot/upstream.js`（深 module，纯函数）**
  - 拥有：清单 URL ×2（引擎 `version.json` / 模块 `update.json`）、测速打靶 URL、`releaseTag()`
    （缺 v 补 v、幂等、空值返回空串）、`engineAssetUrl()` / `engineSumsUrl()`（必须同 tag）、
    加速前缀拼接（`withAccel` 与 `withAccelIfGithub`：只有 GitHub 域才加，自定义更新源照原样直连）、
    `parseSumFor()`（按文件名**整词**取，不被 `amd64`/`darwin-arm64` 行误命中）。
  - `app.js` 里散着的 3 个上游 URL 常量随之收编，装配层不再持有任何地址（回潮扫描兜住）。
- [x] **27.4 可诊断性**：`#eng-out` 首行打印**最终下载地址**与所用加速节点；失败与门禁拒绝时一并带上
  地址（含 `SHA256SUMS` 地址）。这次定位最痛的一点就是面板只说"下载失败"、不说"下的是哪个地址"。
- [x] **27.5 回归（AGENTS §5.0）**
  - 新增 `module/webroot/test/upstream.test.js`（13 例）：原故障地址复现（无 v 必须补 v，并**显式断言
    不等于**缺 v 的那个地址）、资产与校验和同 tag、加速前缀只加 GitHub 域、SHA256SUMS 解析、
    与 `checksumGate` 的 fail-closed 联动；
  - `bridge-commands.test.js` +1 例：`fetch` 必带 `-L` 且不带 `-f`；
  - **红灯自证**：在模块副本上回退三处（`releaseTag` 不补 v / `fetch` 去掉 `-L` / `app.js` 手写 release
    地址）→ **7 例精确变红**，三条断言全部具备拦截能力。
- [x] **27.6 顺手修掉清单漂移与一个门禁孤儿**
  - `tools/check.sh` 的 JS-UNIT 清单改 **glob 全量**：`engine-spec-contract.test.js`（候选 5 的门禁）
    此前**从未被唯一入口执行过**，台账还写着过期例数 —— 现在 5 个文件 83 例全部真跑；
  - `tools/deploy-device.sh` 的 webroot 推送改 glob：否则本次新增的 `upstream.js` 必漏推
    （表现是 `window.KUpstream undefined`，面板整页失效）；
  - 台账 §2.2 / §3 的例数与文件清单同步为实测值。
- [x] **27.7 真机 T13（只读链路门禁，新增）**：在设备上用**设备自己选中的加速节点**走一遍
  「版本清单 → `SHA256SUMS.txt` → arm64 资产 → 比对 sha256 与体积/ELF」；**不执行 install-engine、
  不改引擎版本**（所以"引擎已是最新"时同样能跑）；前置不可达即如实 SKIP，绝不假绿；§4 登记外网依赖。
- [x] **27.8 上游同步 v1.9.2 → v1.9.3（本 Phase 的第二个提交）**
  - 规模：67 文件 / +8278 −809；我们改过的文件里**只有 4 个双方都动过**（`chat.go`、`router.go`、
    `codebuddy.go`、`web/src/api/client.ts`），`git merge` **零文本冲突**。
  - 裁决（详见 `docs/adr/0003` 的「同步裁决记录」）：**撤** codebuddy 提示词清洗（上游 shaping 在清洗
    **之后**执行 → 恒为 no-op）与 `HandleHealth`（上游已自己删掉）；**留** `/web/fetch` 双注册、
    `/api/health`(带 CORS)、SSO 回调 501。
  - 上游本版**行为变化**（不是我们的补丁）：Go 侧新增 `db.EnsureCoreSchema`，启动时幂等补齐 11 张核心表
    与缺列并 seed `_meta`/`settings` → 空白 `DATA_DIR` 从"不受支持"变成"受支持的启动路径"；模块
    `etc/schema.sql` 仍是安装期建库来源（SCHEMA 门禁继续绿，**无需重生成**）。
  - 端点 parity 棘轮随本版**收紧 135 → 131**（上游补上的 4 条 Kiro 路由缺口消失，按棘轮语义收紧；
    基线文件同时被当前生成器去掉了方法名对齐空格，属格式归一）。
  - 上游带来的新依赖 `golang.org/x/sync` 在本机拉不动（`proxy.golang.org` 直连超时）→ 用
    `GOPROXY=https://goproxy.cn,direct`；已登记进 `docs/TESTING.md §4`（环境性，不是回归）。
- [x] **27.9 产物与验证**
  - `FORCE=1 bash build.sh`（必须先重建前端：上游改了 4 个 `web/src/**` 文件）→ 七步绿 →
    `dist/9router-go-1.9.3-r1-magisk.zip`（15MB，`verify_zip` 通过，包内含 `webroot/upstream.js`、
    `etc/engine-version=1.9.3`、引擎 25,755,808B）。
  - 真机（MI 6X / 192.168.10.7，用**模块自己的入口** `ops.sh install-module` 装上）：
    `tools/check.sh --all` = 离线 **11/11**、真机 **T\* 30/30**、**A\* 7/7**、PARITY/UIPARITY 无新增缺口，
    汇总 **15 通过 / 0 失败 / 0 跳过**。
  - **T13 在真机上真的走通了修好的链路**：用设备自己选中的加速节点取 `v1.9.3/SHA256SUMS.txt` 与 arm64
    资产 → 摘要一致（25,821,344B）、ELF 魔数正确（**不安装、不改版本**）。这是本次修复最硬的证据。
  - 设备终态：`module=v1.9.3-r1 / 109030`、`engine_version=1.9.3`（`src=runtime`）、
    engine / dns / watchdog 全 up、apiKeys 数据未动。
- [ ] **待用户验收（下一个上游版本）**：1.9.4 发布后点一次「下载并更新引擎」做端到端按钮确认。
  本次按用户决定**不做直推 webroot 的临时验证**（见下「Grilling 决策」）；链路本身已由 T13 在真机上验过，
  唯一没被覆盖的是"app.js 是否真的调用了正确的构造函数"——那一条由离线回潮扫描兜住。

### Grilling 决策（2026-09-27 · 三轮）
- 「这是不是又一桩 404 正文事故」→ 是同一个 9 字节正文，只是这次 `curl` 有 `-f` 才显式失败
- tag 前缀 → 上游 tag 一律带 v、上游仓库不归我们管 → **模块适应上游**（不改成裸号）
- `fetch` 缺 `-L` → 一并修，但**不加 `-f`**（保留 404 正文供诊断）
- 地址契约放哪 → 新增深 module `upstream.js`（owners-first，§2.1）
- 验证面 → 离线回归 + 面板显示地址 + 真机 T13（只读）；**不直推 webroot**，端到端留到下一个上游版本
- 发布物 → 本次同时同步引擎基线 v1.9.3，发 **v1.9.3-r1**（versionCode 109030）



## Phase 28 · 面板「概览读不出来 + 点重启报错」：收编 URL 常量时漏改一处裸引用（用户报障）✅ 2026-09-27

> 用户报障：「查看手机显示引擎还是 1.9.2，点重启会报错，概览的信息也读不出来了」。
> 取证结论：**引擎其实已经更新到 1.9.4 了**（shell 层验过 PID 20329 与 `/version` 自报 1.9.4），
> 坏的是面板 —— 它显示的是 localStorage 里的旧快照。

- [x] **28.1 根因：Phase 27 的收编动作漏了一处**
  - Phase 27 把 URL 常量收进 `module/webroot/upstream.js` 时改了 4 处引用，漏掉 `renderPanel` 里的
    `state.modUrl = st.mod_url || DEFAULT_MOD_UPDATE_URL`（`app.js:159`）—— 那是一处**裸引用**
    （没有 `KU.` 前缀），而常量已经不存在了。
  - 连锁反应正好解释用户看到的三件事：① 状态行在第 159 行**之前**渲染，所以显示的是缓存快照
    （1.9.2 / 旧 PID 27777）；② `resources()` 与 `renderAddrs()` 在它**之后**，抛错后不再执行
    —— 服务地址卡在"加载中"、资源占用全是 `-`；③ 异常冒泡到 `withBusy` 的 catch，
    原样变成 `❌ 操作失败：DEFAULT_MOD_UPDATE_URL is not defined`。
- [x] **28.2 门禁缺口（比 bug 本身更值得修）**
  - 老门禁全是纯函数用例（parsers / bridge / upstream），**`app.js` 这个装配层从来没有运行时用例**；
    回潮扫描只查 URL 字面量，不查标识符。这类缺陷因此只能等真机报障。
  - 新增 `module/webroot/test/app-wiring.test.js`：在桩 DOM + 桩 `ksu.exec`（cb3 形态 + 命中形态缓存）
    里跑**真实的** `app.js`，断言 ① 初始化链上没有 unhandledRejection、② `renderPanel` **后半段**的
    三处渲染（资源占用 / dnsfwd 内存 / 服务地址）与链尾 `renderAccelCur` 都留下了痕迹、
    ③ 每个 `btn-*` 都真的绑上了处理函数（id 写错会静默失联）。
  - `upstream.test.js` 加"回潮扫描之二"：**`upstream.js` 拥有的名字**（清单从它的 return 对象**现取**，
    不手写 —— 手写清单自己会漂）在 `app.js` 里必须带 `KU.` 前缀，裸引用即红。
  - **红灯自证**：把那一行改回裸引用 → 两条门禁精确变红（`not ok` 两条，其余 84 条不受影响）。
- [x] **28.3 修复与真机恢复**
  - 改回 `KU.DEFAULT_MOD_UPDATE_URL`；`tools/check.sh --offline` **11/11** 全绿。
  - 用 `tools/deploy-device.sh` 直推 webroot 到报障设备（dev 通道，不必等发版）：
    设备侧 `md5(app.js)` 与仓库**逐字节一致**，自检 `panel` 显示
    `engine_version=1.9.4 engine_ver_src=runtime`。
  - 设备面板需**重开** WebUI 才会加载新 JS（管理器 WebView 会缓存脚本）。
- [x] **28.4 顺手修文档结构**：Phase 27 插入时把 `## 验收矩阵` 标题吃掉了，表格成了无头孤儿 —— 已补回。
- [ ] **待发版**：本修复尚未进入任何发布包（**r1 里就是坏的那份**），需要随下一次模块发版带上。

## Phase 29 · 上游同步 v1.9.4 + 发布 v1.9.4-r1 ✅ 2026-09-27

> 起因：上游 2026-09-27 发布 v1.9.4（Dashboard issue #24、Gemini Live STT、上游 v0.5.86–v0.5.91 同步等）。
> 同时 v1.9.3-r1 包里的面板是**坏的**（Phase 28），需要尽快发一版；以 v1.9.4 为基线还顺带解决了
> 「新包引擎基线旧于设备在跑的版本 → 整包更新会把引擎退回旧版」的坑。

- [x] **29.1 同步裁决（零冲突）**：117 文件 / +8766 −1386；我们改过的文件里只有 3 个双方都动过
  （`chat.go`、`router.go`、`web/src/api/client.ts`）。逐条见 ADR-0003「同步裁决记录」：
  三处补丁上游**仍未吸收** → 全部保留；`HandleHealth` 未回归；登录态键未回潮。
- [x] **29.2 schema / parity**：上游只把 Go-only 列的回填提取成 `EnsureAdditiveColumns`，表列无变化 →
  `module/etc/schema.sql` **无需重生成**；PARITY/UIPARITY 无新增缺口（基线 131 / 2 不变）。
- [x] **29.3 版本与清单**：`module v1.9.4-r1 / versionCode 109040`；`update.json` 同步（changelog 里
  同时写明"装了 v1.9.3-r1 的用户会遇到面板故障，本版已修"，免得用户以为是新引入的）。
- [x] **29.4 构建与真机验证**
  - `FORCE=1 bash build.sh`（上游改了 19 个 `web/src/**` 文件，必须重建前端）→ 七步绿 →
    `dist/9router-go-1.9.4-r1-magisk.zip`（引擎 26,083,488B，`etc/engine-version=1.9.4`）。
  - 真机用模块自己的入口装 r1 → `tools/check.sh --all`：离线 **11/11**、真机 **T\* 30/30**、
    **A\* 7/7**、PARITY/UIPARITY 无新增缺口，汇总 **15 通过 / 0 失败 / 0 跳过**。
  - 关键几条：`T9 面板 1.9.4 = 引擎自报 1.9.4`、`T13a v1.9.4 摘要与 SHA256SUMS 一致`、
    `T12a–e 版本自愈（0.0.1 → 1.9.4）`、`T8a 9 字节 404 正文仍被拒`。
- [x] **29.5 发布**：push main + tag `v1.9.4-r1` + `gh release create`（附 zip）。

## Phase 30 · 孤儿扫描永远报"扫描结果异常"（planSteps 门禁语义回归）✅ 2026-09-28

- [x] **30.1 现象与定位**：真机每次点「检查孤儿数据」都显示"⚠️ 扫描结果异常（未读到任何节点/连接…）"，
  孤儿清理功能整体失联。排查排除了三层假说（均有真机证据）：DB/SQL 本身（设备直跑 RC=0、
  输出 86211 字节完整）、ksu.exec 传输截断（cb3/promise 两形态 28 次采样逐字节一致）、
  引擎写事务争用（DB 为 WAL 读写不互斥，写事务风暴下 60/60 扫描全绿）。
- [x] **30.2 根因**：`e0619f5`（计划化重构）把 scanOrphans 的直接判断改成
  `planSteps(KP.ORPHAN_CLEAN_PLAN, { scan: { ok: scanOk } })`——只给了 scan 一个 fact，
  而 planSteps 语义是"遇到第一个未通过的 gate 就停"：scan 过了之后 recheck 门禁没有 fact
  → 默认拒绝 → `blockedBy` 永远非空 → 警告每次都显示，与扫描结果无关。
- [x] **30.3 修复**：scanOrphans 只求值 scan 这一道门禁（单步计划 `[{id:'scan',gate:true}]`）；
  recheck/snapshot 仍由 cleanOrphans 在各自阶段按序求值（其三 fact 调用语义本来就正确）。
- [x] **30.4 回归测试**（修复前必红，`test/orphan-scan.test.js`，桩 DOM + 真 app.js）：
  ① 健康扫描（节点/连接齐全 + 含孤儿别名）→ 必须列出孤儿并启用清理按钮，不得显示警告
  （修复前此条红，精确复现真机症状）；② 有别名但读不到任何节点/连接（2026-09-25 误删事故形态）
  → 必须仍中止判定（防误删护栏不回潮）。全套 `node --test` **88/88**。
- [x] **30.5 排查教训**：`planSteps` 的"默认拒绝"语义下，**部分求值（只传前缀 fact）必然整体拦截**——
  以后凡按阶段分步求值的调用点，只传该阶段自己的单步计划；跨阶段顺序由各阶段各自求值保证。

## Phase 31 · 快照双重转义 + 写操作静默假成功（审计修复）✅ 2026-09-28

- [x] **31.1 快照永远失败（真机用户复测抓到）**：cleanOrphans 的快照 SQL 把全部单引号
  `.replace(/'/g, "''")` 翻倍 → `scope IN (''customModels'',…)` 是 sqlite3 语法错
  （设备实证 Parse error RC=1）→ 0 字节快照 → 门禁每次正确拦截 → 永远"快照失败，已中止删除"。
  别名已过 UUID 形状校验，翻倍转义纯属多余。修复：SQL 直接用原始 like/eq。
  回归测试：`test/orphan-scan.test.js` 用例 3 断言快照 SQL 无翻倍特征、DELETE 必须真实发出、
  必须有"已一次性清理"回执（修复前红，桩会模拟 sqlite3 语法错）。
- [x] **31.2 同族审计——写操作静默假成功**：promise 形态下 stderr 被丢弃、退出码恒 0，
  `writeFile/appendLine` 旧实现 `return !r.err` 把设备上的写失败判成成功——影响面：
  端口写入（谎报已写→引擎按旧端口重启）、DNS 保存/优选（"❌ 写入失败"分支不可达）、
  加速节点选择/添加、模块更新源保存。修复（bridge 层，自报成败模式与 sqlSnapshot/download 一致）：
  命令自吐 `write-ok` / `append-ok` 标记，运行层只认标记；`sqlFile` 自吐 `__SQL_OK__` 并
  以 `r.ok` 暴露读失败（缺失即重试 3 次），标记行由桥剥离不污染调用方输出
  （桩测试抓到：标记行无 `|` 会被孤儿扫描误算进存活节点）。
- [x] **31.3 调用方接上诚实返回**：savePort / DNS 优选最终写入 / 加速选择与添加 / 模块更新源
  失败即中止并如实提示；scanOrphans `r.ok=false` 不再冒充"✅ 未发现孤儿"；
  scanCred 读失败显示"读取失败"而非假阴性"凭据齐全"。
- [x] **31.4 端到端真机验收**：合成孤儿（`openai-compatible-chat-00000000-dead-…`）种入 DB →
  面板扫描列出 → 清理 → DB COUNT=0、`backups/kv-before-orphan-clean-*.sql` 119 字节且内容为
  精确回滚 INSERT。全套 `node --test` **92/92**。

## Phase 32 · 架构走查落地：planGate 阶段切片 + 两个活体 bug + 桥/SQL 收口 ✅ 2026-09-28

走查报告（`/tmp/architecture-review-20260928.html`）六项候选全部落地；证据链来自 2026-09-28
三起事故（Phase 30/31）的结构性回溯。

- [x] **32.1 两个活体 bug（C1，用户真机复现）**：`optimize`（app.js:324）与 `engUpdate`（app.js:600）
  传整计划只给单 fact —— rows-gate/file-gate 通过后被无 fact 的 backup-gate/sum-gate 假拦：
  DNS 优选在有可用上游时必然中止（且错用 rows-gate 文案，用户看到的就是"❌ 没有可用率 ≥50%"）、
  引擎更新在校验和之前必然中止。修复：新增 `KP.planGate(plan, phase, fact)` 阶段切片求值
  （只求值本阶段门禁，顺序仍由唯一计划常量承载）；七个 planSteps 调用点全部迁到 planGate。
- [x] **32.2 复查门禁接 r.ok（C2）**：cleanOrphans 复查读失败时把空输出当"查无存活"放行删除
  ——与扫描门禁同一判据纪律补齐；不再伪造 `scan/recheck:{ok:true}`。
- [x] **32.3 桥诚实化收尾（C3）**：`ops()` 脚本路径与 token 全过 shq（ver 不再裸拼）；
  `readFile` 自报 `__READ_OK__`（读失败与空内容可辨，标记由桥剥离），renderAccelCur
  读失败显示"未知"而非谎报"直连 GitHub"。
- [x] **32.4 SQL 收进命名层（C4）**：`_cmds` 新增 scanOrphansSql / recheckOrphansSql /
  orphanSnapshotSql / orphanDeleteSql / credScanSql / libListing —— SQL 文本获得与 shell
  命令同等的离线断言，装配层不再持有 SQL。
- [x] **32.5 facts 抽纯函数 + 桩具去重（C5）**：`parseScanLines` / `parseCredScan` 进 parsers；
  测试桩具收敛为 `test/lib/app-harness.js`；新增 `test/gate-flows.test.js` 流程级回归
  （optimize/engUpdate 全链可达 + file-gate 失败仍拦）——修复前恰两红一门禁绿，修复后全绿。
- [x] **32.6 engine_version_sync 单一所有者（C6）**：cmd_install_module 的内联第二实现
  撤除，改 `engine_version_sync --from-package`（包刚换过 → 无条件以包内为准，含
  "包里没有 → 删运行期文件"的宁缺毋谎分支）；T12 回归从此同时盖住两个时机。
- [x] **32.7 回归测试**：`node --test` **106/106**（新增 planGate 语义/阶段覆盖、
  parseScanLines/parseCredScan、ops 引号、dbOps 形状、readFile 运行层、复查读失败、
  两条门禁全链流程）。真机：4 文件推送后 `ops.sh status` 冒烟正常，DNS 优选待用户复测。

## Phase 32 · 面板架构升级：清单唯一来源 + 拆分（Grilling 三轮达成的方案）🔄 进行中

> 起因：用户问"要不要为面板升级到 TypeScript 框架"。三轮 Grilling 的结论是：**不引框架/打包器**，
> 症结不在语言而在"同一语义多处复制"与"门禁按文件名写死"。方案 = 类型闸（@ts-check）替代构建步骤
> + 面板按页签拆分 + 所有门禁从 `index.html` 清单派生。价值边界已如实记录：7 个历史 bug 里只有 1 个
> 能被静态类型拦住（Phase 28 的裸引用），其余属于跨进程契约与语义，靠门禁而非类型。

- [x] **32.1 前置收口（S1）**：`test/lib/app-harness.js` 成为桩具唯一一份（`app-wiring.test.js` 从自带桩迁入），
  且**按 `index.html` 的 `<script src>` 顺序在同一 vm 上下文里跑全部脚本**（等价浏览器的"多脚本共享顶层词法作用域"）。
  踩坑记录：桩最初用共享的 `activeWin` 解析回调名 → 前一个 realm 的在途命令找不到自己的回调 → 每个都等满
  120s 超时（**用例全绿但进程不退出**）。改为**按 realm 各建一份 exec 桩**后消失。
- [x] **32.2 门禁覆盖面先补齐（否则拆分=静默失覆盖）**：`contract-keys` / `upstream` 的扫描目标从写死的
  `app.js` 改为清单派生；`upstream.test.js` 的顺序断言从"在 parsers/app 之前"改为"在清单其余脚本之前"。
  **红灯自证**：临时插一个读未知键的页面文件 → 契约门禁精确报 `zzz_probe_key`（仅那一条红）。
- [x] **32.3 `verify_zip` 加打包侧孪生断言**：`index.html` 声明的每个脚本都必须在包里（Phase 27.6 漏推事故那一类）。
  红灯自证：伪造引用 `ghost.js` 的 index.html → `DIE` ✓。
- [x] **32.4 纯搬迁拆分**：`app.js`(666 行) → `app-core.js` / `page-overview.js` / `page-dns.js` /
  `page-consistency.js` / `page-update.js` / `app-boot.js`。**机械验证**：与设备上那份逐字节一致的
  `app.js` 比对，只少 9 行注释（已补回文件头），**零代码行丢失**；全部用例**一行未改**仍绿。
- [x] **32.5 真机部署与一致性**：9 个 `.js` 与仓库 md5 全等；`deploy-device.sh` 按清单清掉仓库已不再提供的 `.js`
  （否则设备留着旧 `app.js`，排查时会误判成"没部署成功"）。
- [x] **32.6 `@ts-check` 类型闸（Step 3）**：8 个脚本一次点亮，闸位 `JSTYPES` 接进离线档。
  类型**从实现派生**（`types/kmod.d.ts` 用 `typeof import('../parsers.js')` 取形状，不手写会漂的清单）。
  **定档先量后定**：strict 全开时仅 `parsers.js` 就 41 条，**全是** `noImplicitAny`（缺 JSDoc 参数标注）、零真 bug
  → 当前档取"零注释成本"那档（allowJs 的结构推断仍能拦 TS2304 / TS2554 / TS2339·TS2551 /
  **TS2300 跨文件重名**——经典脚本共享词法作用域，重名即整页 SyntaxError）。
  收口清单（都是类型噪音，非 bug）：UMD 的 `this` 分支转 any（带 `module.exports` 的文件被 TS 按 CJS 模块处理，
  顶层 `this` 是模块导出对象）；DOM 约定在 `types/kmod.d.ts` **一次声明**（55 处 `getElementById` 不逐处断言，
  `index.html` 结构由 32.7 的 DOMID 门禁缝住）；`cfg()` 补 JSDoc 返回类型；宿主桥 `ksu.exec` 三形态返回 any
  （外部边界，硬写类型是假精度）；toast 定时器从元素自定义属性 `t._tm` 移到顶层变量。
  **红灯自证**：往 `app-boot.js` 插一个裸引用 → `TS2304` 精确拦下 ✓。
  顺带：`build.sh` 纯净发布排除 `webroot/types/*` 与 `webroot/tsconfig.json` + `verify_zip` 反向断言
  （开发专用文件不许进发布包）。
- [x] **32.7 DOM id 契约门禁（Step 4）**：`module/webroot/test/dom-id-contract.test.js`（4 例，落在 JS-UNIT 的
  glob 里自动执行）三个方向：① 脚本请求的 id ⊆ `index.html` 声明的 id；② `index.html` 里每个 `btn-*` 都必须被
  绑定（**死按钮** —— 这是 app-wiring 那条的**另一半**：桩会为拼错的 id 凭空造元素并绑上，所以只有这条能发现
  "HTML 里没那个按钮"）；③ nav 的 `data-page` 必须指向存在的 id（否则点页签 `getElementById(null)` → 整页死）；
  ④ 非空转自检（两侧解析结果都必须有量，否则断言永远绿）。
  **实测结论**：当前面板**没有**不匹配（4/4 绿）。**红灯自证**：三个方向各注入一个错误 →
  精确 3 红 + 非空转自检 1 绿，还原后 4/4 绿（`index.html` 摘要与注入前完全一致）。
  全量用例 108 → **112**。
- [x] **32.8 A5 进度改进（Step 5）**：GitHub 测速改 **shell 内并发 + 每节点独立临时文件 + 分批进度**
  （不碰 `bridge.js` 的全局串行队列，因此**不需要**"按宿主差异化放开并发"那套机制）；DNS 探测/优选加
  「已用时长」心跳（`withElapsed`）。
  - 根因回顾：原实现是面板侧 15+ 次**串行** exec（最坏 15×8s ≈ 120s），期间界面只有一句"测速中…" ——
    用户报的"点 DNS/ GitHub 测速会卡"就是它。
  - 关键设计：**索引写在行里**（`i\turl\t<code> <time>`）—— `cat *.out` 的 glob 是字典序
    （`b0-10.out` 会排在 `b0-2.out` 前），靠行序映射回节点在节点数 ≥ 11 时必然错位。
  - 收口：删除单点 `curlTiming`（执行归 bridge、解析归 parsers 的 `parseCurlTimings`，原先解析内联在 bridge）。
  - 离线：`parsers.test.js` +2（索引还原 / 失败不冒充）、`bridge-commands.test.js` +2（并发形态 + `-w` 契约）。
  - **真机端到端验证**（用桥的构造器生成命令 → 设备执行 → 纯函数解析）：
    `0 http://127.0.0.1:20128/health → 200 0.004837` / `2 .../version → 200 0.004513` /
    不存在的主机 → `000`（`ok=false` 不冒充），临时目录 **已清理**。
    顺带暴露一条现实条件：本机当前**未选加速节点**（直连 GitHub）→ `raw.githubusercontent.com` 全部 `000`，
    面板会如实显示"所有节点都不可达"（不是 bug，但说明真机测速前要先选加速节点）。
- [x] **32.8b 测速再提速 + 消掉"还得手点一个"（用户追加：进一步完善）**
  - **先纠正上一轮的错误结论**：面板测速是把**每个代理节点**拼上 GitHub URL 去测
    （`chunk.map(n => n + target)`），**不依赖**已选节点、也不需要先选一个；上一轮的真机验证用的是
    **裸 URL**（绕过了代理）才全 `000`。用面板真实方式复验：未选节点时 2/4 可用（773 / 734 ms）。
  - **失败节点才是耗时大头**（真机实测：挂死的代理 5.03s，因为 `-m 8` 一直等）→ 加 `--connect-timeout 3`
    （实测同一节点 5.03s → **3.00s**）。
  - 每批 5 → **8**（与 dnsfwd 的 `-j 8` 同量级）；进度条带"已用 Ns"。
  - `%{errormsg}` 带出**失败原因**（curl 8.0.1 支持）：解析失败显示 `Could not resolve host: …`，
    纯超时则留空 → 界面回落到"连不上或超时"（不编造原因）。
  - **测速即选中**（与 DNS 优选的"测速后自动应用"同一惯例）：省掉"测完还得手动点一个"——
    那正是"得先选节点才能用"的摩擦来源。与已选相同则不写设备（省一次 root shell + 一次闪存写）。
    界面如实写明"已自动选中最快"并可改（点别的 / 「清除选中」）。
  - **真机结果**：同一批 4 节点，整批耗时 **~5.3s → 2s**，`3 可用（774/646/1683ms）+ 1 失败（1703ms 快速认输）`。
  - 测试：`parsers` +1（失败原因）、`gate-flows` +1（自动选中，含 base64 解码断言）。
    **新用例第一次跑就抓到桩的顺序缺陷**：写 `github-accel` 的命令里也含 `github-accel` 路径，
    而桩把"含 github-accel"当成读 → 自动选中被静默判成写失败（已改为写命令优先判断）。
- [ ] **32.9 文档（Step 6）**：`AGENT-CONVENTIONS §3/§4` 增加「面板扩展点」一节（新页签/新 shell 命令/
  新门禁计划/新 shell 输出键 各自的落点与必配门禁）+ ADR-0008（零构建自包含的取舍，与被否掉的
  npm `kernelsu` 打包路线、Svelte 重写、MMRL 单宿主三条路）+ README/MAGISK 兼容性口径。

### Grilling 决策（2026-09-28~29 · 五轮）
- 目标 → 只接受"拦一类 bug"或"改得更快"作为判据；"看起来正规"不是判据
- 语言 → **JS + `@ts-check` + JSDoc + `tsc --noEmit`**（不上 TS 迁移、不进构建步骤）
- 宿主 → **KernelSU 原生 WebUI 保证可用**；WebUI X / MMRL / Magisk **附带兼容**（不验收、不承诺、报障再修）；
  MMRL 实测未列出本模块（未完成 root 授权排查即停止），故兼容线不做承诺
- 拆分 → 6 文件、`index.html` 清单是唯一来源、**纯搬迁零行为变化**、`app-wiring` 先迁 harness
- 顺序 → 前置收口 → 拆分 → 类型闸 → DOMID → A5 → 文档；UI 重做排在之后

## Phase 33 · 守护「必然在跑」根治（用户报障：内存涨 + 系统进程挂 + 要手动开）✅ 2026-09-29

> 用户报障：「模块跑一段时间后内存超过 100 多 MB，然后系统进程都挂掉了，要手动开启」。
> 取证结论把两件事分开：
> **① 内存增长在本机复现不出**（开机 20h 后引擎 RSS 12MB、dnsfwd 36kB、守护 2.2MB，MemAvailable 1.8GB，
> dmesg 无 OOM、logcat 无 LMKD 击杀、守护 19h 零自愈动作）。守护自身无累积泄漏（每轮只 `test -f`/`kill -0`；
> dnsfwd 在跑时连 `ss` 都不调）；`VmSize` 2.2GB 是 Android mksh 的固有现象（**新开的 `sh` 也是 2.23GB**，
> 我们的守护比它还小）→ **不是我们**。
> **② 真正抓到的是"守护可能不在跑 + 失败无痕"**：2026-09-29 21:47 开机那次守护没起来，
> `watchdog.log` 无"守护启动"、`watchdog.pid` 空文件，而 `life_boot` 把 `life_wd_start` 的判据丢进
> `/dev/null` → 日志里一个字都没有。守护不在 = 引擎任何死因都不会再自愈 = 用户"要手动开"。
> **③ 并发现 OOM 策略是继承来的**：模块三个进程全是 `oom_score_adj=-1000`（从 init/adbd 继承，没人选过）
> → 内存压力时内核杀不动模块进程，只能去杀系统里其他可杀进程 —— 这正好解释"系统的进程都挂掉了"。

- [x] **33.1 判据绝不丢弃**：`life_boot` 改为 `_w="$(life_wd_start)"` → 失败**重试一次**（开机期系统繁忙，
  首次启动可能在"等 pidfile 就位"的 3s 窗口里超时）→ `life_log "boot: watchdog=$_w"` 如实写日志。
- [x] **33.2 开机第二次机会**：`service.sh` 末尾（网络等待与引擎启动之后、系统已缓和）再 `life_wd_start` 一次，
  写 `wd-ensure: <verdict>`。`life_ensure_engine` 自带 `life_prep` + `runtime.env` 生成 →
  守护抢在 `service.sh` 前拉起引擎**没有跳过 schema/env 的风险**（已核对代码）。
- [x] **33.3 守护身份校验**：`life_pid_is_watchdog`（按 `cmdline` 认 —— 守护是脚本，`/proc/<pid>/exe` 指向 `sh`），
  `life_wd_alive` 同时要求"活着"与"是我们的守护"（pidfile 号会被无关进程复用 → 否则面板永久误报 `up` 且永不拉起）。
- [x] **33.4 OOM 策略显式化**：新增 `life_oom_protect`；`life_wd_start` 启动守护后显式写 `oom_score_adj=-1000`
  （不再靠"从 adbd/init 碰巧继承"），失败写日志不阻塞。**待决策**：引擎是否也保持 -1000（现为继承而来）。
- [x] **33.5 内存证据采集**：守护每 ~60s 采一次引擎 RSS，**≥100MB 才记一行**（两行至少隔 ~12 分钟）。
  下次那份"内存涨到 100+MB"的报告就有时间线可查，而不是只能靠猜。
- [x] **33.6 引导证据行**：`watchdog.sh` 在**任何 source 之前**先 `printf` 一行 `watchdog: 引导中 pid=$$` ——
  把"根本没被执行"与"在 source 库里就死"两种情形彻底分开（此前两者都表现为"什么都没有"）。
- [x] **33.7 回归测试（AGENTS §5.0）**：新增离线档 `LIFECYCLE`（`tools/test-lifecycle-lib.sh`，19 断言）——
  L1/L1b/L2 锁"判据不丢 + 失败重试"、L3/L4 锁身份校验、L5/L6 锁参数护栏。
  **红灯自证**：把 `life_boot` 还原成 `>/dev/null` → L1/L1b/L2 精确变红（3 条）。
  写测试时踩到并记录：`_w="$(life_wd_start)"` 是**子 shell**，变量计数的副作用传不回来 → 计数改用文件。
- [x] **33.8 真机门禁 T14**：T14a 显式 `oom_score_adj=-1000` / T14b 身份可认 / T14c 面板不误判 /
  T14d 引导证据行在位 / T14e 开机判据已入日志（未重启则 SKIP）。真机 **T\* 35/35 + A\* 7/7** 全绿。
- [x] **33.9 开机复现实验（重启一次）**：`boot: watchdog=started`（**第一次就成功**）→
  `watchdog: 引导中 pid=1705` → `守护启动 pid=1705 cgroup=/` → 6 秒后守护**自己把引擎拉起来**
  （`引擎不在（连续 2 次判死），拉起` / `引擎已拉起 pid=2508`，引擎的父进程正是守护）
  → `wd-ensure: running`（第二次机会幂等）。
- [x] **33.10 计划外发现：T10 会留下脏状态**：设备门禁 T10 会把 `lib/` 换成包内（发布版）那份 ——
  开发直推态自此失效、下次开机跑的是包内那份，而下一轮 T14 就会在**旧代码**上跑。
  已在该步输出显式提示，并写进 `docs/TESTING.md` 变更记录。
- [x] **33.11 热路径去 fork（**实测打脸，如实记录**）**：`life_pid_alive` 改用 shell **内建 `read`**（不再
  `$(cat)`）、`interval` 不再每轮 `cat | tr` —— 每轮 fork 数从 4-5 次降到 0-1 次。但**真机 A/B 否掉了收益**：
  9.17ms/轮 → 8.33ms/轮，差异落在 60s 采样的量化误差内（±1ms）→ "省 fork = 省 CPU"这个假设**没被证实**；
  判定性实验说明大头是"每轮创建一个进程"本身（纯 `sleep 5` × 12 的循环自己就要 430ms/60s）。
  **结论钉在这里免得后人重复踩：收益来自"降低轮次频率"（33.13），不是"每轮少几次 fork"。**
  顺手抓到并修掉两个真 bug：① `kill -0` 对**僵尸进程**仍返回成功 → 守护会误判"引擎还在"而**永不拉起**
  （改为读 `/proc/<pid>/stat` 的 state 字段，仍是 0 fork；口径与 `lib/wait.sh` 一致）；
  ② `read v < /proc/... 2>/dev/null` 的**重定向顺序写反** → 刚死的进程会让 `can't open /proc/<pid>/stat`
  混进 `ops.sh panel` 的**键值输出**（真机复现 → 修 → 噪声 0）。
- [x] **33.12 时间口径改秒 + 内存基线**：`life_rss_log_due` 的限流/基线从"轮次"改成**秒**
  （超阈值 12 分钟限流 + 每小时无条件一条基线）。**为什么必须改**：33.13 要把周期从 5s 拉到 60s，
  若仍按轮次记账，"每 12 轮"会从 60 秒**静默**变成 12 分钟 —— 意图被改掉且没人发现。
  基线还补上旧策略的盲点：只记"超 100MB"会漏掉"缓慢爬到 90MB"这种最需要证据的形态。
- [x] **33.13 守护事件驱动（核心里程碑）**：`sleep` + `wait $!` 循环 + `trap CHLD` + `trap USR1`，
  间隔 **5s → 60s**；引擎由守护亲手拉起时还能 `wait` 到**退出原因**并记日志；非子进程（刚开机/守护自己重启过）
  自动退回 10s 短周期兜底。**真机验证的五条事实**（实验脚本 + 现场数据）：
  * CHLD 能**立刻打断**被 `sleep` 阻塞的循环（实测 3s 内醒，不是等满 60s）；
  * 零 fork handler 触发 3 次（正常）；**handler 里写了 `$(date)` 的版本同一秒刷几百行且停不下来**
    —— handler 自己 fork 会再生一个 SIGCHLD，**自我触发风暴**；
  * USR1 能打断 `wait`（用户点重启 → **0.27s** 返回 `engine=up`；纯 60s 轮询会先让 `wait_for 20` 超时）；
  * 主循环 fork 之后，下一次 `wait` **不会**被"迟到的 CHLD"打断 → 不需要 drain 技巧、不会自旋；
  * `inotifyd` 同样可用（同秒回调；但回调程序**必须有 +x**，否则静默无常）。**本轮控制路径仍选 USR1**：
    inotifyd 要盯目录，而数据目录里日志一直在写 → 事件风暴（box 把被盯目录选在"安静"的模块目录，正是这个原因）。
  现场数据：`kill -9` 引擎 → **1.2 秒**恢复（旧实现 ≤12s），日志留下
  `引擎退出（被 SIGKILL 杀（kill -9 / 内存回收 / 连坐清理） pid=…）`；守护 120s 采样 CPU 50ms（旧 ~200ms）
  → 全天唤醒 **17280 → 1440 次**、CPU **158s → ~36s**。
- [x] **33.14 真机 T4 抓到"先动作后意图"的顺序缺陷**：`life_stop_user` / `life_disable_dns` 原来是
  **先停进程、后写意图**。旧代码（5s 轮询 + 连续两次判死 ≈ 10s 窗口）刚好掩盖它；事件驱动把窗口压到毫秒后，
  守护会**把用户刚停掉的服务立刻复活**（T4 两条断言同时红）。修法：**意图先落盘再动进程**。
  配套离线断言 `L11`（断言"动作发生那一刻意图是否已可见"，不必上真机等 30s），并做**红灯自证**：
  把顺序改回错的 → L11 精确变红、L11b（dns）保持绿。
- [ ] **待决策**：引擎的 `oom_score_adj` 是否保持 -1000。选项：保持（压力只能由系统承担）/ 改为可杀
  （"先杀引擎、守护再拉起"）/ 保持保护但加"RSS 超阈值持续 N 分钟 → 守护主动重启"。**先用证据说话**：
  33.5 已经埋好采集，等真实增长数据再定阈值，避免猜着改。
- [x] **待验证（部分完成）**：`boot: watchdog=started` 已 **2/2 复现**（33.9 那次 + 2026-09-29 18:00 那次自然重启，
  两次都是"第一次就成功"）。每次开机都该有这行 + `wd-ensure:` 一行；缺了就是"守护没上班"。
- [x] **33.15 顺带解释用户的"为什么 box 稳、我们不稳"**（证据，不是安慰）：引擎自身**从未崩溃**
  （`panic/fatal` 计数 0、dmesg 无 OOM/LMKD 击杀、`oom_score_adj=-1000`、RSS 23MB 峰值 49MB 无增长）；
  账本里的 66 次"引擎不在"时间分布成对、间隔 26-27 秒、集中在开发时段，与 `tools/device/test-lifecycle.sh`
  每次全量杀引擎 2-3 次（T2/T12/T4）完全吻合 → **都是门禁杀出来的**，且开机至今除门禁外 86 分钟零死亡。
  box 的"稳"有一部分是**它没有账本**（不记死没死），而我们有。真因"要手动开"是**守护没上班**（33.1–33.9 已修）。
- [x] **33.16 真机档"跑旧代码"的失真被根治**（本次最费时的一处排查）：T* 末尾的 **T10 会把设备 `lib/` 换成
  `$DATA_DIR/last-module.zip`**（设备上"上次装过的旧包" —— 那正是它的测试目的：测装包覆写正在运行的
  `lib/` 会不会自毁），副作用是**下一次**真机档就在旧代码上跑。实测代价：**T4 会假红**，
  而真相只是"代码被换回去了"。已在 `tools/check.sh` 真机档**开跑前与结束后各直推一次**
  （跑前确保测的是当前代码，跑后把设备恢复到当前代码稳态），直推失败时显式警告。
  排查中的教训（一并记下）：我一度用 `unzip -p ... '*/lib/lifecycle.sh'` 判断"包里有没有修复"，
  **通配符没命中**却当成"包里没有" ✗ —— 包内路径是 `lib/lifecycle.sh`（无 `module/` 前缀）。
  **"零结果"必须先证明"测量本身命中了"，再下结论**（与"读失败不是空结果"同一条纪律）。

## Phase 34 · 架构走查修复（A1–B5）✅ 2026-09-29

> 由 `improve-codebase-architecture` 走查产出（只读扫描 + 逐条复核到源码行；完整报告是临时目录里的
> `architecture-review-*.html`）。范围取近 40 次提交的热点：`module/lib/*.sh`、`module/webroot/*`、
> `tools/`（含真机门禁）、`module/*.sh`。术语按 `CONTEXT.md` + codebase-design（module / interface /
> seam / depth / locality / 删除测试）。**每条都为"影响 × 可信度"最高的那一批，且都配了门禁。**

- [x] **A1 门禁入口的严格模式空转（最高级别假绿）**：`--require-device/--require-parity` 只设 `REQ_*`
  不选档 → `PICKED=0` → 退回离线档，而 `REQ_*` 只在**被跳过的块**里被读 → CI 报成功却一条 `T*`/`A*`/parity
  都没跑。修法：两个标志同时选档 + 新增 `--print-tiers` 诊断开关。**门禁 `CHECKFLAGS`**（10 断言：
  档位选择 ×4、严格位 ×2、无设备时"非严格 SKIP / 严格必红"的对照 ×2、以及"跑不起来 ≠ 拦住了"的输出断言）。
  **红灯自证**：改回旧实现 → `--require-device --print-tiers` 打印 `tiers=offline`（本该 `device`）→ C2 必红。
  顺手修掉 `usage()` 写死 `2,20p`（头部加一行说明就会把 `set -uo pipefail` 当用法打出来）。
- [x] **A4 管理器「操作」按钮的解析错**：`action.sh` 用行锚 `grep "^key="` 解析 ops.sh 的**单行** status ——
  行中间的 `engine=` 永不命中（显示成空），行首的 `port=` 命中整行、残余被当端口（`/health` 探测必然失败）。
  修法：新增 **`ops.sh get <key>...` 深接口**（"status 是一行 k=v"这个事实的唯一所有者；输出每键一行，
  键不存在则退出 1 —— 读不到 ≠ 空结果）+ `action.sh` 改用它（并改用 `sh "$OPS"` 调用，去掉对**执行位**
  与 **Android 专有 shebang** 的双重隐藏依赖，使它可离线断言）。**门禁 `OPSSTATUS`**（14 断言：单行契约 ×3、
  行首键/中间键/多键/缺键/非法键名 ×8、`action.sh` 端到端值与 status 对齐 ×3）。
  **红灯自证**：把 action.sh 改回旧解析 → 完整复现用户现象（`端口 : 20133 bind=loopback … engine=down …`
  整行残余、`引擎 : (PID )` 空）→ A 组 3 条必红。
- [x] **A3 无守护时 `restart-engine` 停了 DNS 不拉回**：有守护分支特意补了 `life_ensure_dns`，无守护分支只
  `life_ensure_engine` → dnsfwd 静默停摆（Android 无 `/etc/resolv.conf`、引擎只认 `127.0.0.1:53` → 域名解析全挂）。
  修法：抽出 `life_restart_all`（stop → engine → dns，"停了就由同一处负责起回来"），两条分支语义一致。
  **门禁 L12**（用文件记序 —— 该函数经子 shell 调 ensure，变量副作用传不回来，正是 33.7 的教训）。
- [x] **A2 快路径前提陈旧（本轮新代码的洞）**：`eng_ours` 只表示"我们亲手起过它"，而 `life_stop_engine`
  先 `kill` 再删 pidfile → 守护被 CHLD 唤醒时判据不成立 → 陈旧值不清空 → 既收不到 CHLD、又因变量非空停在
  60s 长周期（自愈从秒级退化到最多 60s）。修法：判据从"变量非空"改成"pidfile 里就是它"，当轮归零（对
  dns_ours 同办）。**门禁 T15（真机）**：复刻"停服 → 启服（新引擎不是守护子进程）→ 强杀"，要求 ≤30s 自愈
  （修好走 10s 兜底 ≈20s；没修最多 2×60=120s）。真机实测 **13s** ✓。
- [x] **A5 改端口后谎报成功**：`savePort` 丢弃 `restart-engine` 的返回值却无条件 toast「✅ 已重启」
  （同文件 `restartAll` 一直在判）。修法：按结果分支文案。**门禁**：`page-flows` 新增用例（失败必须报 ❌
  且不含"已重启" + 成功必须报 ✅，双向对照）。
- [x] **A6 回滚点没建成仍改写配置**：`saveUpstreams` 丢弃 `backupOnce` 的返回值照样写（同页 `optimize`
  一直以回滚点可用为前提）。修法：备份失败即提示并**不改写**。**门禁**：断言"备份失败时**不得下发写入命令**"
  （命令流断言，不是文案断言）+ 可用时必须照常写（防误伤保存功能）。
- [x] **B1 生命周期状态文件名有"第二读者"**：`ops.sh`/`watchdog.sh` 都声明"不读写状态文件"，实际共 9 处
  直接引用 `$LIFE_ST_ENGINE/$LIFE_ST_DNS`。修法：新增 `life_engine_pid()/life_dns_pid()/life_watchdog_pid()`
  三个访问器并替换全部外部引用（文件名只留在 lifecycle 内 → 改文件名不再牵连两个文件）。
- [x] **B4 内存账本用"名义间隔之和"而非墙钟**：`elapsed += poll_iv`，但 sleep 会被 CHLD/USR1 打断 →
  时间被高估 → "每 1 小时"基线早触发。修法：改用 `date +%s` 差值（每轮多一次 fork，缺省 60s 一次）。
- [x] **B5 属性语境的转义**：`esc` 只转 `& < >`，却被用在 4 处属性值（`href=`/`data-*=`，值含 `"` 即越出属性
  —— 节点名来自用户输入）。修法：新增 `escAttr`（多转 `"` `'`），4 处改用；**门禁**：源码级扫描
  "属性值里不得出现 `${esc(`"（与上游地址回潮扫描同手法）。
- [ ] **未做（有意）**：`page-dns` 里 "b64-or-readFile" 推导重复两处 → 可抽 `confText()`；本轮不做，
  因为**该路径没有现成用例**，纯重构等于不可验证的风险（用户明确要求"避免产生新的问题"）。
  下次若要动它，先为它补一条用例再抽。
- [ ] **未做（有意）**：A5/A6 的流程各补一个 `*_PLAN` 门禁（与 `DNS_OPTIMIZE_PLAN` 同族）。当前修法沿用
  邻近代码的既有语义、改动最小；等这两条流程再长出第三道门禁时再收敛成计划。

### Phase 34.1 · 第二轮诊断补修（7 条，2026-09-29）

> 用户追问"没有其他 bug 了吗"，于是按 `diagnosing-bugs` 的纪律再扫一遍**上一轮没走到的面**
> （bridge/parsers/upstream/app-core/index.html、其余 page-*.js、tools/*.py、build.sh、
> 以及测试自身的假绿）。每条都先有"能变红的信号"再动手。

- [x] **I1 装包门禁先于动作**：`cmd_install_module` 过去「先 `stop_all` 再 `unzip`」—— zip 坏/非 zip/
  `unzip` 缺失时只能 `echo install-failed` 走人，而引擎与 dnsfwd **已经被停掉**（守护未武装则永不回来）。
  修法：动服务前先 `unzip -l` 验包内有 `module.prop`（与 `cmd_install_engine` 对称）。
  **门禁 `INSTALLGATE`**（7 断言：非 zip / 截断 zip → 挡住 + **pidfile 仍在**（服务没被停）+
  文件不存在仍回 `no-src`；成功路径用"门禁行早于 stop_all"的顺序断言兜住 —— 测试里绝不能真装包）。
- [x] **I2 `start-user` 谎报**：`life_start_user` 无论引擎有没有起来都 `echo started`，面板于是无条件报
  「✅ 服务已启动」（与 refresh 后的状态卡互相打脸）。修法：如实回 `engine=up/down`（等 20s 判一次，
  与 restart 同口径）+ 面板按结果分支文案（`startSvc`/`stopSvc`）。**门禁**：`page-flows` 双向对照。
- [x] **I3 `.prev` 永不刷新**：`optimize()` 用 `backupOnce` 写 `.prev`，而它的语义是"目标已存在就跳过" →
  `.prev` 永远停在"第一次优选前" → 「回滚上一版」实际回到首版（常等于 `.initial`）。修法：先 `remove`
  再 `backupOnce`。**门禁**：源码顺序断言（删旧必须在备份前）。**接缝缺口**：真跑 `optimize()` 需要桩
  DNS 探测输出，本轮没做（见下）。
- [x] **I4 `reloadDns` 的失败被结构性吞掉**：失败提示写在 `if (!silent)` 里，而四个调用点**全部**传 true
  → 死代码 → 配置已写盘但 dnsfwd 未生效时界面照样报「✅ 已恢复/已回滚/已自动应用」。修法：失败永远报，
  `silent` 收窄成"成功时不打扰"。**门禁**：`page-flows`（保存后热重载失败必须出现提示）。
- [x] **I5 `cleanOrphans` 的快照与删除可能不是同一批**：复查剔除后 `scanOrphans()` **未被 await** →
  该异步函数会在下面的 `await` 之后回写 `state.orphans` → 快照取旧数组、删除取新数组（击穿
  `ORPHAN_CLEAN_PLAN` 的核心不变量）。修法：`await scanOrphans()` + 用局部 `targets` 绑定本批目标。
  **门禁缺口（如实记）**：要复现需要"复查期间并发扫描"的时序，离线桩造不出来 —— 属"没有正确接缝"，已记。
- [x] **I6 `clearAccel` 把正常态渲染成故障**：清掉选中后无参 `renderAccelCur()` 去读刚被删掉的文件，
  而 `readFile` 对"文件不存在"与"读失败"返回同一个 `ok:false` → 显示成「未知（读取失败）」，
  同一刻 toast 却说"直连 GitHub"。修法：显式 `renderAccelCur('')`；`addAccel` 不再无参重渲染
  （往候选列表加项不影响当前选中）。**门禁**：源码级断言。
- [x] **I7 测试自身的假绿：L7g 把"跳过"记成 PASS**：本机观察不到僵尸窗口时它调 `ok()` —— 未执行的断言
  被计成通过。修法：新增 `skp()` 与 `SKIP` 计数（汇总打印"跳过 N"），L7g 改用 `skp`。
- [x] ~~**接缝缺口（留给下次，已如实记录）**：① `app-harness` 的桩跑不通 `KB.readFile` 路径（桥要先做执行
  模式探测，桩只答了 `panel`/`rm`）~~ → ❌ **① 是误判，已在 Phase 35.1 纠正**：桩**确实**把
  `cat '<path>' 2>/dev/null && echo __READ_OK__` 发出去（实测命令日志可见），假绿的真因是
  **异步渲染没等到拍**（`renderAccelCur()` 的调用点不 await 它；同上 §34.1 里 `tick()` 那段注释
  记的正是同一件事）。B5b 已从源码扫描升级为真 DOM 断言，并经变异实验证明能精确变红。
- [ ] ② 想给 I5 做真回归需要"并发扫描"的时序桩 —— 这条**仍然成立**，属下一轮测试基建。

## Phase 35 · 上游同步 v1.9.4 → v1.9.5 + 发布 v1.9.5-r1 ✅ 2026-09-29

> 按 `AGENT-CONVENTIONS §7` 八步走的记录（以本地 diff 为准，不读 release notes）。

- **规模**：131 文件 / +10259 −1161。主要内容：新增 `/v1/responses` 翻译器（Chat↔Responses 双向 +
  `reasoning_details`）、Dashboard 更新弹窗四个缺陷（#27/#31）与备份流修复（#32/#34）、配额读取节流（#30）、
  `/v1/models/<model>` 与列表一致（#28）、mimo `reasoning_effort` 按模型级别降级、全仓 gofmt。
- **补丁裁决（§7.2）**：`tools/patches/media-web-fetch-route.patch` **保留**（`/web/fetch` 两路由 +
  `/api/health` + SSO 501 仍在本仓、上游未吸收）。
- **冲突（§7.3，仅 3 处）**：`README.md` → 按 §10.2 取我们的；`CHANGELOG.md` → 取我们的（含模块段）
  并把上游 `[v1.9.5]` 小节插回；`web/src/components/ProfileSettingsView.svelte` → 取我们的
  （密码弹层备份交互）——**上游本版也改了这个文件（备份流四缺陷 #32/#34）→ ✅ 已复核，结论见 Phase 35.1**
  （缺 (b) 取消守卫，同根因的 (a) 解嵌套也缺）。
- **门禁（§7.4–7.6）**：`gen-schema --check` ✅ 一致；`check-parity` ✅ 无新增缺口（基线 131 条）；
  离线档 **16/16**（一次运行出现"失败 1"，随后两次复跑均 0 失败 → 记为**抖动**，待观察）；
  `build.sh` 七步绿；真机用**我们自己的入口** `ops.sh install-module` 装包成功：
  `module_version=v1.9.5-r1 / versioncode=109050 / engine_version=1.9.5 / engine=up dns=up watchdog=up / health=200`，
  并看到"每小时内存基线"在真机线上生效（`引擎内存 26148kB … 已运行 7240s`）。
- **版本物料**：`module/module.prop` v1.9.5-r1 / 109050；`update.json` 同步；`VERSION` 随上游为 `1.9.5`。
- [x] **发布完成（2026-09-29）**：经 `ALL_PROXY=socks5h://127.0.0.1:7890`（本机代理让**代理侧做 DNS**，
  绕开污染；`gh` token 由一次性 credential helper 注入，未落盘）推送 33 个提交 + `v1.9.5-r1` tag，
  GitHub Release 已附 `9router-go-1.9.5-r1-magisk.zip`；并用 `update.json` 里那个 `zipUrl` 实测
  **HTTP 200**（模块更新器的下载路径打通）。
- 注意：`.github/workflows/release.yml` 是**上游继承**的引擎发布流，触发 `v*` 只产出引擎二进制，
  模块 zip 必须手工附加（本次即如此）。

### Phase 35.1 · 发布后复核：svelte 备份流 + 测试基建误判纠正 ✅ 2026-09-29

> 起因：用户问"第 1、3 点（svelte 待复核 / 接缝缺口）应该怎么修最好"。两条都先取证再动手：
> 第 1 条用 `git show <上游提交>` 逐条比对语义（**不是数标记** —— 见下），第 3 条先写探针实测。

- [x] **1 · 备份流复核（对上游 #32/#34 逐条比对语义）**：结论是**四缺陷里我们已修三条、缺一条**：
  - **(c) 两动作互斥** ✅ 已有（两按钮互相 disable）+ 密码在首个 await 前快照 ✅ 已有。
  - **(d) 服务端 contract 测试** ✅ 已在（`TestHandleImportDatabase_ClientContract`）。
  - **(a) 错误文案被吞** ❌ **我们也有同根因（症状不同）**：`/api/settings/database` 在
    `IsAlwaysProtectedPath` 里 → 无会话请求由 `RequireDashboardAuth` 用
    `handlerutil.WriteJSONError` 拒绝，而它写的是**嵌套** envelope
    `{"error":{"message":…,"type":…,"code":…}}`；而 `db-backup.ts` 的 `responseErrorMessage`
    只认扁平 `{"error":"…"}` → `typeof data.error === 'string'` 不成立 → **一律回落成
    "Failed to export database"，真实原因（会话/密码）被吞**。修法：解包交给 `formatApiError`
    （递归取 message/error/detail…），保留"非 JSON / 无可读文案 → fallback"两条口径。
    **回归**：`web/src/lib/db-backup.test.ts` 新增嵌套 envelope 用例（修复前**必红**，已实测）。
  - **(b) 取消不生效（破坏性）** ❌ **我们缺这条**：`Modal` 的 Escape / 遮罩 / ✕ 全接
    `onClose={closeDbAuth}`，而我们原来的 `closeDbAuth()` 只是清状态 —— 用户在导入途中按 Esc
    以为取消，POST 照跑、跑完 alert 成功并刷新 → **数据库已被覆盖**。修法：新增唯一在途判定
    `dbRequestInFlight()`，`closeDbAuth()`/`openDbAuth()` 都在它面前止步（关闭路径唯一）。
  - **顺带 backport**：上游 `851070e`（**晚于 v1.9.5**，不在本次同步范围）把界面里显示的
    "真实默认密码" `Mantep210` 换成文档值 —— 我们这版同样写着 `Mantep210`（提示 + 当前密码
    placeholder），而**我们自己的文档与面板其它处一律是 `123456`**（README/MAGISK.md/CONTEXT.md），
    照抄上游默认密码既误导用户又在公开仓库里泄露它 → 2 行改为 `123456`。
  - **教训**：**复核上游修复不能数标记**。我们文件里 `closeDbAuth` / `responseErrorMessage` /
    `isDownloadingBackup || isImportingBackup` 全都在（名字都在！），但 `closeDbAuth` 少了
    "拒绝在途请求"这半句语义、`responseErrorMessage` 少了"解嵌套"这半句 —— 按名字比对会得出
    "四条都已修复"的错误结论。
- [x] **3 · 测试基建误判纠正（原"接缝缺口 ①"）**：
  - 探针实测：桩**会**发出 `cat '<path>' 2>/dev/null && echo __READ_OK__`（命令日志可见），
    与 34.1 记的"读文件命令根本没发出去"相反。
  - 判定性实验：把 `clearAccel` 变异成无参 `renderAccelCur()` → DOM 断言**精确失败**并报出
    `「未知（读取失败）」`；正确代码下绿。→ 断言从来就是有效的，**假绿的真因是缺一拍 `tick()`**。
  - 修法：`page-flows.test.js` 的 B5b 由源码扫描升级为真 DOM 断言（含"为什么升级"的注释）；
    34.1 那条误判已就地划掉并指向本节；harness 本身**无需改动**。
- **门禁**：`bun test`（Dashboard 纯函数）**100/100**（新增 2 条，其中嵌套 envelope 一条修复前红）；
  面板 `node --test` **128/128**；面板 JSTYPES **0 错**；`tsc -b`（Dashboard）**0 错**；
  LIFECYCLE **55/0（+1 跳过）**。
- [x] **r1 就地替换（用户决策：不发 r2，"这问题本就该在 r1 解决"）✅ 2026-09-29**
  - 顺序（每步都有理由）：提交修正 → `FORCE=1 bash build.sh` → `gh release upload --clobber`（同名 →
    URL 不变）→ tag 移到修正提交 → **再 `gh release edit --draft=false --tag v1.9.5-r1`**。
  - ⚠️ **必须 `FORCE=1`**：`build.sh` 步骤 1 在 `web/dist/index.html` 存在时会**跳过前端构建** ——
    否则就是"改了 `web/src` 却发了旧包"，正是 build.sh 里那段防的静默回归。
  - ⚠️ **改动 `web/src` 后，入库的 `module/bin/9router-go` 也要提交**（build.sh 步骤 5 会重编它，
    而它是入库文件）→ 否则出现"tag 里的二进制是旧的、release 资产是新的"不一致。
  - 🔴 **踩到的坑①：删掉远端 tag 会把 Release 转成草稿**（URL 变 `untagged-…`、**公开下载 404**），
    而 `gh release view`（走 API）**看起来一切正常** —— 只有 `curl` 走**公开 URL** 才暴露。
    → **纪律**：移动/删除 tag 后必须复核 `isDraft:false`，并用公开 URL 下载比对 sha256。
  - 🟠 **坑②：本机 `ALL_PROXY=socks5h://127.0.0.1:7890` 会截断 15MB 资产**（实测收到 9 字节
    "Not Found" 与一次 13.7MB 半包）→ 取发布资产改用 `192.168.10.7:7890`（手机上的 mihomo）即完整。
  - **证据**：新包 sha256 `c64d6f87…`（旧 `027bbbc8…`，15168839 vs 15168697 字节）；`Mantep210` 在
    `web/dist`、引擎二进制、包内引擎里**均为 0 次**（旧包是 1）→ 证明引擎嵌入了新前端；公开 URL
    下载回来逐字节相同 + `unzip -t` 无错；真机用 `ops.sh install-module` 装该包 → `engine=up`（4.21s）、
    `module_version=v1.9.5-r1` / `versioncode=109050` / `engine_version=1.9.5`、`engine/dns/watchdog=up`、
    `health=200`（端口 20128）。
  - **已知副作用（用户确认接受）**：`versionCode` 保持 109050 → 更新器不会对**已装 r1** 的设备提示更新
    （比的是 versionCode，不是字节）；新装/手动重装不受影响。

## Phase 36 · 可用模型里出现内部节点 ID（用户反馈，v1.9.5-r1）✅ 2026-09-30

> 反馈：「最新的版本里，清理孤儿数据之后，还是会出现 `openai-compatible-chat-.......` 的模型」。
> **拿不到他的数据、也无法在他的环境复现** → 按 `diagnosing-bugs` 的纪律改成**离线夹具**建回路：
> 用他数据的形状（节点 ID 形状 + kv 键 = `<节点ID>|<模型>|llm`）+ 真实代码路径。

- [x] **反馈回路（先红后绿）**：`go test ./internal/handlers/chat/ -run TestHandleModels_NoRawNodeID -v`
  - 修复前**红**，且输出与截图**逐字一致**：`openai-compatible-chat-63ab874d-…/deepseek-flash`、`…/识图模式`
    （覆盖"有连接"与"无连接"两条路径）；修复后**绿** ✓ 0.07s 确定性 ✓
- [x] **根因（三层，全部读码确认）**：
  - `kv.customModels` 的键按**节点 ID** 存（设计如此）；列表发布 `<前缀>/<模型>`，前缀来自
    `providerNodes.data.prefix`（连接路径用连接 `providerSpecificData.prefix`），
    **取不到就回退成节点 ID**（`models_list.go:641-650` / `appendConnectionModels` 的 `providerID` 兜底）。
  - `ProviderNodeData` 结构连 `name` 都没有 → 历史数据（旧版本 / 恢复备份 / 别的客户端建的节点）
    只要有 name 没 prefix，就**永久**以内部 ID 出现。
  - 这类行**不是孤儿**（节点或连接还在）→ 清理按设计不会删 → **"清理了也没用"是必然**，清理没坏。
- [x] **修法（改数据，不是改显示）**：前缀**就是客户端要填的模型 ID**（路由按前缀解析，
  `resolution_test.go:157` 为证）→ 只改显示会出现"列表写 A、调用必须用 B"。
  - `internal/db/heal.go` `HealProviderNodePrefixes`：只补空、name 派生（`/`→`-`、空白→`-`）、
    冲突加 `-2`…、单事务、幂等、**不动 `updatedAt`**（那是"用户编辑过"的语义）。
  - `internal/handlers/chat/prefix_heal.go`：读路径自愈，**节流 60s**（列表可能被高频轮询）；
    `internal/app/database.go` 启动时也跑一次（best-effort，与 leases 同风格）。
  - `appendLooseCustomModels`：**节点 ID 形状且既无节点又无连接**的别名不再发布（它路由不了）；
    **有连接的绝不在此列**（悬空连接仍可路由，删掉就是误伤）。
- [x] **不误伤（守卫测试 8 条）**：已有前缀不覆盖 / 幂等第二次 0 改动 / 同名冲突解 / 无 name 不猜 /
  名字含 `/`与空白被规整 / 内置 provider（无节点）不碰 / **json 往返不丢 apiKey** / 悬空连接的模型仍在列表。
- [x] **真机验证（同一形状）**：设备库插一个"有 name 无 prefix"的探针节点 → 装新引擎 →
  日志 `[db] 展示前缀自愈：补回节点 1 个 / 连接 1 个`（那个连接是**库里原本就有的同类历史数据** ✓）→
  探针 `prefix=ZZHealProbe` ✓ → 完整性：`customModels` **978 行不变**、18 条连接凭据完整
  （3 条 oauth 的 access/refresh token 仍在）→ 探针已删、节点数回到 13 ✓。
- [x] **面板侧守卫**：新增两条（`parsers.test.js`：只剩连接的 UUID 别名不算孤儿 + 连接 provider 必须进存活集合；
  `bridge-commands.test.js`：扫描/复查 SQL 必须含 `providerConnections`）。面板 JS **130/130** ✓。
- **有意不做（否则会产生新问题）**：① 不改"有连接=存活"的清理判据 —— 改成"无节点即孤儿"会误删
  内置 provider 的自定义模型（本机库里有 `openrouter/oc/qd` 共 31 行这种合法数据）；② 不给没有 name
  的节点编前缀（猜出来的前缀会改变模型 ID 却没人知道它从哪来）。
- **既有的测试隔离问题（如实记，非本次引入）**：`chat` 包全量跑时 `TestHandleChangelog` 会红，
  与两条外网 E2E（`TestLiveE2E_Cline_SmartCombo` / `TestIntegration_OpenCode_MuseSpark13`，403 RegionError）
  同跑时出现；单独跑绿（0.4s）。本次改动未触及 changelog/update，`-skip 'TestLiveE2E|TestIntegration'`
  后整包 `ok`（100s）。
- **门禁**：`go build ./...` ✓、`go vet` ✓、离线档 **16/16** ✓、面板 JS **130/130** ✓、新回归红→绿 ✓。

### Phase 36.1 · 用户澄清后收紧：内部 ID **彻底不再出现**（不是"旧 ID 仍可用"）✅ 2026-09-30

> 用户原话：*"openai-compatible-chat-<uuid> 旧的 ID 需要彻底不再出现，把问题彻底解决，并且不会再发生"*
> —— 不接受"显示正常但旧 ID 仍可用"作为终点。据此把目标改成**硬不变量 + 根因预防**。

- [x] **硬不变量（显示层兜底）**：客户端拿到的模型 ID 里**绝不允许**出现内部节点 ID 形状。
  - `appendConnectionModels`：连接解析不出任何前缀（连接上没有、注册表也没有）且 provider 是内部 ID 形状 → **不发布** + 记日志 ✓
  - `appendLooseCustomModels`：解析顺序改为 节点前缀 → **连接前缀**（与连接路径同源）→ 内部 ID 形状则**不发布** ✓
  - 形状判定收成**唯一所有者** `db.IsInternalNodeAlias`（chat 包只是短名字转发 ✓）；面板 JS 那份要保持同形状 ✓
- [x] **悬空连接不是"改名"，是"补回节点"**：读码确认路由**只认节点前缀**
  （`resolvePrefixProvider` → `GetProviderNodeByPrefix` ✓）→ 只换显示名会得到一个**能看不能用的假名字** ✗
  （比内部 ID 更糟）。修法：按连接上的 provider（原节点 ID ✓）**补回节点** —— name 取连接名 ✓，
  `apiType`/`baseUrl` 从连接的 `providerSpecificData` 继承 ✓，prefix 由 name 派生（冲突加后缀 ✓）。
  **测试同时断言"能列出"与"能路由"**（`resolveModel("<名字>/<模型>")` → 原节点 ID + 该连接 ✓）。
- [x] **无名字的悬空连接**：宁可不发布（列表侧藏起来）也**绝不编假名字** ✓（测试锁住 ✓）。
- [x] **根因预防（"不会再发生"）**：`DeleteProviderNode` 改成**单事务 + 不再吞第二条语句的错误** ✓——
  过去的"先删节点、再删连接（`_, _ =` 丢错）"正是半状态的来源 ✓；回归测试锁"节点与连接一起没 + 不误删别的 provider" ✓。
- [x] **自愈里我自己踩的坑（如实记）**：早退条件 `len(plans)==0 && len(known)==0` 会跳过
  **一个节点都没有的库** ✗ —— 而悬空连接恰恰只在这种库里出现 ✓（于是"补回节点"永远不执行 ✗）。
  修法：连接列表改成事务前先读 ✓，早退条件补上 `&& len(connList)==0` ✓，并由"悬空连接"用例锁住 ✓。
- [x] **测试**：`internal/db` **10 条全绿**（补空/不覆盖/幂等/冲突/无名字跳过/规整/内置不碰/**悬空补回节点**/**无名字不补**/**原子删除**）；
  chat 侧四条（有连接/无连接/悬空可路由/无名字不发布）+ 不变量 `assertNoInternalAlias` ✓；整包 `-skip 外网E2E` **ok** ✓。

## Phase 37 · 架构扫描：C2 深化（install 全路径可测）+ B1 并发残留 ✅ 2026-09-30

> 起因：用户问"还有其他 bug 或者该提升的地方吗"（附架构扫描技能）。扫描给出 6 个候选 +
> 1 个疑似真 bug；用户选择按 **Top 推荐（C2）** 修，并明确"确认修复完成后再发布"。

- [x] **B1（扫描抓到的真 bug，已核实并修）**：`cleanOrphans` 的快照用本批**冻结的 `targets`** ✓，
  删除却用 **`state.orphans`** ✗ —— 而「检查孤儿数据」按钮在清理期间**未被禁用** ✗ → 在快照的
  await 窗口点它会换批：本批漏删、另一批**没有快照可回滚却被删**，界面照样报「✅ 已一次性清理 N 项」
  （谎报成功）；批次变空时 DELETE 退化成 `... AND ()` 语法错，仍然报成功。
  **这是 I5（2026-09-29）只做了一半的残留**（当时只让快照用了 `targets`）。
  修：删除改用 `targets` + 空批守卫 ✓；回归 `orphan-scan.test.js`「快照在途时并发扫描」——修复前**精确红** ✓。
- [x] **C2（Strong，用户选定）**：`ops.sh` 把 `MODDIR` 写死成"脚本所在目录的上级" → install 的
  **成功与回滚**两条路只能真写设备 → **零测试**；唯一顺序保障是 `test-install-gate.sh` 的 grep 行号
  （文本形状 ✗，重排 `ops.sh` 会静默撤掉护栏）。
  - 改：`MODDIR="${MODDIR:-…}"`（与 `DATA_DIR` 同形，真实调用方不设该变量 → 行为不变 ✓）+
    `OPS_LIB_ONLY=1` 只加载定义（此前**裸 dispatch** 让 `ops.sh` 无法被 source ✗）→ 可在临时
    MODDIR 上重放整个安装流程 ✓。
  - 新增门禁 **INSTALLFLOW（24 例）**：成功路径（引擎起来**才**写版本、**才**刷 `.bak`、清 `.prev`）／
    回滚路径（起不来 → 二进制回滚 + **绝不谎报版本**）／门禁（太小、非 ELF、缺源 → 绝不碰现有二进制；
    当前二进制本身不合格时**不把它当回滚点**）／装包（`module.prop` 换新 + 包内引擎版本落盘 + 留档）。
  - **安全护栏**：测试先断言 `MODDIR` 真的指向临时目录，否则**直接失败** ✓ —— 绝不写仓库里的真实 `module/`。
  - 踩坑（如实记）：三份假引擎内容原本完全相同 → cksum 分不出"新装的那份"与"回滚回去的那份" →
    "失败的新引擎还在"会**假绿** ✓ 已让每份带唯一标记 ✓。
- **层次归属（用户关心的那件事）**：本 Phase 改的是 `module/**` + `tools/**` —— **我们自己的层** ✓，
  上游更新不会碰 ✓；而 Phase 36 / 36.1 改的是引擎（`internal/**`）与 Dashboard（`web/src/**`）——
  **属于上游** ✓ → 已按 ADR-0003 登记（补丁存档 ×2 + ADR 表两行 + `AGENT-CONVENTIONS §10.2` 两行含复核点）✓。
- **门禁**：离线档 **17/17**（新增 INSTALLFLOW）✓、面板 JS **131/131**（新增并发扫描不变量）✓、
  `internal/db` 10/10 ✓、真机冒烟（deploy + `panel`/`get-port`）+ 装机实测见 Phase 38 ✓。

## Phase 38 · 架构深化收尾：C1 / C3 / C4 / C5 / C6 ✅ 2026-09-30

> Phase 37 之后用户要求"继续优化，直到全部完成" —— 把架构扫描报告里剩下的 5 个候选全部做完。
> 每个都遵循同一纪律：**先写会红的门禁 → 再改**（C3 的首次运行就直接抓到真实漂移）。

- [x] **C1 · 两个求值器 → 一个**：`planSteps`（按计划顺序 walk，但**生产零调用**、只有测试引用）
  与 `planGate`（生产唯一入口，却只用计划判断"阶段是不是门禁"）并存 —— 于是"顺序离线可断言"
  实际只断言了常量数组，**把门禁挪到安装之后不会有任何门禁变红**。
  做法：①**删掉 `planSteps`** 及其 9 条"测退役求值器"的用例（假信心）；②**顺序改由流程用例守** ——
  `gate-flows.test.js` 新增真实命令序列断言（ENGINE_UPDATE 的体积/魔数/校验和都必须早于
  install-engine；DNS 的回滚点必须早于改写）；③新增两条不变量：**计划结构**（各计划里门禁的位置）
  与**调用点一致**（`planGate(KP.X_PLAN,'phase')` 的阶段名必须存在于该计划且是门禁 ——
  拼错会让门禁被**静默当成非门禁跳过**）。
- [x] **C3 · 跨语言形状判定接缝门禁**：面板 `UUID_ALIAS` 与引擎 `IsInternalNodeAlias` 是同一形状的
  两份实现，此前只有注释兜底。新增**共享样本夹具** `tools/fixtures/internal-node-alias-samples.txt`
  （16 条正/负样本），Go（`heal_test.go`）与 JS（`parsers.test.js`）**各自断言逐样本一致**。
  **首次运行就抓到真实漂移**：Go 侧区分大小写（缺 `(?i)`），面板带 `/i`
  → 大写节点 ID 会"引擎判不是、面板判是"。已对齐；并用变异（拿掉 JS 的 `/i`）证明门禁能精确红。
- [x] **C4 · 加速选中读取收成一处**：`KB.readFile(ACCEL_SEL)` 原先内联 5 处，而"空串=直连、
  `ok:false`=读失败（≠没选）"这条知识没有家 —— I6 正是漏掉最后一条。新增 `readAccelSel()`
  返回判别式 `{ok, sel}`，6 个调用点全部改走它；新增用例锁"内联只剩 1 处（在入口里）"与
  "读失败必须可辨"（locality + 判别式语义，机制可判定）。
- [x] **C5 · shell 侧顺序不再靠行号**：`test-install-gate.sh` 的 I4 用 grep 比较"门禁行号 < stop_all
  行号"——文本形状，格式化/重构即可打穿，且其前提"成功路径不能跑"已被 INSTALLFLOW 取代。
  已删除 I4：坏包是否停服务由同文件 I1b/I2b 的**行为**断言（真 lifecycle + 临时 DATA_DIR 下，
  那条"活着的引擎" pidfile 就是判据），成功/回滚全路径由 `tools/test-install-flow.sh` 覆盖。
- [x] **C6 · 承载性 env 收成一份声明**：清单（`life_carrier_env_keys`）/ 六行 `echo` / 内联 `export`
  曾是**三份并行实现**（ADR-0006 声称清单是唯一来源，但代码里清单并不驱动写出）。
  做法：新增 `life_carrier_env_value`（键 → 值的唯一来源）+ `life_carrier_env_export`（兜底按清单导出），
  `life_write_runtime_env` 改为**按清单遍历写出**；**值可空但键必齐**（少一个键 = 引擎少一份承载性
  env，比空值危险）。离线新增 **L13**：写出物键集合 == 清单、键都有非空值、内联兜底导出的键 == 清单。
- **门禁**：离线档 **17/17** ✓、面板 JS **128/128**（删 9 条假信心 + 新增 8 条）✓、
  `internal/db` 全绿 ✓、chat 整包（跳过外网 E2E）ok ✓、LIFECYCLE **59/0（+1 跳过）** ✓；
  **真机**：部署 + `restart-engine` → `engine=up`、**T7 等价检查**（6 个承载性 env 在 `runtime.env`
  与引擎进程环境里都齐）✓、`dns/watchdog=up` ✓。
- **层次归属**：本 Phase 全部落在 `module/**` + `tools/**` + `docs/**`（**我们自己的层** ✓，
  上游更新不会碰）；仍无新增引擎侧改动，故 ADR-0003 无需新增登记 ✓。

## Phase 37 · 「程序崩溃 + 内存 571MB」诊断：面板/守护把复用的 pid 当引擎（自愈失效）✅ 2026-09-30

> 用户反馈：引擎反复"崩溃"，面板显示引擎内存 571.7MB。提供了三份日志（9router.log /
> watchdog.log / dnsfwd.log）+ 面板截图。设备不可达、无法复现 → 按诊断纪律全程**离线取证 +
> 代码比对 + 日志考古**，每个假设都落到可证伪的判据上，不让用户白跑一趟。

- [x] **诊断结论（证据链，先于修复）**：真正的故障**不是内存泄漏**，而是
  **`life_engine_healthy` 只看 pidfile 存活、不校验身份** —— 引擎死掉后 pid 被无关进程复用
  （他的设备 82 分钟推进了 12943 个号：6965 → 19908），于是：
  - 面板照报 `engine=up`，守护在 13:56–14:35 **39 分钟**里零判死零拉起（watchdog.log 为证）→
    **自愈形同不存在** —— 这才是用户体感的"崩溃了没人管"；
  - 面板/守护把**那个无关进程的 RSS**（484MB / 571.7MB）当成"引擎内存" ✗ —— 内存症状本身是
    **误读**（12:29 那条"跑了一天的引擎只有 3.4MB"是同一枚硬币的另一面）。
  - **判死推理（不靠猜）**：14:18 新引擎成功 bind 20128 → 老引擎必然已死（否则端口被占）；
    而此前守护/面板一直报 up → 它们被 pid 复用骗过 ✓。此前的"不是慢泄漏/是尖峰"等内存结论
    **全部作废** —— 那些读数在两个方向上都不可信。
- [x] **为何面板会撒谎（代码层）**：`life_state` 里守护走 `life_wd_alive`（存活 + cmdline
  指纹 ✓），引擎却只走 `life_pid_alive`（pidfile 存活 ✗）—— **同一个坑，守护侧修过（注释里
  就写着"少了这层，面板会误报 up、且永不重新拉起"），引擎/dnsfwd 半边一直缺**；离线门禁 L3
  也只覆盖了守护。`life_rss_kb` 的两个读数点（panel / 守护内存证据）同样无身份校验。
- [x] **修复（lib/lifecycle.sh + lib/ops.sh + lib/watchdog.sh）**：
  - 新增 `life_pid_file_is_bin`：pidfile → 身份校验，按 **basename 松匹配**（exe 链接优先，
    兼容模块更新后的 " (deleted)"；cmdline 兜底）—— 与守护侧同一条"宁松勿严"取舍：
    **两条身份信息都读不到时判"在"**（宁可漏判一次复用，绝不把活着的引擎判成不在 →
    不制造重启风暴）；读 pid 用内建 read（热路径 0 次额外 fork）。
  - `life_engine_healthy` / `life_dns_running` = 存活 **+ 身份**；RSS 读数（panel / 守护）
    只在身份确认后才读，否则报 0（面板渲染 "-"，如实表示"没有可报告的内存"）。
  - 顺手修证据标签：守护内存行的 `已运行 Ns` 实为**守护**运行时长（本轮它把诊断引向
    "新进程 12 分钟涨到 484MB"的错误结论）→ 改为 `守护已运行 Ns`。
- [x] **回归（先红后绿）**：`tools/test-lifecycle-lib.sh` 新增 **L3c/L3d/L3e**：无关进程占
  pidfile → `life_engine_healthy` 必须为假、`life_state` 必须报 `engine=down`（修复前**红**：
  60 过 / 2 失败）；**反向用例**锁"自己的引擎必须判在"（防过严 → 重启风暴；修复前后都绿）。
- **门禁**：LIFECYCLE **62/0（+1 跳过）**（修复前 60/2）；完整离线档 **17/0** ✓；真机门禁
  T2/T3/T15 语义不受影响（它们靠"引擎**真死**"触发，与"活着但不是我们"正交）✓。
- [x] **真机验证（192.168.10.7）**：①真引擎判"在" ✓（无误判 → 无重启风暴）②无关 sleep 进程
  占 pidfile 判"不在" ✓ ③把守护（sh 脚本）塞进引擎 pidfile 也判"不在" ✓（辨析力）④dnsfwd
  判"在" ✓；**端到端复现事故形状**：pidfile 塞入无关 pid → 面板如实 `engine=down` +
  `engine_rss=0`（修复前：`engine=up` + 别人的内存），恢复后 `engine=up` / RSS 如实 ✓，
  全程未惊动服务 ✓。
- **遗留给下一轮（与本次根因无关，但都是真的）**：
  ① 引擎**几分钟就死**且死因无记录（9 次判死全走轮询路径，没有一条 `引擎退出（…）`）→
  判死时应记 `life_exit_reason` + cgroup `memory.events` 的 `oom_kill` 计数（判"是不是被
  内存回收杀的"的唯一客观证据）；② 他的自建 DNS 占了 loopback:53、模块转发器被用户关闭
  （`dnsfwd disabled` ×27 证实）→ 引擎解析全走 `[::1]:53` 失败（catalog/版本检查全挂）——
  配置使然，但引擎 resolver 应能指向用户自己的 DNS；③ 他曾把引擎跑在 45300 而某连接也指向
  45300 → 建议加"拒绝转发到自身端口"守卫；④ 模块从不设 PATH（全靠继承），旧守护曾在 PATH
  异常的上下文里连 `sleep/date` 都执行不了（`/system/bin/sleep: No such file` ×340，r1 重写
  后未再现）→ 值得显式设 PATH 兜底。

## Phase 38 · 死因取证落地 + 全量架构扫描（9 项，8 修 1 记）✅ 2026-09-30

> 用户决策："把死因取证做了，以后能查到崩溃情况；再扫一遍有 BUG 就修，没有就发包。"
> 扫描按热区加权（Phase 33–38 连续改动的 module/lib 三件套 + 引擎转发路径 + webroot 桥）。

- [x] **死因取证（Phase 37 遗留①）**：判死时进程已消失，wait 不可得 → 能拿到的客观证据
  只有两样，现在两条判死路径都记：
  - **CHLD 路径**：退出码旁增记 `oom_delta`（cgroup `memory.events` 的 oom_kill 相对基线的
    增量 —— 137=SIGKILL 时它回答"是不是被内存回收杀的"）。
  - **轮询路径**：`死因取证：pid=… 已消失 / 仍活着但已不是引擎（号被复用）；oom_kill 增量…；
    引擎最后 cgroup=…`——"号被复用"正是 2026-09-30 事故的形状，没有这行只能考古。
  - 支撑纯函数（离线可断言）：`life_cgroup_memory_events`（路径映射，拒相对路径/`..`）、
    `life_oom_kill_count`（读不到**不冒充 0**）；守护侧 `oom_delta`/`mark_engine_alive`。
  - 基线策略：`oom_base` 在拉起/收养时采样、保持粘性（增量窗口 = 自引擎拉起以来，不因
    每轮刷新被抹掉）；`eng_cg` 每轮刷新（v2 per-pid cgroup 随旧进程消失，架构审查 S6）。
  - 真机门禁 **T2b**：强杀引擎自愈后，watchdog.log 必须出现"死因取证"行。
- [x] **全量扫描修复（8 项，全部带门禁）**：
  - **S1 自环转发无守卫（高）**：baseUrl 配成本代理监听地址 = 请求链路回到自己，每跳真实
    消耗记账/落库/goroutine。新增 `internal/handlers/chat/selfloop.go`：**窄比较**（回环主机
    + 本机端口才判自环；局域网 IP/其他端口不受影响 —— 不复用 AssertPublicURL，它会把合法
    的本机 Ollama 一并误杀）。守卫放在 `getProviderConfig` —— chat/media/responses 全经它
    取上游配置，一处覆盖全部转发路径。回归：纯函数表驱动 + `GetProviderConfig` 拒绝/放行
    两断言。
  - **S2 全模块不设 PATH（高）**：sleep 失效会让 wait.sh 空转跑满次数（restart 谎报
    engine=down）。5 个入口脚本（service/ops/action/watchdog/uninstall）各加一行显式 PATH
    （toybox 已知路径在前、调用方 PATH 追加在后）。
  - **S3 对非子进程 wait（中）**：ensure 返回 `running`（引擎是别人起的）也记 `*_ours` →
    死亡时 wait 非子进程 = 伪造"退出码 127"并绕过去抖。现在只有 `started` 才记账。
  - **S4 ev_chld 清位吞事件（中）**：引擎在处理段死亡 → CHLD 置位后被无条件清零 → 死因
    永久丢失、自愈退化成最多 120s。现在有 `*_ours` 时保持置位交确认分支消费。
  - **S5 身份指纹裸子串（中，Phase 37 自己引入的反向口子）**：cmdline 含 "9router-go" 即
    认亲 —— WebUI 高频跑 `sqlite3 …/9router-go/db/…`，pid 恰被它复用时误报复发。改按
    **完整参数段**匹配（heredoc 逐行，零 fork）。
  - **S6 取证锚点陈旧（中）**：eng_cg 只在为空时记 → 引擎被外部替换后 cgroup 是旧的 →
    死因取证恒"不可读"。现在每轮刷新（oom_base 保持粘性）。
  - **S7 install-module 顶层 mv 静默失败（中低）**：如实 `install-failed` + 释放维护窗口 +
    把服务拉回来（目录已换无法完整回滚，至少不谎报）。
  - **S8 关键文件非原子写（中低）**：runtime.env / dns-upstreams 改 `.tmp + mv`（照抄
    bridge.js writeFile 的既有惯例）—— 半份 runtime.env 会让下次 source 端口漂移。
  - **S9 webroot 桥（低）**：哨兵改 `lastIndexOf`（readFile 内容恰含 `__KMOD_DONE__` 时
    旧实现提前截断；新增回归用例，旧实现必红）；probe() 超时/异常路径补注销全局回调。
- **观察项（不修，如实记录）**：bridge.js 的 heredoc 定界符与 `${f}` 未走 shq（当前不触发）；
  库内全局变量纪律的三处既有裸名例外（当前无嵌套使用关系，属脆弱点非现行 bug）。
- **门禁**：LIFECYCLE **70/0（+1 跳过）**、完整离线档 **17/0**、面板命令层 **27/0**（+1）、
  Go chat 包（含新增 2 个自环用例）全绿；真机 T2b 随设备门禁验证。
- **真机验证踩出的三条现场教训（均已回写代码/流程）**：
  ① 部分设备（含 192.168.10.7）**根 cgroup 根本没有 `memory.events`** → 取证行若按
  "有值才记"会被整个吞掉 → 改为**无条件落盘**、读不到就写明原因（commit 80fa723）；
  ② **守护是长驻进程**：install-module 只重启引擎不重启守护，新 watchdog 代码要等
  重启守护/重启手机才生效 —— 装机验证取证行时必须先重启守护（这也是普通用户更新后
  要重启一次手机的原因，写进 release notes）；③ 经本机代理推 main 大包（含重编的
  30MB 引擎二进制 delta）会 TLS 掐断 → `git -c http.version=HTTP/1.1` 一次通过。

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
| **生命周期自愈** | `kill -9` 引擎 | **1–2s** 内自动回来（事件驱动；旧实现 ≤12s），引擎由守护亲手拉起，cgroup=`/`；日志里应有 `引擎退出（原因）` 一行 |
| **脱组** | WebUI 点重启引擎 | 新引擎 cgroup=`/`（不再随管理器应用被清理） |
| **pid 复用（Phase 37）** | 把无关活进程的 pid 写进 `9router.pid` | 面板如实 `engine=down`、`engine_rss=0`（不再误报 up / 冒用他人内存）；恢复 pidfile 后 `engine=up` |

## Grilling 决策记录

**2026-09-25 · 第一轮**（用户答复：基本都按推荐）
- Q1 拉起策略 → **(a)** `restart-engine` 复用 service.sh，不造第二份启动实现
- Q2 等待语义 → **(a)** restart-engine 内置 20s 轮询，调用即知 `engine=up/down`，WebUI 与 action.sh 共用
- Q3 初始密码 → **(b)** 保持 123456 不随机生成，MAGISK.md 已如实更新（用户可自行在 Dashboard 改密码）
- Q4 engine_version → **(b)** 停止伪造，拿不到显示"未知"
- Q5 Phase 2 迁移 → **(a)** 渐进：先加命名操作，按类迁移，每类真机验收后再删旧路径

**计划外发现（Phase 1 验收中）**：mksh 参数展开模式 `|` 为"或"运算符的跨 shell 差异 bug，已修复并沉淀为教训（见 1.3）
