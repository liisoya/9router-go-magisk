// Usage detail panels (Activity Heatmap, Node Health) start collapsed; the
// Settings > Display switch flips that default. Each panel can still be opened
// on its own for the current view, so this decides the starting state only.
// Mirrors lib/compactNumber.ts: a localStorage preference, no server round trip.
const DETAILS_EXPANDED_KEY = 'detailsExpandedByDefault'

export function isDetailsExpandedByDefault(): boolean {
  if (typeof localStorage === 'undefined') return false
  return localStorage.getItem(DETAILS_EXPANDED_KEY) === '1'
}

export function setDetailsExpandedByDefault(on: boolean): void {
  if (typeof localStorage === 'undefined') return
  localStorage.setItem(DETAILS_EXPANDED_KEY, on ? '1' : '0')
}

// The Overview's two optional panels (Combo flow, Usage trend) are visible by
// default; the Settings > Display switches below hide the whole panel from the
// section. Visibility, not the collapsed state: a hidden panel is not rendered
// and the preference survives the visit.
const COMBO_FLOW_KEY = 'showComboFlow'
const USAGE_TREND_KEY = 'showUsageTrend'

export function isComboFlowVisible(): boolean {
  if (typeof localStorage === 'undefined') return true
  return localStorage.getItem(COMBO_FLOW_KEY) !== '0'
}

export function setComboFlowVisible(on: boolean): void {
  if (typeof localStorage === 'undefined') return
  localStorage.setItem(COMBO_FLOW_KEY, on ? '1' : '0')
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent('display-prefs-changed'))
  }
}

export function isUsageTrendVisible(): boolean {
  if (typeof localStorage === 'undefined') return true
  return localStorage.getItem(USAGE_TREND_KEY) !== '0'
}

export function setUsageTrendVisible(on: boolean): void {
  if (typeof localStorage === 'undefined') return
  localStorage.setItem(USAGE_TREND_KEY, on ? '1' : '0')
  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent('display-prefs-changed'))
  }
}
