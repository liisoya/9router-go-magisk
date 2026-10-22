<script lang="ts">
  // Details-section dashboard (issue #280): activity heatmap and node health
  // above the request-details table.
  import { onMount } from 'svelte'
  import { api, type UsageDashboardResponse } from '../../api/client'
  import Card from '../../lib/ui/Card.svelte'
  import ActivityHeatmap from './ActivityHeatmap.svelte'
  import NodeHealthTable from './NodeHealthTable.svelte'
  import { isDetailsExpandedByDefault } from '../../lib/displayPrefs'

  let data = $state<UsageDashboardResponse | null>(null)
  let loading = $state(true)
  let failed = $state(false)

  onMount(async () => {
    try {
      data = await api.getUsageDashboard()
    } catch {
      failed = true
    } finally {
      loading = false
    }
  })

  const lifetime = $derived(data?.lifetime ?? null)

  // Both detail panels start collapsed unless Settings > Display says
  // otherwise; each one still opens on its own for the current view.
  let heatmapOpen = $state(isDetailsExpandedByDefault())
  let healthOpen = $state(isDetailsExpandedByDefault())
</script>

<div class="flex min-w-0 flex-col gap-6">
  {#if loading}
    <Card padding="md">
      <div class="flex items-center justify-center gap-2 py-8 text-text-muted text-sm">
        <span class="material-symbols-outlined animate-spin text-[20px]" aria-hidden="true">progress_activity</span>
        Loading dashboard...
      </div>
    </Card>
  {:else if failed || !data}
    <Card padding="md">
      <p class="py-6 text-center text-sm text-text-muted">Failed to load usage dashboard</p>
    </Card>
  {:else}
    <Card padding="md" title="Activity Heatmap" icon={calIcon} action={heatmapToggle}>
      {#if heatmapOpen}
        <ActivityHeatmap daily={data.daily || []} currentStreak={lifetime?.currentStreak ?? 0} />
      {/if}
    </Card>

    <Card padding="md" title="Node Health" icon={dnsIcon} action={healthToggle}>
      {#if healthOpen}
        <p class="mb-2 text-xs text-text-muted">Last 7 days (≥50 real requests)</p>
        <NodeHealthTable nodes={data.nodes || []} models={data.models || []} />
      {/if}
    </Card>
  {/if}
</div>

{#snippet calIcon()}
  <span class="material-symbols-outlined text-[18px]" aria-hidden="true">calendar_month</span>
{/snippet}
{#snippet dnsIcon()}
  <span class="material-symbols-outlined text-[18px]" aria-hidden="true">dns</span>
{/snippet}
{#snippet heatmapToggle()}
  <button
    type="button"
    aria-expanded={heatmapOpen}
    aria-label="Toggle Activity Heatmap"
    onclick={() => (heatmapOpen = !heatmapOpen)}
    class="p-1 rounded-lg text-text-muted hover:text-text-main hover:bg-surface-2 transition-colors cursor-pointer"
  >
    <span
      class="material-symbols-outlined text-[18px] transition-transform duration-200"
      style:transform={heatmapOpen ? 'rotate(180deg)' : 'rotate(0deg)'}
    >
      expand_more
    </span>
  </button>
{/snippet}
{#snippet healthToggle()}
  <button
    type="button"
    aria-expanded={healthOpen}
    aria-label="Toggle Node Health"
    onclick={() => (healthOpen = !healthOpen)}
    class="p-1 rounded-lg text-text-muted hover:text-text-main hover:bg-surface-2 transition-colors cursor-pointer"
  >
    <span
      class="material-symbols-outlined text-[18px] transition-transform duration-200"
      style:transform={healthOpen ? 'rotate(180deg)' : 'rotate(0deg)'}
    >
      expand_more
    </span>
  </button>
{/snippet}
