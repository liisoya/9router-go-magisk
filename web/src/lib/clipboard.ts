/**
 * Copies text to the clipboard with fallback for non-secure contexts (HTTP / LAN IP).
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  if (typeof window !== 'undefined' && window.isSecureContext && navigator?.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return true
    } catch {
      // Fall through to fallback
    }
  }

  if (typeof document === 'undefined') {
    return false
  }

  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.style.position = 'fixed'
  textarea.style.left = '-9999px'
  textarea.style.top = '-9999px'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)
  textarea.focus()
  textarea.select()

  let successful = false
  try {
    successful = document.execCommand('copy')
  } catch {
    successful = false
  } finally {
    document.body.removeChild(textarea)
  }

  return successful
}
