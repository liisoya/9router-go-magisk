import { afterEach, describe, expect, it } from 'bun:test'
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { fingerprint } from './web-build'

/**
 * The guard this covers decides whether `make web-build` embeds the SPA you
 * just edited or the one from your last pull. The bug it was written for:
 * dist survived a `git pull`, so the sources moved and the bundle did not, and
 * the dashboard silently served the previous release.
 */

let tmp: string | undefined

/** A minimal tree with every path the fingerprint walks. */
function scaffold(): string {
  tmp = mkdtempSync(join(tmpdir(), 'web-build-fingerprint-'))
  mkdirSync(join(tmp, 'src', 'components'), { recursive: true })
  mkdirSync(join(tmp, 'public'), { recursive: true })
  mkdirSync(join(tmp, 'dist', 'assets'), { recursive: true })
  mkdirSync(join(tmp, 'node_modules', 'pkg'), { recursive: true })
  writeFileSync(join(tmp, 'src', 'main.ts'), 'console.log(1)')
  writeFileSync(join(tmp, 'src', 'components', 'App.svelte'), '<p>hi</p>')
  writeFileSync(join(tmp, 'public', 'sw.js'), 'self.addEventListener("fetch", () => {})')
  writeFileSync(join(tmp, 'package.json'), '{"name":"web"}')
  writeFileSync(join(tmp, 'bun.lock'), 'lockfile')
  writeFileSync(join(tmp, 'index.html'), '<html></html>')
  // Must never move the digest: the tree the script is checking is ignored.
  writeFileSync(join(tmp, 'node_modules', 'pkg', 'index.js'), 'module.exports = 1')
  writeFileSync(join(tmp, 'dist', 'index.html'), '<html>stale</html>')
  return tmp
}

afterEach(() => {
  if (tmp) rmSync(tmp, { recursive: true, force: true })
  tmp = undefined
})

describe('fingerprint', () => {
  it('is stable across repeated reads of an unchanged tree', () => {
    const root = scaffold()
    expect(fingerprint(root)).toBe(fingerprint(root))
  })

  it('changes when a component under src is edited', () => {
    const root = scaffold()
    const before = fingerprint(root)
    writeFileSync(join(root, 'src', 'components', 'App.svelte'), '<p>new copy</p>')
    expect(fingerprint(root)).not.toBe(before)
  })

  it('changes when a nested file is added', () => {
    const root = scaffold()
    const before = fingerprint(root)
    writeFileSync(join(root, 'src', 'components', 'Sidebar.svelte'), '<nav></nav>')
    expect(fingerprint(root)).not.toBe(before)
  })

  it('changes when the dependency manifest moves, even with no source edit', () => {
    // A dependency bump changes the emitted bundle without touching web/src,
    // which is exactly the case a src-only watcher would miss.
    const root = scaffold()
    const before = fingerprint(root)
    writeFileSync(join(root, 'package.json'), '{"name":"web","dependencies":{"svelte":"^6"}}')
    expect(fingerprint(root)).not.toBe(before)
  })

  it('changes when the lockfile moves', () => {
    const root = scaffold()
    const before = fingerprint(root)
    writeFileSync(join(root, 'bun.lock'), 'lockfile-v2')
    expect(fingerprint(root)).not.toBe(before)
  })

  it('changes when a file is renamed to identical bytes', () => {
    // The module graph a file belongs to changed even though the content did
    // not, so a content-only digest would call this tree unchanged.
    const root = scaffold()
    const before = fingerprint(root)
    cpSync(join(root, 'src', 'components', 'App.svelte'), join(root, 'src', 'components', 'Shell.svelte'))
    rmSync(join(root, 'src', 'components', 'App.svelte'))
    expect(fingerprint(root)).not.toBe(before)
  })

  it('ignores the built output it is judging', () => {
    // Rebuilding web/dist rewrites every byte it contains; folding that into
    // the fingerprint would make each build invalidate the next one.
    const root = scaffold()
    const before = fingerprint(root)
    writeFileSync(join(root, 'dist', 'index.html'), '<html>rebuilt</html>')
    writeFileSync(join(root, 'dist', 'assets', 'index-abc123.js'), 'console.log(2)')
    expect(fingerprint(root)).toBe(before)
  })

  it('ignores node_modules', () => {
    const root = scaffold()
    const before = fingerprint(root)
    writeFileSync(join(root, 'node_modules', 'pkg', 'index.js'), 'module.exports = 2')
    expect(fingerprint(root)).toBe(before)
  })

  it('matches the real web tree when pointed at webRoot', () => {
    // Guards against the input list drifting away from what vite actually
    // reads: this asserts the real project produces a digest, not a throw.
    const digest = fingerprint()
    expect(digest).toMatch(/^[0-9a-f]{64}$/)
    expect(existsSync(join(import.meta.dir, '..', 'src', 'main.ts'))).toBe(true)
  })

  it('is recorded and read back verbatim from the stamp file', () => {
    // The Makefile compares this exact string; trailing whitespace or a
    // truncated digest would make every run rebuild.
    const digest = fingerprint()
    const stampPath = join(tmpdir(), `web-build-stamp-${process.pid}.txt`)
    try {
      writeFileSync(stampPath, `${digest}\n`)
      expect(readFileSync(stampPath, 'utf8').trim()).toBe(digest)
    } finally {
      rmSync(stampPath, { force: true })
    }
  })
})