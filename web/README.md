# 9router-go Dashboard

The dashboard is a Svelte 5 single-page application built with Vite, TypeScript, and Tailwind CSS 4. It calls the native Go server for dashboard and proxy APIs; it does not access SQLite directly and does not require a JavaScript runtime in production.

## Architecture

- `src/main.ts` mounts the Svelte application with `mount(App, ...)`.
- `src/App.svelte` owns application-level navigation and view selection.
- `src/components/` contains the dashboard views and reusable UI components.
- `src/api/client.ts` provides the typed browser API client.
- `src/lib/` contains shared Svelte state, model helpers, and UI primitives.
- `vite.config.ts` enables the Svelte and Tailwind plugins. During development, Vite proxies `/api`, `/v1`, `/usage`, `/translator`, `/debug`, and `/admin` to `http://localhost:20130`. (`/debug` covers `/debug/traces` latency tracing and pprof; `/admin` covers `POST /admin/health/reset`.)

Components use Svelte 5 runes such as `$state`, `$derived`, and `$effect`, along with modern Svelte event handlers such as `onclick` and `onchange`.

## Development

Run the Go API server and the Vite dev server in **two terminals** — frontend changes hot-reload (HMR) with **no rebuild** and no binary restart:

```bash
make dev        # terminal 1: Go API on :20130 (go run, auto-rebuild on Go changes)
make web-dev    # terminal 2: Vite dev server + HMR on :5173
```

Open the Vite URL printed by `make web-dev` (default `http://localhost:5173`); API requests under `/api`, `/v1`, `/usage`, `/translator`, `/debug`, and `/admin` are proxied to the Go server. Static assets from `web/public/` (`/providers/*.png`, `/icons/*`, `/sw.js`, `/manifest*`) are served directly by Vite.

Equivalently, from this directory: `bun install --frozen-lockfile && bun run dev`.

> Dev-only caveat: the PWA service worker (`sw.js`) registers in dev too. If a page looks stale in the browser, hard-refresh or unregister the service worker via DevTools → Application → Service Workers.

## Scripts

```bash
bun run dev       # start Vite with hot module replacement
bun run build     # type-check, then build the production bundle in dist/
bun run lint      # run Oxlint
bun run preview   # serve the production bundle locally
```

`npm run <script>` is also supported for the same package scripts, but the committed `bun.lock` is the project's reproducible dependency source.

## Production Integration

`bun run build` writes static assets to `web/dist`. `web/embed.go` embeds that directory with Go's `embed` package, and the compiled Go binary serves the files through `web.Handler()`. The handler serves existing assets directly and falls back to `index.html` for client-side routes, so production needs no separate web server.

Build the frontend and binary together from the repository root:

```bash
make web-build
make build
```

`make build` runs `web-build` first, but the target skips installation/build when `web/dist/index.html` already exists. Use `FORCE=1 make web-build` after frontend changes so fresh assets are embedded.

## Testing and Caveats

This package currently defines development, build, lint, and preview scripts, but no frontend unit-test, component-test, end-to-end-test, or `svelte-check` script. Validate UI changes by running the Go server and exercising the affected dashboard path in a browser; validation of backend endpoints remains in the Go test suites.

The frontend implements the upstream Next.js dashboard through Svelte 5 components. Preserve the public dashboard routes, API request/response contracts, and visible behavior while following Svelte 5 syntax.

Upstream parity means behavioral compatibility; it does not mean sharing the upstream component implementation.
