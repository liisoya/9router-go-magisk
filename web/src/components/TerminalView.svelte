<script lang="ts">
  // Port of decolua/9router src/app/(dashboard)/dashboard/console-log/ConsoleLogClient.js
  import { Trash2 } from 'lucide-svelte'
  import { getAuthHeaders, responseErrorMessage } from '../api/client'
  import Button from '../lib/ui/Button.svelte'
  import Card from '../lib/ui/Card.svelte'
  import { notifications } from '../lib/notifications'

  const MAX_LINES = 200

  const LOG_LEVEL_COLORS: Record<string, string> = {
    LOG: 'text-green-400',
    INFO: 'text-blue-400',
    INF: 'text-blue-400',
    WARN: 'text-yellow-400',
    WRN: 'text-yellow-400',
    ERROR: 'text-red-400',
    ERR: 'text-red-400',
    DEBUG: 'text-purple-400',
    DBG: 'text-purple-400',
  }

  const ANSI_RE = /\u001b\[[0-9;]*m/g
  const BRACKET_TAG_RE = /\[(\w+)\]/
  const LEADING_LEVEL_RE = /^(LOG|INFO|INF|WARN|WRN|ERROR|ERR|DEBUG|DBG)\b/

  let logs = $state<string[]>([])
  let logElement = $state<HTMLDivElement | null>(null)
  let logLevel = $state('info')
  let levelBusy = $state(false)

  const LOG_LEVELS = ['debug', 'info', 'warn', 'error']

  /** Server lines can still carry terminal colors (e.g. captured stdout). */
  function stripAnsi(line: string): string {
    return line.replace(ANSI_RE, '')
  }

  /** Same rule as Next: a level tag wins, everything else is green. */
  function levelColor(line: string): string {
    const stripped = stripAnsi(line)
    const tagged = stripped.match(BRACKET_TAG_RE)?.[1] ?? ''
    const level = tagged || stripped.match(LEADING_LEVEL_RE)?.[1] || ''
    return LOG_LEVEL_COLORS[level] || 'text-green-400'
  }

  function trim(lines: string[]): string[] {
    return lines.length > MAX_LINES ? lines.slice(-MAX_LINES) : lines
  }

  async function fetchLogLevel() {
    try {
      const res = await fetch('/api/translator/console-logs/level', {
        headers: getAuthHeaders(),
      })
      if (!res.ok) return
      const data = await res.json()
      if (typeof data.level === 'string') logLevel = data.level
    } catch {
      // keep default; selector still works for setting
    }
  }

  async function handleLevelChange(e: Event) {
    const next = (e.target as HTMLSelectElement).value
    const prev = logLevel
    logLevel = next
    levelBusy = true
    try {
      const res = await fetch('/api/translator/console-logs/level', {
        method: 'PUT',
        headers: getAuthHeaders(),
        body: JSON.stringify({ level: next }),
      })
      if (!res.ok) throw new Error(await responseErrorMessage(res, `HTTP ${res.status}`))
      const data = await res.json()
      if (!data?.success) throw new Error('Log level update was rejected')
      logLevel = data.level || next
      notifications.success(`Log level set to ${logLevel} (no restart needed)`)
    } catch (err) {
      logLevel = prev
      notifications.error(`Failed to set log level: ${err instanceof Error ? err.message : err}`)
    } finally {
      levelBusy = false
    }
  }

  async function handleClear() {
    try {
      const res = await fetch('/api/translator/console-logs', {
        method: 'DELETE',
        headers: getAuthHeaders(),
      })
      if (!res.ok) throw new Error(await responseErrorMessage(res, `HTTP ${res.status}`))
      // UI cleared via SSE "clear" event
    } catch (err) {
      notifications.error(`Failed to clear console logs: ${err instanceof Error ? err.message : err}`)
    }
  }

  // Read the SSE body through fetch so the same authenticated dashboard
  // transport and error handling are used for the stream.
  fetchLogLevel()
  $effect(() => {
    let isCancelled = false
    let controller: AbortController | null = null

    const connect = async () => {
      try {
        controller = new AbortController()
        const res = await fetch('/api/translator/console-logs/stream', {
          headers: getAuthHeaders(),
          signal: controller.signal,
        })
        if (!res.ok) throw new Error(await responseErrorMessage(res, `HTTP ${res.status}`))

        const reader = res.body?.getReader()
        const decoder = new TextDecoder()
        if (!reader) return

        let buffer = ''
        while (!isCancelled) {
          const { done, value } = await reader.read()
          if (done) break

          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split('\n')
          buffer = lines.pop() || ''

          for (const line of lines) {
            const trimmed = line.trim()
            if (!trimmed || trimmed.startsWith(':')) continue
            if (!trimmed.startsWith('data: ')) continue

            try {
              const msg = JSON.parse(trimmed.slice(6))
              if (msg.type === 'init') {
                logs = trim((msg.logs || []).slice(-MAX_LINES))
              } else if (msg.type === 'line') {
                logs = trim([...logs, msg.line])
              } else if (msg.type === 'lines') {
                logs = trim([...logs, ...(msg.lines || [])])
              } else if (msg.type === 'clear') {
                logs = []
              }
            } catch {
              // ignore malformed frames
            }
          }
        }
      } catch {
        // stream closed or aborted — reconnect below
      }

      if (!isCancelled) setTimeout(connect, 5000)
    }

    connect()

    return () => {
      isCancelled = true
      controller?.abort()
    }
  })

  // Auto-scroll to bottom on new logs
  $effect(() => {
    void logs.length
    if (logElement) logElement.scrollTop = logElement.scrollHeight
  })
</script>

<div class="">
  <Card>
    <div class="flex items-center justify-end gap-2 px-4 pt-3 pb-2">
      <label class="text-xs text-text-muted" for="console-log-level">Level</label>
      <select
        id="console-log-level"
        class="text-xs bg-surface border border-border rounded-md px-2 py-1.5 text-text-primary disabled:opacity-50"
        value={logLevel}
        disabled={levelBusy}
        onchange={handleLevelChange}
      >
        {#each LOG_LEVELS as lvl}
          <option value={lvl}>{lvl}</option>
        {/each}
      </select>
      <Button size="sm" variant="outline" onclick={handleClear}>
        <Trash2 class="w-3.5 h-3.5" />
        Clear
      </Button>
    </div>
    <div
      bind:this={logElement}
      class="bg-black rounded-b-lg p-4 text-xs font-mono h-[calc(100vh-220px)] overflow-y-auto"
    >
      {#if logs.length === 0}
        <span class="text-text-muted">No console logs yet.</span>
      {:else}
        <div class="space-y-0.5">
          {#each logs as line, i (i)}
            <div><span class={levelColor(line)}>{stripAnsi(line)}</span></div>
          {/each}
        </div>
      {/if}
    </div>
  </Card>
</div>
