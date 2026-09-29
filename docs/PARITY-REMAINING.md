# LANJUT PERTAMA — buka file ini dulu

> Berkas ini tinggal di `docs/PARITY-REMAINING.md`. Sebelumnya di luar repo
> (`~/gomod/project/PARITY-REMAINING.md`); dipindah ke sini agar ikut ter-versioning.

**Snapshot 2026-09-28 malam.** `main` = `7e321f0d` (dulu `76012230` — ada commit lain yang masuk setelah
snapshot diambil). Audit tag upstream `v0.5.86..v0.5.91`.

Yang sudah **terpush** (semua hijau, tidak ada file separuh jadi):

| Branch | Commit | Isi |
|:---|:---|:---|
| `parity/responses-translator` | `36b17d46` | Chat SSE → Responses SSE, fix #4307, `reasoning_details` |
| | `b3af67ad` | arah request Responses → Chat Completions |
| | `0eafdedf` | clamp `max→high` mimo |
| | `5c9a654d` | **diff 3 + diff 4: wiring `/v1/responses` + non-streaming** |
| | `3a2f8e94` | empat jalur yang terlewat dari bridge (antigravity, mimo-free non-stream, `/responses/compact`, kode mati) |
| `chore/gofmt` | `c8e9de66` | gofmt 32 file, branch terpisah dari main |

Empat keputusan yang menggantung **sudah diambil**: clamp generik (dipanggil dari jalur mimo dulu) ·
urutan clamp dulu baru diff 3 · biaya Usage **tetap $0 seperti upstream** · gofmt **ya, commit sendiri**.
**Tidak ada keputusan terbuka.**

### Tugas berikutnya: `thinkingUnified.js` (§2) atau gap lain di bawah

Stack **Responses sudah tuntas** — diff 1, 2, 3, dan 4 semuanya terpush, `go build ./...` bersih,
`go test ./...` hijau, dan smoke live terhadap binary sudah membuktikannya (lihat §1).

---

## Ringkasan gap yang tersisa

- **thinkingUnified.js** utuh · audit jalur deepseek selain mimo
- **Claude:** reset grant free tier · guard `ANTHROPIC_AUTH_TOKEN`
- **Codex:** multi model profile `~/.codex/*.config.toml`
- **Dashboard:** combo limits · vision adapter tabel · Tailwind health wait · lazy-load chart · tray arm64
- **Bug test `-count=2`** — dua hipotesis sudah ruled out, jangan diulang
- **Penentuan "Responses-native" masih per-provider, upstream-nya per-koneksi** — lihat §6

---

## 1. Penerjemah Chat → Responses — ✅ 4 dari 4 selesai

`POST /v1/responses` ke upstream yang **bukan** Responses-native.

Worktree + branch: `/Users/luqmannul.hakim/gomod/project/9router-go-wt` → `parity/responses-translator`
(sudah push ke origin).

| Diff | Isi | Status |
|:---|:---|:---|
| 1 | Chat SSE → Responses SSE + `reasoning_details` | ✅ `36b17d46` |
| 2 | Arah request: body Responses → body Chat Completions | ✅ `b3af67ad` |
| 3 | Wiring di `HandleResponses` | ✅ `5c9a654d` |
| 4 | `response.completed` untuk jalur non-streaming | ✅ `5c9a654d` |

---

### Yang sudah ada
`internal/translator/responses_state.go` · `responses_message.go` · `responses_toolcall.go` · `responses_stream.go`
`responses_request.go` · `responses_request_items.go` · `responses_request_tools.go` · `responses_request_content.go`

API respons: `InitResponsesState(model, flushReachesUs)`, `TranslateOpenAIToResponses(chunk, state) []ResponsesEvent`,
`FlushResponses(state)`, `FormatResponsesSSE(event)`, `ResponsesState.SetCustomToolNames([]string)`.

API request: `ResponsesToChatRequest(body []byte) (*ResponsesRequestResult, error)` →
`{Body, CustomToolNames, Converted}`.

**Keduanya sekarang sudah dipakai** — diff 3 menyambungkannya ke `HandleResponses`, diff 4 menutup
jalur non-streaming. Peta titik sisipnya yang ada di bawah sudah dipakai — jangan diulang; yang
tersisa tinggal catatan yang masih berlaku.

### Yang harus diingat soal diff 3–4

- **Rute `/responses` pindah ke `ChatHandler.HandleResponses`** (cermin `HandleMessages`), bukan lagi
  `MediaHandler` yang cuma passthrough tanpa resolution, combo, fallback, atau usage logging.
- **Penjaga native memakai data, bukan tabel baru.** `executor.UpstreamSpeaksResponses` membaca base
  URL berakhiran `/responses` (codex, grok-cli, perplexity-agent) atau model opencode yang dilayani
  dari `/responses`. Provider seperti itu **tidak boleh** dikonversi — `previous_response_id`, `store`,
  dan id item hilang kalau body-nya diubah ke Chat Completions. Balasannya diteruskan byte for byte.
- **Format klien lewat context**: `WithClientFormat` / `IsResponsesClient` / `NeedsResponsesBridge`.
  Nol perubahan signature; `forwardRequestParams` dan kelima call site-nya tidak tersentuh.
- **Upstream Claude = dua hop** (Claude → chunk Chat → event Responses); `feedFrames` menerima frame
  siap jadi maupun payload per chunk.
- **Stream terpotong tetap ditutup** oleh `bridge.close()` dengan `response.completed`.
- **Non-streaming** dibangun lewat `ChatResponseToResponses`, yang memain ulang satu chunk sintetis
  melalui translator streaming — item dan usage jadi identik di kedua jalur secara konstruksi.
- **Combo/fallback** tidak lagi meng-hardcode `TranslateResponse: true` + `/v1/messages`;
  keduanya diturunkan dari format klien lewat `forwardEndpoint`.

Diverifikasi saat commit `5c9a654d`: `go build ./...` bersih, `go test ./...` hijau, dan smoke live
terhadap binary dengan upstream tiruan — streaming 9 event Responses, non-streaming satu objek
`Response`, upstream native meneruskan `input[]` apa adanya.

### Detail penting yang sudah dipatuhi (jangan dibalik)
- `recordCompletedOutputItem` **wajib** menempel di item yang sama yang memancarkan
  `response.output_item.done`. Kalau terpisah, index yang tercatat menyimpang dari yang dikirim.
- `FlushReachesUs=false` (hop kedua pivot) **tidak boleh** menunda penyelesaian — chunk terminal sudah
  dibuang `translateResponse`, jadi menunda akan menelan `response.completed` sepenuhnya.
- `collectCompletedOutputItems` harus mengembalikan `[]`, bukan `nil` — `null` merusak klien.
- `ResponsesRequestResult.CustomToolNames` **tidak boleh** ditulis ke body upstream. Upstream menaruh
  `_customToolNames` di body lalu menghapusnya di `chatCore.js:214` sebelum mengirim.

## ⚠️ Bug test yang sudah ada sebelumnya (bukan dari kerja paritas)

`go test -count=2 ./internal/translator/` gagal di `TestTranslateOpenAIToClaudeStream*`
("expected default model, got: "). Terverifikasi juga ada di `origin/main` memakai worktree bersih,
jadi **pre-existing**, bukan efek perubahan ini. `-count=1` hijau.

**Dua hipotesis sudah dicoba dan KEDUA-nya gagal — jangan diulang tanpa bukti baru.**

1. `defer ClearStreamState("")` di `TranslateOpenAIToClaudeStream` — **tidak memperbaiki apa pun.**
   Penyebabnya: kunci state untuk session kosong **bukan `""`**. `response.go:378-384` menyelesaikannya
   berurutan: `sessionKey` → `chunk.ID` → `"default-session"`. Menghapus `""` menghapus kunci yang
   tidak pernah dipakai.
2. Membersihkan juga `"default-session"` **dan** kunci dari `chunk.ID` — ini membuat
   `TestGetStreamUsage` dan `TestTranslateOpenAIToClaudeStream_CompletionTokensDetails` **gagal di
   `count=1`**, yaitu merusak test yang sebelumnya hijau. Test-test itu sengaja berbagi kunci.
   Perubahan dibatalkan.

**Pelajaran:** test yang gagal hanya gagal kalau seluruh paket berjalan, jadi bocornya global
yang dipakai bersama, bukan state satu-shot yang bisa dibersihkan sendiri. Bersihkan state per-test
(`t.Cleanup`) kemungkinan akan merusak test lain yang bergantung pada state itu. Pendekatan yang
benar kemungkinan memisolasi test-nya, bukan membersihkan state global.

**Lead yang sudah ruled out:** `knownProviders` disebut di komentar `response_test.go:293`, tapi
simbol itu **tidak ada di Go** — sisa dari port JS. Bukan penyebabnya.

## 1b. Clamp `max→high` mimo — berdiri sendiri, SUDAH DIPETAKAN

**KEPUTUSAN (2026-09-28):** aturan **generik** sesuai upstream — helper yang memetakan effort ke
bentuk deepseek lalu menurunkan `"max"` ke `"high"` bila level yang dideklarasikan model tidak
memuatnya. Helper-nya generik, tapi **dipanggil dari jalur mimo dulu** (`injectMimoMarker`), karena
hanya mimo yang terbukti mengembalikan 400 saat diberi `"max"` (probe upstream, live). Alasannya: di Go, `levelOpenAI`
(`thinking_levels.go:19`) juga tidak memuat `"max"`, jadi aturan ini memang general — bukan
mimo-spesifik. Jalur lain menyusul saat diaudite.

## 1c. Empat keputusan yang sudah diambil

1. **Clamp mimo** → generik, dipanggil dari jalur mimo dulu (lihat di atas).
2. **Urutan** → clamp dulu (kecil, terisolasi, menutup 400 yang nyata), baru diff 3 yang butuh sesi penuh.
3. **Fallback rate biaya Usage** → **biarkan $0 seperti upstream** (`eb34b8ff`). Mengarang $1/$3 untuk
   model yang tidak dihargai siapa pun menghasilkan angka yang terlihat otoritatif tapi salah; $0
   setidaknya jujur soal "tidak diketahui". 556 dari 1592 pasangan model/provider terdampak.
4. **gofmt** → **ya, rapikan sekali jalan sebagai commit sendiri.** 32 file noise sudah dibatalkan
   berulang kali, dan tiap pembatalan berisiko menjatuhkan perubahan nyata di tengahnya.

⚠️ Koreksi: item ini **tidak** terblokir `thinkingUnified.js`. Yang sudah ada di `main` adalah clamp
`max→xhigh` untuk Codex/opencode zen (`transform.go:387`, `providers.go:1130`) — nilai target berbeda
dan tidak menyentuh mimo.

Aturan upstream (`1b72f02e`, `open-sse/translator/thinkingUnified.js` deepseek `applyFormat`):

```js
const want = level === "xhigh" || level === "max" ? "max" : "high";
body.reasoning_effort = want === "max" && supportedLevels && !supportedLevels.includes("max") ? "high" : want;
```

Artinya: di jalur deepseek, `xhigh`/`max` biasa dipetakan ke `"max"`, **tapi** diturunkan ke `"high"`
bila level yang dideklarasikan model itu tidak memuat `"max"`. Level mimo sudah ada di
`internal/providers/thinking_levels.go` (baris `*mimo*v2.5-pro*` dan `*mimo*v2.6*`, sudah di-commit
`b6faf9e2`) — jadi sisi datanya beres, yang hilang hanya penerapan aturannya.

**Masalahnya:** Go tidak punya `applyFormat` deepseek sama sekali, jadi tidak ada fungsi tempat
aturan inibelongs. `MimoFreeChat` (`internal/handlers/chat/mimofree.go:66`) adalah jalur mimo yang
nyata, dan `injectMimoMarker` (`:172`) **tidak** menyentuh `reasoning_effort`.

**Keputusan yang perlu diambil sebelum menulis:** terapkan generik (butuh `applyFormat` deepseek,
menyentuh `MimoFreeChat` + jalur opencode-go lain — permukaan lebih besar tapi sesuai upstream), atau
perkecil jadi clamp mimo-spesifik di `injectMimoMarker` (kecil, tapi menyimpang dari bentuk upstream).
Perkecilnya harus membaca nama model dari body, karena `MimoFreeChat` tidak menerima model.

## 2. `thinkingUnified.js` utuh

Modul ini belum di-port di Go (`internal/translator/` tidak punya `applyThinking`/`applyFormat`).
Yang sudah terpisah: `internal/providers/thinking_levels.go` (level resolver) + `thinking.display`
di `internal/proxy/executor/claude_thinking.go`.

Memblokir:
- **clamp `max→high` mimo** (`1b72f02e`) — hidup di `applyFormat` case `"deepseek"`
- `thinking.display` yang sekarang masih separuh jalan

---

## 3. Claude

| Item | Bukti belum ada |
|:---|:---|
| reset grant free tier (`consumeClaudeResetGrant`, `?cedar_ember=1`, route `claude-reset`) | `grep consumeClaudeResetGrant` → 0 |
| guard `ANTHROPIC_AUTH_TOKEN` di cli-tools claude-settings | `grep ANTHROPIC_AUTH_TOKEN` → 0 |
| decloak suffix-fallback (`b65d2d0a`) | ✅ **selesai** (`3292e247`) |
| `x-claude-code-session-id` / beta merge / rate-limit forward | ✅ **selesai** (`17fe9f8b`) |

---

## 4. Codex / OpenCode

- Codex multi model profile (`~/.codex/*.config.toml`) + settings refresh → `grep codex-profiles` → 0
- `responsesLite` untuk `gpt-6-sol`/`gpt-6-luna` → ✅ **selesai** (`ac103551`)
- clamp `max→high` mimo → ikut item 2

---

## 5. Dashboard

- **combo limits** di-resolve via server capabilities (`#4360`) — `AggregateComboCapabilities` ada,
  tapi tanpa resolver server-side
- vision adapter jadi **tabel berurutan** (sekarang chip inline terpotong 3)
- Tailwind: health wait dibatasi 20s
- lazy-load chart
- tray arm64 native

---

## ⭐ Dua keputusan yang masih menggantung (BUKAN pekerjaan tertunda)

1. **Fallback rate biaya Usage.** `eb34b8ff` sudah port tabel harga upstream. **556 dari 1592** pasangan
   `(provider, model)` tidak dihargai siapa pun (upstream juga) → upstream mencatat **$0**, port Go lama
   mengarang $1/$3. Efeknya kolom biaya turun untuk model itu. → **restore fallback, atau biarkan $0?**

2. **gofmt.** Repo belum clean: **31 file** `gofmt -l` (sebagian besar pre-existing, bukan dari kerja
   paritas). Selama ini noise gofmt selalu di-revert tiap commit. → **rapikan sekali jalan sebagai
   commit sendiri, atau biarkan?**

---

## Yang SUDAH selesai (jangan diulang)

- CommandCode parity halaman provider (list model, thinking level, caps) — `e2b80d9c`
- Fix sinkronisasi katalog models.dev (skema `modalities.input`) — `201acd4e`
- Thinking levels sampai v0.5.91 (MiMo + codex per-model) — `b6faf9e2`
- Gemini turn guard · Claude thinking text · usage byApiKey — `abcfad66`
- 5 aggregator provider + sinkron katalog + cline free tier — `773b5d96`
- Tabel harga penuh + rumus biaya sebenarnya — `eb34b8ff`
- Claude header parity — `17fe9f8b`
- `responsesLite` + guard konflik nama — `ac103551`
- Transport Gemini Live realtime STT — `d9338e5e`
- Claude decloak fallback + `claude-opus-5-5` — `3292e247`
- **Responses translator, arah respons** (Chat SSE → Responses SSE, #4307, `reasoning_details`) — `36b17d46`
- **Responses translator, arah request** (body Responses → body Chat) — `b3af67ad`
- **Clamp `max→high` mimo** (port `1b72f02e`) — `0eafdedf`
- **Responses wiring + non-streaming** (`/v1/responses` pindah ke `ChatHandler`, penjaga native, dua hop Claude, `ChatResponseToResponses`) — `5c9a654d`
- **gofmt 32 file** (branch `chore/gofmt`) — `c8e9de66`

Keempat diff Responses sudah terpush dan saling berantai. `/v1/responses` **sudah** memakai
penerjemah; tidak ada lagi kode yang sengaja tidak terpanggil.

## 6. Peningkatan yang belum perlu — "Responses-native" per-koneksi

**Bukan bug sekarang.** Yang ditulis di sini supaya tidak hilang kalau nanti relevan.

**Yang happening sekarang.** `newResponsesContext` memanggil `getProviderConfig(provider, nil)`,
dan itu membaca **config registry**, bukan `connData`. Padahal `getProviderConfig` memang mendukung
override per koneksi — `connData.BaseURL` menggantikan `cfg.BaseURL` (`connections.go:285`). Jadi
keputusan native-ness diambil sekali per request, bukan per percobaan koneksi.

**Upstream berbeda.** `handleChatCore` dipanggil **di dalam** loop fallback akun
(`chat.js:234` `while (true)` → `:270`), dengan `body` asli yang di-spread ulang tiap iterasi — jadi
terjemahan diulang per koneksi. Dan target format-nya memang bisa per koneksi:
`getTargetFormat(provider, credentials)` → `resolveOpenAICompatibleApiType` membaca
`credentials.providerSpecificData.apiType` (`provider.js:134` dan `:28`).

**Kenapa belum berbahaya di Go.** Dua kebetulan yang tidak dijaga kode:
1. `apiType` dipatok di provider id — `openai-compatible-responses-<uuid>` vs `openai-compatible-chat-<uuid>`
   (`provider_nodes.go:132`), sama seperti upstream. Semua koneksi satu provider sudah satu kelas endpoint.
2. `getProviderConfig` masih **hardcode `/chat/completions`** untuk config provider node
   (`connections.go:307-314`) dan tidak pernah membaca `apiType`. Jadi node `responses` pun belum
   benar-benar diarahkan ke `/responses`.

**Pemicu untuk dikerjakan.** Kalau salah satu di bawah dikerjakan, keputusan ini harus turun ke level
koneksi lebih dulu — kalau tidak, request akan dikirim dalam format yang salah ke upstream yang punya
kelas endpoint berbeda, dan gejalanya biasanya senyap (upstream menolak dengan bentuk yang salah):
- Hormati `apiType: "responses"` saat membangun URL (itu gap parity yang memang terpisah, dan memperbaiki
  nomor 2 di atas).
- Dua koneksi satu provider dengan override BaseURL yang berbeda kelas endpoint.
- Node kompatibel yang `apiType`-nya diubah setelah node dibuat, jadi data blob dan provider id tidak
  lagi cocok.

**Bentuk perbaikannya kalau nanti dikerjakan.** Pindahkan penentuan native-ness ke
`tryForwardWithConnection`, tempat `providerCfg` per koneksi sudah diketahui, dan simpan body Responses
asli di context supaya tiap percobaan menerjemahkannya sendiri — meniru `chat.js` yang meng-spread
`body` asli tiap iterasi. Bukan pekerjaan besar, tapi menyentuh `tryForwardWithConnection` dan kelima
call site-nya, jadi jangan dikerjakan di tengah-tengah tanpa sesi penuh.

**Tidak boleh:** memindahkan penentuan ke level koneksi untuk provider Responses bernama
(codex, grok-cli, perplexity-agent, opencode-zen) — `resolveTransport(provider, sourceFormat)` upstream
membaca registry saja dan tidak pernah menyentuh credentials, jadi di sana per-provider memang benar.

## Catatan repositori

Worktree yang dipakai saat stack Responses dikerjakan:
- tree utama → `main`
- `../9router-go-wt` → `parity/responses-translator` (semua commit di tabel atas ada di sini)
- `.worktrees/fix-usage-animation-edges` → sudah ter-merge

Worktree parity **wajib dibangun ulang** sebelum dipakai: `web/dist` tidak ter-commit,
jadi `bun install && bun run build` di `web/` dulu, kalau tidak `go build` gagal dengan
`pattern dist/*: no matching files found`.

Hindari `git add -A` di tree utama: `.worktrees/` masih untracked dan akan ikut ter-stage.
