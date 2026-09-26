/* session.ts — 「登录态」的单一所有者（架构候选 8）
 *
 * 现状：同一批键（9router_auth / 9router_key）原先在 5 个文件里直接读写
 * localStorage / sessionStorage —— isAuthenticated 自己拼两个 storage，登录写两行、
 * 登出写两行，而且**三种"登出"变体**散在不同地方（只清标记 / 清标记+key），
 * 读代码分不出哪个是故意的、哪个是漏的。
 *
 * 做法：照本仓已认可的 lib/oauth-handoff.ts 的 KVStore 做法 —— 把存储**注入**进来，
 * 于是全部是纯函数，可离线断言（bun test）；键名与"什么算已登录"只在本文件定义一次。
 */

export const AUTH_FLAG_KEY = '9router_auth'
export const API_KEY_STORAGE_KEY = '9router_key'

export interface KVStore {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

/** 存储可能不存在（SSR / 测试）—— 所有读写都要能容忍 */
export type MaybeStore = KVStore | undefined

export interface SessionStores {
  /** 标签页内（关标签即失效） */
  session?: MaybeStore
  /** 跨标签、跨刷新 */
  local?: MaybeStore
}

function read(store: MaybeStore, key: string): string {
  if (!store) return ''
  try {
    return store.getItem(key) ?? ''
  } catch {
    return ''
  }
}

function write(store: MaybeStore, key: string, value: string): void {
  if (!store) return
  try {
    store.setItem(key, value)
  } catch {
    /* 隐私模式 / 配额满：登录态写不进去，不该把界面炸掉 */
  }
}

function drop(store: MaybeStore, key: string): void {
  if (!store) return
  try {
    store.removeItem(key)
  } catch {
    /* 同上 */
  }
}

/** 浏览器全局 storage；SSR / 测试环境里为 undefined（读写都安全） */
export function browserStores(): SessionStores {
  const g = globalThis as { sessionStorage?: KVStore; localStorage?: KVStore }
  return { session: g.sessionStorage, local: g.localStorage }
}

/** 是否已登录：两个 storage 任一为 'true'（**既有语义**，见 client.ts isAuthenticated） */
export function isAuthed(stores: SessionStores): boolean {
  return read(stores.session, AUTH_FLAG_KEY) === 'true' || read(stores.local, AUTH_FLAG_KEY) === 'true'
}

/** 登录成功：两个 storage 都置位（关标签、刷新都还在 —— 既有行为） */
export function markAuthed(stores: SessionStores): void {
  write(stores.session, AUTH_FLAG_KEY, 'true')
  write(stores.local, AUTH_FLAG_KEY, 'true')
}

/** 只清登录标记（保留存储的 API key）—— 服务端说未登录、显式 logout 走这条 */
export function clearAuthed(stores: SessionStores): void {
  drop(stores.session, AUTH_FLAG_KEY)
  drop(stores.local, AUTH_FLAG_KEY)
}

/** 全清：登录标记 + 存储的 API key（401 之后清掉过期的本地凭据） */
export function clearAll(stores: SessionStores): void {
  clearAuthed(stores)
  drop(stores.local, API_KEY_STORAGE_KEY)
}

/** 读存储的 API key（trim 后返回；可用性判断留给 isUsableAPIKey） */
export function getStoredApiKey(stores: SessionStores): string {
  return read(stores.local, API_KEY_STORAGE_KEY).trim()
}

export function setStoredApiKey(stores: SessionStores, value: string): void {
  write(stores.local, API_KEY_STORAGE_KEY, value)
}
