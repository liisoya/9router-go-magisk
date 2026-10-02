// Reset-credit presentation helpers, ported from OmniRoute's
// src/app/(dashboard)/dashboard/usage/components/ProviderLimits/
// CodexResetCreditsModal.tsx. Kept as pure functions so they are unit
// testable — the component holds only the state machine.

export interface CodexResetCreditView {
  selectionToken: string
  resetType?: string
  status?: string
  grantedAt?: string
  expiresAt?: string
  title?: string
  description?: string
}

// formatRelativeExpiry renders how long a credit is still valid, coarsened to
// the unit that fits. A credit already past its expiry reads "0m" rather
// than a negative countdown.
export function formatRelativeExpiry(
  expiresAt: string | null | undefined,
  now: number = Date.now(),
): string | null {
  if (!expiresAt) return null
  const diffMs = new Date(expiresAt).getTime() - now
  if (!Number.isFinite(diffMs)) return null
  if (diffMs <= 0) return '0m'

  const minutes = Math.max(1, Math.ceil(diffMs / 60_000))
  if (minutes < 60) return `${minutes}m`
  const hours = Math.ceil(minutes / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.ceil(hours / 24)}d`
}

// getResetCreditExpiryLabel pairs the relative countdown with the absolute
// timestamp, so the user can judge "in 2d" against a specific date.
export function getCodexResetCreditExpiryLabel(
  expiresAt: string | null | undefined,
  locale?: string,
  now: number = Date.now(),
): { absolute: string | null; relative: string | null } {
  if (!expiresAt) return { absolute: null, relative: null }
  const date = new Date(expiresAt)
  if (!Number.isFinite(date.getTime())) return { absolute: null, relative: null }
  return {
    absolute: date.toLocaleString(locale, { dateStyle: 'medium', timeStyle: 'short' }),
    relative: formatRelativeExpiry(expiresAt, now),
  }
}

// getResetCreditWindowTitle names a credit, preferring what Codex gave it.
export function getResetCreditWindowTitle(credit: CodexResetCreditView): string {
  if (credit.title) return credit.title
  if (credit.resetType) return credit.resetType.replace(/[_-]+/g, ' ')
  return 'Full reset'
}

// getResetCreditConfirmation is the irreversibility warning shown before a
// redeem. Redeeming spends the credit for good, so this must say so.
export function getResetCreditConfirmation(credit: CodexResetCreditView): string {
  const title = getResetCreditWindowTitle(credit)
  return `Redeeming immediately resets the eligible Codex usage windows and permanently consumes this credit (${title}). It cannot be undone.`
}

// newResetCreditIdempotencyKey mints the key that makes a redeem idempotent
// upstream: the same key sent twice redeems once. An empty key is not "no
// key" — the server replaces it with a freshly minted one per request, which
// is exactly the double-spend this exists to prevent — so the caller must never
// dispatch a consume without a real value.
export function newResetCreditIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) return crypto.randomUUID()
  return `reset-${Date.now()}-${Math.random().toString(16).slice(2)}`
}
