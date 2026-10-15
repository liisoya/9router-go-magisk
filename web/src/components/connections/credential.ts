import { api, type ProviderConnection } from '../../api/client'

/**
 * Credential rotation for the Edit Connection modal (issue #154).
 *
 * The stored key never reaches the browser: the server ships a mask
 * (`apiKeyMasked`) instead, and the modal doubles it as the input's
 * placeholder, so an empty field honestly means "keep the key already stored".
 * Upstream parity: decolua/9router EditConnectionModal (same rule, same
 * "leave blank" hint).
 */

/** Placeholder showing the masked key on file, so the user can confirm it. */
export function credentialPlaceholder(conn: Pick<ProviderConnection, 'apiKeyMasked'>): string {
  const masked = conn.apiKeyMasked
  return typeof masked === 'string' && masked ? masked : 'Enter new API key'
}

/**
 * The update fragment a typed key implies, or null when nothing was typed.
 * Whitespace is not a key: a field the user opened and closed again must not
 * wipe the stored credential.
 */
export function credentialUpdate(typed: string): { apiKey: string } | null {
  const apiKey = typed.trim()
  return apiKey ? { apiKey } : null
}

export type CredentialCheck = 'valid' | 'invalid' | 'unsupported' | null

export interface CredentialProbe {
  check: CredentialCheck
  error: string | null
}

/**
 * Probes a candidate key against the provider before it may replace a working
 * one: a rejected key would take the account out of rotation, and the
 * provider's answer is the only evidence against that. An unsupported probe
 * is not a verdict and never blocks the save.
 */
export async function probeReplacementKey(
  conn: Pick<ProviderConnection, 'provider' | 'providerSpecificData'>,
  typed: string
): Promise<CredentialProbe> {
  try {
    const res = await api.validateProvider({
      provider: conn.provider,
      apiKey: typed,
      ...(conn.providerSpecificData ? { providerSpecificData: conn.providerSpecificData } : {})
    })
    const check = credentialCheckState(res)
    return { check, error: check === 'invalid' ? (res.error ?? 'The provider rejected this key.') : null }
  } catch (err) {
    return { check: 'invalid', error: err instanceof Error ? err.message : 'Could not check the new key.' }
  }
}

/** The verdict a raw provider probe answered with, as the modal shows it. */
export function credentialCheckState(result: { supported?: boolean; valid?: boolean }): CredentialCheck {
  if (result.supported === false) return 'unsupported'
  return result.valid ? 'valid' : 'invalid'
}