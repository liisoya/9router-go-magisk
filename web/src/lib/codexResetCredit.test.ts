import { describe, expect, it } from 'bun:test'
import {
  formatRelativeExpiry,
  getCodexResetCreditExpiryLabel,
  getResetCreditConfirmation,
  getResetCreditWindowTitle,
  newResetCreditIdempotencyKey,
} from './codexResetCredit'

const NOW = Date.parse('2026-09-29T12:00:00Z')
// OmniRoute coarsens to the unit that fits and floors a credit that is
// already expired at "0m" rather than a negative countdown.
describe('formatRelativeExpiry', () => {
  it('picks the unit that fits', () => {
    const cases: Array<[string, string]> = [
      [new Date(NOW + 30 * 60_000).toISOString(), '30m'],
      [new Date(NOW + 59 * 60_000).toISOString(), '59m'],
      [new Date(NOW + 2 * 3_600_000).toISOString(), '2h'],
      [new Date(NOW + 2 * 86_400_000).toISOString(), '2d'],
      [new Date(NOW + 25 * 3_600_000).toISOString(), '2d'],
    ]
    for (const [expiresAt, want] of cases) {
      expect(formatRelativeExpiry(expiresAt, NOW)).toBe(want)
    }
  })

  it('floors an expired credit at 0m', () => {
    expect(formatRelativeExpiry('2020-01-01T00:00:00Z', NOW)).toBe('0m')
  })

  it('rounds a near-miss up to at least 1m', () => {
    expect(formatRelativeExpiry('2026-09-29T12:00:30Z', NOW)).toBe('1m')
  })

  it('returns null when there is nothing to render', () => {
    expect(formatRelativeExpiry(null, NOW)).toBeNull()
    expect(formatRelativeExpiry(undefined, NOW)).toBeNull()
    expect(formatRelativeExpiry('', NOW)).toBeNull()
    expect(formatRelativeExpiry('not-a-date', NOW)).toBeNull()
  })
})

describe('getCodexResetCreditExpiryLabel', () => {
  it('pairs a relative countdown with an absolute date', () => {
    const label = getCodexResetCreditExpiryLabel('2026-10-01T12:00:00Z', 'en-GB', NOW)
    expect(label.relative).toBe('2d')
    expect(label.absolute).toContain('2026')
  })

  it('reports nothing for a credit with no expiry', () => {
    expect(getCodexResetCreditExpiryLabel(null, 'en-GB', NOW)).toEqual({ absolute: null, relative: null })
  })

  it('reports nothing for an unparseable expiry', () => {
    expect(getCodexResetCreditExpiryLabel('whenever', 'en-GB', NOW)).toEqual({ absolute: null, relative: null })
  })
})

describe('getResetCreditWindowTitle', () => {
  it('prefers the title Codex supplied', () => {
    expect(getResetCreditWindowTitle({ selectionToken: 'a', title: '5-hour window reset' })).toBe(
      '5-hour window reset',
    )
  })

  it('falls back to a readable reset type', () => {
    expect(getResetCreditWindowTitle({ selectionToken: 'a', resetType: 'usage_limit' })).toBe('usage limit')
    expect(getResetCreditWindowTitle({ selectionToken: 'a', resetType: 'weekly' })).toBe('weekly')
  })

  it('falls back to a neutral default', () => {
    expect(getResetCreditWindowTitle({ selectionToken: 'a' })).toBe('Full reset')
  })
})

// The confirmation copy is the only warning a user gets before an
// irreversible action, so it has to name the credit and say it cannot be undone.
describe('getResetCreditConfirmation', () => {
  it('warns that the credit is permanently consumed and cannot be undone', () => {
    const text = getResetCreditConfirmation({ selectionToken: 'a', title: '5-hour window reset' })
    expect(text).toContain('permanently consumes')
    expect(text).toContain('cannot be undone')
    expect(text).toContain('5-hour window reset')
  })
})

// A redeem spends the credit for good, so the whole double-spend protection
// rests on the key being non-empty: the server replaces an empty key with a
// fresh one per request, which makes a second submit a second real redeem.
describe('newResetCreditIdempotencyKey', () => {
  it('never returns an empty value', () => {
    for (let i = 0; i < 50; i++) {
      expect(newResetCreditIdempotencyKey()).not.toBe('')
    }
  })

  it('returns a distinct key per call, so two sessions cannot collide', () => {
    const keys = new Set<string>()
    for (let i = 0; i < 50; i++) keys.add(newResetCreditIdempotencyKey())
    expect(keys.size).toBe(50)
  })
})
