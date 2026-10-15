// Parsing rules for the Console Log page.
//
// The server stamps every captured line with the level the logger reported
// (see internal/log.ConsoleEntry), so a row is never assigned a severity by
// matching its text. The text fallbacks here exist only for lines that never
// went through the logger — captured stdout, a panic dump, a library printing
// straight to the process — where no level was ever recorded.

export type LogLevel = 'debug' | 'info' | 'warn' | 'error'

export interface ConsoleEntry {
  /** Arrival time as reported by the server; the display clock when absent. */
  time: string
  level: LogLevel
  line: string
  /** Sequence number, so rows sharing a timestamp keep their arrival order. */
  seq: number
}

/** The raw SSE / REST payload, before display parsing. */
export interface RawConsoleEntry {
  time?: unknown
  level?: unknown
  line?: unknown
}

export interface LevelCounts {
  debug: number
  info: number
  warn: number
  error: number
}

// Logger prefixes (DBG/INF/WRN/ERR) plus the long forms other emitters print.
// The trailing lookahead keeps a bare word like "WARN" from reading as a level
// when it is only the start of a message.
const PREFIX_RE = /^(DBG|DEBUG|INF|INFO|WRN|WARN|ERR|ERROR)(?=\s)/

// A leading Go log timestamp, which hides the level prefix behind it.
const LEADING_TIMESTAMP_RE = /^\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)?\s*/

// The ESC byte is the point here, not an oversight — these are the CSI
// sequences a terminal emits and they must be removed, not tolerated.
// oxlint-disable-next-line no-control-regex
const ANSI_RE = /\u001b\[[0-9;]*m/g

const pad = (value: number, size = 2) => String(value).padStart(size, '0')

function coerceLevel(value: unknown): LogLevel | null {
  if (typeof value !== 'string') return null
  const lowered = value.toLowerCase()
  if (lowered === 'debug' || lowered === 'info' || lowered === 'warn' || lowered === 'error') {
    return lowered
  }
  return null
}

/** ANSI escapes survive only on lines the logger never formatted itself. */
export function stripAnsi(line: string): string {
  return line.includes('\u001b[') ? line.replace(ANSI_RE, '') : line
}

function levelFromText(line: string): LogLevel | null {
  const prefix = PREFIX_RE.exec(line.replace(LEADING_TIMESTAMP_RE, ''))?.[1]?.toLowerCase()
  if (!prefix) return null
  if (prefix.startsWith('err')) return 'error'
  if (prefix.startsWith('wrn') || prefix.startsWith('warn')) return 'warn'
  if (prefix.startsWith('dbg')) return 'debug'
  return 'info'
}

/**
 * A JSON log line (LOG_FORMAT=json) is a complete record on its own: the body
 * carries the authoritative timestamp and level. Returns null for text that
 * merely starts with a brace, so a truncated print is not silently swallowed.
 */
function parseJsonLine(line: string): { time?: string; level?: LogLevel } | null {
  const trimmed = line.trimStart()
  if (!trimmed.startsWith('{') || !trimmed.endsWith('}')) return null

  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    return null
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) return null

  const record = parsed as Record<string, unknown>
  const time = typeof record.time === 'string' ? record.time : undefined
  const level = coerceLevel(record.level)
  return { time, level: level ?? undefined }
}

/**
 * Turns one raw payload into a display row.
 *
 * Level precedence: the level the server stamped, then the JSON body's own
 * level, then a text prefix, and finally "info". Untagged stdout is ordinary
 * output — painting it red would cry wolf on every plain print, which is the
 * habit this page is being fixed for.
 */
export function toConsoleEntry(
  raw: RawConsoleEntry,
  seq: number,
  receivedAt: number = Date.now()
): ConsoleEntry {
  const line = stripAnsi(typeof raw.line === 'string' ? raw.line : '')
  const json = parseJsonLine(line)
  const serverTime = typeof raw.time === 'string' ? Date.parse(raw.time) : Number.NaN

  return {
    time: json?.time ?? (Number.isNaN(serverTime) ? new Date(receivedAt) : new Date(serverTime)).toISOString(),
    level: coerceLevel(raw.level) ?? json?.level ?? levelFromText(line) ?? 'info',
    line,
    seq,
  }
}

export function countByLevel(entries: ConsoleEntry[]): LevelCounts {
  const counts: LevelCounts = { debug: 0, info: 0, warn: 0, error: 0 }
  for (const entry of entries) counts[entry.level]++
  return counts
}

export function filterEntries(entries: ConsoleEntry[], query: string): ConsoleEntry[] {
  const needle = query.trim().toLowerCase()
  if (!needle) return entries
  return entries.filter((entry) => entry.line.toLowerCase().includes(needle))
}

/** HH:MM:SS.mmm — a burst of requests needs sub-second resolution to be readable. */
export function formatClock(iso: string): string {
  const parsed = Date.parse(iso)
  if (Number.isNaN(parsed)) return '--:--:--.---'
  const date = new Date(parsed)
  return `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}.${pad(date.getMilliseconds(), 3)}`
}

/**
 * Splits the leading "[tag]" group so the row can show subsystem and message
 * apart. The logger prints "INF [usage] logged", so the timestamp and level
 * prefix in front of the bracket are skipped rather than demanded absent.
 */
export function splitTag(line: string): { tag: string; message: string } | null {
  const withoutPrefix = line.replace(LEADING_TIMESTAMP_RE, '').replace(PREFIX_RE, '').trimStart()
  const match = /^\[([^\]\s]+)\]\s?([\s\S]*)$/.exec(withoutPrefix)
  return match ? { tag: match[1], message: match[2] } : null
}


/** Export text, one row per line, timestamped like the on-screen gutter. */
export function toPlainText(entries: ConsoleEntry[]): string {
  return entries
    .map((entry) => `${formatClock(entry.time)} ${entry.level.toUpperCase()} ${entry.line}`)
    .join('\n')
}

export function downloadFilename(when = new Date()): string {
  return `9router-console-${when.getFullYear()}${pad(when.getMonth() + 1)}${pad(when.getDate())}-${pad(when.getHours())}${pad(when.getMinutes())}${pad(when.getSeconds())}.log`
}