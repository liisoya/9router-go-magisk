<script lang="ts">
  // Provider icon with a graceful fallback.
  //
  // Every provider tile resolves its artwork through getIconPath, which points
  // at /providers/<id>.png. A provider added to the catalog without shipping a
  // PNG therefore 404s; the call sites hid the failed <img>, which left an
  // empty 32px box. Handling the error here instead means any missing or
  // renamed asset degrades to an initials badge in the provider's brand colour,
  // and one component covers every surface rather than ten onerror handlers.
  import { getIconPath, getProviderGlyph } from './types'

  interface Props {
    id?: string | null
    apiType?: string
    size?: 'sm' | 'md' | 'lg'
    class?: string
  }

  let { id, apiType, size = 'sm', class: klass = '' }: Props = $props()

  let failed = $state(false)
  const glyph = $derived(getProviderGlyph(id))
  const src = $derived(getIconPath(id, apiType))

  const box = $derived(
    size === 'lg' ? 'w-10 h-10' : size === 'md' ? 'w-8 h-8' : 'w-6 h-6'
  )
  const text = $derived(size === 'lg' ? 'text-sm' : size === 'md' ? 'text-[11px]' : 'text-[9px]')
</script>

<div
  class="{box} shrink-0 rounded-lg flex items-center justify-center bg-black/5 dark:bg-white/5 border border-border overflow-hidden {klass}"
  role="img"
  aria-label={glyph.name}
  title={glyph.name}
>
  {#if failed}
    <span
      class="font-semibold {text} leading-none"
      style="color: {glyph.color}"
    >{glyph.initials}</span>
  {:else}
    <img
      {src}
      alt={glyph.name}
      class="w-[60%] h-[60%] object-contain"
      onerror={() => (failed = true)}
    />
  {/if}
</div>
