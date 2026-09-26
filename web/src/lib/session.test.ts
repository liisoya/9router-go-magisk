import { describe, expect, it } from 'bun:test'
import {
  API_KEY_STORAGE_KEY,
  AUTH_FLAG_KEY,
  clearAll,
  clearAuthed,
  getStoredApiKey,
  isAuthed,
  markAuthed,
  setStoredApiKey,
  type KVStore,
  type SessionStores
} from './session'

/* 登录态契约（架构候选 8）：这批键原先在 5 个文件里手写，三种"登出"变体分不清是故意还是漏。
 * 存储注入 → 全部纯函数 → 离线可断言。 */

type FakeStore = KVStore & { map: Map<string, string> }

function fake(init: Record<string, string> = {}): FakeStore {
  const map = new Map(Object.entries(init))
  return {
    map,
    getItem: k => (map.has(k) ? (map.get(k) as string) : null),
    setItem: (k, v) => {
      map.set(k, v)
    },
    removeItem: k => {
      map.delete(k)
    }
  }
}

function stores(init: { session?: Record<string, string>; local?: Record<string, string> } = {}): SessionStores {
  return { session: fake(init.session), local: fake(init.local) }
}

describe('登录态 module（唯一所有者）', () => {
  it('键名只在这里定义一次（防漂）', () => {
    expect(AUTH_FLAG_KEY).toBe('9router_auth')
    expect(API_KEY_STORAGE_KEY).toBe('9router_key')
  })

  it('isAuthed：两个 storage 任一为 true 即已登录', () => {
    expect(isAuthed(stores({ session: { [AUTH_FLAG_KEY]: 'true' } }))).toBe(true)
    expect(isAuthed(stores({ local: { [AUTH_FLAG_KEY]: 'true' } }))).toBe(true)
    expect(isAuthed(stores())).toBe(false)
    expect(isAuthed(stores({ local: { [AUTH_FLAG_KEY]: 'false' } }))).toBe(false)
  })

  it('isAuthed：storage 不存在（SSR/隐私模式）不抛且判未登录', () => {
    expect(isAuthed({})).toBe(false)
    expect(isAuthed({ session: undefined, local: undefined })).toBe(false)
  })

  it('markAuthed：两个都置位（关标签、刷新都还在 —— 既有行为）', () => {
    const s = stores()
    markAuthed(s)
    expect((s.session as FakeStore).getItem(AUTH_FLAG_KEY)).toBe('true')
    expect((s.local as FakeStore).getItem(AUTH_FLAG_KEY)).toBe('true')
  })

  it('clearAuthed：只清登录标记，保留 API key（App.svelte / logout 那条路）', () => {
    const s = stores({ session: { [AUTH_FLAG_KEY]: 'true' }, local: { [AUTH_FLAG_KEY]: 'true', [API_KEY_STORAGE_KEY]: 'sk-1' } })
    clearAuthed(s)
    expect(isAuthed(s)).toBe(false)
    expect(getStoredApiKey(s)).toBe('sk-1')
  })

  it('clearAll：登录标记 + API key 一起清（401 之后清过期凭据）', () => {
    const s = stores({ session: { [AUTH_FLAG_KEY]: 'true' }, local: { [AUTH_FLAG_KEY]: 'true', [API_KEY_STORAGE_KEY]: 'sk-1' } })
    clearAll(s)
    expect(isAuthed(s)).toBe(false)
    expect(getStoredApiKey(s)).toBe('')
  })

  it('API key 读写：走 local、trim 后返回', () => {
    const s = stores()
    setStoredApiKey(s, '  sk-abc  ')
    expect(getStoredApiKey(s)).toBe('sk-abc')
    expect((s.local as FakeStore).map.has(API_KEY_STORAGE_KEY)).toBe(true)
  })

  it('写失败（配额满/隐私模式）不抛，界面不被炸掉', () => {
    const boom: KVStore = {
      getItem: () => null,
      setItem: () => {
        throw new Error('QuotaExceededError')
      },
      removeItem: () => {
        throw new Error('nope')
      }
    }
    expect(() => markAuthed({ session: boom, local: boom })).not.toThrow()
    expect(() => clearAll({ session: boom, local: boom })).not.toThrow()
    expect(isAuthed({ session: boom, local: boom })).toBe(false)
  })

  it('门禁：登录态键的字面量只允许出现在 lib/session.ts（防回潮）', async () => {
    const { readdirSync, readFileSync, statSync } = await import('node:fs')
    const { join } = await import('node:path')
    const root = join(import.meta.dir, '..')
    const files: string[] = []
    const walk = (dir: string): void => {
      for (const entry of readdirSync(dir)) {
        const p = join(dir, entry)
        if (statSync(p).isDirectory()) walk(p)
        else if (/\.(ts|svelte)$/.test(p)) files.push(p)
      }
    }
    walk(root)
    const offenders = files
      .filter(f => !/(^|[/\\])session\.(ts|test\.ts)$/.test(f))
      .filter(f => /9router_auth|9router_key/.test(readFileSync(f, 'utf8')))
      .map(f => f.slice(root.length + 1))
    expect(offenders).toEqual([])
  })
})
