import { describe, expect, test, beforeEach, afterEach } from 'bun:test'
import { copyToClipboard } from './clipboard'

describe('copyToClipboard', () => {
  const originalWindow = globalThis.window
  const originalDocument = globalThis.document
  const originalNavigator = globalThis.navigator

  afterEach(() => {
    globalThis.window = originalWindow
    globalThis.document = originalDocument
    globalThis.navigator = originalNavigator
  })

  test('uses navigator.clipboard.writeText when isSecureContext is true', async () => {
    let written = ''
    globalThis.window = { isSecureContext: true } as any
    globalThis.navigator = {
      clipboard: {
        writeText: async (t: string) => {
          written = t
        },
      },
    } as any

    const result = await copyToClipboard('hello secure')
    expect(result).toBe(true)
    expect(written).toBe('hello secure')
  })

  test('falls back to execCommand when isSecureContext is false', async () => {
    globalThis.window = { isSecureContext: false } as any
    globalThis.navigator = {} as any

    let execCommandCalledWith = ''
    let appendedChild: any = null
    let removedChild: any = null

    globalThis.document = {
      createElement: (tag: string) => {
        return {
          tagName: tag,
          value: '',
          style: {},
          focus: () => {},
          select: () => {},
        }
      },
      body: {
        appendChild: (el: any) => {
          appendedChild = el
        },
        removeChild: (el: any) => {
          removedChild = el
        },
      },
      execCommand: (command: string) => {
        execCommandCalledWith = command
        return true
      },
    } as any

    const result = await copyToClipboard('hello http fallback')
    expect(result).toBe(true)
    expect(execCommandCalledWith).toBe('copy')
    expect(appendedChild).toBeDefined()
    expect(appendedChild.value).toBe('hello http fallback')
    expect(appendedChild.style.position).toBe('fixed')
    expect(appendedChild.style.left).toBe('-9999px')
    expect(removedChild).toBe(appendedChild)
  })

  test('falls back to execCommand when navigator.clipboard.writeText rejects', async () => {
    globalThis.window = { isSecureContext: true } as any
    globalThis.navigator = {
      clipboard: {
        writeText: async () => {
          throw new Error('Denied')
        },
      },
    } as any

    let execCommandCalledWith = ''
    globalThis.document = {
      createElement: () => ({
        value: '',
        style: {},
        focus: () => {},
        select: () => {},
      }),
      body: {
        appendChild: () => {},
        removeChild: () => {},
      },
      execCommand: (command: string) => {
        execCommandCalledWith = command
        return true
      },
    } as any

    const result = await copyToClipboard('fallback test')
    expect(result).toBe(true)
    expect(execCommandCalledWith).toBe('copy')
  })
})
