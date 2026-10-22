import { describe, expect, it } from 'bun:test'
import { TAB_ROUTES } from './router'
import {
  ALWAYS_VISIBLE_NAV_IDS,
  DEFAULT_HIDDEN_NAV_IDS,
  TOGGLEABLE_NAV_ENTRIES,
  NAV_GROUPS,
  NAV_ORDER,
  isNavHidden,
  isNavLinkActive,
  normalizeHiddenNav,
  setNavVisible,
  visibleNavGroups,
  type NavEntry,
} from './nav'

const links: Extract<NavEntry, { kind: 'link' }>[] = NAV_GROUPS.flatMap((group) =>
  group.entries.flatMap((entry) => (entry.kind === 'accordion' ? entry.children : [entry]))
)

const groupIds = (groups = NAV_GROUPS): string[] =>
  groups.flatMap((group) =>
    group.entries.flatMap((entry) =>
      entry.kind === 'accordion' ? [entry.id, ...entry.children.map((child) => child.id)] : [entry.id]
    )
  )

const renderedIds = (hidden: unknown): string[] => groupIds(visibleNavGroups(hidden))

const MEDIA_CHILD_IDS = NAV_GROUPS.flatMap((group) =>
  group.entries.flatMap((entry) => (entry.kind === 'accordion' ? entry.children.map((c) => c.id) : []))
)

describe('nav model', () => {
  it('addresses a route the router knows for every entry', () => {
    // The consumer check: an id with no route is an entry the sidebar would
    // render but the app could not open.
    for (const link of links) {
      expect(TAB_ROUTES[link.id as keyof typeof TAB_ROUTES]).toBeTruthy()
    }
  })

  it('uses every id exactly once', () => {
    expect(new Set(NAV_ORDER).size).toBe(NAV_ORDER.length)
    expect(NAV_ORDER.length).toBeGreaterThan(0)
  })

  it('offers the Plans entry to the panel, and nothing it may not hide', () => {
    expect(TOGGLEABLE_NAV_ENTRIES.map((entry) => entry.id)).toEqual(['plans'])
    for (const entry of TOGGLEABLE_NAV_ENTRIES) {
      expect(NAV_ORDER).toContain(entry.id)
      expect(ALWAYS_VISIBLE_NAV_IDS).not.toContain(entry.id)
    }
  })

  it('keeps the panel behind an entry it can never hide', () => {
    expect(ALWAYS_VISIBLE_NAV_IDS).toContain('settings')
    expect(renderedIds({ hidden: ['settings'] })).toContain('settings')
    expect(normalizeHiddenNav({ hidden: ['settings'] })).toEqual([])
    expect(setNavVisible([], 'settings', false)).toEqual([])
  })
})

describe('default visibility', () => {
  it('hides Plans, and only Plans, until a list is saved', () => {
    expect([...DEFAULT_HIDDEN_NAV_IDS]).toEqual(['plans'])
    for (const saved of [undefined, null, {}, 'plans', { hidden: 'plans' }]) {
      const ids = renderedIds(saved)
      expect(ids, JSON.stringify(saved)).not.toContain('plans')
      // Security was never a default and must stay visible out of the box.
      expect(ids, JSON.stringify(saved)).toContain('security')
      expect(ids, JSON.stringify(saved)).toContain('settings')
    }
  })

  it('treats an explicit empty list as "show everything"', () => {
    expect(normalizeHiddenNav({ hidden: [] })).toEqual([])
    expect(renderedIds({ hidden: [] })).toEqual(NAV_ORDER)
  })

  it('blocks the default-hidden tab until the operator restores it', () => {
    expect(isNavHidden('plans', undefined)).toBe(true)
    // Security is no longer a default: only a saved list hides it.
    expect(isNavHidden('security', undefined)).toBe(false)
    expect(isNavHidden('security', { hidden: [] })).toBe(false)
    expect(isNavHidden('plans', { hidden: ['plans'] })).toBe(true)
  })
})

describe('visibility filtering', () => {
  it('removes only the hidden link', () => {
    const ids = renderedIds({ hidden: ['quota'] })
    expect(ids).not.toContain('quota')
    expect(ids).toContain('endpoint')
    expect(ids).toContain('media-video')
  })

  it('removes an accordion with its children', () => {
    const ids = renderedIds({ hidden: ['media-providers'] })
    expect(ids).not.toContain('media-providers')
    for (const child of MEDIA_CHILD_IDS) {
      expect(ids).not.toContain(child)
    }
  })

  it('drops an accordion whose children are all hidden', () => {
    const ids = renderedIds({ hidden: MEDIA_CHILD_IDS })
    expect(ids).not.toContain('media-providers')
    expect(ids).toContain('proxy-pools')
  })

  it('keeps an accordion that still has one visible child', () => {
    const groups = visibleNavGroups({ hidden: MEDIA_CHILD_IDS.filter((id) => id !== 'media-video') })
    const accordion = groups
      .flatMap((group) => group.entries)
      .find((entry) => entry.kind === 'accordion')
    expect(accordion?.kind).toBe('accordion')
    expect(accordion?.kind === 'accordion' && accordion.children.map((c) => c.id)).toEqual(['media-video'])
  })

  it('ignores ids it cannot address and lower-cases the rest', () => {
    expect(normalizeHiddenNav({ hidden: ['QUOTA', 'not-an-entry', 7, 'quota'] })).toEqual(['quota'])
    expect(normalizeHiddenNav({ hidden: 'quota' })).toEqual([...DEFAULT_HIDDEN_NAV_IDS])
    expect(renderedIds({ hidden: ['not-an-entry'] })).toEqual(NAV_ORDER)
  })
})

describe('stored hide list', () => {
  it('stores ids in render order', () => {
    const first = setNavVisible({ hidden: [] }, 'security', false)
    expect(first).toEqual(['security'])
    expect(setNavVisible(first, 'quota', false)).toEqual(['quota', 'security'])
    expect(setNavVisible(['quota', 'security'], 'quota', true)).toEqual(['security'])
  })

  it('starts from the defaults when nothing was saved', () => {
    expect(setNavVisible(undefined, 'plans', true)).toEqual([])
    expect(setNavVisible(undefined, 'quota', false)).toEqual(['quota', 'plans'])
  })

  it('survives a junk payload without losing a valid entry', () => {
    expect(setNavVisible({ hidden: ['quota', 'bogus'] }, 'media-tts', false)).toEqual([
      'quota',
      'media-tts',
    ])
  })
})

describe('route guard', () => {
  it('follows the aliases the sidebar keeps lit', () => {
    expect(isNavHidden('keys', { hidden: ['endpoint'] })).toBe(true)
    expect(isNavHidden('terminal', { hidden: ['console-log'] })).toBe(true)
    expect(isNavHidden('usage-cache', { hidden: ['analytics'] })).toBe(true)
    expect(isNavHidden('usage-compression', { hidden: ['analytics'] })).toBe(true)
  })

  it('treats a hidden accordion as hiding its children', () => {
    expect(isNavHidden('media-video', { hidden: ['media-providers'] })).toBe(true)
    expect(isNavHidden('media-video', { hidden: ['media-tts'] })).toBe(false)
  })

  it('never blocks the login page or an entry nobody hid', () => {
    expect(isNavHidden('login', { hidden: ['login'] })).toBe(false)
    expect(isNavHidden('quota', { hidden: [] })).toBe(false)
    expect(isNavHidden('endpoint', undefined)).toBe(false)
  })
})

describe('active highlighting', () => {
  it('keeps the aliased entries lit', () => {
    expect(isNavLinkActive('endpoint', 'keys')).toBe(true)
    expect(isNavLinkActive('console-log', 'terminal')).toBe(true)
    expect(isNavLinkActive('analytics', 'usage-cache')).toBe(true)
    expect(isNavLinkActive('analytics', 'usage-compression')).toBe(true)
    expect(isNavLinkActive('quota', 'analytics')).toBe(false)
  })
})
