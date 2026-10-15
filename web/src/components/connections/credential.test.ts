import { describe, expect, test } from 'bun:test'
import { credentialCheckState, credentialPlaceholder, credentialUpdate } from './credential'

describe('credentialPlaceholder', () => {
  test('shows the masked key so the user can confirm what is on file', () => {
    expect(credentialPlaceholder({ apiKeyMasked: 'sk-abc…mnop' })).toBe('sk-abc…mnop')
  })

  test('falls back to a prompt when the connection carries no credential', () => {
    expect(credentialPlaceholder({})).toBe('Enter new API key')
    expect(credentialPlaceholder({ apiKeyMasked: '' })).toBe('Enter new API key')
  })
})

describe('credentialUpdate', () => {
  test('a typed key becomes the update fragment', () => {
    expect(credentialUpdate('sk-new-key')).toEqual({ apiKey: 'sk-new-key' })
  })

  test('surrounding whitespace is not part of the key', () => {
    expect(credentialUpdate('  sk-new-key \n')).toEqual({ apiKey: 'sk-new-key' })
  })

  test('an untouched field sends nothing, keeping the stored key', () => {
    expect(credentialUpdate('')).toBeNull()
  })

  test('a field the user opened and closed again is still untouched', () => {
    expect(credentialUpdate('   ')).toBeNull()
  })
})

describe('credentialCheckState', () => {
  test('maps a passing probe to valid', () => {
    expect(credentialCheckState({ valid: true })).toBe('valid')
  })

  test('maps a rejected key to invalid', () => {
    expect(credentialCheckState({ supported: true, valid: false })).toBe('invalid')
  })

  test('a provider without a probe is not the key’s fault', () => {
    expect(credentialCheckState({ supported: false, valid: false })).toBe('unsupported')
  })
})