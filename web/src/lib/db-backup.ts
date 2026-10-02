/* 备份/恢复的请求形状 —— 唯一实现，与上游 Next 控制台逐字对齐。
 *
 * 为什么有这个 module（2026-09-26 用户报障：Settings → Download Backup 报
 * "Invalid password"，明明已登录）：仪表盘原先用一个裸
 * `<a href="/api/settings/database">` 下载 —— 不带密码头、也没有让用户输密码的弹层，
 * 而服务端要求 `x-9r-password`（上游 spec：
 * 9router/src/app/api/settings/database/route.js:16；Go 侧 parity：
 * internal/handlers/dashboard/settings.go:119）→ 必然 401 {"error":"Invalid password"}。
 * 登录态救不了它：那是 handler 自己的独立判据（与 apiKeys/会话无关）。
 *
 * 顺带修掉两处同源偏差：导出文件名（内容其实是 JSON，却叫 .sqlite）、
 * 导入形状（上游是 JSON + password，本 fork 曾是 multipart 文件）。
 *
 * 做成纯函数是为了**离线可断言**：bun:test 直接断言"导出请求带 x-9r-password"，
 * 并让 build.sh 在构建产物上再验一次（避免"改了源码忘了重建 dist"这类静默回归）。
 */
import { formatApiError } from '../api/client'

export const DB_BACKUP_URL = '/api/settings/database'
export const DB_BACKUP_PASSWORD_HEADER = 'x-9r-password'

// 上游 1.9.6 起备份改为 zip 归档（服务端 export 带 ?format=zip，import 按 PK 头识别），
// 文件名随之 .json → .zip（settings.go:154 的 Content-Disposition 同名）。
export function backupFileName(date: Date = new Date()): string {
  return `9router-backup-${date.toISOString().replace(/[.:]/g, '-')}.zip`
}

export function buildExportRequest(password: string): { url: string; init: RequestInit } {
  return {
    url: `${DB_BACKUP_URL}?format=zip`,
    init: { headers: { [DB_BACKUP_PASSWORD_HEADER]: password } }
  }
}

// 文件体导入（上游 1.9.6 形状）：zip 与 json 原样上传，Content-Type 按扩展名/MIME 判，
// 密码走 x-9r-password 头（服务端：JSON body 或该头二选一 —— settings.go:169-174）。
export function buildImportFileRequest(
  file: File,
  password: string
): { url: string; init: RequestInit } {
  const isZip = file.name.endsWith('.zip') || file.type === 'application/zip'
  return {
    url: DB_BACKUP_URL,
    init: {
      method: 'POST',
      headers: {
        [DB_BACKUP_PASSWORD_HEADER]: password,
        'Content-Type': isZip ? 'application/zip' : 'application/json'
      },
      body: file
    }
  }
}

// 服务端错误体**两种形状都真实存在**，所以解包规则要写在这里（唯一所有者）：
//   扁平  {"error":"Invalid password"}                     ← handler 自己写（settings.go）
//   嵌套  {"error":{"message":"Unauthorized: …","type":…,"code":…}}  ← 中间件拒绝
//         （handlerutil.WriteJSONError；/api/settings/database 在 IsAlwaysProtectedPath 里，
//          无会话请求必然走它 —— 上游 #34 的第一条缺陷就是它，2026-09-29 复核证实我们同根因：
//          旧实现只认扁平串 → 一律回落成 "Failed to export database"，**真实原因被吞掉**）
// 所以把解包交给 formatApiError（它按 message/error/detail… 递归取，两种都出得来文案）；
// 但"非 JSON"与"有 JSON 却没有可读文案"（{} / 纯结构）仍回落 fallback —— 别把 JSON 吐给用户。
export async function responseErrorMessage(res: Response, fallback: string): Promise<string> {
  try {
    const data = (await res.json()) as unknown
    const unwrapped =
      data && typeof data === 'object' && 'error' in data
        ? (data as Record<string, unknown>).error
        : data
    const msg = formatApiError(unwrapped, '')
    // 形如 {…} / […] 的返回值说明没取到人话（只是把结构 stringify 了）→ 用 fallback
    return msg && !/^[[{]/.test(msg) ? msg : fallback
  } catch {
    return fallback
  }
}

// 浏览器侧下载（DOM 相关，故不在单元测试里覆盖；测试只覆盖纯函数）
export function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}
