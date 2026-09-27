<div align="center">

# 9router-go — FREE AI Router & Token Saver (Single Binary)

**Never stop coding. Save 20-40% tokens with RTK + auto-fallback to FREE & cheap AI models.**

**Connect Claude Code, Cursor, Antigravity, Codex, Gemini, OpenCode, Cline, OpenClaw... to 40+ AI providers & 100+ models — no Node.js needed at runtime.**

[![CI](https://github.com/luqman-v1/9router-go/actions/workflows/ci.yml/badge.svg)](https://github.com/luqman-v1/9router-go/actions/workflows/ci.yml)
[![Release](https://github.com/luqman-v1/9router-go/actions/workflows/release.yml/badge.svg)](https://github.com/luqman-v1/9router-go/actions/workflows/release.yml)
[![GitHub release](https://img.shields.io/github/v/release/luqman-v1/9router-go)](https://github.com/luqman-v1/9router-go/releases/latest)
[![License](https://img.shields.io/github/license/luqman-v1/9router-go)](https://github.com/luqman-v1/9router-go/blob/main/LICENSE)

[🚀 Quick Start](#-quick-start) • [💡 Features](#-key-features) • [⚙️ Setup](#-setup-guide) • [🌐 Upstream](https://github.com/decolua/9router)

</div>

---

## 🤔 Why 9router-go?

Same idea as [9Router](https://github.com/decolua/9router), minus the Node.js runtime: **one Go binary** serves the proxy APIs + an embedded Svelte dashboard.

**Stop wasting money, tokens and hitting limits:**

- ❌ Subscription quota expires unused every month
- ❌ Rate limits stop you mid-coding
- ❌ Tool outputs (git diff, grep, ls...) burn tokens fast
- ❌ Manual switching between providers

**9router-go solves this:**

- ✅ **RTK Token Saver** — auto-compress tool_result content, save 20-40% tokens
- ✅ **Auto fallback** — Subscription → Cheap → Free, zero downtime
- ✅ **Multi-account** — round-robin between accounts per provider
- ✅ **Single binary** — Go + embedded dashboard, works with Claude Code, Codex, Cursor, Cline, any CLI tool

---

## 🔄 How It Works

```text
┌─────────────┐
│  Your CLI   │  (Claude Code, Codex, OpenClaw, Cursor, Cline...)
│   Tool      │
└──────┬──────┘
       │ http://localhost:20130/v1
       ↓
┌─────────────────────────────────────────────┐
│         9router-go (Smart Router)           │
│  • RTK Token Saver (cut tool_result tokens) │
│  • Format translation (OpenAI ↔ Claude)     │
│  • Quota tracking                           │
│  • Auto token refresh                       │
└──────┬──────────────────────────────────────┘
       │
       ├─→ [Tier 1: SUBSCRIPTION] Claude Code, Codex, GitHub Copilot
       │   ↓ quota exhausted
       ├─→ [Tier 2: CHEAP] GLM ($0.6/1M), MiniMax ($0.2/1M)
       │   ↓ budget limit
       └─→ [Tier 3: FREE] Kiro, OpenCode Free, Vertex ($300 credits)

Result: Never stop coding, minimal cost + 20-40% token savings via RTK
```

---

## ⚡ Quick Start

**1. Install (one line):**

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/luqman-v1/9router-go/main/install.sh | bash
```

```powershell
# Windows (PowerShell, no admin needed)
irm https://raw.githubusercontent.com/luqman-v1/9router-go/main/install.ps1 | iex
```

🎉 Then start it (defaults: port `20130`, data `~/.9router` — no flags needed):

```bash
9router-go
# dashboard: http://localhost:20130
```

> Already use upstream 9Router? Point Go at the same data dir — it opens the **same `DATA_DIR/db/data.sqlite`**: providers, connections, combos, and usage carry over. Details in [`DATABASE.md`](DATABASE.md).

**2. Connect a FREE provider (no signup needed):**

Dashboard → Providers → Connect **Kiro AI** (~50 credits/month free) or **OpenCode Free** (no auth) → Done!

**3. Use in your CLI tool:**

```text
Claude Code / Codex / OpenClaw / Cursor / Cline Settings:
  Endpoint: http://localhost:20130/v1
  API Key:  [Settings → API Keys in the dashboard]
  Model:    kr/claude-sonnet-4.5
```

**That's it!** Start coding with FREE AI models.

**Alternatives:**

```bash
# Docker — no build needed
docker run -d --name 9router-go --restart unless-stopped \
  -p 20130:20130 -v "$HOME/.9router:/data" \
  -e PORT=20130 -e DATA_DIR=/data \
  luqmenul/9router-go:latest

# Manual download — pick your file, no command line guesswork:
# Windows → .exe | Mac M1+ → darwin-arm64 | Mac Intel → darwin-amd64
# Linux VPS → linux-amd64 | Raspberry Pi → linux-arm64
```

📦 [All release binaries](https://github.com/luqman-v1/9router-go/releases/latest) • 🔨 [Build from source](#-setup-guide)

---

## 💡 Key Features

- 🖥️ Native Svelte 5 dashboard: providers, OAuth, combos, proxy pools, API keys, usage, quota, settings
- 🔌 OpenAI Chat, Claude Messages, Gemini, Ollama-compatible, Responses, embeddings, media, search, web tools
- 🔁 Combos with fallback, round-robin, sticky routing, fusion, capability-aware reordering
- 👥 Per-provider executors, OAuth refresh, reactive 401 retry
- 📡 Live usage + console-log SSE streams, stall detection
- 💾 SQLite WAL persistence, outbound proxy support, self-update, MITM commands, Docker, cross-compilation

---

## ⚙️ Setup Guide

### Release binary

Download from [GitHub Releases](https://github.com/luqman-v1/9router-go/releases/latest), verify against `SHA256SUMS.txt`.

### Build from source

Prerequisites: Go 1.27 and Bun 1.x (dashboard is embedded into the binary, so build web first):

```bash
git clone https://github.com/luqman-v1/9router-go.git
cd 9router-go
make web-build   # bun install --frozen-lockfile && bun run build
make build       # embeds VERSION into the Go binary
```

### Run

Defaults are enough for most people — plain `9router-go` listens on port `20130` with data in `~/.9router`:

```bash
9router-go
curl http://localhost:20130/health
./9router-go version
```

Only override when you need something different (`PORT`, `DATA_DIR`/`DB_PATH` — there are no `--port` flags):

```bash
PORT=20129 ./9router-go                        # different port
DATA_DIR=/srv/9router ./9router-go              # different data dir
DB_PATH=/srv/9router/data.sqlite ./9router-go   # explicit SQLite file
HOST=127.0.0.1 ./9router-go                     # localhost only, behind a reverse proxy
```

First dashboard login uses the compatibility password until you set your own (remote fresh installs must change it or set `INITIAL_PASSWORD`).

### Client example

```bash
curl http://localhost:20130/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer sk-your-api-key' \
  -d '{"model":"ag/gemini-3.8-flash-high","messages":[{"role":"user","content":"Hello"}],"stream":true}'
```

For Claude Messages clients: `ANTHROPIC_BASE_URL=http://localhost:20130/v1`.

<details>
<summary><b>Advanced: environment variables, API surface, auth, database</b></summary>

### Environment

| Variable | Default | Purpose |
| --- | --- | --- |
| `PORT` | `20130` | HTTP port |
| `HOST` / `BIND_ADDR` | all interfaces | Listener address |
| `DATA_DIR` | `~/.9router` (`%APPDATA%/9router` on Windows) | Data root |
| `DB_PATH` | `$DATA_DIR/db/data.sqlite` | SQLite file |
| `INITIAL_PASSWORD` | unset (fallback `123456` locally) | First dashboard password |
| `RTK_ENABLED` | `true` | RTK input compression |
| `CAVEMAN_ENABLED` / `PONYTAIL_ENABLED` | `false` | Style savers |
| `AUTO_UPDATE` | `false` | Background self-update |
| `HTTP_PROXY` / `HTTPS_PROXY` | Go defaults | Upstream egress proxy |
| `PPROF_ENABLED` | `false` | Expose `/debug/pprof/*` (keep off on untrusted networks) |
| `TRUST_PROXY` / `TRUST_CLOUDFLARE` | unset | Trust forwarded client-IP headers |

`.env.example` documents the security-sensitive subset and OAuth overrides.

### API surface

```text
POST /v1/chat/completions       OpenAI Chat Completions
POST /v1/messages               Claude Messages
POST /v1/responses              Responses API
POST /v1/embeddings             Embeddings
POST /api/chat                  Ollama-compatible chat
GET  /v1/models                 Model catalog
POST /v1/images/generations     Image generation
POST /v1/videos/generations     Video generation
POST /v1/audio/speech           Text to speech
POST /v1/audio/transcriptions   Speech to text
POST /v1/search                 Web search
GET  /api/usage/stream          Live usage SSE
GET  /health                    Liveness
GET  /api/version               Version metadata
```

### Authentication

- Public: `/health`, dashboard HTML/assets, `/login`, OAuth callbacks.
- Proxy routes need an active client API key (`Authorization: Bearer` / `X-API-Key`).
- Dashboard APIs need a session cookie, CLI token, or API key; destructive ops (shutdown, update, DB import) need a session or CLI token.
- The API server defaults to all interfaces — bind localhost or protect the port outside trusted machines.

### Database compatibility

Go reads/writes the upstream 9router table/JSON shapes and bootstraps the core schema on startup (creates the 11 tables when absent, backfills missing columns, seeds an empty settings row) — a fresh `DATA_DIR` just works, no upstream install needed. Existing databases are never modified beyond additive backfills. Full contract in [`DATABASE.md`](DATABASE.md), routing internals in [`ARCHITECTURE.md`](ARCHITECTURE.md).

</details>

---

## 📚 Docs

- [`ARCHITECTURE.md`](ARCHITECTURE.md) — routing, providers, runtime layout
- [`DATABASE.md`](DATABASE.md) — SQLite schema & operator contract
- [`ROADMAP.md`](ROADMAP.md) — proposals only, not current behavior
- [`CHANGELOG.md`](CHANGELOG.md) — release history (Go **v1.9.2**, upstream baseline `decolua/9router` v0.5.85)

## Credits

- [9Router](https://github.com/decolua/9router) — original Next.js gateway & dashboard this Go port preserves compatibility with
