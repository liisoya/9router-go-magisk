# 9router-go vs Historical Upstream 9Router

This document compares the current native `9router-go` v1.9.1 with the local historical upstream checkout (`decolua/9router` v0.5.85, Next.js dashboard and gateway). It is not a provider-by-provider certification.

## Version and comparison scope

`9router-go` v1.9.1 is the current release (`VERSION`, `version.json`, `internal/updater.CurrentVersion`). The declared manifest/README sync target is upstream v0.5.85. `CHANGELOG.md` separately records two selected v0.5.86 parity ports and explicitly defers one feature; that does not make the whole v0.5.86 release synced. Treat the release declarations as a known documentation gap until they are reconciled.

Statuses below mean:

- **Current**: implemented in the Go binary and/or embedded Svelte SPA now.
- **Parity target**: a path or behavior intentionally aligned with upstream v0.5.85; parity is scoped, not universal.
- **Accepted limitation**: current native behavior differs intentionally or is explicitly deferred.
- **Future work**: no current claim of completion.

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

## Architecture

| Area | Current 9router-go | Historical upstream v0.5.85 | Classification |
|------|--------------------|-----------------------------|----------------|
| Runtime | One Go server, default port `20130` | Next.js/Node gateway and dashboard, production port `20128` | Different architecture |
| Dashboard | Svelte 5 + Vite SPA embedded into the Go binary (`web/`, `web/embed.go`) | Next.js App Router / React dashboard | Current native implementation, not the historical UI |
| Routing | chi router (`internal/handlers/router.go`) | Next route handlers and Node routing stack | Different architecture |
| Persistence | SQLite via `modernc.org/sqlite`, WAL | SQLite layer with versioned/additive migrations and backups | Shared data contract with material differences; see `DATABASE.md` |
| Distribution | Standalone Go binary, Docker image, embedded web assets | Node/Bun application plus separate CLI launcher | Different distribution |
| Proxy engine | Native Go routing, translation, SSE and provider executors | Node engine under `open-sse` and Next API routes | Ported/parity-tested behavior, not source-level equivalence |

The current dashboard is served by the same binary as the gateway. It is not a browser UI for a separately running historical Next.js process.

## Proxy and media routes

The current chi router mounts the following primary routes. Unlike upstream, most operations are root-native; the router does not register a blanket `/v1/*` middleware alias. The explicit `/v1` registrations are limited (notably models and video job paths), so clients that depend on upstream's `/api/v1/...` or `/v1/...` rewriting must use the routes actually exposed by the Go server.

| Capability | Current Go route | Classification / limit |
|------------|------------------|-----------------------|
| OpenAI chat | `POST /chat/completions` | Current parity target; root-native, not a general `/v1` alias |
| Claude messages and count | `POST /messages`, `/messages/count_tokens` | Current parity target |
| Responses | `POST /responses`, `/responses/compact` | Current parity target |
| Embeddings | `POST /embeddings` | Current parity target |
| Images | `POST /images/generations` | Current; model behavior depends on configured providers |
| Audio | `POST /audio/speech`, `/audio/transcriptions`; `GET /audio/voices` | Current; no claim of identical provider/model catalogs |
| Video | `POST /videos/generations|edits|extensions`; `GET /videos/{id}` | Current async job surface; provider support is conditional |
| Search/fetch | `POST /search`, `/scrape` | Current; upstream provider-specific behavior is not exhaustively certified |
| System One | `POST /systemone` | Current v0.5.85-aligned route |
| Models | root, `/v1`, and `/api/v1` model/catalog routes | Current aliases; catalogs remain provider/config dependent |

Do not infer provider parity from a shared route. Provider executors, model aliases, model capabilities, cloaking, OAuth behavior, and provider account policies can differ at any release. A feature is only parity-classified after a source-backed, path-specific comparison or executable contract test.

## Native dashboard coverage

The embedded Svelte SPA currently contains these main areas:

- endpoint and API-key setup;
- providers, compatible nodes, API-key/OAuth connections, probes, and per-connection detail;
- combo creation/editing, routing strategies, judge/fusion configuration, and vision/audio capacity adapters;
- usage analytics, real-time topology, request details, quota tracker, and live console log;
- proxy pools and Vercel/Cloudflare/Deno deployment helpers;
- media views for embedding, image, TTS, STT, video, System One, and web tools;
- token saver/Headroom controls, tunnel/Tailscale status, settings, and profile configuration; and
- Agent Skills instructions and in-app version/changelog UI.

These are current native surfaces. The changelog often calls individual ports “full parity,” but that wording is per feature; it is not evidence that every upstream screen, provider, locale, or route is complete.

## Authentication and operations

| Area | Current 9router-go | Upstream alignment / difference |
|------|--------------------|--------------------------------|
| Client API auth | SQLite-backed `apiKeys`; proxy routes accept bearer or `X-API-Key`; SSE stream routes also accept query keys | Scoped compatibility, not identical middleware |
| Dashboard session | HS256 `auth_token` cookie, 24-hour expiry, local CLI token, login lockout/tunnel checks | Mirrors important upstream contracts |
| Admin boundary | Shutdown, update, database export/import, and health reset require dashboard session/local CLI token, not client API keys | Intentional upstream-aligned protection |
| OIDC/SAML | Current routes test OIDC/SAML configuration and serve SAML metadata; the Svelte login links target start/callback routes that are not currently mounted by the Go router | Accepted partial difference; configuration UI is not a complete SSO login flow |
| Default password compatibility | `INITIAL_PASSWORD` is used when configured; the dashboard code still accepts upstream's well-known `123456` fallback and forces change for remote access | Compatibility behavior, not a recommendation |
| Tunnel/Tailscale | Native status and enable/disable endpoints | Current; exact upstream cloud behavior is not claimed |
| Auto-update | Go manifest/GitHub release check and in-place update | Current operational feature with the integrity limitation in `TECHNICAL_DEBT.md` |
| Cloud sync | No current native cloud-sync implementation | Accepted limitation versus historical upstream features |
| Historical npm CLI/tray | Embedded dashboard plus Go CLI/binary distribution | Accepted difference; no claim that npm tray behavior is ported |

## Database and backup relationship

Go and upstream use the same default `DATA_DIR/db/data.sqlite` path and mostly shared table/JSON conventions. This is **schema compatibility for exercised paths**, not “100% identical schema”:

- upstream bootstraps and migrates core tables; Go production startup does not;
- Go creates only the optional `upstream_leases` table;
- Go success paths optionally write `lastUsedAt` and `consecutiveUseCount`, columns absent from upstream v0.5.85;
- `_meta` belongs to upstream migration handling and is not consumed by Go;
- Go's dashboard JSON export covers configuration, not all usage/diagnostic/lease data; and
- shared JSON blobs must be verified field by field.

`DATABASE.md` is authoritative for bootstrap, permissions, backup, and multi-process limits.

## Current differences and accepted limitations

1. **Blank DB is not production-ready for Go alone.** Bootstrap/migrate with compatible upstream first; do not claim migration-free fresh installation.
2. **Public route aliases are incomplete.** Go exposes many operations at root paths and does not implement upstream's blanket `/api/v1/*` and `/v1/*` rewrites. Model lookup has selected aliases; client compatibility must be checked per route.
3. **Provider/model parity is partial.** For example, upstream v0.5.85 lists Qoder CN and Xiaomi MiMo desktop work, while the native registries do not establish the same coverage and the changelog explicitly defers MiMo v2.6 desktop login.
4. **Single active Go writer is the supported operational model.** WAL avoids immediate lock errors but does not make settings merges, daily aggregates, or caches process-coherent.
5. **Plaintext credential storage.** Upstream-compatible SQLite stores provider/client secrets; filesystem controls reduce exposure but do not encrypt at rest.
6. **OIDC/SAML is incomplete as a login flow.** Configuration tests and metadata are not successful end-to-end SSO parity.
7. **Dynamic CLI-tool configuration is not ported.** Native status/instructions exist, but upstream v0.5.85's per-tool configuration management is not present.
8. **Dashboard MITM and Translator workbench are not ported.** Go has a MITM engine and console stream, but not upstream's complete dashboard management surfaces or Translator workbench.
9. **Analytics, pricing, and backup transport differ.** Some declared breakdown fields are not populated, pricing is approximate rather than upstream's full DB-backed pricing behavior, and the Svelte backup download/import transport does not currently match the handler's re-auth/JSON contract.
10. **Combo surface is not full v0.5.85 parity.** Core strategies and capacity adapters are implemented, while upstream preset/bulk-management behavior is not established as current.
11. **No native cloud-sync or npm tray parity.** The dashboard supports local configuration handling, not historical cloud synchronization or the upstream npm tray launcher.
12. **Version declarations are inconsistent.** v1.9.1 declares upstream v0.5.85 sync while the changelog separately lists selected v0.5.86 ports. Resolve release metadata before describing a blanket version sync.

## Future-work candidates

- add a versioned Go schema manifest, compatibility probe, and explicit migration/bootstrap strategy;
- add deliberate public route aliases or a compatibility middleware rather than relying on path-specific assumptions;
- finish or remove the Svelte OIDC/SAML start/callback surface so the UI does not advertise unsupported login paths;
- complete or remove the dashboard backup controls and align their request contract with the Go handlers;
- decide whether dynamic CLI-tool configuration, dashboard MITM, Translator workbench, cloud sync, tray launcher, localization, or other upstream features are required for a later release;
- create a generated, source-backed endpoint/provider parity inventory; and
- refresh benchmark and comparison artifacts against the current binary and clearly identify historical results.

## Evidence

- Current version: `VERSION`, `version.json`, `internal/updater/updater.go`
- Current route and embedded UI surface: `internal/handlers/router.go`, `web/src/App.svelte`, `web/src/components/Sidebar.svelte`
- Current authentication: `internal/middleware/auth.go`, `internal/middleware/dashboard_auth.go`, `internal/auth/session.go`
- Current SSO routes: `internal/handlers/sso/sso.go`, `internal/handlers/router.go`
- Database contract: `DATABASE.md`
- Historical upstream: `/Users/luqmannul.hakim/htdocs/9router/package.json` and `src/`
