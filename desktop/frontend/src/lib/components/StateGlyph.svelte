<script lang="ts">
  import Icon from './Icon.svelte';
  import type { IconName } from '$lib/icons';

  let { glyph, label = '', cls = '', mono = false, class: extra = '' }: {
    glyph: string; label?: string; cls?: string; mono?: boolean; class?: string;
  } = $props();

  // Status badges share a circular silhouette. Shape and fill distinguish states
  // even without colour; the parent owns the accessible status label.
  const state = $derived(glyph === 'review' ? 'review'
    : glyph === '◉' ? 'attention'
    : glyph === '◐' ? 'working'
    : glyph === '●' ? 'finished'
    : glyph === '✕' ? (label === 'Orphaned' ? 'orphaned' : 'exited')
    : glyph === '$' ? 'shell' : 'resting');
  const names: Record<string, IconName> = {
    review: 'circle', attention: 'circle-alert', working: 'circle',
    finished: 'circle-check', orphaned: 'circle-help', exited: 'circle-minus',
    shell: 'square-terminal', resting: 'circle',
  };
</script>

<span class="state-badge {cls} {extra}" data-state-glyph={glyph} data-state-badge={state} aria-hidden="true">
  <Icon name={names[state]} class="state-badge-icon" />
</span>

<style>
  .state-badge { position: relative; display: inline-flex; width: var(--state-badge-size, 16px); height: var(--state-badge-size, 16px); flex-shrink: 0; align-items: center; justify-content: center; vertical-align: middle; }
  .state-badge :global(.state-badge-icon) { width: 100%; height: 100%; }
  [data-state-badge='working'] { color: var(--color-accent); }
  /* Fixed quarter / three-quarter fills identify workflow stages, not measured progress. */
  [data-state-badge='working']::after { content: ''; position: absolute; inset: 27%; border-radius: 50%; background: conic-gradient(currentColor 0 90deg, transparent 90deg); }
  [data-state-badge='attention'] { color: var(--color-orange); }
  [data-state-badge='review'] { color: var(--color-magenta); }
  [data-state-badge='review']::after { content: ''; position: absolute; inset: 24%; border-radius: 50%; background: conic-gradient(currentColor 0 270deg, transparent 270deg); }
  [data-state-badge='finished'] { color: var(--color-accent); }
  :is([data-state-badge='attention'], [data-state-badge='finished']) :global(circle) { fill: currentColor; }
  :is([data-state-badge='attention'], [data-state-badge='finished']) :global(path),
  [data-state-badge='attention'] :global(line) { stroke: var(--color-on-accent); }
  :is([data-state-badge='resting'], [data-state-badge='exited'], [data-state-badge='orphaned']) { color: var(--color-faint); }
  [data-state-badge='orphaned'] :global(circle) { stroke-dasharray: 3 4; }
</style>
