import { describe, expect, it } from 'bun:test'
import {
  backupFileName,
  buildExportRequest,
  buildImportRequest,
  DB_BACKUP_PASSWORD_HEADER,
  DB_BACKUP_URL,
  responseErrorMessage
} from './db-backup'

// 回归用例：Settings → Download Backup 曾报 401 Invalid password（用户实测），
// 根因是导出的请求不带 x-9r-password 头（上游 spec 要求）。该断言在修复前必红。
describe('db-backup 请求形状（上游 parity）', () => {
  it('导出必须带 x-9r-password 头（缺了就是 401 Invalid password）', () => {
    const { url, init } = buildExportRequest('s3cret-pw')
    expect(url).toBe(DB_BACKUP_URL)
    const headers = init.headers as Record<string, string>
    expect(headers[DB_BACKUP_PASSWORD_HEADER]).toBe('s3cret-pw')
  })

  it('导入是 JSON + password（上游形状），不是 multipart', () => {
    const { url, init } = buildImportRequest({ settings: { a: 1 } }, 'pw')
    expect(url).toBe(DB_BACKUP_URL)
    expect(init.method).toBe('POST')
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json')
    const body = JSON.parse(String(init.body))
    expect(body.password).toBe('pw')
    expect(body.settings).toEqual({ a: 1 })
  })

  it('备份文件名是 .json（内容是 JSON；旧实现叫 .sqlite 会误导用户）', () => {
    const fixed = new Date('2026-09-26T07:39:49.123Z')
    expect(backupFileName(fixed)).toBe('9router-backup-2026-09-26T07-39-49-123Z.json')
  })

  it('服务端 { error } 文案要透出给用户（Invalid password 不能被吞成通用提示）', async () => {
    const res = new Response(JSON.stringify({ error: 'Invalid password' }), { status: 401 })
    expect(await responseErrorMessage(res, 'Failed to export database')).toBe('Invalid password')
    const broken = new Response('<html>504</html>', { status: 504 })
    expect(await responseErrorMessage(broken, 'Failed to export database')).toBe(
      'Failed to export database'
    )
  })

  // 上游 #34 的第一条缺陷（2026-09-29 复核 v1.9.5 时发现我们这边**也是**同根因）：
  // /api/settings/database 在 IsAlwaysProtectedPath 里 → 无会话请求被 RequireDashboardAuth
  // 用 handlerutil.WriteJSONError 拒绝，而它写的是**嵌套** envelope：
  //   {"error":{"message":"Unauthorized: admin session or CLI token required","type":…,"code":…}}
  // 旧的 responseErrorMessage 只认扁平 {"error":"…"} → typeof data.error === 'string' 不成立
  // → 一律回落成 "Failed to export database"，**真实原因（会话/密码问题）被吞掉**。
  it('嵌套 envelope（中间件拒绝形状）也要解出文案，不能吞成通用提示', async () => {
    const res = new Response(
      JSON.stringify({
        error: {
          message: 'Unauthorized: admin session or CLI token required',
          type: 'invalid_request_error',
          code: '401'
        }
      }),
      { status: 401 }
    )
    expect(await responseErrorMessage(res, 'Failed to export database')).toBe(
      'Unauthorized: admin session or CLI token required'
    )
  })

  it('没有可读文案的结构化响应一律回落 fallback（别把 JSON 吐给用户）', async () => {
    const empty = new Response('{}', { status: 500 })
    expect(await responseErrorMessage(empty, 'Failed to import database')).toBe(
      'Failed to import database'
    )
    const noMsg = new Response(JSON.stringify({ error: { type: 'x', code: '500' } }), {
      status: 500
    })
    expect(await responseErrorMessage(noMsg, 'Failed to import database')).toBe(
      'Failed to import database'
    )
  })
})
