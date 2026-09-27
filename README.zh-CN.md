<div align="center">

# 9Router Go 安卓模块

**在安卓手机上跑 9Router Go。**

[![Release](https://img.shields.io/github/v/release/liisoya/9router-go-magisk)](https://github.com/liisoya/9router-go-magisk/releases/latest)
![Platform](https://img.shields.io/badge/platform-Android%20arm64-3ddc84)
![Root](https://img.shields.io/badge/root-KernelSU%20%2F%20Magisk-blue)

[🤔 这是什么](#-这是什么) • [⚡ 快速开始](#-快速开始) • [💡 模块做了什么](#-模块做了什么) • [📖 常见问题](#-常见问题) • [🙏 Credits](#-credits)

</div>

---

## 🤔 这是什么

这是一个 Magisk / KernelSU 模块，干的事只有一件：让 [luqman-v1/9router-go](https://github.com/luqman-v1/9router-go) 在你手机上跑起来。

9router 是什么？ 它是一套 AI 网关，把 Claude Code、Cursor、Codex、Gemini、OpenCode、Cline、Copilot、Antigravity、OpenClaw 这些编程工具，统一接到 40 多家 AI 服务商、100 多个模型上。哪个额度没用完、哪个便宜、哪个免费，它自己挑。

9router-go 是这套网关的 Go 语言单文件版本，不依赖 Node.js ，实现低功耗高能效，每秒能处理 5920 个请求（原版 505 个），内存占 42MB（原版 271MB）。非常适合在手机上跑。

## ⚡ 快速开始

前提是 arm64 的安卓机，并且已经 root（KernelSU 或 Magisk）。

1. 在管理器里刷入 `9router-go-<版本>-magisk.zip`，然后重启手机。
2. 浏览器打开 `http://手机IP:20130`，密码 `123456`。进去第一件事是改密码。
3. 打开 Providers 页，接一个服务商。
4. 到 API Keys 页拿一个 key。
5. 在你的编程工具里填三样东西：

```text
Endpoint   http://<手机IP>:20130/v1
API Key    第 4 步拿到的那个
Model      供应商提供的模型（加上前缀）比如 kr/claude-sonnet-4.5
```

Claude Code 的不同点只在 Endpoint：`ANTHROPIC_BASE_URL=http://<手机IP>:20130/v1`。

> ⚠️ 默认密码是 `123456`，引擎默认对整个局域网开放。刷完先把密码改掉。

---

## 💡 模块做了什么

| 项目 | 说明 |
|---|---|
| 🚀 开机自启 | 手机重启后自己起来，会先等网络通了再启动。 |
| 🌐 本地 DNS | 安卓上没有 `/etc/resolv.conf`，模块自带转发器补上，实现自动DNS优选，自动避让已有同类。 |
| 📊 网页面板 | 管理器里点开就能看运行状态、内存占用、服务地址，改端口、开关服务、管 DNS等。 |
| ⬆️ 面板里升级 | 引擎和模块都能在面板里更新。 |

---

## 📖 常见问题

<details>
<summary><b>需要什么样的手机？</b></summary>

arm64 的安卓机，KernelSU 或 Magisk。其他架构装不上，安装时会直接拒绝，不会给你装一个跑不起来的包。

</details>

<details>
<summary><b>引擎跑起来了，但模型全连不上？</b></summary>

先看 DNS。安卓上没有 `/etc/resolv.conf`，引擎只会去 `127.0.0.1:53` 找解析器，所以模块自带一个转发器守着这个端口。

打开面板的 DNS 页，确认它在运行。如果显示“已让路”，说明你手机上已经有别的 DNS 服务占着这个端口了，引擎的解析由它接管，这也是正常的。要是你在面板里关掉了转发器，而 `:53` 又没有别的服务，面板会直接警告你模型会连不上。如果是DNS连接失败，可以自定义填写DNS服务器，比如 `1.1.1.1` 或 `8.8.8.8`。

</details>

<details>
<summary><b>数据存在哪？卸载会删吗？</b></summary>

都在 `/data/adb/9router-go/`，包括数据库、DNS 配置、端口设置和日志。卸载模块不删这些，重装还在。想清干净得手动删这个目录。

</details>

<details>
<summary><b>怎么改端口？</b></summary>

面板 → 概览 → 引擎端口，写完重启引擎。默认是 20130，和上游一致。

</details>

<details>
<summary><b>更新失败会不会把设备弄坏？</b></summary>

不会。装之前先校验，不合格就直接中止，不碰现有文件；引擎装完起不来会自动退回上一版。

</details>

<details>
<summary><b>默认密码 123456，安全吗？</b></summary>

不安全，所以第一件事就是改掉它。引擎默认监听所有网卡，局域网能访问，别把这个端口放到公网上；要远程用就走内网穿透或者 VPN。

</details>

---

## 🔗 和上游的关系

这个仓库是上游 9router-go 的源码加上一层安卓模块。引擎默认不改，只有上游的 bug 挡住了功能才做定点修补，每处都记在 [docs/adr/0003](docs/adr/0003-engine-parity-fix-exception.md) 里。


## 🙏 Credits

- [9router-go](https://github.com/luqman-v1/9router-go) —— 这个模块跑的就是它：Go 单二进制引擎，自带 Dashboard
- [9Router](https://github.com/decolua/9router) —— 最初的 Next.js 版本，9router-go 要兼容的就是它的接口与数据格式