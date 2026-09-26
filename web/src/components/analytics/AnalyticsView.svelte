<script lang="ts">
  import { api, getAuthHeaders, type ProviderConnection, type ProviderNode } from '../../api/client'
  import { browserStores, getStoredApiKey } from '../../lib/session'
  import { PROVIDER_CATALOG } from '../../lib/providers'
  import Card from '../../lib/ui/Card.svelte'
  import {
    fmt,
    timeAgo,
    PERIODS,
    type MainTab,
    type Period,
    type StatsData,
    type RequestDetailItem,
    type ActiveRequestItem,
    type RecentRequestItem
  } from './types'
  import SummaryKpiCards from './SummaryKpiCards.svelte'
  import UsageBreakdownTable from './UsageBreakdownTable.svelte'
  import RequestDetailsTab from './RequestDetailsTab.svelte'
  import ProviderTopologyCard from './ProviderTopologyCard.svelte'
  interface Props {
    connections?: ProviderConnection[]
    providerNodes?: ProviderNode[]
  }

  let { connections = [], providerNodes = [] }: Props = $props()

  let activeTab = $state<MainTab>('overview')
  let period = $state<Period>('today')
  let isFetching = $state(false)
  let stats = $state<StatsData>({})
  let activeRequests = $state<ActiveRequestItem[]>([])
  let pulseProvider = $state<string>('')
  let lastProvider = $state<string>('')
  let errorProvider = $state<string>('')
  let pulseTimer: ReturnType<typeof setTimeout> | null = null

  function triggerPulse(provider: string) {
    if (!provider) return
    pulseProvider = provider
    if (pulseTimer) clearTimeout(pulseTimer)
    pulseTimer = setTimeout(() => {
      pulseProvider = ''
    }, 3000)
  }
  // mergeRecent unions an incoming SSE list with what is on screen. The SSE
  // stream carries only this process's in-memory ring, so a plain replace
  // collapses the DB-backed list (20 rows after a REST load) down to the few
  // rows seen since the last restart — the list visibly blinks and rows below
  // vanish. Union + dedupe + newest-first keeps rows the stream has not seen.
  function mergeRecent(
    prev: RecentRequestItem[] | undefined,
    next: RecentRequestItem[] | undefined,
  ): RecentRequestItem[] {
    const byKey = new Map<string, RecentRequestItem>()
    const keyOf = (r: RecentRequestItem) =>
      `${r.model}|${r.provider}|${r.promptTokens}|${r.completionTokens}|${(r.timestamp || '').slice(0, 16)}`
    for (const r of prev || []) byKey.set(keyOf(r), r)
    for (const r of next || []) byKey.set(keyOf(r), r)
    return [...byKey.values()]
      .sort((a, b) => (b.timestamp || '').localeCompare(a.timestamp || ''))
      .slice(0, 20)
  }
  // Request details tab state
  let details = $state<RequestDetailItem[]>([])
  let detailsTotal = $state(0)
  let detailsPage = $state(1)
  let detailsLoading = $state(false)
  async function loadStats(targetPeriod: Period) {
    isFetching = true
    try {
      const res = await api.getUsageStats(targetPeriod)
      if (res) {
        stats = res
        if (!lastProvider && Array.isArray(res.recentRequests) && res.recentRequests.length > 0) {
          lastProvider = res.recentRequests[0].provider || ''
        }
      }
    } catch (err) {
      console.error('Failed to load usage stats:', err)
    } finally {
      isFetching = false
    }
  }

  async function loadDetails(page = 1) {
    detailsLoading = true
    try {
      const limit = 20
      const offset = (page - 1) * limit
      const res = await api.getRequestDetails(limit, offset)
      if (res && Array.isArray(res.details)) {
        details = res.details
        detailsTotal = res.total || 0
        detailsPage = page
      }
    } catch (err) {
      console.error('Failed to load request details:', err)
    } finally {
      detailsLoading = false
    }
  }

  $effect(() => {
    loadStats(period)
  })

  $effect(() => {
    if (activeTab === 'details') {
      loadDetails(detailsPage)
    }
  })

  // SSE real-time updates for activeRequests, recentRequests and error notifications
  $effect(() => {
    let isCancelled = false
    let controller: AbortController | null = null
    let reconnectTimeout: ReturnType<typeof setTimeout> | null = null

    const token = getStoredApiKey(browserStores())
    let streamInitialized = false

    const connectStream = async () => {
      if (isCancelled) return
      controller = new AbortController()

      try {
        const streamUrl = token ? `/api/usage/stream?key=${encodeURIComponent(token)}` : '/api/usage/stream'
        const res = await fetch(streamUrl, {
          headers: getAuthHeaders(),
          signal: controller.signal,
        })

        if (!res.ok) {
          throw new Error(`usage stream failed: ${res.status}`)
        }

        const reader = res.body?.getReader()
        const decoder = new TextDecoder()
        if (!reader) return

        let buffer = ''
        while (!isCancelled) {
          const { done, value } = await reader.read()
          if (done) break

          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split('\n')
          buffer = lines.pop() || ''

          for (const line of lines) {
            const trimmed = line.trim()
            if (!trimmed || trimmed.startsWith(':')) continue
            if (!trimmed.startsWith('data: ')) continue

            try {
              const data = JSON.parse(trimmed.slice(6))
              if (Array.isArray(data.recentRequests) && data.recentRequests.length > 0) {
                const prevTop = stats.recentRequests?.[0]
                const newTop = data.recentRequests[0]
                // Only pulse animation when a GENUINE new model request arrives AFTER stream initialization
                if (streamInitialized && prevTop) {
                  const isNewRequest =
                    newTop.timestamp !== prevTop.timestamp ||
                    newTop.model !== prevTop.model ||
                    newTop.tokens !== prevTop.tokens
                  if (isNewRequest && newTop.provider) {
                    lastProvider = newTop.provider
                    triggerPulse(newTop.provider)
                  }
                } else if (!lastProvider && newTop.provider) {
                  lastProvider = newTop.provider
                }
                streamInitialized = true
                stats = { ...stats, recentRequests: mergeRecent(stats.recentRequests, data.recentRequests) }
              }
              if (Array.isArray(data.activeRequests)) {
                activeRequests = data.activeRequests
                stats = { ...stats, activeRequests: data.activeRequests }
                if (data.activeRequests.length > 0 && data.activeRequests[0].provider) {
                  lastProvider = data.activeRequests[0].provider
                }
              }
              if (data.errorProvider) {
                errorProvider = data.errorProvider
              }
            } catch (err) {
              console.error('Failed to parse SSE usage stream:', err)
            }
          }
        }
      } catch (err) {
        if (!isCancelled) {
          reconnectTimeout = setTimeout(connectStream, 3000)
        }
      }
    }

    connectStream()

    // Auto-poll stats every 5s so KPI counters smoothly increment in real time
    const pollTimer = setInterval(() => {
      if (activeTab === 'overview' && (typeof document === 'undefined' || !document.hidden)) {
        loadStats(period)
      }
    }, 5000)

    return () => {
      isCancelled = true
      if (controller) controller.abort()
      if (reconnectTimeout) clearTimeout(reconnectTimeout)
      if (pulseTimer) clearTimeout(pulseTimer)
      clearInterval(pollTimer)
    }
  })
  let nodeNameById = $derived.by(() => {
    const m = new Map<string, string>()
    for (const n of providerNodes || []) {
      if (n?.id && n?.name) m.set(n.id, n.name)
    }
    return m
  })

  function topologyName(providerId: string, fallbackName?: string): string {
    const nodeName = nodeNameById.get(providerId)
    if (nodeName) return nodeName
    const cat = PROVIDER_CATALOG.find((p) => p.id === providerId || p.alias === providerId)
    if (cat?.name) return cat.name
    if (fallbackName && fallbackName !== providerId) {
      // Numeric key names (e.g. "12") are connection labels, not provider names —
      // fall back to the raw provider id so custom nodes never render as "12".
      if (!/^\d+$/.test(fallbackName.trim())) return fallbackName
      return providerId
    }
    return providerId
  }

  let topologyProviders = $derived.by(() => {
    const seen = new Set<string>()
    const list: { id: string; name: string; color?: string; type: string }[] = []

    for (const c of connections) {
      if (c.isActive !== 0 && c.provider && !seen.has(c.provider)) {
        seen.add(c.provider)
        const cat = PROVIDER_CATALOG.find((p) => p.id === c.provider || p.alias === c.provider)
        list.push({
          id: c.provider,
          name: topologyName(c.provider, c.name || undefined),
          color: cat?.color || '#3B82F6',
          type: 'connection'
        })
      }
    }

    if (stats.byProvider) {
      for (const prov of Object.keys(stats.byProvider)) {
        if (!seen.has(prov)) {
          seen.add(prov)
          const cat = PROVIDER_CATALOG.find((p) => p.id === prov || p.alias === prov)
          list.push({
            id: prov,
            name: topologyName(prov),
            color: cat?.color || '#10B981',
            type: 'active'
          })
        }
      }
    }
    // Always include key free/noAuth providers if not yet listed
    const FREE_DEFAULTS = [
      { id: 'antigravity', name: 'Antigravity', color: '#F59E0B' },
      { id: 'opencode', name: 'OpenCode Free', color: '#3B82F6' },
      { id: 'nvidia', name: 'NVIDIA NIM', color: '#76B900' },
      { id: 'openrouter', name: 'OpenRouter', color: '#6366F1' },
      { id: 'clinepass', name: 'ClinePass', color: '#8B5CF6' }
    ]
    for (const f of FREE_DEFAULTS) {
      if (!seen.has(f.id)) {
        seen.add(f.id)
        list.push({
          id: f.id,
          name: f.name,
          color: f.color,
          type: 'default'
        })
      }
    }

    return list.slice(0, 14)
  })
</script>

<div class="flex min-w-0 flex-col gap-6 px-1 sm:px-0">
  <!-- Tabs + Period Selector Row -->
  <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
    <div class="inline-flex rounded-xl bg-surface border border-border p-1 shadow-sm">
      <button
        type="button"
        onclick={() => (activeTab = 'overview')}
        class="rounded-lg px-4 py-1.5 text-xs sm:text-sm font-medium transition-colors cursor-pointer {activeTab === 'overview'
          ? 'bg-brand-500 text-white font-semibold shadow-sm'
          : 'text-text-muted hover:text-text-main'}"
      >
        Overview
      </button>
      <button
        type="button"
        onclick={() => (activeTab = 'details')}
        class="rounded-lg px-4 py-1.5 text-xs sm:text-sm font-medium transition-colors cursor-pointer {activeTab === 'details'
          ? 'bg-brand-500 text-white font-semibold shadow-sm'
          : 'text-text-muted hover:text-text-main'}"
      >
        Details
      </button>
    </div>

    {#if activeTab === 'overview'}
      <div class="flex items-center gap-1.5 self-start sm:self-auto">
        <div class="inline-flex rounded-xl bg-surface border border-border p-1 shadow-sm">
          {#each PERIODS as p}
            <button
              type="button"
              onclick={() => (period = p.value)}
              disabled={isFetching}
              class="rounded-lg px-3 py-1 text-xs sm:text-sm font-medium transition-colors cursor-pointer {period === p.value
                ? 'bg-brand-500 text-white font-semibold shadow-sm'
                : 'text-text-muted hover:text-text-main'}"
            >
              {p.label}
            </button>
          {/each}
        </div>
        {#if isFetching}
          <span class="w-2 h-2 rounded-full bg-brand-500 animate-ping"></span>
        {/if}
      </div>
    {/if}
  </div>

  {#if activeTab === 'overview'}
    <!-- 5 Overview KPI Cards -->
    <SummaryKpiCards {stats} />

    <!-- Topology + Recent Requests -->
    <div class="grid min-w-0 grid-cols-1 items-stretch gap-2 lg:grid-cols-[minmax(0,2fr)_minmax(280px,1fr)]">
      <ProviderTopologyCard
        providers={topologyProviders}
        {activeRequests}
        {pulseProvider}
        {lastProvider}
        {errorProvider}
        onRefresh={() => loadStats(period)}
      />
      <!-- Recent Requests Card -->
      <div class="bg-surface border border-border-subtle rounded-[14px] shadow-[var(--shadow-soft)] p-4 flex min-w-0 flex-col overflow-hidden" style="height: 480px">
        <div class="px-1 py-2 border-b border-border shrink-0">
          <span class="text-xs font-semibold text-text-muted uppercase tracking-wide">Recent Requests</span>
        </div>

        {#if !stats.recentRequests || stats.recentRequests.length === 0}
          <div class="flex-1 flex items-center justify-center text-text-muted text-xs">
            No requests recorded yet.
          </div>
        {:else}
          <div class="flex-1 overflow-y-auto">
            <table class="w-full table-fixed min-w-[280px] border-collapse text-xs">
              <colgroup>
                <col class="w-[20px]" />
                <col />
                <col class="w-[96px]" />
                <col class="w-[56px]" />
              </colgroup>
              <thead class="sticky top-0 bg-bg z-10">
                <tr class="border-b border-border">
                  <th class="py-1.5 pl-3 text-left font-semibold text-text-muted"></th>
                  <th class="py-1.5 text-left font-semibold text-text-muted">Model</th>
                  <th class="py-1.5 text-right font-semibold text-text-muted whitespace-nowrap">In / Out</th>
                  <th class="py-1.5 pr-3 text-right font-semibold text-text-muted whitespace-nowrap">When</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border/50 font-mono text-[11px]">
                {#each stats.recentRequests as req}
                  <tr class="hover:bg-bg-subtle transition-colors">
                    <td class="py-1.5 pl-3 align-middle">
                      <span class="mx-auto block w-1.5 h-1.5 rounded-full {req.status === 'ok' || req.status === 'success' ? 'bg-success' : 'bg-error'}"></span>
                    </td>
                    <td class="py-1.5 pr-2 min-w-0">
                      <span class="block truncate font-mono text-[11px]" title={req.model}>{req.model}</span>
                    </td>
                    <td class="py-1.5 pr-3 text-right whitespace-nowrap">
                      <span class="text-primary">{fmt(req.promptTokens)}↑</span>
                      <span class="text-success">{fmt(req.completionTokens)}↓</span>
                    </td>
                    <td class="py-1.5 pr-3 text-right text-text-muted whitespace-nowrap text-[10px]">
                      {timeAgo(req.timestamp)}
                    </td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </div>
    </div>

    <!-- Breakdown Table -->
    <UsageBreakdownTable {stats} />
  {:else}
    <RequestDetailsTab
      {details}
      {detailsTotal}
      {detailsPage}
      {detailsLoading}
      onPageChange={loadDetails}
      onRefresh={() => loadDetails(detailsPage)}
    />
  {/if}
</div>
