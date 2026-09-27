<div align="center">

# 9Router Go 安卓模块

在安卓手机上跑 9Router Go。开机自己起来，局域网里的设备随时能用。

[![Release](https://img.shields.io/github/v/release/liisoya/9router-go-magisk)](https://github.com/liisoya/9router-go-magisk/releases/latest)
![Platform](https://img.shields.io/badge/platform-Android%20arm64-3ddc84)
![Root](https://img.shields.io/badge/root-KernelSU%20%2F%20Magisk-blue)

</div>

## 这是什么

这是一个 Magisk / KernelSU 模块，干的事只有一件：让 [luqman-v1/9router-go](https://github.com/luqman-v1/9router-go) 在你手机上跑起来。

先说 9router 是什么。它是一套 AI 网关，把 Claude Code、Cursor、Codex、Gemini、OpenCode、Cline、Copilot、Antigravity、OpenClaw 这些编程工具，统一接到 40 多家 AI 服务商、100 多个模型上。哪个额度没用完、哪个便宜、哪个免费，它自己挑。9router-go 是这套网关的 Go 语言单文件版本，跑得很轻：和 Next.js 版本在同一台机器上比，它每秒能处理 5920 个请求（对方 505 个），内存占 42MB（对方 271MB）。

```text
你的编程工具  →  手机 :20130  →  AI 服务商
```

所以一台闲置的手机就够用了。装好之后，手机上的 20130 端口就是一个 AI 接口，同一个 Wi-Fi 下的电脑、平板、其他手机都连它，不用一台台单独配。

## 快速开始

手机要是 arm64 架构，并且已经 root（KernelSU 或 Magisk）。

1. 在管理器里刷入 `9router-go-<版本>-magisk.zip`，然后重启手机。
2. 浏览器打开 `http://手机IP:20130`，密码 `123456`。进去第一件事是改密码。
3. 打开 Providers 页，接一个服务商。Kiro 每月有免费额度，OpenCode Free 不用登录。
4. 到 API Keys 页拿一个 key。
5. 在你的编程工具里填：

```text
Endpoint   http://手机IP:20130/v1
API Key    第 4 步拿到的那个
Model      比如 kr/claude-sonnet-4.5
```

Claude Code 的话，只填一个环境变量就行：`ANTHROPIC_BASE_URL=http://手机IP:20130/v1`。

## 模块做了什么

| 项目 | 说明 |
|---|---|
| 开机自启 | 手机重启后自己起来，会先等网络通了再启动。 |
| 挂了拉回来 | 进程被系统清掉或者自己崩了，十秒左右重新起来。 |
| 不被清理连坐 | 管理器 App 被系统清理时，引擎不会跟着一起被杀。 |
| 本地 DNS 转发 | 安卓上没有 `/etc/resolv.conf`，引擎解析不了域名，模块自带一个转发器补上。支持普通 DNS、DoH、DoT，还能测速挑最快的。 |
| 网页面板 | 管理器里点开就能看：运行状态、内存占用、服务地址、改端口、开关服务、管 DNS、清残留数据。 |
| 面板里升级 | 引擎和模块都能在面板里更新。下载和安装都有校验，装完起不来会自动退回上一版。 |

## 常见问题

**需要什么样的手机？**
arm64 的安卓机，KernelSU 或 Magisk。其他架构装不上，安装时会直接拒绝，不会装一个跑不起来的包。

**引擎跑起来了，但模型全连不上？**
先看 DNS。安卓上没有 `/etc/resolv.conf`，引擎只会去 `127.0.0.1:53` 找解析器，所以模块自带一个转发器守着这个端口。打开面板的 DNS 页确认它在运行。如果显示“已让路”，说明你手机上已经有别的 DNS 服务占着这个端口了，引擎的解析由它接管，这也是正常的。要是你在面板里关掉了转发器，而 `:53` 又没有别的服务，面板会直接警告你模型会连不上。

**数据存在哪？卸载会删吗？**
都在 `/data/adb/9router-go/`，包括数据库、DNS 配置、端口设置和日志。卸载模块不删这些，重装还在。想清干净得手动删这个目录。

**怎么改端口？**
面板 → 概览 → 引擎端口，写完重启引擎。默认是 20130，和上游一致。

**三种升级方式怎么选？**
管理器里的在线更新最省事，模块和引擎一起升。面板的“模块更新”也是整包升。只想换引擎、不动模块的话用面板的“引擎更新”，它从上游 release 取最新二进制（当前上游是 v1.9.3）。更新失败不会弄坏设备：装前校验不过就直接中止，不碰现有文件；引擎起不来会自动退回上一版。

**默认密码 123456，安全吗？**
不安全，所以第一件事就是改掉它。引擎默认监听所有网卡（局域网能访问），别把这个端口放到公网上；要远程用就走内网穿透或者 VPN。

## 和上游的关系

这个仓库是上游 9router-go 的源码（当前 v1.9.3）加上一层安卓模块。引擎默认不改，只有上游的 bug 挡住了功能才做定点修补，每处都记在 [docs/adr/0003](docs/adr/0003-engine-parity-fix-exception.md) 里，现在有两处。

上游仓库：[luqman-v1/9router-go](https://github.com/luqman-v1/9router-go)。更早的原始项目是 [decolua/9router](https://github.com/decolua/9router)，Next.js 写的。

想改代码的话，从 [MAGISK.md](MAGISK.md)（模块层怎么组织）和 [AGENT-CONVENTIONS.md](AGENT-CONVENTIONS.md)（工程约定）开始看。
