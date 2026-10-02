import { describe, expect, it } from 'bun:test'
import { api, formatApiError, getAuthHeaders, getStoredAPIKey, isUsableAPIKey, onUnauthorized, responseErrorMessage } from './client'
// 键名不在测试里手写：登录态的唯一所有者在 lib/session（候选 8）
import { API_KEY_STORAGE_KEY, AUTH_FLAG_KEY } from '../lib/session'

describe('dashboard API authentication and errors', () => {

  const storage: Record<string, string> = {}
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => storage[key] ?? null,
      setItem: (key: string, value: string) => {
        storage[key] = value
      },
      removeItem: (key: string) => {
        delete storage[key]
      },
    },
  })
  it('omits Authorization when no API key is explicitly stored', () => {
    localStorage.removeItem(API_KEY_STORAGE_KEY)

    expect(getAuthHeaders()).toEqual({ 'Content-Type': 'application/json' })
  })

  it('uses an explicitly stored API key', () => {
    localStorage.setItem(API_KEY_STORAGE_KEY, 'sk-test')

    try {
      expect(getAuthHeaders().Authorization).toBe('Bearer sk-test')
    } finally {
      localStorage.removeItem(API_KEY_STORAGE_KEY)
    }
  })

  it('rejects masked or non-ASCII values for Authorization headers', () => {
    expect(isUsableAPIKey('sk-test-123')).toBe(true)
    expect(isUsableAPIKey('sk-8b7…e34f')).toBe(false)
    expect(isUsableAPIKey('sk-日本語')).toBe(false)
  })

  it('omits Authorization when the stored value is masked', () => {
    localStorage.setItem(API_KEY_STORAGE_KEY, 'sk-8b7…e34f')
    try {
      expect(getAuthHeaders()).toEqual({ 'Content-Type': 'application/json' })
    } finally {
      localStorage.removeItem(API_KEY_STORAGE_KEY)
    }
  })

  it('uses only the full stored key for media runners', () => {
    localStorage.setItem(API_KEY_STORAGE_KEY, ' sk-test-123 ')
    try {
      expect(getStoredAPIKey()).toBe('sk-test-123')
      expect(getAuthHeaders().Authorization).toBe('Bearer sk-test-123')
    } finally {
      localStorage.removeItem(API_KEY_STORAGE_KEY)
    }
  })

  it('extracts messages from nested API errors', () => {
    expect(formatApiError({ error: { message: 'provider rejected the request' } })).toBe(
      'provider rejected the request',
    )
    expect(formatApiError({ errors: [{ detail: { message: 'invalid upstream response' } }] })).toBe(
      'invalid upstream response',
    )
  })

  it('reads an error response body without producing an object literal', async () => {
    const response = new Response(
      JSON.stringify({ error: { message: 'console clearing is unavailable' } }),
      { status: 503, headers: { 'Content-Type': 'application/json' } },
    )

    expect(await responseErrorMessage(response)).toBe('console clearing is unavailable')
  })

  it('uses the upstream Antigravity authorize and exchange contract', async () => {
    const originalFetch = globalThis.fetch
    const requests: Array<{ url: string; init?: RequestInit }> = []
    globalThis.fetch = async (input, init) => {
      requests.push({ url: String(input), init })
      return Response.json({ success: true, state: 'state-123', redirectUri: 'http://localhost:20130/callback' })
    }
    try {
      await api.getAntigravityAuthorizeUrl('http://localhost:20130/callback')
      await api.antigravityExchange('code-123', 'http://localhost:20130/callback', 'state-123')
    } finally {
      globalThis.fetch = originalFetch
    }

    expect(requests[0]?.url).toBe(
      '/api/oauth/antigravity/authorize?redirect_uri=http%3A%2F%2Flocalhost%3A20130%2Fcallback',
    )
    expect(requests[1]?.url).toBe('/api/oauth/antigravity/exchange')
    expect(requests[1]?.init?.method).toBe('POST')
    expect(JSON.parse(String(requests[1]?.init?.body))).toEqual({
      code: 'code-123',
      redirectUri: 'http://localhost:20130/callback',
      state: 'state-123',
    })
  })

  it('triggers onUnauthorized and clears storage on 401 responses', async () => {
    const originalFetch = globalThis.fetch
    localStorage.setItem(AUTH_FLAG_KEY, 'true')

    const { promise, resolve } = Promise.withResolvers<void>()
    const unsub = onUnauthorized(() => {
      resolve()
    })

    globalThis.fetch = async () => {
      return new Response(JSON.stringify({ error: 'Unauthorized' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      })
    }

    try {
      await expect(api.getConnections()).rejects.toThrow()
      await promise

      expect(localStorage.getItem(AUTH_FLAG_KEY)).toBeNull()
    } finally {
      unsub()
      globalThis.fetch = originalFetch
      localStorage.removeItem(AUTH_FLAG_KEY)
    }
  })

  it('uses the upstream Codex reset-credit contract', async () => {
    const originalFetch = globalThis.fetch
    const requests: Array<{ url: string; init?: RequestInit }> = []
    globalThis.fetch = async (input, init) => {
      requests.push({ url: String(input), init })
      if (String(input).endsWith('/consume')) {
        return Response.json({ outcome: 'reset', selectionToken: 'c1', idempotencyKey: 'idem-1' })
      }
      return Response.json({ credits: [{ selectionToken: 'c1', title: 'Weekly reset' }], availableCount: 1 })
    }
    try {
      await api.listCodexResetCredits('codex-1')
      await api.consumeCodexResetCredit('codex-1', 'c1', 'idem-1')
    } finally {
      globalThis.fetch = originalFetch
    }

    expect(requests[0]?.url).toBe('/api/usage/codex-1/reset-credits')
    expect(requests[1]?.url).toBe('/api/usage/codex-1/reset-credits/consume')
    expect(requests[1]?.init?.method).toBe('POST')
    expect(JSON.parse(String(requests[1]?.init?.body))).toEqual({
      selectionToken: 'c1',
      idempotencyKey: 'idem-1',
    })
  })

  it('encodes the connection id in the reset-credit path', async () => {
    const originalFetch = globalThis.fetch
    const urls: string[] = []
    globalThis.fetch = async (input) => {
      urls.push(String(input))
      return Response.json({ credits: [], availableCount: 0 })
    }
    try {
      await api.listCodexResetCredits('id with/slash')
    } finally {
      globalThis.fetch = originalFetch
    }

    expect(urls[0]).toBe('/api/usage/id%20with%2Fslash/reset-credits')
  })

  it('normalizes object lastError to string message when listing connections', async () => {
    const originalFetch = globalThis.fetch
    globalThis.fetch = async () => {
      return Response.json([
        {
          id: 'conn-1',
          provider: 'openai-compatible-chat-123',
          authType: 'apikey',
          isActive: 1,
          data: JSON.stringify({
            lastError: {
              status: 429,
              message: 'Rate limit exceeded: resets in 10s',
              timestamp: '2026-09-30T12:00:00Z',
            },
          }),
        },
        {
          id: 'conn-2',
          provider: 'openai-compatible-chat-456',
          authType: 'apikey',
          isActive: 1,
          lastError: {
            status: 500,
            error: 'Internal upstream error',
          },
        },
      ])
    }
    try {
      const conns = await api.getConnections()
      expect(conns.length).toBe(2)
      expect(conns[0].lastError).toBe('Rate limit exceeded: resets in 10s')
      expect(conns[1].lastError).toBe('Internal upstream error')
    } finally {
      globalThis.fetch = originalFetch
    }
  })

  it('normalizes object lastError in getProvidersClient and getProvidersClientPage', async () => {
    const originalFetch = globalThis.fetch
    globalThis.fetch = async () => {
      return Response.json({
        connections: [
          {
            id: 'conn-1',
            provider: 'openai-compatible-chat-123',
            authType: 'apikey',
            isActive: 1,
            lastError: { message: 'Quota exhausted' },
          },
        ],
      })
    }
    try {
      const clientRes = await api.getProvidersClient()
      expect(clientRes.connections[0].lastError).toBe('Quota exhausted')

      const pageRes = await api.getProvidersClientPage('page=1')
      expect(pageRes.connections[0].lastError).toBe('Quota exhausted')
    } finally {
      globalThis.fetch = originalFetch
    }
  })
})
