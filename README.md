<div align="center">

# 9Router Go — Android Module

**Runs 9Router Go on your Android phone.**

[![Release](https://img.shields.io/github/v/release/liisoya/9router-go-magisk)](https://github.com/liisoya/9router-go-magisk/releases/latest)
![Platform](https://img.shields.io/badge/platform-Android%20arm64-3ddc84)
![Root](https://img.shields.io/badge/root-KernelSU%20%2F%20Magisk-blue)

[English](README.md) · [简体中文](README.zh-CN.md) · [Upstream README](UPSTREAM-README.md)

[🤔 What is this](#-what-is-this) • [⚡ Quick Start](#-quick-start) • [💡 What the module does](#-what-the-module-does) • [📖 FAQ](#-faq) • [🙏 Credits](#-credits)

</div>

---

## 🤔 What is this

A Magisk / KernelSU module that does one thing: run [luqman-v1/9router-go](https://github.com/luqman-v1/9router-go) on your phone.

So what is 9router? It's an AI gateway. It connects coding tools such as Claude Code, Cursor, Codex, Gemini, OpenCode, Cline, Copilot, Antigravity and OpenClaw to 40+ AI providers and 100+ models, and picks whichever one still has quota, costs less, or is free.

9router-go is the single-binary Go version of that gateway. No Node.js. It handles 5,920 requests per second against the original's 505, and uses 42MB of RAM against the original's 271MB. That is what makes running it on a phone realistic.

## ⚡ Quick Start

You need an arm64 Android phone that is already rooted (KernelSU or Magisk).

1. Flash `9router-go-<version>-magisk.zip` in your manager, then reboot.
2. Open `http://<phone-ip>:20130`. The password is `123456` — change it before anything else.
3. Go to the Providers tab and connect one provider.
4. Grab a key from the API Keys tab.
5. Fill in three fields in your coding tool:

| Setting | Value |
|---|---|
| Endpoint | `http://<phone-ip>:20130/v1` |
| API Key | the one from step 4 |
| Model | a model your provider serves, with its prefix, e.g. `kr/claude-sonnet-4.5` |

Claude Code differs in the endpoint only: `ANTHROPIC_BASE_URL=http://<phone-ip>:20130/v1`.

> ⚠️ The default password is `123456`, and the engine listens on the whole LAN. Change the password right after flashing.

---

## 💡 What the module does

| Feature | What it does |
|---|---|
| 🚀 Starts on boot | Comes up by itself after a reboot, and waits for the network before starting. |
| 🌐 Local DNS | Android has no `/etc/resolv.conf`, so the module ships a forwarder: it picks the fastest upstream for you, and steps aside when something else already serves DNS. |
| 📊 Web panel | Open it from your manager to see status, memory use and service addresses, and to change the port, start or stop services, manage DNS and more. |
| ⬆️ Updates in the panel | Both the engine and the module update from the panel. |

---

## 📖 FAQ

<details>
<summary><b>What phones does it work on?</b></summary>

arm64 Android with KernelSU or Magisk. Other architectures cannot install: the installer checks and refuses, rather than leaving you with a module that quietly will not run.

</details>

<details>
<summary><b>The engine is up, but no model connects.</b></summary>

Check DNS first. Android has no `/etc/resolv.conf`, and the engine only looks for a resolver at `127.0.0.1:53`, so the module ships a forwarder to hold that port.

Open the DNS tab in the panel and confirm the forwarder is running. If the panel says 已让路 (yielded), another DNS service on your phone already owns that port and the engine's lookups go through it, which is normal. If you turn the forwarder off in the panel and nothing else serves `:53`, the panel warns you that models will stop connecting. When DNS resolution just will not work, you can also put your own resolver in the upstream list, for example `1.1.1.1` or `8.8.8.8`.

</details>

<details>
<summary><b>Where does my data live, and does uninstalling delete it?</b></summary>

All of it is in `/data/adb/9router-go/`: the database, DNS config, port setting and logs. Uninstalling the module leaves everything in place, so reinstalling picks up where you left off. Delete that directory by hand if you want it gone for good.

</details>

<details>
<summary><b>How do I change the port?</b></summary>

Panel → Overview → Engine port, then restart the engine. The default is 20130, same as upstream.

</details>

<details>
<summary><b>Can a failed update brick the device?</b></summary>

No. Everything is verified before install, and a bad download is rejected without touching the current files. If the engine fails to start after an update, it rolls back to the previous build.

</details>

<details>
<summary><b>Is the default password 123456 safe?</b></summary>

No, so change it first thing. The engine listens on every interface by default, which means your LAN can reach it. Do not put that port on the public internet; use a tunnel or a VPN if you need remote access.

</details>

---

## 🔗 Relationship to upstream

This repository is the upstream 9router-go source plus an Android layer. The engine stays untouched by default; we only patch it in place when an upstream bug blocks a feature, and every patch is recorded in [docs/adr/0003](docs/adr/0003-engine-parity-fix-exception.md).

## 🙏 Credits

- [9router-go](https://github.com/luqman-v1/9router-go) — the engine this module runs: a single Go binary with a built-in dashboard
- [9Router](https://github.com/decolua/9router) — the original Next.js project that 9router-go keeps compatibility with
