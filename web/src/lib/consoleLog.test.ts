import { describe, expect, it } from 'bun:test'
import {
  countByLevel,
  downloadFilename,
  filterEntries,
  formatClock,
  splitTag,
  stripAnsi,
  toConsoleEntry,
  toPlainText,
  type LogLevel,
  type RawConsoleEntry,
} from './consoleLog'

const entry = (overrides: RawConsoleEntry = {}, seq = 0, receivedAt?: number) =>
  toConsoleEntry(overrides, seq, receivedAt)

describe('toConsoleEntry', () => {
  // The server stamp is authoritative: a message whose text happens to say
  // "error" inside a debug row must stay debug.
  it('trusts the level the server stamped', () => {
    const cases: Array<[string, LogLevel]> = [
      ['debug', 'debug'],
      ['info', 'info'],
      ['warn', 'warn'],
      ['error', 'error'],
    ]
    for (const [level, want] of cases) {
      expect(entry({ level, line: `WRN whatever` }).level).toBe(want)
    }
  })

  it('falls back to a text prefix when the server sends no level', () => {
    const cases: Array<[string, LogLevel]> = [
      ['ERR [chat] resolve model failed', 'error'],
      ['ERROR [chat] boom', 'error'],
      ['WRN [combo] skip locked connection', 'warn'],
      ['WARN [proxy] pool inactive', 'warn'],
      ['DBG [proxy] routing requests', 'debug'],
      ['INF [usage] logged', 'info'],
      ['INFO [usage] logged', 'info'],
      ['2026/10/03 18:39:32 ERR [chat] boom', 'error'],
      ['2026/10/03 18:39:32 INF [usage] logged', 'info'],
    ]
    for (const [line, want] of cases) {
      expect(entry({ line }).level).toBe(want)
    }
  })

  // The old view painted everything it could not parse green, which read as
  // "everything succeeded" on a page whose whole job is spotting failures.
  it('treats unrecognised plain output as info, not error', () => {
    expect(entry({ line: 'panic: runtime error: index out of range' }).level).toBe('info')
    expect(entry({ line: 'listening on 20130' }).level).toBe('info')
    expect(entry({}).level).toBe('info')
  })

  it('does not mistake a message that merely contains a level word', () => {
    expect(entry({ line: 'ERR [usage] model=error-model retried' }).level).toBe('error')
    expect(entry({ line: 'handle error-classification failure' }).level).toBe('info')
    expect(entry({ line: 'WARN' }).level).toBe('info')
  })

  it('prefers the JSON body timestamp over the arrival stamp', () => {
    const parsed = entry({
      time: '2026-10-03T07:00:00.000000000Z',
      level: 'warn',
      line: '{"time":"2026-10-03T09:30:00.5Z","level":"error","tag":"proxy","msg":"pool down"}',
    })
    expect(parsed.time).toBe('2026-10-03T09:30:00.5Z')
    expect(parsed.level).toBe('warn')
    expect(parsed.line).toContain('pool down')
  })

  it('reads level and time out of a JSON log line when the envelope has neither', () => {
    const parsed = entry({ line: '{"time":"2026-10-03T09:30:00Z","level":"error","msg":"boom"}' })
    expect(parsed.level).toBe('error')
    expect(parsed.time).toBe('2026-10-03T09:30:00Z')
  })

  it('survives a truncated brace line instead of swallowing it', () => {
    const parsed = entry({ line: '{"time":"2026-10-03T09:30:00Z","level":"err' })
    expect(parsed.level).toBe('info')
    expect(parsed.line).toContain('"level":"err')
  })

  it('strips ANSI that survived a non-logger print', () => {
    expect(entry({ line: '\u001b[32mINF\u001b[0m [request] GET / status=200' }).line).toBe(
      'INF [request] GET / status=200'
    )
  })

  it('falls back to the receive clock when no time was stamped', () => {
    const received = Date.parse('2026-10-03T11:22:33.444Z')
    expect(entry({ line: 'INF [x] y' }, 7, received).time).toBe('2026-10-03T11:22:33.444Z')
  })

  it('rejects a non-string level rather than trusting it', () => {
    expect(entry({ level: 42, line: 'ERR boom' }).level).toBe('error')
    expect(entry({ level: null, line: 'boom' }).level).toBe('info')
  })

  it('carries the sequence number for stable ordering', () => {
    expect(entry({ line: 'a' }, 12).seq).toBe(12)
  })
})

describe('countByLevel', () => {
  it('counts every level present', () => {
    const rows = [
      entry({ level: 'error' }),
      entry({ level: 'error' }),
      entry({ level: 'warn' }),
      entry({ level: 'info' }),
    ]
    expect(countByLevel(rows)).toEqual({ debug: 0, info: 1, warn: 1, error: 2 })
  })

  it('returns zeroes for an empty buffer', () => {
    expect(countByLevel([])).toEqual({ debug: 0, info: 0, warn: 0, error: 0 })
  })
})

describe('filterEntries', () => {
  const rows = [
    entry({ line: 'INF [usage] logged provider=deepseek' }, 0),
    entry({ line: 'ERR [chat] resolve model failed model=x' }, 1),
    entry({ line: 'WRN [combo] skip unhealthy' }, 2),
  ]

  it('matches case-insensitively on the line body', () => {
    expect(filterEntries(rows, 'PROVIDER=deepseek')).toHaveLength(1)
    expect(filterEntries(rows, 'model=')).toHaveLength(1)
  })

  it('returns everything for an empty or whitespace query', () => {
    expect(filterEntries(rows, '')).toHaveLength(3)
    expect(filterEntries(rows, '   ')).toHaveLength(3)
  })

  it('returns nothing when no line matches', () => {
    expect(filterEntries(rows, 'antigravity')).toEqual([])
  })
})

describe('formatClock', () => {
  it('renders sub-second precision in local time', () => {
    expect(formatClock(new Date(2026, 9, 3, 7, 8, 9, 45).toISOString())).toBe('07:08:09.045')
  })

  it('marks an unparsable stamp instead of printing NaN', () => {
    expect(formatClock('not-a-time')).toBe('--:--:--.---')
    expect(formatClock('')).toBe('--:--:--.---')
  })
})

describe('splitTag', () => {
  it('separates the subsystem tag from the message', () => {
    expect(splitTag('INF [usage] logged provider=x')).toEqual({
      tag: 'usage',
      message: 'logged provider=x',
    })
  })

  it('returns null when the line has no leading tag', () => {
    expect(splitTag('plain output with [brackets] inside')).toBeNull()
    expect(splitTag('')).toBeNull()
  })
})

describe('toPlainText', () => {
  it('exports one timestamped, levelled row per line', () => {
    const rows = [
      toConsoleEntry({ level: 'info', line: 'INF [usage] logged' }, 0, Date.parse('2026-10-03T07:08:09.045Z')),
      toConsoleEntry({ level: 'error', line: 'ERR [chat] boom' }, 1, Date.parse('2026-10-03T07:08:09.046Z')),
    ]
    expect(toPlainText(rows)).toBe(
      '07:08:09.045 INFO INF [usage] logged\n07:08:09.046 ERROR ERR [chat] boom'
    )
  })

  it('exports an empty buffer as an empty string', () => {
    expect(toPlainText([])).toBe('')
  })
})

describe('stripAnsi', () => {
  it('leaves plain text untouched', () => {
    expect(stripAnsi('INF [usage] logged')).toBe('INF [usage] logged')
  })
})

describe('downloadFilename', () => {
  it('stamps the export time', () => {
    expect(downloadFilename(new Date(2026, 9, 3, 7, 8, 9))).toBe(
      '9router-console-20261003-070809.log'
    )
  })
})