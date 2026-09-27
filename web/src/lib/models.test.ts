import { describe, expect, it } from 'bun:test'
import { getModelsByProviderId, PROVIDER_ID_TO_ALIAS, PROVIDER_MODELS } from './models'

// PROVIDER_MODELS is keyed by provider id, while PROVIDER_ID_TO_ALIAS holds the
// short display prefix (uiAlias) used to render `cmc/<model>`. The two only
// coincide for some providers, so the resolver must try the id first and fall
// back to the alias instead of handing the dashboard an empty catalog.
describe('getModelsByProviderId', () => {
  it('resolves a provider whose display prefix differs from its id', () => {
    expect(PROVIDER_ID_TO_ALIAS.commandcode).toBe('cmc')

    const models = getModelsByProviderId('commandcode')

    expect(models.length).toBe(22)
    expect(models.map((m) => m.id)).toContain('deepseek/deepseek-v4-pro')
    expect(models.map((m) => m.id)).toContain('nvidia/nemotron-3-ultra-550b-a55b')
  })

  it('never returns an empty catalog for a provider that has one', () => {
    const broken = Object.keys(PROVIDER_ID_TO_ALIAS)
      .filter((id) => (PROVIDER_MODELS[id] ?? []).length > 0)
      .filter((id) => getModelsByProviderId(id).length === 0)

    expect(broken).toEqual([])
  })

  it('round-trips every catalog key that declares models', () => {
    // `zd` is a declared-but-empty entry: Zed's catalog is fetched live, so an
    // empty array is a valid catalog state, not a resolution failure.
    const declared = Object.keys(PROVIDER_MODELS).filter((id) => PROVIDER_MODELS[id].length > 0)

    for (const id of declared) {
      expect(getModelsByProviderId(id).length).toBe(PROVIDER_MODELS[id].length)
    }
  })

  it('returns an empty list for a provider with no static catalog', () => {
    expect(getModelsByProviderId('does-not-exist')).toEqual([])
  })
})

// The dashboard renders the rows in catalog order, so the catalog sequence is
// part of the contract with upstream: `open-sse/providers/registry/commandcode.js`
// lists the models in exactly this order. A reordering here silently changes
// what the two dashboards show.
describe('commandcode catalog', () => {
  const UPSTREAM_REGISTRY_ORDER = [
    'deepseek/deepseek-v4-pro',
    'deepseek/deepseek-v4-flash',
    'moonshotai/Kimi-K2.7-Code',
    'moonshotai/Kimi-K2.7-Code-Highspeed',
    'moonshotai/Kimi-K2.6',
    'moonshotai/Kimi-K2.5',
    'zai-org/GLM-5.2',
    'zai-org/GLM-5.2-Fast',
    'zai-org/GLM-5.1',
    'zai-org/GLM-5',
    'MiniMaxAI/MiniMax-M3',
    'MiniMaxAI/MiniMax-M2.7',
    'MiniMaxAI/MiniMax-M2.5',
    'xiaomi/mimo-v2.5-pro',
    'xiaomi/mimo-v2.5',
    'Qwen/Qwen3.6-Max-Preview',
    'Qwen/Qwen3.6-Plus',
    'Qwen/Qwen3.7-Max',
    'Qwen/Qwen3.7-Plus',
    'stepfun/Step-3.7-Flash',
    'stepfun/Step-3.5-Flash',
    'nvidia/nemotron-3-ultra-550b-a55b',
  ]

  it('keeps the upstream registry order', () => {
    expect(getModelsByProviderId('commandcode').map((m) => m.id)).toEqual(UPSTREAM_REGISTRY_ORDER)
  })
})

