# CONTEXT.md — 9router-go-magisk 术语表

> 本文件只是术语表，不含实现细节。实现决策见 `docs/adr/`。

## 术语

**引擎（Engine）**
上游 `luqman-v1/9router-go` 的 Go 代理网关二进制。本仓库与上游保持同源同版本号；模块层只做打包，不改引擎代码。

**Dashboard**
引擎内置的官方 Svelte 管理界面（`web/`，经 `go:embed` 编进引擎二进制），由引擎进程直接服务。默认登录密码见首次启动生成的 `initial-password` 文件。

**dnsfwd**
本地 DNS 转发器（C 单文件，源码 `tools/dnsfwd.c`）。Android 无 `/etc/resolv.conf`，Go 解析器会回落 `127.0.0.1:53`，必须有它在 :53 接住，引擎才能解析域名。与引擎无依赖关系，纯承载性组件。

**模块（Module）**
`module/` 目录下的 Magisk/KernelSU 模块层：`module.prop`、生命周期脚本、预编译二进制、WebUI。与引擎代码物理隔离，随模块 zip 整体更新。

**WebUI**
模块级 DNS 管理页（`module/webroot/index.html`），KernelSU 内置浏览器或 WebUIX 打开。管理 dnsfwd：上游增删改、探测、热重载、绑定范围。独立于 Dashboard。

**生命周期（lifecycle）**
"服务该不该在跑"的全部事实与操作：进程启停、用户意图、状态文件、cgroup 脱组。
唯一所有者是 `lib/lifecycle.sh`（可 source 的库）；其它文件只表达意图，不读写状态文件（ADR-0005）。
_Avoid_: 服务管理、进程控制（会与通用含义混淆）

**意图（intent）**
用户/运维"要服务处于什么状态"的声明，与"当前实际状态"分开表示：`life_boot`（开机 = 要它跑）、
`life_stop_user`（用户要它停，守护必须尊重）、`life_wd_hold`（维护窗口，暂时别插手）。
状态里 `engine=stopped`（用户要的）与 `engine=down`（故障）是两件事。
_Avoid_: 开关、标志（实现细节的词，不该出现在接口层）

**承载性 env（carrier env）**
缺了它引擎就不可用或不受管的运行环境变量（当前 6 个：`SSL_CERT_DIR` / `AUTO_UPDATE` /
`PORT` / `DATA_DIR` / `MODDIR` / `INITIAL_PASSWORD`）。清单与产出物
（`$DATA_DIR/runtime.env`）是唯一来源，门禁逐条断言它真的进了引擎进程（ADR-0006）。
_Avoid_: 环境变量、配置（太泛，会漏掉"承载"这层意思）

**数据目录**
`/data/adb/9router-go/`。全新安装，不迁移旧模块（panel-9router / nine-router-go）的任何数据。

## 刻意不做

- **不改上游代码**：所有模块层功能以"只增不改"文件形式存在（见 ADR-0002）。
- **无独立面板进程**：旧 panel-9router 方案已废弃（见 ADR-0001）。
- **DNS 自动优选**：暂不实现，列入后续优化。
