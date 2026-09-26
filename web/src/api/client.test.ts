import { describe, expect, it } from 'bun:test'
import { api, formatApiError, getAuthHeaders, getStoredAPIKey, isUsableAPIKey, onUnauthorized, responseErrorMessage } from './client'

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
    localStorage.removeItem('9router_key')

    expect(getAuthHeaders()).toEqual({ 'Content-Type': 'application/json' })
  })

  it('uses an explicitly stored API key', () => {
    localStorage.setItem('9router_key', 'sk-test')

    try {
      expect(getAuthHeaders().Authorization).toBe('Bearer sk-test')
    } finally {
      localStorage.removeItem('9router_key')
    }
  })

  it('rejects masked or non-ASCII values for Authorization headers', () => {
    expect(isUsableAPIKey('sk-test-123')).toBe(true)
    expect(isUsableAPIKey('sk-8b7…e34f')).toBe(false)
    expect(isUsableAPIKey('sk-日本語')).toBe(false)
  })

  it('omits Authorization when the stored value is masked', () => {
    localStorage.setItem('9router_key', 'sk-8b7…e34f')
    try {
      expect(getAuthHeaders()).toEqual({ 'Content-Type': 'application/json' })
    } finally {
      localStorage.removeItem('9router_key')
    }
  })

  it('uses only the full stored key for media runners', () => {
    localStorage.setItem('9router_key', ' sk-test-123 ')
    try {
      expect(getStoredAPIKey()).toBe('sk-test-123')
      expect(getAuthHeaders().Authorization).toBe('Bearer sk-test-123')
    } finally {
      localStorage.removeItem('9router_key')
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
    localStorage.setItem('9router_auth', 'true')

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

      expect(localStorage.getItem('9router_auth')).toBeNull()
    } finally {
      unsub()
      globalThis.fetch = originalFetch
      localStorage.removeItem('9router_auth')
    }
  })
})
