<div align="center">

# 9Router Go · Magisk / KernelSU 模块

**把 9Router Go 引擎装进 Android：开机自启、被清理能自愈、面板在线升级。**

**一台常插电的旧手机 = 一个常驻的 AI 路由网关。把订阅额度、廉价模型与免费模型聚合成一个
`http://<设备IP>:20130/v1`，局域网里的 CLI 工具、App、脚本都能直接连。**

[![Release](https://img.shields.io/github/v/release/liisoya/9router-go-magisk)](https://github.com/liisoya/9router-go-magisk/releases/latest)
![Platform](https://img.shields.io/badge/platform-Android%20arm64-3ddc84)
![Root](https://img.shields.io/badge/root-KernelSU%20%2F%20Magisk-blue)

[🚀 快速开始](#-快速开始) • [💡 为什么需要模块](#-为什么需要一个模块) • [⚙️ 模块做了什么](#-模块层做了什么) • [❓ 常见问题](#-常见问题) • [🌐 上游引擎](https://github.com/luqman-v1/9router-go)

</div>

---

## 🤔 这是什么

**上游引擎**：[`luqman-v1/9router-go`](https://github.com/luqman-v1/9router-go) —— 一个 Go 单二进制
AI 路由网关（内置 Svelte 5 Dashboard），把 Claude Code、Codex、Cursor、Cline、OpenCode 等客户端
接到 40+ 供应商、100+ 模型上，自带 token 压缩、自动 fallback、多账号轮换、用量统计。

**本仓库**：那个引擎的 **Android 发行版**。引擎本身是给 Linux/macOS/Windows 用的命令行程序 ——
在手机上，`nohup` 起个进程只能算"能跑"，离"可靠地一直在跑"还差一整套东西。本模块补齐的就是这一层，
并且**让模块自己可以被人从管理器里点着升级**。

```text
┌──────────────┐   ┌──────────────┐   ┌──────────────┐
│ CLI 工具      │   │ 手机 App      │   │ 局域网其他设备 │
│ Claude Code… │   │ (兼容 OpenAI) │   │ 脚本 / 自建服务 │
└──────┬───────┘   └──────┬───────┘   └──────┬───────┘
       └──────────────────┼──────────────────┘
                          │  http://<设备IP>:20130/v1
                 ┌────────┴─────────┐
                 │  Android 手机     │
                 │  ┌─────────────┐ │
                 │  │ 9router-go  │ │  ← 本模块拉起的引擎（自动重启、自动脱组）
                 │  │  Dashboard  │ │
                 │  └──────┬──────┘ │
                 │  ┌──────┴──────┐ │
                 │  │ dnsfwd :53  │ │  ← 本模块内置：Android 上没有 /etc/resolv.conf
                 │  └─────────────┘ │
                 └────────┬─────────┘
                          └──→ 订阅模型 / 廉价模型 / 免费模型（自动 fallback）
```

---

## 💡 为什么需要一个模块

上游引擎解决的问题是"**选哪个模型**"；模块要解决的是"**它凭什么一直在跑**"。Android 比服务器
苛刻得多：开机时机、进程清理（cgroup 连坐）、没有 `/etc/resolv.conf`、没有 systemd、升级要么刷包
要么 adb。下面每一条都是真机取证后修出来的，不是理论推演：

| 直接跑二进制会遇到 | 本模块的做法 |
|---|---|
| 重启手机就没了 | `service.sh` 在 `late_start` 阶段开机自启，先等网络就绪再拉起引擎 |
| 管理器 App 被系统清理时，引擎被**同一 cgroup 连坐静默杀掉**（无日志、无 OOM 记录） | **启动即脱组**：把引擎迁到 cgroup 根（`cgroup=/`），从根上不再随 App 被清 |
| 死了没人知道，表现为"跑一段时间就得手动重启" | **守护**（仅开机上下文武装）：只判"进程在不在"，连续两次判死才动手，约 10 秒内拉回 |
| 引擎是纯 Go 静态二进制，读不到 Android 上**根本不存在**的 `/etc/resolv.conf` → 域名解析失败、模型全连不上 | 内置 **dnsfwd** 转发器接住 `127.0.0.1:53`；支持 DoH / DoT / 明文与一键优选；若设备上已有 DNS 服务会自动**让路**而不是抢占 |
| 升级=拷文件，失败了还得手动救 | **三条更新通道**，全部带 SHA256 校验、装前门禁与回滚点（见下） |
| 看不到状态，出问题只能猜 | 模块 **WebUI**：进程/内存/端口/日志；管理器「操作」按钮也能直接看状态 |
| 服务器默认值直接搬过来不安全 | 面板显示局域网地址 + 首登强制建议改密；「停止服务」是**显式意图**，守护不会把它偷偷拉回来 |

一句话：**上游给你路由能力，本模块给你"常驻 + 可观测 + 可升级 + 开机即用"**。

---

## 🚀 快速开始

**前提**：Android **arm64** 设备 + Root（KernelSU / Magisk）。模块刻意只做 arm64（安装时会检查
`ro.product.cpu.abi`，不匹配直接中止，不会装一个跑不起来的包）。

**1. 刷入**

管理器（KernelSU / Magisk）→ 模块 → 从本地安装 → 选
`9router-go-<版本>-magisk.zip` → 重启。

也可以用管理器的**在线更新**（本模块的 `module.prop` 带 `updateJson`，管理器自己会比对版本）。

**2. 打开面板**

```text
http://<设备IP>:20130        # 默认端口 20130；引擎监听 0.0.0.0
```

默认初始密码 **`123456`** —— 登录后请**立刻改掉**（这是把"能远程访问"和"首装零门槛"放在一起的
显式取舍，见 [`AGENT-CONVENTIONS.md §10`](AGENT-CONVENTIONS.md)）。

**3. 接一个供应商**

Dashboard → Providers → 接 **Kiro AI**（每月免费额度）或 **OpenCode Free**（免登录）→ API Keys 页
拿一个 key。

**4. 在你的 CLI 工具里用**

```text
Claude Code / Codex / Cursor / Cline / OpenCode 设置：
  Endpoint: http://<设备IP>:20130/v1
  API Key:  <Dashboard → API Keys>
  Model:    kr/claude-sonnet-4.5      # 或任意已接供应商的模型
```

```bash
# 自检
curl http://127.0.0.1:20130/health
curl http://<设备IP>:20130/version
```

**5. 会用到的地址**

| 想做什么 | 去哪里 |
|---|---|
| 引擎状态 / 端口 / 日志 / 重启 | `/data/adb/modules/ninerouter-go/lib/ops.sh status` |
| 模块 WebUI（DNS 优选、一致性检查、更新、状态） | 管理器 → 本模块 → WebUI / 「操作」 |
| 全部数据（数据库、配置、日志） | `/data/adb/9router-go/` |
| 卸载后想彻底清干净 | 手动删 `/data/adb/9router-go/`（**卸载不会删数据**） |

---

## ⚙️ 模块层做了什么

### 生命周期（唯一所有者：`module/lib/lifecycle.sh`）

开机拉起、用户停服/启服、重启引擎、守护启停、cgroup 脱组 —— 语义全部收在一个文件里，其它文件
只表达意图（`service.sh` 是开机顺序、`action.sh` / WebUI / `ops.sh` 是调用方）。
`engine=stopped`（用户要的）与 `engine=down`（故障）是两回事，面板会区分显示。

### DNS 转发器（`dnsfwd`）

- 上游/候选池管理，明文 / DoH / DoT 都支持
- 内置探测与评分（可用率、RTT、fake-ip 惩罚）→ 一键应用 Top5，且有回滚点
- **`:53` 让路**：设备上已有 DNS 服务时不抢占；面板关闭 dnsfwd 时会**如实警告**
  "现在 :53 没人接、模型会连不上"，而不是假装一切正常

### 模块 WebUI（四页签）

| 页签 | 做什么 |
|---|---|
| **概览** | 引擎/dnsfwd/守护三态、内存占用、服务地址（本机 + 局域网）、引擎端口、启停服务 |
| **DNS** | 当前上游、增删改、探测/优选/回滚、转发器开关 |
| **一致性检查** | 扫孤儿数据（残留在客户端模型列表里的噪音）、无凭据的活跃连接 |
| **更新** | GitHub 加速节点本机测速选优、**引擎更新**、**模块更新** |

### 三条更新通道

| 通道 | 触发 | 安全措施 |
|---|---|---|
| 管理器在线更新 | KernelSU/Magisk 读 `module.prop` 的 `updateJson` | 管理器自带校验流程 |
| 面板「模块更新」 | 模块 WebUI 下载整包 zip | 必须含 `module.prop`；先备份到 `last-module.zip`；解压用**换 inode** 的方式落位（不覆写正在执行的脚本） |
| 面板「引擎更新」 | 从上游 release 取 arm64 二进制 | **装前双门禁**：体积 + ELF 魔数 → `SHA256SUMS` 校验和（取不到就拒绝）；起来之后才写版本号；起不来自动回滚到上一份可用二进制 |

> 这些门禁不是摆设：`curl` 少一个 `-f`、版本号拼错一个 `v`、`fetch` 少一个 `-L`，都会让"更新"
> 变成"把 404 正文装成引擎"或"永远下载失败"。每一条都在
> [`docs/FIXPLAN.md`](docs/FIXPLAN.md) 里留了取证与回归用例。

### 工程与门禁（给想改它的人）

```bash
tools/check.sh --offline   # 离线：shell 语法 / 模块 WebUI 纯函数 / go build+test / tsc / schema / 注入器 / 棘轮
tools/check.sh --device    # 真机：T1–T13（生命周期·安装门禁·版本自愈·更新链路）+ A1–A5（仪表盘 API）
tools/check.sh --parity    # 对照：端点 parity 棘轮 + UI 调用 parity（需上游参照树）
tools/check.sh --all       # 三档全跑
bash build.sh              # 发布构建：七步管线（前端 → 引擎交叉编译 → MODID 注入 → 打包校验）
```

模块层有自己的**工程契约**（`AGENT-CONVENTIONS.md`：谁是所有者、改 A 必做 B、门禁档位）、
**门禁台账**（`docs/TESTING.md`：每条断言失败意味着什么）与 **ADR**（`docs/adr/`）。
每个 bug 修复都必须带一条会复现原故障的回归用例 —— 包括本次"更新引擎必失败"：
地址契约回归 + 真机 `T13`。

---

## 📖 文档

| 文档 | 内容 |
|---|---|
| [`MAGISK.md`](MAGISK.md) | 模块层详解：目录结构、门禁、生命周期与守护、数据目录、面板功能、卸载 |
| [`AGENT-CONVENTIONS.md`](AGENT-CONVENTIONS.md) | 工程契约：事实优先级、架构不变量、模块边界速查、变更映射 |
| [`docs/TESTING.md`](docs/TESTING.md) | 门禁台账（测什么 / 怎么跑 / 失败意味着什么 / 变更记录） |
| [`docs/FIXPLAN.md`](docs/FIXPLAN.md) | 修复计划与真机取证记录（含每次事故的根因） |
| [`docs/adr/`](docs/adr/) | 架构决策记录（生命周期所有权、产物日志、引擎安装门禁、上游补丁政策） |
| [`CONTEXT.md`](CONTEXT.md) | 术语表 |
| [`update.json`](update.json) | 管理器在线更新清单 |
| 上游引擎文档 | [luqman-v1/9router-go](https://github.com/luqman-v1/9router-go)：架构、API 面、环境变量、Dashboard |

---

## ❓ 常见问题

<details>
<summary><b>支持哪些设备 / 为什么只有 arm64？</b></summary>

arm64（aarch64）的 Android 设备 + KernelSU 或 Magisk。安装脚本会读 `ro.product.cpu.abi` 检查，
不匹配直接中止 —— 与其装一个静默跑不起来的模块，不如装不上。armv7 / x86 没有构建产物。

</details>

<details>
<summary><b>引擎跑起来了，但模型全部连不上？</b></summary>

九成是 DNS。引擎是纯 Go 静态二进制，在 Android 上读不到 `/etc/resolv.conf`，它只会去
`127.0.0.1:53` 找解析器 —— 所以本模块内置 `dnsfwd` 来接住这个端口。

- 面板「DNS」页确认转发器是**运行中**；显示"已让路"说明设备上已有别的 DNS 服务在 `:53`，
  引擎解析由它接管（这是正常的）
- 如果你在面板里**关闭**了 dnsfwd，而 `:53` 又没有其它服务，面板会明确警告：此时模型域名无法解析
- 想自己换上游：DNS 页可编辑，**不要**填 `127.0.0.1`（自我循环，面板会拦）

</details>

<details>
<summary><b>我的数据在哪？卸载会丢吗？</b></summary>

全部在 `/data/adb/9router-go/`（数据库、DNS 配置、端口、日志、守护状态）。**卸载模块不会删数据**，
重装即用；要彻底清干净需手动删除该目录。目录清单见 [`MAGISK.md`](MAGISK.md#数据目录)。

</details>

<details>
<summary><b>怎么改端口？</b></summary>

模块 WebUI → 概览 → 引擎端口 → 写新值并重启引擎（写进 `/data/adb/9router-go/port`，重启后仍生效）。
默认 20130 与上游一致。

</details>

<details>
<summary><b>怎么升级？三条通道怎么选？</b></summary>

- **管理器在线更新**：最省事，管理器自己比对 `updateJson` 后下载安装（模块代码一个字节都不跑，
  所以运行期派生文件靠读取时自愈收敛）
- **面板「模块更新」**：整包升级模块层（含引擎二进制）
- **面板「引擎更新」**：只换引擎二进制，从**上游 release** 取（所以引擎永远能跟到上游最新）
- 只想更新 Dashboard/引擎、不动模块层 → 用第三条；想连模块层一起升 → 前两条

更新失败不会把设备搞坏：装前门禁不合格就中止且**不碰**现有二进制；引擎起不来会自动回滚到上一份。

</details>

<details>
<summary><b>为什么面板显示的版本和引擎自报的不一样？</b></summary>

引擎版本只认两个真实来源：运行期记录（`install-engine` 写入）与包内记录（构建期写入），
**绝不拿模块版本冒充**。整包更新不会跑模块代码，所以运行期记录可能落后一个版本 —— 这种情况
面板会在读取时**自愈**，并在「引擎版本」旁标出来源（`运行期记录` / `包内 · 刚自愈` / `无来源`），
让"看着像假更新"这件事一眼可查，而不是靠人比对。

</details>

<details>
<summary><b>安全吗？默认密码 / 监听 0.0.0.0 怎么看？</b></summary>

默认是"零门槛优先"：引擎监听 `0.0.0.0:<port>`（局域网可用），Dashboard 初始密码固定 `123456`。

- **请第一时间改密码**（Dashboard 内可改）
- 不要把这个端口暴露到公网；需要远程访问就用内网穿透 / VPN，并保持登录开启
- 面板会显示服务地址（本机 + 局域网），方便你判断当前暴露面
- 模块本身不做任何网络外连，除了你显式点的更新检查

</details>

<details>
<summary><b>这和上游仓库是什么关系？</b></summary>

本仓库 = 上游引擎源码（基线见下）+ **模块层**（`module/`、`tools/`、`build.sh`、`update.json` 等）
+ 文档。引擎默认零改动；只有在**阻断核心功能**的上游缺陷上才做定点补丁，且必须补丁存档、
并在 `docs/adr/0003` 登记（当前在册 2 条：Dashboard 网页抓取端点从未挂载、内嵌 Dashboard 的
跨域可达性探测 `/api/health` 缺失）。

**引擎基线**：上游 `v1.9.3`（模块版本 `v1.9.3-r1`）。

</details>

---

## 🔗 相关链接

- 上游引擎：**[luqman-v1/9router-go](https://github.com/luqman-v1/9router-go)**
- 更上游的原始项目（Next.js 版）：[decolua/9router](https://github.com/decolua/9router)
- 本模块发布页：[Releases](https://github.com/liisoya/9router-go-magisk/releases)

## Credits

- [9router-go](https://github.com/luqman-v1/9router-go) —— 引擎与内嵌 Dashboard（本仓库的引擎部分）
- [9Router](https://github.com/decolua/9router) —— 原始网关设计与兼容契约
