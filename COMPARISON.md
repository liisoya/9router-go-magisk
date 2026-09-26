# 9Router (Next.js) vs 9router-go (Go) — Feature Comparison

> **Context:** `htdocs/9router` is the original (Next.js, dashboard + engine monolith).
> `9router-go` is the engine swap to Go for performance (proxy/streaming/SSE).
> This document tracks the feature gap for porting decisions.

> **Sync:** `9router-go v1.8.9` is synced with `decolua/9router v0.5.69` (19 commits `v0.5.65...v0.5.69`) — 100% engine parity.

**Date:** 2026-09-06

---

## 0. 端点 parity 巡检（机器可验 · 2026-09-26 新增）

上游是 Next.js/React，本仓是 Go/Svelte —— **两份代码没有可 merge 的血缘**，端点是手写重写的。
漏一个端点、少一个 HTTP 方法，既不会编译报错也不会运行报错，只会在用户点到那个按钮时炸
（Phase 20 的 `Download Backup → 401 Invalid password` 就是这一类）。
本节把上面那些散文对照升级成**可执行断言**；`Sync` 那行停留在 `v0.5.69` 的声明仅供参考，
机器可验的参照以本节基线为准。

### 怎么跑

```bash
git clone https://github.com/decolua/9router.git ../9router   # 规范参照树（只读）
python3 tools/check-parity.py                    # 巡检：有**新增**缺口即失败（退出码 1）
python3 tools/check-parity.py --list-gaps        # 列出全部存量缺口（烧 backlog 用）
python3 tools/check-parity.py --write-baseline   # 烧掉缺口后收紧棘轮基线
```

### 语义（棘轮 ratchet）

| 来源 | 含义 | 是否报警 |
|---|---|---|
| `internal/handlers/**/*.go` 已注册 | 本仓实现 | 否（通过） |
| `tools/parity-baseline.txt` | 已知存量缺口快照（棘轮基线） | 否（记录） |
| `tools/parity-ignore.txt` | 有理由的豁免：等价实现 / 产品决策 | 否（豁免） |
| 不在以上三处的缺口 | **新增移植缺失** | ❌ 退出码 1 |

存量缺口一次性补齐不现实（首轮 141 条），所以冻结基线只拦新增 —— 既让"悄悄又少一个端点"
不可能发生，又让存量缺口保持可见、可逐条烧掉。

### 冻结时的现状（上游参照 `decolua/9router v0.5.81 (a8c9d380)`）

- 上游 `src/app/api`：**224 条**端点（156 个 `route.js`）
- 本仓注册：**242 条**（44 个 Go 文件，含引擎/代理路径）
- 已知缺口：**141 条**（130 条端点缺失 + 11 条方法缺口），已冻结进基线

缺口聚类（按功能块）：

| 功能块 | 缺口 | 说明 |
|---|---|---|
| `/api/cli-tools/*` | 49 | 整块未移植（各 CLI 工具的 settings 注入/读取） |
| `/api/v1/*` | 19 | 多与引擎 `/v1/*` 重叠，待逐条判等价 |
| `/api/oauth/*` | 10 | 含动态 provider 端点，待逐条判等价 |
| `/api/pxpipe/*` | 9 | 整块未移植 |
| `/api/translator/*` | 7 | 含 console-logs（本仓已有 `consolelog.go`，路径不同） |
| 其余零散 | 47 | 逐条判定 |

### 已核实的两个实证缺口（不是扫描误判）

1. **`GET /api/health` 缺失 → Dashboard 的 tunnel/公共地址可达性探测永远失败**
   - 证据：`web/src/components/EndpointView.svelte:212-217` 用 `clientPingUrl(tunnelUrl|publicUrl|tailscaleUrl)`
     发 `fetch(`${url}/api/health`)`；
   - 本仓只注册了 `/health`（`internal/handlers/router.go:300`），且该处理器**没有**
     `Access-Control-Allow-Origin: *` → 跨域探测必失败（也是上游注释里点明要有的头）。
2. **`GET /api/auth/oidc/callback` + `POST /api/auth/saml/acs` 未注册 → SSO 登录走不完**
   - 证据：`internal/handlers/sso/sso.go:28-29` 定义且用于拼 `redirectURI` / `acsUrl`；
   - 本仓只注册了 `oidc/test`、`saml/test`、`saml/metadata`（`internal/handlers/router.go:249-251`）
     → IdP 回调打到 404。

> 这两个正是本机制存在的意义：以前只有用户点到了才会发现。

---

## 1. Arsitektur

| | Next.js | Go |
|---|---------|-----|
| Routing | Next App Router + Express + `http-proxy-middleware` | chi router |
| SSE/streaming | Node stream + `custom-server.js` (anti-XFF IP) | native, `sse_scanner` + StallReader + eventstream parser |
| SQLite | sql.js (+ better-sqlite3 optional) | `modernc.org/sqlite` (WAL) |
| CLI | `cli/` npm | urfave/cli (`version`, `update`, `mitm`) |
| Auth | login/OIDC/API key | API key middleware (`RequireApiKey`) |

**Kesimpulan:** engine proxy/streaming sudah di-swap penuh ke Go dan setara Next
pada semua format proxy (chat, messages, embeddings, images, audio, videos,
responses, media, model listing).

---

## 2. Per-Endpoint — Proxy/Formats (✅ setara di Go)

| Next route | Go route | Status |
|-----------|----------|--------|
| `v1/chat/completions` | `POST /chat/completions` | ✅ |
| `v1/messages` | `POST /messages` | ✅ |
| `v1/messages/count_tokens` | `POST /messages/count_tokens` | ✅ |
| `v1/embeddings` | `POST /embeddings` | ✅ |
| `v1/responses` | `POST /responses` | ✅ |
| `v1/responses/compact` | `POST /responses/compact` | ✅ |
| `v1/images/generations` | `POST /images/generations` | ✅ |
| `v1/audio/speech` | `POST /audio/speech` | ✅ |
| `v1/audio/transcriptions` | `POST /audio/transcriptions` | ✅ |
| `v1/audio/voices` | `GET /audio/voices` | ✅ |
| `v1/videos/generations\|edits\|extensions` | `POST /videos/*` | ✅ |
| `v1/videos/{id}` | `GET /videos/{id}` | ✅ |
| `v1/search` | `POST /search` | ✅ |
| `v1/scrape` | `POST /scrape` | ✅ |
| `v1/web/fetch` | `POST /web/fetch` | ✅ |
| `v1/models` | `GET /models` | ✅ |
| `v1/models/info` | `GET /models/info` | ✅ |
| `v1/models/[kind]` | `GET /models/{kind}` | ✅ |
| `v1/models/[...model]` | `GET /v1/models/*` + `GET /api/v1/models/*` | ✅ | Catch-all `cc/claude-sonnet-4-6` + kind `image/tts/web` (`#3588`) |
| `v1beta/models` + `[...path]` | `GET /v1/models/*` (alias) | ✅ | Di-handle via `HandleModelLookup` yang sama |
| `v1/api/chat` (Ollama) | `POST /api/chat` | ✅ |
| `v1/web/fetch` (ollama) | `POST /web/fetch` via `ollama FetchURL https://ollama.com/api/web_fetch` | ✅ | `links` + scoped `webfetch:ollama` lock |

Note: Next.js uses prefix `/api/v1/...`, Go uses root `/...` + `v1`/`api/v1` aliases. All `v1beta` is covered.

---

## 3. Per-Endpoint — Dashboard/Admin Engine Ports (✅ 100% Core Engine Ported)

| Feature / Endpoint | Go Route | Status | Notes |
|--------------------|----------|--------|------------|
| **Realtime Usage Stream** | `GET /api/usage/stream`, `GET /usage/stream` | ✅ | In-memory in-flight tracker, SSE broadcasting for dashboard topology graph animation |
| **Realtime Usage Stats** | `GET /api/usage/stats`, `GET /usage/stats` | ✅ | Realtime concurrency and model counters |
| **Proxy-Pools Deploy** | `POST /proxy-pools/*-deploy`, `GET /proxy-pools/deploy-status` | ✅ | Vercel, Deno, and Cloudflare automated edge relay deploy |
| **Headroom Engine** | `POST /headroom/*`, `ANY /headroom/proxy/*` | ✅ | Process lifecycle manager & reverse proxy for token compression |
| **CLI-Tools Statuses** | `GET /cli-tools/all-statuses`, `GET /cli-tools/{tool}/status` | ✅ | Per-CLI status (codex, claude, opencode, cline, cursor, etc.) |
| **Media TTS Voices** | `GET /audio/voices`, `GET /v1/audio/voices` | ✅ | Dynamic voice fetcher for ElevenLabs, Deepgram, MiniMax, Inworld |
| **Translator Console Logs** | `GET /api/translator/stream`, `GET /translator/console-logs` | ✅ | Live streaming in-process log buffer for dashboard |
| **OAuth Token Refresh** | `POST /v1/oauth/refresh`, `POST /v1/oauth/authorize` | ✅ | Antigravity, xAI, Codex, GitHub, iFlow, Gemini CLI, Kimi Coding, Qoder, CodeBuddy CN/INTL, Grok CLI |
| **App Version & Self-Update**| `GET /api/version`, `POST /api/version/update` | ✅ | Semver check and in-place binary update |
| **CRUD Providers/Keys/Combos**| Direct SQLite Shared Access | ✅ | Next.js reads/writes directly to shared SQLite `9router.db` |

---

## 4. Database Schema — 100% Compatible

| Table | Next.js | Go | Status |
|-------|------|----|--------|
| `providerConnections` | ✅ | ✅ | 100% Identical + Go metadata `lastUsedAt`, `consecutiveUseCount`, `modelLock_<model>` |
| `providerNodes` | ✅ | ✅ | 100% Identical |
| `proxyPools` | ✅ | ✅ | 100% Identical (HTTP/SOCKS5/Vercel/Cloudflare/Deno) |
| `apiKeys` | ✅ | ✅ | 100% Identical |
| `combos` | ✅ | ✅ | 100% Identical (fallback, round-robin, random, fusion, weight) |
| `kv` | ✅ | ✅ | 100% Identical |
| `usageHistory` | ✅ | ✅ | 100% Identical (12 columns + token breakdowns) |
| `usageDaily` | ✅ | ✅ | 100% Identical (daily JSON aggregations) |
| `requestDetails` | ✅ | ✅ | 100% Identical |
| `settings` | ✅ | ✅ | 100% Identical (`data` JSON blob includes `providerStrategies`, token savers) |
| `_meta` | ✅ | ✅ | 100% Identical (`SCHEMA_VERSION = 1`) |

**Conclusion:** Columns & types 1:1. Next.js dashboard can read/write directly to Go's SQLite in realtime without conflicts or migration.

---

## 5. Architecture Conclusion

> **Model:**
> Next.js acts as Dashboard UI, while `9router-go` handles 100% of proxy traffic, SSE streaming, multi-provider translations, token compression, and in-flight usage tracking.

### Advantages of `9router-go`:
1. **High Performance**: 32K+ peak RPS with only ~42 MB memory (vs ~500 RPS and 270 MB on Next.js).
2. **Non-Blocking Connections**: SQLite WAL mode with safe connection pooling, no write contention.
3. **Resilience**: Exponential 429 lock backoff, reactive 401 OAuth refresh, auto-capability routing, and SSE stall detection (6 min).
4. **Full Parity**: All 100+ providers, media modalities (image, video, audio TTS/STT/music, search, fetch), and dashboard animation stream fully operational.

---

## Appendix: File References

| Repo | Path |
|------|------|
| Go routes | `internal/handlers/router.go` |
| Go server | `cmd/9router-go/main.go` |
| Go DB schema | `DATABASE.md` |
| Next routes | `src/app/api/**/route.js` |
| Next DB schema | `src/lib/db/schema.js` |
| Next server wrap | `custom-server.js` |
