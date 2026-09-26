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
- [ ] **待用户发布**：tag `v1.9.2-r1` → 上传 zip → 推送 `update.json`
- [ ] **待决策**：`/api/health` 与 SSO 回调是否补（上游同样缺失，可顺手提 PR）；大块缺口是否排期

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
| **生命周期自愈** | `kill -9` 引擎 | 10s 内自动回来（守护拉起，cgroup=/），面板无需手动重启 |
| **脱组** | WebUI 点重启引擎 | 新引擎 cgroup=`/`（不再随管理器应用被清理） |

## Grilling 决策记录

**2026-09-25 · 第一轮**（用户答复：基本都按推荐）
- Q1 拉起策略 → **(a)** `restart-engine` 复用 service.sh，不造第二份启动实现
- Q2 等待语义 → **(a)** restart-engine 内置 20s 轮询，调用即知 `engine=up/down`，WebUI 与 action.sh 共用
- Q3 初始密码 → **(b)** 保持 123456 不随机生成，MAGISK.md 已如实更新（用户可自行在 Dashboard 改密码）
- Q4 engine_version → **(b)** 停止伪造，拿不到显示"未知"
- Q5 Phase 2 迁移 → **(a)** 渐进：先加命名操作，按类迁移，每类真机验收后再删旧路径

**计划外发现（Phase 1 验收中）**：mksh 参数展开模式 `|` 为"或"运算符的跨 shell 差异 bug，已修复并沉淀为教训（见 1.3）
