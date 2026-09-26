// PWA Service Worker & Install Prompt helper
export interface BeforeInstallPromptEvent extends Event {
  readonly platforms: string[]
  readonly userChoice: Promise<{
    outcome: 'accepted' | 'dismissed'
    platform: string
  }>
  prompt(): Promise<void>
}

let deferredPrompt: BeforeInstallPromptEvent | null = null
const listeners: Array<(canInstall: boolean) => void> = []

export function isStandalone(): boolean {
  if (typeof window === 'undefined') return false
  return (
    window.matchMedia('(display-mode: standalone)').matches ||
    Boolean((navigator as unknown as { standalone?: boolean }).standalone) ||
    document.referrer.includes('android-app://')
  )
}

export function registerServiceWorker() {
  if (typeof window === 'undefined') return

  if ('serviceWorker' in navigator) {
    window.addEventListener('load', () => {
      navigator.serviceWorker
        .register('/sw.js')
        .then((reg) => {
          // Listen for updates
          reg.onupdatefound = () => {
            const installingWorker = reg.installing
            if (installingWorker) {
              installingWorker.onstatechange = () => {
                if (installingWorker.state === 'installed' && navigator.serviceWorker.controller) {
                  // New content is available once old tabs close
                }
              }
            }
          }
        })
        .catch((err) => {
          console.warn('PWA service worker registration failed:', err)
        })
    })
  }

  window.addEventListener('beforeinstallprompt', (e) => {
    e.preventDefault()
    deferredPrompt = e as BeforeInstallPromptEvent
    notify(true)
  })

  window.addEventListener('appinstalled', () => {
    deferredPrompt = null
    notify(false)
  })
}

function notify(canInstall: boolean) {
  for (const fn of listeners) {
    fn(canInstall)
  }
}

export function subscribeInstallPrompt(callback: (canInstall: boolean) => void): () => void {
  listeners.push(callback)
  callback(deferredPrompt !== null && !isStandalone())
  return () => {
    const idx = listeners.indexOf(callback)
    if (idx !== -1) {
      listeners.splice(idx, 1)
    }
  }
}

export async function promptInstall(): Promise<boolean> {
  if (!deferredPrompt) return false
  try {
    await deferredPrompt.prompt()
    const choice = await deferredPrompt.userChoice
    if (choice.outcome === 'accepted') {
      deferredPrompt = null
      notify(false)
      return true
    }
  } catch (err) {
    console.error('Error prompting install:', err)
  }
  return false
}
