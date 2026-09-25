# 9router-go — Team Engineering & AI Collaboration Guidelines

This repository (`9router-go`) is the high-performance, native Golang implementation and companion of [**decolua/9router**](https://github.com/decolua/9router).

---

## 1. Upstream Source of Truth & Local Reference

Whenever you implement features, fix bugs, add providers, update routing logic, or verify API parity, **always treat the upstream project as the behavioral specification**.

- **Upstream Repository**: `https://github.com/decolua/9router`
- **Local Clone Path**: `/Users/luqmannul.hakim/htdocs/9router`

### Parity Protocol
1. **Always Inspect Local Upstream First**:
   - Before writing or modifying logic, inspect the corresponding code in `/Users/luqmannul.hakim/htdocs/9router`.
   - Check upstream git history, PRs, issues, and vitest unit tests in `/Users/luqmannul.hakim/htdocs/9router/tests/` to understand edge cases, headers, and protocol nuances.
2. **Behavioral Compatibility (100% Contract Match)**:
   - Request and response schemas for `/v1/*` (Chat Completions, Messages, Embeddings, Audio, Models).
   - Dashboard endpoints `/api/*` (Connections, Combos, ProxyPools, Settings, ProviderNodes, Usage, Auth).
   - Combo expansion, model fallback, account rotation, and strike-breaker quota handling.
   - SQLite database compatibility (`~/.9router/db/data.sqlite`).
3. **Changelog Tracking**:
   - When porting features or fixes, reference the upstream commit/issue/PR in `CHANGELOG.md` (e.g. `upstream decolua/9router#4197 parity`).

---

## 2. Architecture & Codebase Mapping

| Upstream Path (`/Users/luqmannul.hakim/htdocs/9router`) | 9router-go Path | Description & Role |
| :--- | :--- | :--- |
| `open-sse/translator/` | `internal/translator/` | Protocol format converters (OpenAI ↔ Claude Messages ↔ Gemini ↔ Antigravity). |
| `open-sse/executors/` | `internal/proxy/executor/`, `internal/proxy/` | Upstream provider callers, SSE stream adapters, tool ID repair, cloaking. |
| `open-sse/providers/registry/`, `open-sse/config/providerModels.js` | `internal/providers/` | Provider definitions (120+), model catalogs (`registry_models.go`), aliases (`aliases.go`), OAuth (`oauth.go`). |
| `open-sse/handlers/chatCore.js`, `src/sse/handlers/chat.js` | `internal/handlers/chat/` | Chat routing, combo fallback, fusion, resolution, quota handling. |
| `open-sse/rtk/` | `internal/tokensaver/` | Prompt token compression (RTK), Caveman, and Ponytail system prompts. |
| `src/app/api/` (Next.js API routes) | `internal/handlers/dashboard/`, `internal/handlers/` | Dashboard REST & SSE APIs (connections, combos, proxypools, usage, settings). |
| `src/lib/db/` | `internal/db/` | SQLite database layer (using pure-Go `modernc.org/sqlite`). |
| `src/app/` (Next.js React 19 Frontend) | `web/src/` | Dashboard UI: Built with **Svelte 5 + Vite 8**, embedded into the binary via `web/embed.go`. |
| `cli/` (npm launcher package) | `cmd/9router-go/main.go` | Single-binary CLI launcher and runtime daemon. |
| `tests/` (Vitest suites) | `*_test.go` | Go unit and integration test suites. |

---

## 3. Provider Independence & Anti-Hardcoding Principles (STRICTLY MANDATORY)

Every upstream provider in this repository (`antigravity`, `opencode`, `claude`, `codex`, `cline`, `freebuff`, `xai`, `gemini-cli`, `qoder`, etc.) is **strictly distinct, independent, and isolated**. You **MUST NEVER** merge, conflate, cross-alias, or hijack one provider into another.

### A. Strict Provider Isolation (No Cross-Hijacking)
1. **Never Confuse or Conflate Providers**:
   - `antigravity` is **NOT** `opencode`. They have completely separate upstream APIs, different authentication mechanisms (Google Cloud Code OAuth tokens vs OpenCode public tokens), and separate model catalogs.
   - A request targeted at `ag/<model>` or `antigravity/<model>` must **always and only** be routed to the Antigravity executor.
   - An OpenCode model (e.g. `space-bunny-free`, `muse-spark-*`, `big-pickle`, `*-free`) belongs exclusively to `opencode` (`oc/`). It must **never** be injected into, aliased to, or redirected from Antigravity.
2. **Zero Cross-Provider Aliasing**:
   - `ResolveProviderProxyPoolID` and connection lookups must only check genuine aliases (e.g. `ag` ↔ `antigravity`, `oc` ↔ `opencode`, `cline` ↔ `clinepass`). Never fall through from `antigravity` to `opencode` or vice versa.

### B. Ban on Hardcoded Model Rewrites
- **No Substring-Based Provider Switching**:
  - ❌ `if (provider == "antigravity" || provider == "ag") && strings.Contains(model, "muse-spark") { provider = "opencode" }` (Strictly forbidden).
  - ❌ `if isOpenCodeModel(model) { provider = "opencode" }` (Strictly forbidden).
- **Uniform Provider Handling**:
  - Every provider must handle all of its models uniformly ("seluruh provider disamakan").
  - Routing decisions must be driven by explicit catalog registration, model aliases in the database, custom provider node prefixes, or standard `provider/model` wire syntax—**never by ad-hoc string inspections**.

### C. General vs Custom Abstractions
- **Shared / General Abstractions**:
  - If a mechanism is general across providers (e.g. streaming SSE adapters, HTTP retry with direct fallback on proxy errors, token estimation, client session resolution), encapsulate it into an agnostic, generally named package or helper:
    - ✅ `proxy.NewFallbackTransport(...)`
    - ✅ `proxy.ForwardOpenAI(...)`
    - ✅ `handlerutil.ExtractSessionID(r)`
    - ✅ `translator.ConcealFingerprintTools(...)`
- **Provider-Specific Custom Logic**:
  - If a provider has unique protocol requirements, custom headers, or quirky payload envelopes, build a dedicated, self-contained executor or handler file:
    - ✅ `internal/translator/antigravity.go` (Google Cloud Code assist envelope)
    - ✅ `internal/proxy/executor/freebuff.go` (Freebuff multi-session & client_id cloaking)
    - ✅ `internal/proxy/executor/opencode.go` (OpenCode fingerprinting & Responses API transformation)
    - ✅ `internal/handlers/media/antigravity_image.go` (Antigravity image generation)
  - **Rule**: Provider-specific custom logic must stay strictly within that provider's execution path. It must **never** leak into generic routing, sibling provider paths, or universal middleware.

### D. Dashboard & UI Parity Discipline
- **Respect Registry Flags (`modelsFetcher`)**:
  - The "Suggested free models" UI section must strictly depend on the provider's `modelsFetcher` declared in the catalog.
  - Never add artificial `else if (providerId === '...')` blocks in `ProviderDetailView.svelte` to inject models from one provider into another.
  - Buttons like `Import from /models` must only appear for providers that genuinely support dynamic catalog discovery upstream (`cline`, `clinepass`, `qoder`, `qoder-cn`).

---

## 4. Golang Engineering & Optimization Principles
> **CORE MANDATE: Do NOT blindly transliterate JavaScript / TypeScript into Go!**  
> While the observable external behavior and API contract must match upstream 100%, the internal Go implementation must leverage Go's performance, strong typing, low memory footprint, and idiomatic concurrency.

### A. Performance & Zero-Allocation Streaming
- **Stream, Don't Buffer**:
  - Upstream JS often buffers entire response bodies or converts buffers to strings. In Go, stream SSE chunks directly using `io.Reader`, `io.Writer`, `bufio.Reader`, or `bufio.Scanner`.
  - Avoid buffering whole LLM streaming responses in memory unless modification/repair is strictly required.
- **Minimize Memory Allocations**:
  - Reuse byte slices and buffers with `bytes.Buffer` or `sync.Pool` in hot proxy forwarding paths.
  - Avoid unnecessary `[]byte(str)` and `string(bytes)` conversions in hot loops.
  - Use `json.RawMessage` to forward opaque/passthrough payload segments without unmarshaling into generic intermediate `map[string]any`.
- **HTTP Transport Hygiene**:
  - Reuse `*http.Client` instances with pooled `http.Transport` (`MaxIdleConnsPerHost`, keep-alives enabled, appropriate response header timeouts). Never instantiate an unconfigured `&http.Client{}` per request.

### B. Concurrency & Goroutine Safety
- **Context Propagation**:
  - Always accept and propagate `context.Context` down to database queries, HTTP requests, and background workers. Respect client disconnections (`r.Context()`) immediately to abort in-flight upstream LLM calls.
- **Prevent Goroutine Leaks**:
  - Every spawned goroutine must have a deterministic lifecycle tied to a context or channel exit condition.
- **Concurrency Control & Synchronization**:
  - Guard mutable shared state (token caches, strike counters, rate limiters, proxy pools) using unexported `sync.RWMutex` / `sync.Mutex` followed immediately by `defer mu.Unlock()`.
  - Never concurrently read/write Go maps without mutex protection.
  - Use `golang.org/x/sync/singleflight` to collapse duplicate concurrent requests for identical external resources (e.g. OAuth token refresh, remote catalog sync).

### C. Type Safety vs Dynamic JSON
- **Strong Typing Over `map[string]any`**:
  - Use strongly typed structs for known wire protocols (OpenAI Chat Completions, Anthropic Messages, Gemini GenerateContent, Antigravity envelopes).
  - Use unexported fields or typed wrappers instead of passing untyped maps across package boundaries.
  - Reserve `any` / `map[string]any` strictly for dynamic, provider-specific, or open-ended passthrough payloads.
- **Clean JSON Serialization**:
  - Use accurate struct tags (`json:"fieldName,omitempty"`). Omit zero-value fields when required by upstream provider specs.

### D. Code Style, Modularity & Cognitive Complexity
- **Cognitive Complexity**: Maintain SonarQube cognitive complexity $\le 15$ per function/method.
- **Guard Clauses**: Keep the happy path left-aligned using early returns. Avoid nested `if/else` ladders (3+ levels forbidden). Decompose branching into focused private helper functions.
- **File Size Discipline**: Target files $\le 300$ LoC (modularize files $>400$ LoC into focused files or subpackages).
- **Dependency Injection**: Use Uber Fx (`go.uber.org/fx`) in `internal/app/` for clean wiring. Usecases and handlers must never perform defensive `nil` checks on injected dependencies—fail fast during startup container initialization.
- **Functional Helpers**: Prefer [`github.com/samber/lo`](https://github.com/samber/lo) (`lo.Map`, `lo.Filter`, `lo.Contains`, `lo.Ternary`, `lo.ToPtr`) for clean slice operations instead of handwritten boilerplate loops.

### E. Error Handling
- **Wrap Errors with `%w`**: Always provide caller context: `fmt.Errorf("receiver.MethodName: %w", err)` or `fmt.Errorf("op: %w", err)`.
- **Error Inspection**: Use `errors.Is(err, target)` for sentinels and `errors.As(err, &target)` for custom types. Never rely on `strings.Contains(err.Error(), ...)`.
- **Zero Panic Policy**: No `panic()` in handlers, services, or proxy execution. Recover gracefully and emit appropriate HTTP/SSE error payloads.

---

## 5. Idiomatic Go Unit Testing Convention (`testing.T`)

All unit tests in this repository **MUST** use Go's standard library `testing` package (`testing.T`). Do **NOT** introduce heavy external BDD test frameworks (such as Ginkgo/Gomega or testify suite) to keep `go.mod` clean, compilation fast, and the test suite unified across all packages.

### 0. Regression-Test-or-It-Didn't-Happen (HARD RULE, 2026-09-26)

Every bug fix — engine (Go) or module layer (shell/JS) — MUST ship a **regression test that reproduces the original failure** before it can be marked done in `docs/FIXPLAN.md`. A documentation hint, a comment, or a UI warning alone is **not a fix** (lesson: the "backup import wipes apiKeys → model tests 401" issue was previously "fixed" with a hint text in `webroot/index.html` and resurfaced within a day). Requirements:

1. The test fails on the pre-fix code and passes on the post-fix code.
2. Engine fixes: Go test in the same package (e.g. `TestSetupServerRouter_ModelTestSessionAuth`).
3. Module JS fixes: node --test case in `module/webroot/test/` (parsers / bridge-commands suites).
4. Shell environment quirks (mksh vs POSIX: `[^…]` caret is a LITERAL in mksh/dash — use `[!…]`; `|` in parameter-expansion patterns is ALTERNATION — escape as `\|`) must be captured in a test or a comment with the device-verified evidence.

### A. Table-Driven Tests Pattern
For functions with multiple inputs, edge cases, or status transformations, always structure tests using table-driven tests:

```go
func TestResolveModelAlias(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "resolves known alias",
			input:    "cbcn",
			expected: "codebuddy-cn",
			wantErr:  false,
		},
		{
			name:     "canonical id passes through",
			input:    "openai",
			expected: "openai",
			wantErr:  false,
		},
		{
			name:     "unknown alias returns error",
			input:    "unknown-xyz",
			expected: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveModelAlias(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ResolveModelAlias() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.expected {
				t.Errorf("ResolveModelAlias() = %q, want %q", got, tt.expected)
			}
		})
	}
}
```

### B. HTTP Handler Testing (`httptest`)
Use `net/http/httptest` (`httptest.NewRecorder()`, `httptest.NewRequest()`, and `httptest.NewServer()`) for testing endpoints, SSE streams, middleware, and reverse proxy forwarding:
- Verify HTTP response status codes: `if rec.Code != http.StatusOK { t.Fatalf(...) }`.
- Verify response headers (e.g. `Content-Type: text/event-stream`).
- Inspect response body or unmarshal JSON into typed structs rather than `map[string]any`.

### C. Testing Best Practices
1. **Meaningful Subtest Names**: Use clear `t.Run("describes behavior/condition", ...)` naming.
2. **Fail Fast vs Accumulate**:
   - Use `t.Fatalf()` when a failure invalidates subsequent checks (e.g. nil pointer, unexpected error, failed setup).
   - Use `t.Errorf()` when verifying independent fields of a struct.
3. **No External Network Dependencies in Unit Tests**:
   - Use `httptest.NewServer` or mock clients to simulate upstream LLM providers and OAuth endpoints.
   - Tag live/upstream-dependent network tests clearly (e.g. `live_e2e_test.go` or guard with env checks) so that standard unit tests pass completely offline.
4. **Deterministic & Isolated**:
   - Tests must clean up temporary database files or use in-memory SQLite (`:memory:`) where applicable.
---

## 6. Frontend Engineering & Svelte 5 Standards (`web/`)

The dashboard UI in `web/` is built with **Svelte 5 + Vite 8 + TypeScript + Tailwind CSS**, compiled to `web/dist`, and embedded directly into the Go binary via `embed.go` (`//go:embed all:dist`).

> **Upstream Parity Notice**: While upstream `9router` uses Next.js 16 and React 19 (`useState`, `useEffect`, JSX), `9router-go` uses **Svelte 5**. All UI features, views, and modals from upstream must be ported to idiomatic Svelte 5.

### A. Svelte 5 Runes (STRICTLY MANDATORY)
Always write modern Svelte 5 code using Runes. **Never use legacy Svelte 3/4 syntax.**

1. **Component Props (`$props`)**:
   - Define an explicit TypeScript `interface Props { ... }` or inline type.
   - Use `$props()` to destructure with defaults:
     ```svelte
     <script lang="ts">
       interface Props {
         title: string
         isOpen?: boolean
         count?: number
         onClose?: () => void
       }

       let {
         title,
         isOpen = false,
         count = 0,
         onClose = () => {},
       }: Props = $props()
     </script>
     ```
   - Use `$bindable()` for two-way bound props: `let { activeTab = $bindable('endpoint') } = $props()`.
   - ❌ **Forbidden**: `export let title = ''` (legacy Svelte 3/4 syntax).

2. **Reactivity (`$state`, `$derived`, `$effect`)**:
   - **State**: Use `$state(initialValue)` for local reactive variables: `let loading = $state(false)`.
   - **Derived Values**: Use `$derived(expression)` instead of legacy `$:` labels:
     ```svelte
     let filteredItems = $derived(
       items.filter(i => i.name.toLowerCase().includes(search.toLowerCase()))
     )
     ```
   - **Side Effects**: Use `$effect(() => { ... })` instead of legacy `$:` reactive statements:
     ```svelte
     $effect(() => {
       if (isOpen) {
         loadData()
       }
     })
     ```

3. **Snippets over Slots (`Snippet` & `{@render}`)**:
   - Use Svelte 5 snippets for slot content:
     ```svelte
     <script lang="ts">
       import type { Snippet } from 'svelte'
       let { children, footer }: { children?: Snippet; footer?: Snippet } = $props()
     </script>

     <div class="content">{@render children?.()}</div>
     {#if footer}<div class="footer">{@render footer()}</div>{/if}
     ```
   - ❌ **Forbidden**: `<slot />` or `<slot name="footer" />`.

4. **Modern Event Handlers**:
   - Use lowercase HTML event attributes: `onclick={handleClick}`, `onkeydown={handleKeyDown}`, `onchange={handleChange}`.
   - ❌ **Forbidden**: `on:click={handleClick}` (legacy Svelte 3/4 syntax).

---

### B. Upstream React 19 $\rightarrow$ Svelte 5 Porting Matrix

| Upstream Next.js / React 19 | 9router-go Svelte 5 Equivalent |
| :--- | :--- |
| `useState(initial)` | `let val = $state(initial)` |
| `useMemo(() => compute(x), [x])` | `let computed = $derived(compute(x))` |
| `useEffect(() => { ... }, [deps])` | `$effect(() => { ... })` |
| `const ref = useRef(null)` | `let el = $state<HTMLElement \| null>(null); <div bind:this={el}>` |
| `{condition && <Component />}` | `{#if condition}<Component />{/if}` |
| `{items.map(item => <Row key={item.id} />)}` | `{#each items as item (item.id)}<Row {item} />{/each}` (always key with `(item.id)`) |
| `value={val} onChange={e => setVal(e.target.value)}` | `bind:value={val}` |
| `props.children` | `{@render children?.()}` |

---

### C. Design Tokens & Styling (Tailwind CSS)

Always use the established design tokens defined in `web/src/index.css` for consistent appearance across light and dark themes:

- **Brand (Terracotta)**: `bg-brand-500`, `text-brand-500`, `bg-primary`, `text-primary`, `hover:bg-primary-hover` (`#e56a4a`).
- **Surfaces**: `bg-surface`, `bg-surface-2`, `bg-surface-3`, `bg-sidebar`.
- **Borders**: `border-border`, `border-border-subtle`.
- **Text**: `text-text-main`, `text-text-muted`, `text-text-subtle`.
- **Radii**: `rounded-[10px]`, `rounded-[14px]`, `rounded-2xl`.
- **Shadows**: `shadow-[var(--shadow-warm)]`, `shadow-[var(--shadow-elev)]`.
- **Icons**:
  - Google Material Symbols: `<span class="material-symbols-outlined text-[18px]">icon_name</span>`
  - Lucide Svelte: `import { IconName } from 'lucide-svelte'; <IconName size={18} />`

---

### D. API Integration & Routing

1. **Centralized API Client**:
   - All HTTP requests to backend endpoints must go through `web/src/api/client.ts` (`api.getConnections()`, `api.getCombos()`, `api.getSystemVersion()`, etc.).
   - Define strongly typed TypeScript interfaces in `client.ts` or view-specific `types.ts`.
2. **Single Page Routing**:
   - Navigation tabs are managed via `web/src/lib/router.ts` (`ActiveTab` and `TAB_ROUTES`).
   - Modal dialogs should handle `Escape` key and click-outside backdrop dismissals.

---

## 7. Upstream Sync Workflow (Step-by-Step)

When tasked with syncing a feature, bugfix, or provider from upstream:

1. **Investigate Upstream**:
   ```bash
   # Check recent upstream commits, tags, or file changes
   git -C /Users/luqmannul.hakim/htdocs/9router log -n 5 --oneline
   # Inspect specific upstream file or test
   cat /Users/luqmannul.hakim/htdocs/9router/open-sse/...
   ```
2. **Locate Equivalent Component**:
   - Backend: Use the **Architecture & Codebase Mapping** table in Section 2 (`internal/...`).
   - Frontend: Identify matching page/component in `web/src/components/...`.
3. **Design for Go & Svelte 5**:
   - Backend: Plan Go-specific optimizations (streaming buffers, strong types, mutexes, singleflight).
   - Frontend: Convert React logic into Svelte 5 runes (`$state`, `$derived`, `$effect`, `bind:value`).
4. **Implement & Test**:
   - Backend: Write code in `internal/...` and unit tests in `*_test.go` (table-driven tests using `testing.T`).
   - Frontend: Write/update components in `web/src/` and verify `cd web && bun run build`.
5. **Verify**:
   ```bash
   rtk go test ./...
   make build
   ```
6. **Update Changelog**:
   - Add entry to `CHANGELOG.md` under `[Unreleased]` detailing the parity sync.

---

## 8. Daily Commands Cheat-Sheet

```bash
# Run unit tests
rtk go test ./...
rtk go test ./internal/providers/... -v

# Build frontend SPA (Svelte 5 / Vite 8 via Bun)
make web-build
# Or directly inside web/
cd web && bun run build

# Build binary (automatically builds web SPA into web/dist first)
make build

# Run dev server with live go run (PORT=20130 default)
make dev

# Git operations (always prefix with rtk)
rtk git status
rtk git diff
```
