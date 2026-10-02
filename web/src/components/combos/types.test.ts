import { describe, expect, it } from 'bun:test'
import { COMBO_STRATEGIES, resolveComboStrategy } from './types'

const OPTION_VALUES = COMBO_STRATEGIES.map((s) => s.value)

describe('resolveComboStrategy', () => {
  // The server fills combo.strategy from settings.comboStrategies[name] and
  // otherwise from the global `comboStrategy`, so a card can receive either
  // vocabulary. Anything it can receive has to render as one of the options.
  it('maps every value the server can send onto an existing option', () => {
    const serverValues = [
      undefined,
      null,
      '',
      'fallback',
      'round-robin',
      'first-model',
      'sticky',
      'capacity',
      'fusion',
      'nonsense-from-a-hand-edited-backup'
    ]

    for (const value of serverValues) {
      expect(OPTION_VALUES).toContain(resolveComboStrategy(value))
    }
  })

  it('treats the global routing mode as the card fallback', () => {
    // `first-model` is the global Combo Routing Mode and the default when a
    // combo has no per-combo entry. It is try-in-order, which is exactly what
    // the card calls `fallback`.
    expect(resolveComboStrategy('first-model')).toBe('fallback')
  })

  it('passes a real per-combo strategy through unchanged', () => {
    for (const value of ['round-robin', 'sticky', 'capacity', 'fusion']) {
      expect(resolveComboStrategy(value)).toBe(value)
    }
  })

  it('falls back for a missing or unrecognised value', () => {
    expect(resolveComboStrategy(undefined)).toBe('fallback')
    expect(resolveComboStrategy(null)).toBe('fallback')
    expect(resolveComboStrategy('')).toBe('fallback')
    expect(resolveComboStrategy('nonsense')).toBe('fallback')
  })

  it('lists every option with a non-empty label', () => {
    for (const strategy of COMBO_STRATEGIES) {
      expect(strategy.value.length).toBeGreaterThan(0)
      expect(strategy.label.length).toBeGreaterThan(0)
    }
  })
})
