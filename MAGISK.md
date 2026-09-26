# MAGISK.md — 模块层使用与构建说明

> 引擎/Dashboard 本身见上游 `README.md`；本文件只讲模块层。

## 目录

```
9router-go-magisk/
├── cmd/ internal/ web/ ...   # 上游 v1.9.2 引擎源码（默认不改；ADR-0003 定点修复除外，补丁存档 tools/patches/）
├── module/                   # Magisk/KernelSU 模块层
│   ├── module.prop           # id=ninerouter-go + updateJson
│   ├── customize.sh          # 安装期：ABI 检查 + chmod 兜底
│   ├── service.sh            # 开机：ops.sh(prep-db/start-dns/seed-key) + 武装守护 + 引擎拉起
│   ├── lib/ops.sh            # 运维唯一实现（seam）：status/panel/prep-db/start-engine/stop-all/restart-engine/start-dns/watchdog-start/…
│   ├── lib/watchdog.sh       # 生命周期守护（只判进程在不在；仅开机上下文武装）
│   ├── action.sh             # 管理器「操作」按钮：状态显示（数据来自 ops.sh）
│   ├── uninstall.sh          # 卸载：停进程（保留数据）
│   ├── bin/                  # 9router-go（构建产物）、dnsfwd、sqlite3
│   ├── etc/schema.sql        # 数据库 schema 引导（上游 DATABASE.md 派生，构建期断言防漂移）
│   └── webroot/              # 模块 WebUI（KSU/WebUIX）：概览/DNS/一致性检查/更新
│       ├── index.html        # HTML 结构（MODDIR 由构建期注入 __MOD_ID__）
│       ├── parsers.js        # 纯函数解析层（可离线 node --test 回归）
│       ├── bridge.js         # root-shell 桥（串行队列 / CRLF / sqlFile 环境补偿）
│       ├── app.js            # UI 装配与事件
│       └── test/             # 解析层 fixture 回归测试
├── tools/                    # 工具与门禁：check.sh（唯一入口）、check-parity.py、check-ui-parity.py、
│                             # gen-schema.py、patch-clipboard.py、deploy-device.sh、build-dnsfwd.sh、
│                             # dnsfwd.c、parity-*.txt、ui-parity-*.txt、device/（真机门禁）、patches/
├── build.sh                  # 一键构建（7 步全校验管线，第 3 步复用 check.sh --offline）
├── AGENT-CONVENTIONS.md      # 工程契约（事实优先级/架构不变量/所有权/变更映射/门禁档位/ADR 触发条件）
├── CONTEXT.md                # 术语表
├── docs/TESTING.md           # 门禁台账（测什么/怎么跑/失败意味着什么/变更记录）
└── docs/adr/                 # 架构决策记录
```

## 门禁（唯一入口 `tools/check.sh`，清单与断言含义见 `docs/TESTING.md`）

```bash
tools/check.sh --offline   # 离线：shell 语法 / 模块 WebUI 46 例 / Dashboard 82 例 / go build+test / tsc / schema
tools/check.sh --device    # 真机：T1–T10（生命周期与安装）+ A1–A5（仪表盘 API）
tools/check.sh --parity    # 对照：端点 parity 棘轮 + UI 调用 parity（需 ../9router，UPSTREAM= 可覆盖）
tools/check.sh --all       # 三档全跑（缺前置 → SKIP 摘要，退出 0；--require-device/--require-parity 为严格模式）
```

- 模块 WebUI 三套离线回归（`module/webroot/test/`）：`parsers.test.js` 解析层 fixture、
  `bridge-commands.test.js` 命令构造器与成败判据、`contract-keys.test.js` 键契约门禁
  （shell 的 `emit` 键集合 vs `app.js` 消费键，源码即契约）
- 真机断言逐条含义、已知不绿灯（外网依赖用例）与**变更记录**都在 `docs/TESTING.md`
- 新增/修改/删除任何门禁断言 → 必须同步登记台账（`AGENT-CONVENTIONS.md §4`）

## 构建

```bash
./build.sh        # 产物 dist/9router-go-<版本>-arm64.zip
```

- web 前端：优先 bun，退化 npm
- 引擎：`CGO_ENABLED=0 GOOS=linux GOARCH=arm64` 静态编译，嵌入 `web/dist`
- dnsfwd：用仓库内预编译产物；重编见 `tools/build-dnsfwd.sh`（需 aarch64 sysroot）

## 安装与首次使用

1. KernelSU / Magisk 刷入 zip
2. 重启后引擎监听 **:20130**（默认，与上游一致），Dashboard 即引擎地址
3. 首次登录使用固定初始密码 **123456**（写入 `initial-password`）。引擎监听 `0.0.0.0:<port>`
   （默认 20130），模块 WebUI 会显示局域网地址 —— 安全性由密码与可选 `requireLogin` 承担
   （这是显式取舍，见 `AGENT-CONVENTIONS.md §10`）。
   用户登录 Dashboard 后请立即自行修改密码（不替用户生成随机密码）：
   `adb shell su -c "cat /data/adb/9router-go/initial-password"`
4. DNS 管理：管理器 → 模块 → WebUI
   - 设备上已有 DNS 服务（监听 127.0.0.1:53）时，内置 dnsfwd 启动会**自动让路**，
     引擎解析由已有服务接管（面板显示"已让路"）。开机含 5 秒让位宽限窗口，
     但 Magisk 服务先于普通 App 启动，极端情况下第三方服务晚到仍可能抢不到端口，
     此时在面板关闭 dnsfwd 再启动自己的服务即可
   - 引擎（纯 Go 静态二进制）域名解析**只认 127.0.0.1:53**；关闭 dnsfwd 且 :53
     无服务时模型域名无法解析，面板关闭时会如实警告

## 生命周期与守护

「跑一段时间后引擎未运行、Dashboard 打不开、必须手动重启」 的根因（2026-09-26 真机取证）：

1. 模块 WebUI 用 `ksu.exec`，它派生的 `sh` 是**管理器应用的子进程**（实测 cgroup
   `0::/uid_10235/pid_2661`）；`service.sh` 里的 `setsid` **只换会话、不换 cgroup**。
2. 系统清理该应用时（`dumpsys activity exit-info` 实测 `reason=10 USER REQUESTED /
   description=LockScreenClean`，同刻另有多个 App 被清），整组 SIGKILL ——
   引擎与 `dnsfwd` 同时静默死亡（无退出日志、无 OOM、无 panic）。
3. 老版本没有守护，死了就永久停机 —— 表现为"跑一段时间后必须手动重启"。

对策（两条独立防线，都要有）：

| 防线 | 实现 | 作用 |
|---|---|---|
| **启动即脱组** | `lib/lifecycle.sh` 的 `life_cgroup_escape`（写 cgroup 根 `cgroup.procs`） | 从根上不再随启动者（管理器应用）被清理 |
| **守护** | `lib/watchdog.sh`，由 `service.sh` 在**开机上下文**武装启动 | 任何原因（OOM/崩溃/被清/端口被抢）进程没了，10 秒内自己回来 |

**状态与意图只有一个所有者**（ADR-0005）：`lib/lifecycle.sh` 是唯一读写
`watchdog-armed/-hold/-req/-off`、`service-off` 与三个 pidfile 的地方；其它文件只表达意图：
`life_boot`（开机）/ `life_stop_user` / `life_start_user` / `life_restart_engine` /
`life_ensure_engine|dns` / `life_wd_should_supervise`。`ops.sh` 退为"配置与编排"（数据、端口、
安装、状态聚合），对外子命令集合不变。

守护的三条护栏：**只判"pid 在不在"**（不做错误率判据，否则上游 502 会把健康引擎反复重启）；
**连续两次判死才动手**；**维护窗口让路**（`watchdog-hold` 绝对到期时间，到期自动失效）。
`ops.sh restart-engine` 在守护在场时**委托守护**执行停止+启动，保证重启后的进程天然在免疫上下文中。

**用户意图能被尊重**：WebUI 概览的「停止服务」写 `service-off`（`life_stop_user`），
守护不会再把它拉回来；「启动服务」清除该意图并拉起。状态里 `engine=stopped` 与
`engine=down`（故障）是两件事 —— 前者是用户要的，后者才要查。

排查用（都不需要手动重启）：

```bash
su -c 'cat /data/adb/9router-go/watchdog.log'            # 谁在什么时候死、什么时候被拉回
su -c '/data/adb/modules/ninerouter-go/lib/ops.sh status' # watchdog=up|down|stale
```

`watchdog=stale` = 已武装但不在跑（重启设备或任一次「重启引擎」会自动补起）。
想彻底关掉守护：`touch $DATA_DIR/watchdog-off`（下次循环自尽）。

## 数据目录

`/data/adb/9router-go/`（全新安装，不迁移旧模块数据）

| 文件 | 说明 |
|---|---|
| `db/data.sqlite` | 引擎数据库（供应商/密钥/设置） |
| `initial-password` | Dashboard 初始密码（固定 123456，首登必改） |
| `dns-upstreams.conf` | DNS 上游列表（WebUI 可编辑，严禁 127.0.0.1） |
| `dns-bind` | dnsfwd 绑定范围：loopback（默认）/ any |
| `port` | 持久端口（可选，默认 20130） |
| `9router.pid` / `dnsfwd.pid` | 进程 PID |
| `9router.log` / `dnsfwd.log` | 引擎 / 转发器日志 |
| `watchdog.pid` / `watchdog.log` | 守护 PID 与日志（谁死了、何时被拉起） |
| `watchdog-armed` | 守护武装标志（只由 `service.sh` 开机路径写入） |
| `watchdog-hold` | 维护窗口（值=绝对到期时间；换二进制/解压模块期间守护让路） |
| `watchdog.req` | 运维请求（`restart`/`start`，优先于 hold） |
| `watchdog-off` | 置此后守护自尽（卸载/显式关闭） |
| `watchdog-interval` | 守护轮询秒数（可选，默认 5，允许 2–300） |
| `service-off` | 用户显式停服的意图（WebUI「停止服务」写入；`life_boot`/「启动服务」清除） |

## 模块 WebUI 功能

管理器 → 模块 → WebUI 打开（KernelSU 内置 / WebUIX），四页签：

| 页签 | 功能 |
|---|---|
| 概览 | 引擎/dnsfwd 状态与内存占用、引擎端口修改（写 `port` 文件并重启） |
| DNS | 上游增删改、`dnsfwd -P` 探测、优选评分（可用率/RTT/fake-ip）一键应用 Top5、回滚、绑定范围 |
| 一致性检查 | 修复 Dashboard 出厂 key、清理孤儿 customModels/disabledModels（已删节点的残留，即客户端模型列表里 `openai-compatible-chat-<uuid>` 噪音的来源）、无凭据活跃连接警告 |
| 更新 | GitHub 加速节点本机测速选优（初始清单来自 moretools.app 聚合，可自定义）、引擎 release 检查/更新（SHA256 校验）、模块 zip 覆盖更新 |

- 加速节点选中值存 `$DATA_DIR/github-accel`，自定义清单 `$DATA_DIR/accel-list.conf`
- 模块更新源存 `$DATA_DIR/module-update-url`（默认指向 fork 的 `update.json`，
  发布 release 时在仓库根放 `{version, versionCode, zipUrl, changelog}`）
- module.prop 的 `updateJson` 字段供 KernelSU 管理器原生在线更新（与本 WebUI 通道独立）

## Dashboard 复制按钮补丁

HTTP（非 localhost）访问 Dashboard 时 `navigator.clipboard` 不存在、复制按钮
全部失效——上游问题。构建时由 `tools/patch-clipboard.py` 向 `web/dist/index.html`
注入 polyfill 降级实现（上游源码零改动，注入块带 BEGIN/END 标记）。
上游修复后：`CLIPBOARD_PATCH=0 ./build.sh` 跳过注入。

## 卸载

管理器直接卸载即可停进程；数据目录保留，需彻底清除请手动删 `/data/adb/9router-go/`。
