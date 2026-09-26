<script lang="ts">
  import { api, type SystemVersionInfo } from '../api/client'
  import { TAB_ROUTES, type ActiveTab } from '../lib/router'

  export type { ActiveTab }

  let {
    activeTab = $bindable('endpoint'),
    navigate = (tab: ActiveTab) => {
      activeTab = tab
    },
    activeConnections = 0,
    totalConnections = 0,
    onClose,
  }: {
    activeTab: ActiveTab
    navigate?: (tab: ActiveTab, replace?: boolean) => void
    activeConnections?: number
    totalConnections?: number
    onClose?: () => void
  } = $props()

  let version = $state('')
  let updateInfo = $state<SystemVersionInfo | null>(null)
  let showUpdateModal = $state(false)
  let isUpdating = $state(false)
  let updateStatus = $state<'idle' | 'updating' | 'success' | 'error'>('idle')
  let updateMsg = $state('')
  let copied = $state(false)
  let shutdownCountdown = $state(0)
  let isDisconnected = $state(false)

  const INSTALL_CMD = '9router-go update'

  // Media providers accordion (collapsed by default)
  let isMediaOpen = $state(false)

  $effect(() => {
    api
      .getSystemVersion()
      .then((v) => {
        if (v) {
          version = v.currentVersion || ''
          if (v.hasUpdate) {
            updateInfo = v
          }
        }
      })
      .catch(() => {})

    const timer = setTimeout(() => {
      api
        .checkUpdate()
        .then((v) => {
          if (v) {
            version = v.currentVersion || version
            if (v.hasUpdate) {
              updateInfo = v
            }
          }
        })
        .catch(() => {})
    }, 2500)

    return () => clearTimeout(timer)
  })

  async function copyInstallCmd() {
    try {
      await navigator.clipboard.writeText(INSTALL_CMD)
    } catch {}
    copied = true
    setTimeout(() => {
      copied = false
    }, 2000)
  }

  async function handleAutoUpdate() {
    isUpdating = true
    updateStatus = 'updating'
    updateMsg = 'Downloading and applying binary update...'
    try {
      const res = await api.triggerUpdate()
      updateStatus = 'success'
      updateMsg = res?.message || 'Update installed successfully. Process is restarting...'
      setTimeout(() => {
        globalThis.location.reload()
      }, 3500)
    } catch (err: any) {
      updateStatus = 'error'
      updateMsg = err?.message || 'Auto update failed. Please run update command manually.'
      isUpdating = false
    }
  }

  async function handleCopyAndShutdown() {
    await copyInstallCmd()
    let remaining = 5
    shutdownCountdown = remaining
    const timer = setInterval(() => {
      remaining -= 1
      shutdownCountdown = remaining
      if (remaining <= 0) {
        clearInterval(timer)
        api.shutdownServer().catch(() => {})
        isDisconnected = true
      }
    }, 1000)
  }

  function handleNav(tab: ActiveTab, e?: MouseEvent) {
    if (e) {
      if (e.ctrlKey || e.metaKey || e.shiftKey || e.altKey || e.button !== 0) return
      e.preventDefault()
    }
    navigate(tab)
    onClose?.()
  }

  const mainNavLinks = [
    { tab: 'endpoint' as ActiveTab, label: 'Endpoint & Key', icon: 'api' },
    { tab: 'connections' as ActiveTab, label: 'Providers', icon: 'dns' },
    { tab: 'combos' as ActiveTab, label: 'Combo & Vision Adapter', icon: 'layers' },
    { tab: 'analytics' as ActiveTab, label: 'Usage', icon: 'bar_chart' },
    { tab: 'quota' as ActiveTab, label: 'Quota Tracker', icon: 'data_usage' },
    { tab: 'token-saver' as ActiveTab, label: 'Token Saver', icon: 'savings' },
    { tab: 'cli-tools' as ActiveTab, label: 'CLI Tools', icon: 'terminal' },
  ] as const

  const mediaNavLinks = [
    { tab: 'media-embedding' as ActiveTab, label: 'Embedding', icon: 'data_array' },
    { tab: 'media-image' as ActiveTab, label: 'Text to Image', icon: 'brush' },
    { tab: 'media-tts' as ActiveTab, label: 'Text To Speech', icon: 'record_voice_over' },
    { tab: 'media-stt' as ActiveTab, label: 'Speech To Text', icon: 'mic' },
    { tab: 'media-video' as ActiveTab, label: 'Video', icon: 'movie' },
    { tab: 'media-systemone' as ActiveTab, label: 'System One', icon: 'psychology' },
    { tab: 'media-web' as ActiveTab, label: 'Web Fetch & Search', icon: 'travel_explore' },
  ] as const

  const systemNavLinks = [
    { tab: 'proxy-pools' as ActiveTab, label: 'Proxy Pools', icon: 'lan' },
    { tab: 'skills' as ActiveTab, label: 'Skills', icon: 'extension' },
    { tab: 'console-log' as ActiveTab, label: 'Console Log', icon: 'terminal' },
  ] as const

  function isLinkActive(tab: ActiveTab): boolean {
    if (tab === 'endpoint') {
      return activeTab === 'endpoint' || activeTab === 'keys'
    }
    if (tab === 'console-log') {
      return activeTab === 'console-log' || activeTab === 'terminal'
    }
    return activeTab === tab
  }
</script>

<aside
  class="flex h-full max-h-screen w-72 flex-col overflow-hidden border-r border-border-subtle bg-sidebar backdrop-blur-xl transition-colors duration-300 flex-shrink-0 select-none z-30"
>
  <!-- Window control / traffic lights -->
  <div class="flex items-center gap-2 px-6 pt-5 pb-2 shrink-0">
    <div class="w-3 h-3 rounded-full bg-[#FF5F56]"></div>
    <div class="w-3 h-3 rounded-full bg-[#FFBD2E]"></div>
    <div class="w-3 h-3 rounded-full bg-[#27C93F]"></div>
  </div>

  <!-- Brand header: 9router-go with official favicon.svg logo -->
  <div class="px-6 py-4 flex flex-col gap-2 shrink-0">
    <a
      href={TAB_ROUTES.endpoint}
      onclick={(e) => handleNav('endpoint', e)}
      class="flex items-center gap-3 cursor-pointer group"
    >
      <div
        class="flex items-center justify-center size-9 rounded-[10px] bg-surface-2 border border-border-subtle shadow-[var(--shadow-warm)] flex-shrink-0 group-hover:scale-105 transition-transform overflow-hidden p-1.5"
      >
        <img
          src="/favicon.svg"
          alt="9router-go"
          class="w-full h-full object-contain"
        />
      </div>
      <div class="flex flex-col min-w-0">
        <h1 class="text-lg font-semibold tracking-tight text-text-main truncate leading-snug">
          9router-go
        </h1>
        <span class="text-xs text-text-muted leading-tight">
          {version ? `v${version}` : 'v1.9.1'}
        </span>
      </div>
    </a>

    <!-- Update notification banner below version (matches upstream 9router) -->
    {#if updateInfo && updateInfo.hasUpdate}
      <div
        class="flex flex-col gap-1.5 rounded-lg p-2 bg-green-500/10 dark:bg-amber-500/10 border border-green-500/30 dark:border-amber-500/30 text-green-700 dark:text-amber-400 mt-0.5 animate-in fade-in duration-200"
      >
        <span class="text-[11px] font-semibold text-green-700 dark:text-amber-400 flex items-center gap-1">
          <span class="material-symbols-outlined text-[14px]">arrow_upward</span>
          <span class="truncate">New version available: v{updateInfo.latestVersion}</span>
        </span>
        <div class="flex items-center gap-1.5">
          <button
            type="button"
            onclick={() => (showUpdateModal = true)}
            class="px-2 py-0.5 rounded bg-green-600 hover:bg-green-700 dark:bg-amber-500 dark:hover:bg-amber-600 text-white text-[11px] font-semibold transition-colors cursor-pointer shrink-0"
          >
            Update now
          </button>
          <button
            type="button"
            onclick={copyInstallCmd}
            title="Copy install command"
            class="flex-1 text-left hover:opacity-80 transition-opacity cursor-pointer min-w-0"
          >
            <code class="block text-[10px] text-green-700 dark:text-amber-300 font-mono truncate bg-surface/70 px-1 py-0.5 rounded">
              {copied ? '✓ copied!' : INSTALL_CMD}
            </code>
          </button>
        </div>
      </div>
    {/if}
  </div>

  <!-- Navigation -->
  <nav class="flex-1 min-h-0 px-4 py-2 space-y-0.5 overflow-y-auto custom-scrollbar">
    <!-- 1-7 Main navigation links -->
    {#each mainNavLinks as item (item.tab)}
      {@const active = isLinkActive(item.tab)}
      <a
        href={TAB_ROUTES[item.tab]}
        onclick={(e) => handleNav(item.tab, e)}
        class="flex items-center gap-3 px-3 py-1.5 rounded-lg transition-all group cursor-pointer {active
          ? 'bg-primary/10 text-primary font-medium'
          : 'text-text-muted hover:bg-surface-2 hover:text-text-main'}"
      >
        <span
          class="material-symbols-outlined text-[18px] {active
            ? 'fill-1'
            : 'group-hover:text-primary transition-colors'}"
        >
          {item.icon}
        </span>
        <span class="text-[13px]">{item.label}</span>
      </a>
    {/each}

    <!-- System section header -->
    <div class="pt-3 mt-2 space-y-0.5">
      <p class="px-3 text-xs font-semibold text-text-muted/60 uppercase tracking-wider mb-2">
        System
      </p>

      <!-- 8. Media Providers accordion -->
      <button
        type="button"
        onclick={() => (isMediaOpen = !isMediaOpen)}
        class="w-full flex items-center gap-3 px-3 py-1.5 rounded-lg transition-all group cursor-pointer {activeTab.startsWith(
          'media-'
        )
          ? 'bg-primary/10 text-primary font-medium'
          : 'text-text-muted hover:bg-surface-2 hover:text-text-main'}"
      >
        <span class="material-symbols-outlined text-[18px]">perm_media</span>
        <span class="text-[13px] flex-1 text-left">Media Providers</span>
        <span
          class="material-symbols-outlined text-[14px] transition-transform duration-200"
          style:transform={isMediaOpen ? 'rotate(180deg)' : 'rotate(0deg)'}
        >
          expand_more
        </span>
      </button>

      {#if isMediaOpen}
        <div class="pl-4 space-y-0.5">
          {#each mediaNavLinks as item (item.tab)}
            {@const active = isLinkActive(item.tab)}
            <a
              href={TAB_ROUTES[item.tab]}
              onclick={(e) => handleNav(item.tab, e)}
              class="flex items-center gap-3 px-3 py-1.5 rounded-lg transition-all group cursor-pointer {active
                ? 'bg-primary/10 text-primary font-medium'
                : 'text-text-muted hover:bg-surface-2 hover:text-text-main'}"
            >
              <span
                class="material-symbols-outlined text-[16px] {active
                  ? 'fill-1'
                  : 'group-hover:text-primary transition-colors'}"
              >
                {item.icon}
              </span>
              <span class="text-[13px]">{item.label}</span>
            </a>
          {/each}
        </div>
      {/if}

      <!-- 9-11 System links: Proxy Pools, Skills, Console Log -->
      {#each systemNavLinks as item (item.tab)}
        {@const active = isLinkActive(item.tab)}
        <a
          href={TAB_ROUTES[item.tab]}
          onclick={(e) => handleNav(item.tab, e)}
          class="flex items-center gap-3 px-3 py-1.5 rounded-lg transition-all group cursor-pointer {active
            ? 'bg-primary/10 text-primary font-medium'
            : 'text-text-muted hover:bg-surface-2 hover:text-text-main'}"
        >
          <span
            class="material-symbols-outlined text-[18px] {active
              ? 'fill-1'
              : 'group-hover:text-primary transition-colors'}"
          >
            {item.icon}
          </span>
          <span class="text-[13px]">{item.label}</span>
        </a>
      {/each}

      <!-- 12. Settings -->
      <a
        href={TAB_ROUTES.settings}
        onclick={(e) => handleNav('settings', e)}
        class="flex items-center gap-3 px-3 py-1.5 rounded-lg transition-all group cursor-pointer {isLinkActive('settings')
          ? 'bg-primary/10 text-primary font-medium'
          : 'text-text-muted hover:bg-surface-2 hover:text-text-main'}"
      >
        <span
          class="material-symbols-outlined text-[18px] {isLinkActive('settings')
            ? 'fill-1'
            : 'group-hover:text-primary transition-colors'}"
        >
          settings
        </span>
        <span class="text-[13px]">Settings</span>
      </a>
    </div>
  </nav>

  <!-- Bottom connection summary -->
  <div class="p-4 border-t border-border-subtle shrink-0">
    <div
      class="p-2.5 rounded-[10px] bg-surface border border-border-subtle flex items-center justify-between"
    >
      <div class="min-w-0">
        <p class="text-[11px] text-text-muted uppercase tracking-wide">Providers</p>
        <p class="text-xs font-semibold text-text-main mt-0.5 truncate">
          {activeConnections} <span class="text-text-muted font-normal">/ {totalConnections} active</span>
        </p>
      </div>
      <span class="material-symbols-outlined text-primary text-[18px]">radio_button_checked</span>
    </div>
  </div>
</aside>

<!-- Update Modal -->
{#if showUpdateModal}
  <div class="fixed inset-0 z-50 flex items-center justify-center p-4">
    <div
      class="absolute inset-0 bg-black/50 backdrop-blur-sm"
      onclick={() => (showUpdateModal = false)}
      onkeydown={(e) => e.key === 'Escape' && (showUpdateModal = false)}
      role="button"
      tabindex="-1"
      aria-label="Close background"
    ></div>

    <div
      class="relative w-full max-w-lg bg-surface border border-border-subtle rounded-2xl shadow-2xl p-6 flex flex-col gap-4 z-10 animate-in fade-in zoom-in-95"
    >
      <div class="flex items-center justify-between pb-3 border-b border-border-subtle">
        <div class="flex items-center gap-2.5">
          <div class="size-9 rounded-full flex items-center justify-center bg-amber-500/10 text-amber-500">
            <span class="material-symbols-outlined text-[20px]">upgrade</span>
          </div>
          <div>
            <h2 class="text-base font-semibold text-text-main">
              Update 9router-go{updateInfo?.latestVersion ? ` to v${updateInfo.latestVersion}` : ''}
            </h2>
            <p class="text-xs text-text-muted">
              Current version: v{version || '1.9.1'}
            </p>
          </div>
        </div>
        <button
          type="button"
          onclick={() => (showUpdateModal = false)}
          class="p-1 rounded-lg text-text-muted hover:text-text-main hover:bg-surface-2 transition-colors cursor-pointer"
          aria-label="Close"
        >
          <span class="material-symbols-outlined text-[20px]">close</span>
        </button>
      </div>

      {#if updateInfo?.releaseNotes}
        <div class="p-3 rounded-lg bg-surface-2 border border-border-subtle text-xs text-text-muted leading-relaxed">
          <p class="font-medium text-text-main mb-1">Release Notes:</p>
          <p class="whitespace-pre-line">{updateInfo.releaseNotes}</p>
        </div>
      {/if}

      <!-- Terminal Command Box -->
      <div class="flex flex-col gap-1.5">
        <label class="text-xs font-medium text-text-muted">Terminal Command</label>
        <div class="flex items-center gap-2 p-2.5 rounded-xl bg-surface-2 border border-border-subtle">
          <code class="text-xs font-mono text-amber-600 dark:text-amber-400 flex-1 truncate select-all">
            {INSTALL_CMD}
          </code>
          <button
            type="button"
            onclick={copyInstallCmd}
            class="px-2.5 py-1 text-xs rounded-lg bg-surface hover:bg-surface-3 border border-border-subtle text-text-main transition-colors cursor-pointer shrink-0 flex items-center gap-1"
          >
            <span class="material-symbols-outlined text-[14px]">content_copy</span>
            <span>{copied ? 'Copied!' : 'Copy'}</span>
          </button>
        </div>
      </div>

      {#if updateStatus === 'updating'}
        <div class="p-3 rounded-xl bg-blue-500/10 border border-blue-500/20 text-blue-600 dark:text-blue-400 flex items-center gap-2 text-xs">
          <span class="material-symbols-outlined animate-spin text-[18px]">progress_activity</span>
          <span>{updateMsg}</span>
        </div>
      {:else if updateStatus === 'success'}
        <div class="p-3 rounded-xl bg-green-500/10 border border-green-500/20 text-green-600 dark:text-green-400 flex items-center gap-2 text-xs">
          <span class="material-symbols-outlined text-[18px]">check_circle</span>
          <span>{updateMsg}</span>
        </div>
      {:else if updateStatus === 'error'}
        <div class="p-3 rounded-xl bg-red-500/10 border border-red-500/20 text-red-600 dark:text-red-400 flex items-center gap-2 text-xs">
          <span class="material-symbols-outlined text-[18px]">error</span>
          <span>{updateMsg}</span>
        </div>
      {/if}

      <!-- Action buttons -->
      <div class="flex items-center justify-end gap-2 pt-2 border-t border-border-subtle">
        <button
          type="button"
          onclick={() => (showUpdateModal = false)}
          disabled={isUpdating}
          class="px-3.5 py-2 text-xs font-medium rounded-lg text-text-muted hover:text-text-main hover:bg-surface-2 transition-colors cursor-pointer"
        >
          Cancel
        </button>
        <button
          type="button"
          onclick={handleCopyAndShutdown}
          disabled={isUpdating || shutdownCountdown > 0}
          class="px-3.5 py-2 text-xs font-medium rounded-lg border border-border-subtle text-text-main hover:bg-surface-2 transition-colors cursor-pointer"
        >
          {shutdownCountdown > 0 ? `Stopping in ${shutdownCountdown}s...` : 'Copy & Shutdown'}
        </button>
        <button
          type="button"
          onclick={handleAutoUpdate}
          disabled={isUpdating}
          class="px-4 py-2 text-xs font-semibold rounded-lg bg-primary hover:bg-primary-hover text-white transition-colors cursor-pointer flex items-center gap-1.5 shadow-sm"
        >
          <span class="material-symbols-outlined text-[16px]">autorenew</span>
          <span>{isUpdating ? 'Updating...' : 'Auto Update'}</span>
        </button>
      </div>
    </div>
  </div>
{/if}

<!-- Disconnected Overlay -->
{#if isDisconnected}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm p-6">
    <div class="text-center p-8 bg-surface border border-border-subtle rounded-2xl shadow-2xl max-w-sm w-full animate-in fade-in">
      <div class="flex items-center justify-center size-14 rounded-full bg-red-500/20 text-red-500 mx-auto mb-4">
        <span class="material-symbols-outlined text-[28px]">power_off</span>
      </div>
      <h2 class="text-lg font-semibold text-text-main mb-1">Server Stopped</h2>
      <p class="text-xs text-text-muted mb-4">
        Now run <code class="px-1.5 py-0.5 rounded bg-surface-2 font-mono text-amber-500">9router-go update</code> in your terminal.
      </p>
      <button
        type="button"
        onclick={() => globalThis.location.reload()}
        class="w-full py-2 px-4 rounded-lg bg-primary hover:bg-primary-hover text-white text-xs font-semibold transition-colors cursor-pointer"
      >
        Reload Dashboard
      </button>
    </div>
  </div>
{/if}

