<script lang="ts">
  import { onMount } from 'svelte'
  import {
    AlertCircle,
    Check,
    Database,
    Download,
    Eye,
    EyeOff,
    Globe,
    Key,
    Laptop,
    Loader2,
    Lock,
    RefreshCw,
    Save,
    Shield,
    Sliders,
    Upload,
    User,
    Zap
  } from 'lucide-svelte'
  import Card from '../lib/ui/Card.svelte'
  import Input from '../lib/ui/Input.svelte'
  import Modal from '../lib/ui/Modal.svelte'
  import Toggle from '../lib/ui/Toggle.svelte'
  import { api, type Settings } from '../api/client'
  import {
    backupFileName,
    buildExportRequest,
    buildImportRequest,
    downloadJSON,
    responseErrorMessage
  } from '../lib/db-backup'

  interface Props {
    settings?: Settings
    onRefresh?: () => void
  }

  let {
    settings = {},
    onRefresh
  }: Props = $props()

  // General Settings State
  let requireLogin = $state(false)
  let sessionTimeout = $state('24h')
  let selectedLanguage = $state('en')
  let enableObservability = $state(false)

  // Routing Strategy State
  let fallbackStrategy = $state('failover')
  let stickyRoundRobinLimit = $state(3)
  let comboStrategy = $state('first-model')

  // Password Management State
  let currentPassword = $state('')
  let newPassword = $state('')
  let confirmNewPassword = $state('')
  let isUpdatingPassword = $state(false)
  let passwordSuccessMessage = $state<string | null>(null)
  let passwordErrorMessage = $state<string | null>(null)
  let showPasswordFields = $state(false)

  // SSO State
  let authMode = $state<'password' | 'oidc' | 'saml'>('password')
  let oidcIssuerUrl = $state('')
  let oidcClientId = $state('')
  let oidcScopes = $state('openid profile email')
  let oidcLoginLabel = $state('Sign in with OIDC')

  let samlEntryPoint = $state('')
  let samlIssuer = $state('')
  let samlCert = $state('')
  let samlLoginLabel = $state('Sign in with SAML SSO')

  // Save states
  let isSavingSettings = $state(false)
  let saveSuccess = $state(false)

  // Database Backup / Import
  let isDownloadingBackup = $state(false)
  let isImportingBackup = $state(false)
  let fileInput: HTMLInputElement | null = $state(null)
  // 下载/导入前必须输密码：服务端要求 x-9r-password 头（上游 parity，见 lib/db-backup.ts）
  let dbAuth = $state({ open: false, mode: '' as '' | 'export' | 'import', password: '' })
  let pendingImportFile = $state<File | null>(null)

  $effect(() => {
    if (settings) {
      requireLogin = !!settings.requireLogin
      enableObservability = !!settings.enableObservability
      if (typeof settings.sessionTimeout === 'string') sessionTimeout = settings.sessionTimeout
      if (typeof settings.language === 'string') selectedLanguage = settings.language
      if (typeof settings.fallbackStrategy === 'string') fallbackStrategy = settings.fallbackStrategy
      if (typeof settings.stickyRoundRobinLimit === 'number') stickyRoundRobinLimit = settings.stickyRoundRobinLimit
      if (typeof settings.comboStrategy === 'string') comboStrategy = settings.comboStrategy

      // SSO
      if (settings.authMode === 'oidc' || settings.authMode === 'saml') authMode = settings.authMode
      if (typeof settings.oidcIssuerUrl === 'string') oidcIssuerUrl = settings.oidcIssuerUrl
      if (typeof settings.oidcClientId === 'string') oidcClientId = settings.oidcClientId
      if (typeof settings.oidcScopes === 'string') oidcScopes = settings.oidcScopes
      if (typeof settings.oidcLoginLabel === 'string') oidcLoginLabel = settings.oidcLoginLabel

      if (typeof settings.samlEntryPoint === 'string') samlEntryPoint = settings.samlEntryPoint
      if (typeof settings.samlIssuer === 'string') samlIssuer = settings.samlIssuer
      if (typeof settings.samlCert === 'string') samlCert = settings.samlCert
      if (typeof settings.samlLoginLabel === 'string') samlLoginLabel = settings.samlLoginLabel
    }
  })

  async function loadSettings() {
    try {
      const s = await api.getSettings()
      if (s) {
        requireLogin = !!s.requireLogin
        enableObservability = !!s.enableObservability
        if (s.sessionTimeout) sessionTimeout = String(s.sessionTimeout)
        if (s.language) selectedLanguage = String(s.language)
        if (s.fallbackStrategy) fallbackStrategy = String(s.fallbackStrategy)
        if (typeof s.stickyRoundRobinLimit === 'number') stickyRoundRobinLimit = s.stickyRoundRobinLimit
        if (s.comboStrategy) comboStrategy = String(s.comboStrategy)

        if (s.authMode === 'oidc' || s.authMode === 'saml') authMode = s.authMode
        if (s.oidcIssuerUrl) oidcIssuerUrl = String(s.oidcIssuerUrl)
        if (s.oidcClientId) oidcClientId = String(s.oidcClientId)
        if (s.oidcScopes) oidcScopes = String(s.oidcScopes)
        if (s.oidcLoginLabel) oidcLoginLabel = String(s.oidcLoginLabel)

        if (s.samlEntryPoint) samlEntryPoint = String(s.samlEntryPoint)
        if (s.samlIssuer) samlIssuer = String(s.samlIssuer)
        if (s.samlCert) samlCert = String(s.samlCert)
        if (s.samlLoginLabel) samlLoginLabel = String(s.samlLoginLabel)
      }
    } catch {
      // silent
    }
  }

  onMount(() => {
    loadSettings()
  })

  async function handleSaveAll() {
    isSavingSettings = true
    saveSuccess = false
    try {
      await api.updateSettings({
        requireLogin,
        sessionTimeout,
        language: selectedLanguage,
        enableObservability,
        fallbackStrategy,
        stickyRoundRobinLimit,
        comboStrategy,
        authMode,
        oidcIssuerUrl,
        oidcClientId,
        oidcScopes,
        oidcLoginLabel,
        samlEntryPoint,
        samlIssuer,
        samlCert,
        samlLoginLabel,
      })
      saveSuccess = true
      onRefresh?.()
      setTimeout(() => (saveSuccess = false), 3000)
    } catch (err) {
      alert(`Failed to save settings: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      isSavingSettings = false
    }
  }

  async function handleUpdatePassword(e: SubmitEvent) {
    e.preventDefault()
    passwordErrorMessage = null
    passwordSuccessMessage = null

    if (newPassword !== confirmNewPassword) {
      passwordErrorMessage = 'Passwords do not match'
      return
    }
    if (newPassword.length < 6) {
      passwordErrorMessage = 'Password must be at least 6 characters'
      return
    }

    isUpdatingPassword = true
    try {
      await api.updateSettings({
        currentPassword,
        newPassword,
      })
      passwordSuccessMessage = 'Password updated successfully!'
      currentPassword = ''
      newPassword = ''
      confirmNewPassword = ''
      showPasswordFields = false
      onRefresh?.()
    } catch (err) {
      passwordErrorMessage = err instanceof Error ? err.message : 'Failed to update password'
    } finally {
      isUpdatingPassword = false
    }
  }

  // ── 备份 / 恢复（上游 parity：都先弹层输密码，请求形状见 lib/db-backup.ts）──
  function openDbAuth(mode: 'export' | 'import') {
    dbAuth = { open: true, mode, password: '' }
  }
  function closeDbAuth() {
    dbAuth = { open: false, mode: '', password: '' }
  }
  function handleDownloadBackup() {
    openDbAuth('export')
  }
  function handleFileSelected(e: Event) {
    const target = e.target as HTMLInputElement
    const file = target.files?.[0]
    if (!file) return
    pendingImportFile = file
    openDbAuth('import')
    target.value = ''
  }
  async function handleExportDatabase(password: string) {
    isDownloadingBackup = true
    try {
      const { url, init } = buildExportRequest(password)
      const res = await fetch(url, init)
      if (!res.ok) throw new Error(await responseErrorMessage(res, 'Failed to export database'))
      downloadJSON(await res.json(), backupFileName())
    } catch (err) {
      alert(`Failed to export database: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      isDownloadingBackup = false
      pendingImportFile = null
    }
  }
  async function handleImportDatabase(password: string) {
    const file = pendingImportFile
    if (!file) return
    isImportingBackup = true
    try {
      const payload = JSON.parse(await file.text())
      const { url, init } = buildImportRequest(payload, password)
      const res = await fetch(url, init)
      if (!res.ok) throw new Error(await responseErrorMessage(res, 'Failed to import database'))
      alert('Database backup imported successfully! Reloading page...')
      window.location.reload()
    } catch (err) {
      alert(`Failed to import database: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      isImportingBackup = false
      pendingImportFile = null
    }
  }
  async function handleDbAuthConfirm() {
    const { mode, password } = dbAuth
    if (!password) return
    closeDbAuth()
    if (mode === 'export') await handleExportDatabase(password)
    else if (mode === 'import') await handleImportDatabase(password)
  }
</script>

<div class="flex flex-col gap-6">
  <!-- PAGE HEADER & SAVE BUTTON -->
  <div class="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
    <div class="space-y-1">
      <div class="flex items-center gap-2">
        <div class="p-2 rounded-lg bg-brand-500/10 text-brand-500">
          <User class="w-5 h-5" />
        </div>
        <div>
          <h1 class="font-headline text-2xl sm:text-3xl font-bold text-text-main tracking-tight flex items-center gap-2">
            Settings & Profile
          </h1>
          <p class="font-body text-xs sm:text-sm text-text-muted">
            System configuration, authentication credentials, and database state
          </p>
        </div>
      </div>
    </div>

    <button
      type="button"
      onclick={handleSaveAll}
      disabled={isSavingSettings}
      class="flex items-center gap-1.5 px-4 py-2 rounded-lg bg-brand-500 hover:bg-brand-600 disabled:opacity-50 text-white font-semibold text-xs transition cursor-pointer shadow-md shadow-brand-500/20"
    >
      {#if isSavingSettings}
        <Loader2 class="w-3.5 h-3.5 animate-spin" />
        <span>Saving...</span>
      {:else if saveSuccess}
        <Check class="w-3.5 h-3.5" />
        <span>Settings Saved!</span>
      {:else}
        <Save class="w-3.5 h-3.5" />
        <span>Save Changes</span>
      {/if}
    </button>
  </div>

  <div class="grid grid-cols-1 lg:grid-cols-2 gap-5">
    <!-- SECTION 1: Local Machine Mode & Database -->
    <Card padding="md" class="space-y-4">
      <div class="flex items-center justify-between pb-2 border-b border-border">
        <div class="flex items-center gap-2">
          <Laptop class="w-4 h-4 text-brand-500" />
          <h2 class="text-sm font-bold text-text-main">Local Mode & Database</h2>
        </div>
        <span class="text-[10px] font-mono font-bold px-2 py-0.5 rounded bg-success/10 text-success border border-success/20">
          Running on your machine
        </span>
      </div>

      <div class="space-y-2 text-xs">
        <div class="p-3 rounded-xl bg-bg border border-border space-y-1">
          <div class="text-[10px] text-text-subtle uppercase font-mono tracking-wider font-semibold">
            Database File Location
          </div>
          <div class="font-mono text-xs text-text-main font-semibold">
            ~/.9router/db/data.sqlite
          </div>
          <div class="text-[11px] text-text-muted pt-1">
            SQLite WAL Mode • SetMaxOpenConns(4) • Automatic schema migration
          </div>
        </div>

        <div class="flex items-center gap-2 pt-1">
          <button
            type="button"
            onclick={handleDownloadBackup}
            disabled={isDownloadingBackup}
            class="flex-1 py-2 px-3 rounded-lg bg-surface-2 hover:bg-surface-3 border border-border text-xs font-semibold text-text-main transition cursor-pointer flex items-center justify-center gap-1.5"
          >
            <Download class="w-3.5 h-3.5 text-brand-500" />
            <span>Download Backup</span>
          </button>

          <input
            type="file"
            accept=".json"
            bind:this={fileInput}
            onchange={handleFileSelected}
            class="hidden"
          />

          <button
            type="button"
            onclick={() => fileInput?.click()}
            disabled={isImportingBackup}
            class="flex-1 py-2 px-3 rounded-lg bg-surface-2 hover:bg-surface-3 border border-border text-xs font-semibold text-text-main transition cursor-pointer flex items-center justify-center gap-1.5"
          >
            <Upload class="w-3.5 h-3.5 text-text-muted" />
            <span>Import Backup</span>
          </button>
        </div>
      </div>
    </Card>

    <!-- SECTION 2: Language & Region -->
    <Card padding="md" class="space-y-4">
      <div class="flex items-center gap-2 pb-2 border-b border-border">
        <Globe class="w-4 h-4 text-brand-500" />
        <h2 class="text-sm font-bold text-text-main">Language & Display</h2>
      </div>

      <div class="space-y-3 text-xs">
        <div class="space-y-1">
          <label for="lang-select" class="block font-semibold text-text-main">Dashboard Language</label>
          <select
            id="lang-select"
            bind:value={selectedLanguage}
            class="w-full px-3 py-2 rounded-lg bg-bg border border-border text-xs text-text-main focus:outline-none focus:border-brand-500"
          >
            <option value="en">English (US)</option>
            <option value="zh-CN">简体中文 (Simplified Chinese)</option>
            <option value="zh-TW">繁體中文 (Traditional Chinese)</option>
            <option value="ja">日本語 (Japanese)</option>
            <option value="ko">한국어 (Korean)</option>
            <option value="es">Español</option>
            <option value="de">Deutsch</option>
          </select>
          <p class="text-[11px] text-text-subtle">
            Select the primary interface language for the 9router-go web dashboard.
          </p>
        </div>
      </div>
    </Card>

    <!-- SECTION 3: Security & Master Password -->
    <Card padding="md" class="space-y-4">
      <div class="flex items-center justify-between pb-2 border-b border-border">
        <div class="flex items-center gap-2">
          <Shield class="w-4 h-4 text-brand-500" />
          <h2 class="text-sm font-bold text-text-main">Security & Password</h2>
        </div>
        <Toggle
          checked={requireLogin}
          size="sm"
          label="Require login"
          onChange={(val) => (requireLogin = val)}
        />
      </div>

      <div class="space-y-3 text-xs">
        <div class="flex items-center justify-between">
          <div>
            <p class="font-semibold text-text-main">Require Password on Localhost</p>
            <p class="text-[11px] text-text-subtle">Default password is <code class="font-mono text-brand-500">Mantep210</code></p>
          </div>
        </div>

        <div class="space-y-1 pt-1">
          <label for="session-timeout" class="block font-semibold text-text-main">Session Timeout</label>
          <select
            id="session-timeout"
            bind:value={sessionTimeout}
            class="w-full px-3 py-2 rounded-lg bg-bg border border-border text-xs text-text-main focus:outline-none focus:border-brand-500"
          >
            <option value="15m">15 Minutes</option>
            <option value="1h">1 Hour</option>
            <option value="24h">24 Hours</option>
            <option value="7d">7 Days</option>
            <option value="never">Never (Stay signed in)</option>
          </select>
        </div>

        <!-- Update Password Toggle & Form -->
        <div class="pt-2 border-t border-border/60">
          <button
            type="button"
            onclick={() => (showPasswordFields = !showPasswordFields)}
            class="text-xs font-semibold text-brand-500 hover:opacity-80 cursor-pointer"
          >
            {showPasswordFields ? 'Hide Change Password' : 'Change Master Password'}
          </button>

          {#if showPasswordFields}
            <form onsubmit={handleUpdatePassword} class="space-y-2.5 pt-3">
              {#if passwordErrorMessage}
                <div class="p-2.5 rounded-lg bg-danger/10 border border-danger/20 text-danger text-[11px]">
                  {passwordErrorMessage}
                </div>
              {/if}
              {#if passwordSuccessMessage}
                <div class="p-2.5 rounded-lg bg-success/10 border border-success/20 text-success text-[11px]">
                  {passwordSuccessMessage}
                </div>
              {/if}

              <div class="space-y-1">
                <label for="curr-pwd" class="block text-[11px] font-semibold text-text-muted">Current Password</label>
                <input
                  id="curr-pwd"
                  type="password"
                  bind:value={currentPassword}
                  placeholder="Mantep210"
                  class="w-full px-3 py-1.5 rounded-lg bg-bg border border-border text-xs font-mono text-text-main focus:outline-none focus:border-brand-500"
                  required
                />
              </div>

              <div class="space-y-1">
                <label for="new-pwd" class="block text-[11px] font-semibold text-text-muted">New Password</label>
                <input
                  id="new-pwd"
                  type="password"
                  bind:value={newPassword}
                  placeholder="At least 6 characters"
                  class="w-full px-3 py-1.5 rounded-lg bg-bg border border-border text-xs font-mono text-text-main focus:outline-none focus:border-brand-500"
                  required
                />
              </div>

              <div class="space-y-1">
                <label for="confirm-pwd" class="block text-[11px] font-semibold text-text-muted">Confirm New Password</label>
                <input
                  id="confirm-pwd"
                  type="password"
                  bind:value={confirmNewPassword}
                  placeholder="Re-enter new password"
                  class="w-full px-3 py-1.5 rounded-lg bg-bg border border-border text-xs font-mono text-text-main focus:outline-none focus:border-brand-500"
                  required
                />
              </div>

              <button
                type="submit"
                disabled={isUpdatingPassword}
                class="w-full py-2 px-3 rounded-lg bg-brand-500 hover:bg-brand-600 text-white font-semibold text-xs transition cursor-pointer"
              >
                {isUpdatingPassword ? 'Updating...' : 'Update Password'}
              </button>
            </form>
          {/if}
        </div>
      </div>
    </Card>

    <!-- SECTION 4: Single Sign-On (SSO) -->
    <Card padding="md" class="space-y-4">
      <div class="flex items-center justify-between pb-2 border-b border-border">
        <div class="flex items-center gap-2">
          <Key class="w-4 h-4 text-brand-500" />
          <h2 class="text-sm font-bold text-text-main">Single Sign-On (SSO)</h2>
        </div>
      </div>

      <div class="space-y-3 text-xs">
        <div class="space-y-1">
          <label for="auth-mode-select" class="block font-semibold text-text-main">Authentication Mode</label>
          <select
            id="auth-mode-select"
            bind:value={authMode}
            class="w-full px-3 py-2 rounded-lg bg-bg border border-border text-xs text-text-main focus:outline-none focus:border-brand-500"
          >
            <option value="password">Password Only</option>
            <option value="oidc">OpenID Connect (OIDC)</option>
            <option value="saml">SAML 2.0 SSO</option>
          </select>
        </div>

        {#if authMode === 'oidc'}
          <div class="space-y-2 pt-2 border-t border-border/60">
            <div class="space-y-1">
              <label for="oidc-issuer" class="block font-semibold text-text-muted">Issuer URL</label>
              <input
                id="oidc-issuer"
                type="text"
                bind:value={oidcIssuerUrl}
                placeholder="https://accounts.google.com or Okta URL"
                class="w-full px-3 py-1.5 rounded-lg bg-bg border border-border text-xs font-mono text-text-main"
              />
            </div>
            <div class="space-y-1">
              <label for="oidc-client-id" class="block font-semibold text-text-muted">Client ID</label>
              <input
                id="oidc-client-id"
                type="text"
                bind:value={oidcClientId}
                placeholder="client-id"
                class="w-full px-3 py-1.5 rounded-lg bg-bg border border-border text-xs font-mono text-text-main"
              />
            </div>
            <div class="space-y-1">
              <label for="oidc-scopes" class="block font-semibold text-text-muted">Scopes</label>
              <input
                id="oidc-scopes"
                type="text"
                bind:value={oidcScopes}
                placeholder="openid profile email"
                class="w-full px-3 py-1.5 rounded-lg bg-bg border border-border text-xs font-mono text-text-main"
              />
            </div>
          </div>
        {:else if authMode === 'saml'}
          <div class="space-y-2 pt-2 border-t border-border/60">
            <div class="space-y-1">
              <label for="saml-entrypoint" class="block font-semibold text-text-muted">SAML EntryPoint (SSO URL)</label>
              <input
                id="saml-entrypoint"
                type="text"
                bind:value={samlEntryPoint}
                placeholder="https://idp.example.com/sso"
                class="w-full px-3 py-1.5 rounded-lg bg-bg border border-border text-xs font-mono text-text-main"
              />
            </div>
            <div class="space-y-1">
              <label for="saml-issuer" class="block font-semibold text-text-muted">SP Entity ID / Issuer</label>
              <input
                id="saml-issuer"
                type="text"
                bind:value={samlIssuer}
                placeholder="https://9router.local"
                class="w-full px-3 py-1.5 rounded-lg bg-bg border border-border text-xs font-mono text-text-main"
              />
            </div>
            <div class="space-y-1">
              <label for="saml-cert" class="block font-semibold text-text-muted">X.509 Certificate (PEM)</label>
              <textarea
                id="saml-cert"
                bind:value={samlCert}
                rows={3}
                placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
                class="w-full p-2.5 rounded-lg bg-bg border border-border text-xs font-mono text-text-main"
              ></textarea>
            </div>
          </div>
        {/if}
      </div>
    </Card>

    <!-- SECTION 5: Routing Strategy & Limits -->
    <Card padding="md" class="space-y-4">
      <div class="flex items-center gap-2 pb-2 border-b border-border">
        <Sliders class="w-4 h-4 text-brand-500" />
        <h2 class="text-sm font-bold text-text-main">Default Routing Strategy</h2>
      </div>

      <div class="space-y-3 text-xs">
        <div class="space-y-1">
          <label for="fallback-strat" class="block font-semibold text-text-main">Connection Fallback Strategy</label>
          <select
            id="fallback-strat"
            bind:value={fallbackStrategy}
            class="w-full px-3 py-2 rounded-lg bg-bg border border-border text-xs text-text-main focus:outline-none focus:border-brand-500"
          >
            <option value="failover">Failover (Try next on error/rate-limit)</option>
            <option value="round-robin">Round Robin (Distribute requests across all connections)</option>
            <option value="sticky-round-robin">Sticky Round-Robin (Keep active connection up to limit)</option>
          </select>
        </div>

        {#if fallbackStrategy === 'sticky-round-robin'}
          <div class="space-y-1">
            <label for="sticky-limit" class="block font-semibold text-text-main">Sticky Request Limit</label>
            <input
              id="sticky-limit"
              type="number"
              bind:value={stickyRoundRobinLimit}
              min="1"
              max="100"
              class="w-full px-3 py-2 rounded-lg bg-bg border border-border text-xs font-mono text-text-main focus:outline-none focus:border-brand-500"
            />
            <p class="text-[11px] text-text-subtle">
              Number of consecutive requests routed to the same credential before rotating.
            </p>
          </div>
        {/if}

        <div class="space-y-1">
          <label for="combo-strat" class="block font-semibold text-text-main">Combo Routing Mode</label>
          <select
            id="combo-strat"
            bind:value={comboStrategy}
            class="w-full px-3 py-2 rounded-lg bg-bg border border-border text-xs text-text-main focus:outline-none focus:border-brand-500"
          >
            <option value="first-model">First Model (Always try primary model first)</option>
            <option value="round-robin">Round Robin (Rotate starting model)</option>
          </select>
        </div>
      </div>
    </Card>

    <!-- SECTION 6: Observability & Tracing -->
    <Card padding="md" class="space-y-4">
      <div class="flex items-center justify-between pb-2 border-b border-border">
        <div class="flex items-center gap-2">
          <Zap class="w-4 h-4 text-brand-500" />
          <h2 class="text-sm font-bold text-text-main">Observability</h2>
        </div>
        <Toggle
          checked={enableObservability}
          size="sm"
          label="Enable Observability"
          onChange={(val) => (enableObservability = val)}
        />
      </div>

      <div class="space-y-2 text-xs">
        <p class="text-text-muted leading-relaxed">
          Collect real-time telemetry, TTFT (Time to First Token), round-trip latency, and token throughput for all routed LLM requests.
        </p>
        <div class="p-3 rounded-xl bg-bg border border-border text-[11px] text-text-subtle space-y-1">
          <div>Telemetry destination: In-memory ring buffer & SQLite <code class="font-mono text-brand-500">requestDetails</code></div>
          <div>Real-time stream: <code class="font-mono text-info">/api/usage/stream</code> (SSE)</div>
        </div>
      </div>
    </Card>
  </div>

  <!-- 备份/恢复前的密码确认：服务端要求 x-9r-password 头（上游 parity，profile/page.js:1673-1699）。
       没有这一步，Download Backup 必然 401 Invalid password（2026-09-26 用户报障）。 -->
  <Modal isOpen={dbAuth.open} onClose={closeDbAuth} title="Confirm Password" size="sm">
    <p class="text-text-muted mb-3 text-sm">
      Enter your current password to {dbAuth.mode === 'export' ? 'export' : 'import'} the database.
      {#if dbAuth.mode === 'import'}<span class="text-warn"> This will overwrite existing local data.</span>{/if}
    </p>
    <Input type="password" bind:value={dbAuth.password} placeholder="Current password" />
    {#snippet footer()}
      <button
        type="button"
        onclick={closeDbAuth}
        disabled={isDownloadingBackup || isImportingBackup}
        class="py-2 px-3 rounded-lg bg-surface-2 hover:bg-surface-3 border border-border text-xs font-semibold text-text-main transition cursor-pointer disabled:opacity-50"
      >Cancel</button>
      <button
        type="button"
        onclick={handleDbAuthConfirm}
        disabled={!dbAuth.password || isDownloadingBackup || isImportingBackup}
        class="py-2 px-3 rounded-lg bg-brand-500 hover:bg-brand-600 text-white text-xs font-semibold transition cursor-pointer disabled:opacity-50"
      >Confirm</button>
    {/snippet}
  </Modal>
</div>
