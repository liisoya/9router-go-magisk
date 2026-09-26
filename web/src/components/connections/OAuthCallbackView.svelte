<script lang="ts">
  import { onMount } from 'svelte'
  import {
    OAUTH_CHANNEL,
    parseCallbackURL,
    writeCallback,
  } from '../../lib/oauth-handoff'

  let status = $state<'working' | 'ok' | 'error'>('working')
  let message = $state('Memproses callback…')
  let raw = $state('')
  let countdown = $state(0)

  function store() {
    return typeof window !== 'undefined' ? window.localStorage : null
  }

  onMount(() => {
    const ls = store()
    const parsed = parseCallbackURL(window.location.href)
    if (parsed.error) {
      status = 'error'
      message = `Login gagal: ${parsed.error}${parsed.errorDesc ? ` — ${parsed.errorDesc}` : ''}`
      // Tetap teruskan ke dashboard agar modal menampilkan errornya.
      if (ls) writeCallback(ls, { state: parsed.state, raw: '', error: parsed.error, errorDesc: parsed.errorDesc })
    } else if (parsed.raw) {
      status = 'ok'
      message = 'Login berhasil! Mengirim ke dashboard…'
      raw = parsed.raw
      const payload = { state: parsed.state, raw: parsed.raw }
      if (ls) writeCallback(ls, payload)
      try {
        const bc = new BroadcastChannel(OAUTH_CHANNEL)
        bc.postMessage({ ...payload, at: Date.now() })
        bc.close()
      } catch {
        /* BroadcastChannel tidak tersedia — dashboard pakai polling/storage event */
      }
      if (window.opener) {
        const origins = [window.location.origin, 'http://localhost:1455']
        for (const origin of origins) {
          try {
            window.opener.postMessage({
              type: 'oauth_callback',
              data: { ...payload, error: '', errorDescription: '' },
            }, origin)
          } catch {
            /* opener may have navigated away */
          }
        }
      }
      // Tab ini dibuka via window.open → boleh tutup sendiri.
      countdown = 3
      const timer = setInterval(() => {
        countdown -= 1
        if (countdown <= 0) {
          clearInterval(timer)
          window.close()
        }
      }, 1000)
    } else {
      status = 'error'
      message = 'Tidak ada code di URL ini. Ulangi Login dari dashboard.'
    }
  })

  function copyRaw() {
    if (!raw) return
    navigator.clipboard.writeText(raw).catch(() => {})
  }
</script>

<div class="min-h-screen flex items-center justify-center bg-bg p-4">
  <div class="max-w-xl w-full rounded-xl border border-border bg-surface-1 p-6 text-center">
    {#if status === 'working'}
      <p class="text-text-muted">Memproses callback…</p>
    {:else if status === 'ok'}
      <p class="text-lg font-semibold text-green-500">Login berhasil!</p>
      <p class="text-sm text-text-muted mt-2">{message}</p>
      <p class="text-xs text-text-muted mt-1">
        Tab ini {countdown > 0 ? `tertutup otomatis dalam ${countdown}…` : 'bisa ditutup.'} Koneksi diproses otomatis di tab dashboard.
      </p>
      {#if raw}
        <button
          type="button"
          onclick={copyRaw}
          class="mt-4 px-4 py-2 text-xs font-semibold rounded-lg bg-surface-2 hover:bg-surface-3 border border-border cursor-pointer"
        >
          Copy manual (kalau auto-submit gagal)
        </button>
      {/if}
    {:else}
      <p class="text-lg font-semibold text-red-500">Callback gagal</p>
      <p class="text-sm text-text-muted mt-2">{message}</p>
      <p class="text-xs text-text-muted mt-1">Tutup tab ini dan ulangi Login dari dashboard.</p>
    {/if}
  </div>
</div>
