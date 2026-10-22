import { describe, expect, it } from 'bun:test'
import { matchMemberRow, modelTail, parseMember, sameProvider } from './comboMatch'

interface Row {
  provider: string
  model: string
  marker: string
}

const row = (provider: string, model: string): Row => ({ provider, model, marker: `${provider}/${model}` })

describe('parseMember', () => {
  it('splits on the first slash so nested model paths survive', () => {
    expect(parseMember('cutadai/cbcn/deepseek-v4.1-flash')).toEqual({
      provider: 'cutadai',
      model: 'cbcn/deepseek-v4.1-flash',
    })
  })

  it('keeps a bare provider', () => {
    expect(parseMember('lucif')).toEqual({ provider: 'lucif', model: '' })
  })
})

describe('modelTail', () => {
  it('returns the last path segment', () => {
    expect(modelTail('cbcn/deepseek-v4.1-flash')).toBe('deepseek-v4.1-flash')
  })

  it('is empty for an empty model', () => {
    expect(modelTail('')).toBe('')
  })
})

describe('sameProvider', () => {
  it('matches identical names', () => {
    expect(sameProvider('lucif', 'lucif')).toBe(true)
  })

  it('matches a name qualified by a separator', () => {
    expect(sameProvider('lucif.ai', 'lucif')).toBe(true)
    expect(sameProvider('openai-compatible-chat-abc', 'openai-compatible-chat')).toBe(true)
  })

  // The reason this function exists: "oc" is a prefix of "ocz", so a bare
  // prefix test would make one provider's row stand in for the other's.
  it('does not treat oc and ocz as the same provider', () => {
    expect(sameProvider('ocz', 'oc')).toBe(false)
    expect(sameProvider('oc', 'ocz')).toBe(false)
  })

  it('does not match on an empty side', () => {
    expect(sameProvider('', 'lucif')).toBe(false)
    expect(sameProvider('lucif', undefined)).toBe(false)
  })
})

describe('matchMemberRow', () => {
  it('prefers the provider whose model tail also matches', () => {
    const rows = [row('oc', 'space-bunny-free'), row('ocz', 'space-bunny-free')]
    expect(matchMemberRow(rows, 'ocz', 'space-bunny-free')?.marker).toBe('ocz/space-bunny-free')
  })

  it('never lets a twin provider answer for the other', () => {
    const rows = [row('oc', 'space-bunny-free')]
    // the combo holds both members, so `oc` is a known sibling, not a slug
    expect(matchMemberRow(rows, 'ocz', 'space-bunny-free', ['oc', 'ocz'])).toBeUndefined()
  })

  it('still matches a twin by its own provider when both rows exist', () => {
    const rows = [row('oc', 'space-bunny-free'), row('ocz', 'space-bunny-free')]
    expect(matchMemberRow(rows, 'ocz', 'space-bunny-free', ['oc', 'ocz'])?.marker).toBe('ocz/space-bunny-free')
  })

  it('falls back to the model tail for a provider reported as a slug', () => {
    // the stream reports the custom provider's slug, not the name in the combo
    const rows = [row('openai-compatible-chat-e00b5504', 'deepseek-v4.1-flash')]
    expect(matchMemberRow(rows, 'cutadai', 'deepseek-v4.1-flash')?.marker).toBe(
      'openai-compatible-chat-e00b5504/deepseek-v4.1-flash'
    )
  })

  it('matches a provider whose name is qualified in the stream', () => {
    const rows = [row('lucif.ai', 'lucif-claude')]
    expect(matchMemberRow(rows, 'lucif', 'lucif-claude')?.marker).toBe('lucif.ai/lucif-claude')
  })

  it('returns undefined when the stream has no row for the member', () => {
    expect(matchMemberRow([row('lucif', 'lucif')], 'tokenharbor', 'deepseek-v4-flash:free')).toBeUndefined()
  })
})
