import { afterEach, beforeEach, describe, expect, it } from 'bun:test'
import {
  isComboFlowVisible,
  isDetailsExpandedByDefault,
  isUsageTrendVisible,
  setComboFlowVisible,
  setDetailsExpandedByDefault,
  setUsageTrendVisible,
} from './displayPrefs'

// Bun has no DOM storage; displayPrefs guards on `typeof localStorage`, so a
// stub is installed per test and the previous descriptor is restored after
// (same shape as compactNumber.test.ts).
let entries: Record<string, string> = {}
const storageStub = {
  getItem: (key: string) => entries[key] ?? null,
  setItem: (key: string, value: string) => {
    entries[key] = value
  },
  removeItem: (key: string) => {
    delete entries[key]
  },
  clear: () => {
    entries = {}
  },
}
const previousDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')

beforeEach(() => {
  entries = {}
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    writable: true,
    value: storageStub,
  })
})

afterEach(() => {
  if (previousDescriptor) {
    Object.defineProperty(globalThis, 'localStorage', previousDescriptor)
  } else {
    Reflect.deleteProperty(globalThis, 'localStorage')
  }
})

describe('usage detail collapse preference', () => {
  it('starts collapsed on a browser that never touched the switch', () => {
    expect(isDetailsExpandedByDefault()).toBe(false)
  })

  it('round-trips the switch', () => {
    setDetailsExpandedByDefault(true)
    expect(isDetailsExpandedByDefault()).toBe(true)
    setDetailsExpandedByDefault(false)
    expect(isDetailsExpandedByDefault()).toBe(false)
  })

  it('treats a hand-edited value as collapsed', () => {
    localStorage.setItem('detailsExpandedByDefault', 'yes')
    expect(isDetailsExpandedByDefault()).toBe(false)
  })
})

describe('Overview panel visibility preference', () => {
  it('shows both panels on a browser that never touched the switches', () => {
    expect(isComboFlowVisible()).toBe(true)
    expect(isUsageTrendVisible()).toBe(true)
  })

  it('round-trips each switch independently', () => {
    setComboFlowVisible(false)
    expect(isComboFlowVisible()).toBe(false)
    expect(isUsageTrendVisible()).toBe(true)
    setUsageTrendVisible(false)
    expect(isUsageTrendVisible()).toBe(false)
    setUsageTrendVisible(true)
    expect(isUsageTrendVisible()).toBe(true)
    expect(isComboFlowVisible()).toBe(false)
  })

  it('treats a hand-edited value as visible', () => {
    localStorage.setItem('showComboFlow', 'yes')
    expect(isComboFlowVisible()).toBe(true)
  })
})
