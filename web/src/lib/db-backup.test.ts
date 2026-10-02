import { describe, expect, it } from 'bun:test'
import {
  backupFileName,
  buildExportRequest,
  buildImportFileRequest,
  DB_BACKUP_PASSWORD_HEADER,
  DB_BACKUP_URL,
  responseErrorMessage
} from './db-backup'

// 回归用例：Settings → Download Backup 曾报 401 Invalid password（用户实测），
// 根因是导出的请求不带 x-9r-password 头（上游 spec 要求）。该断言在修复前必红。
describe('db-backup 请求形状（上游 1.9.6 parity：zip 归档）', () => {
  it('导出必须带 x-9r-password 头 + ?format=zip（缺头就是 401；缺参数拿不到 zip）', () => {
    const { url, init } = buildExportRequest('s3cret-pw')
    expect(url).toBe(`${DB_BACKUP_URL}?format=zip`)
    const headers = init.headers as Record<string, string>
    expect(headers[DB_BACKUP_PASSWORD_HEADER]).toBe('s3cret-pw')
  })

  it('文件体导入：zip/json 原样上传，Content-Type 按扩展名判，密码走头', () => {
    const zip = new File(['PK\x03\x04'], 'backup.zip', { type: 'application/zip' })
    const z = buildImportFileRequest(zip, 'pw')
    expect(z.url).toBe(DB_BACKUP_URL)
    expect(z.init.method).toBe('POST')
    const zh = z.init.headers as Record<string, string>
    expect(zh[DB_BACKUP_PASSWORD_HEADER]).toBe('pw')
    expect(zh['Content-Type']).toBe('application/zip')
    expect(z.init.body).toBe(zip)
    const json = new File(['{}'], 'backup.json', { type: 'application/json' })
    const j = buildImportFileRequest(json, 'pw')
    expect((j.init.headers as Record<string, string>)['Content-Type']).toBe('application/json')
  })

  it('备份文件名是 .zip（上游 1.9.6 起 export 是 zip 归档，服务端同名落盘）', () => {
    const fixed = new Date('2026-09-26T07:39:49.123Z')
    expect(backupFileName(fixed)).toBe('9router-backup-2026-09-26T07-39-49-123Z.zip')
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
