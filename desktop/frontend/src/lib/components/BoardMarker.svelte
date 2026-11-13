<script lang="ts">
  import Icon from './Icon.svelte';
  import type { IconName } from '$lib/icons';

  // Shared report/check states. Motion is opt-in and never the only state cue.
  type Kind = 'done' | 'active' | 'pending' | 'pass' | 'fail' | 'running' | 'blocked' | 'refused' | 'unknown';
  let { kind, live = false, size = 16, tone = '' }: { kind: Kind; live?: boolean; size?: number; tone?: '' | 'warn' } = $props();
  const marks: Record<Kind, { name: IconName; color: string }> = {
    done: { name: 'circle-check', color: 'text-good' },
    pass: { name: 'circle-check', color: 'text-good' },
    fail: { name: 'circle-x', color: 'text-bad' },
    active: { name: 'circle-dot', color: 'text-accent' },
    pending: { name: 'circle', color: 'text-faint' },
    running: { name: 'loader-circle', color: 'text-info' },
    blocked: { name: 'circle-pause', color: 'text-orange' },
    refused: { name: 'ban', color: 'text-faint' },
    unknown: { name: 'circle-help', color: 'text-warn' },
  };
  const mark = $derived(marks[kind]);
</script>

<Icon name={mark.name} {size} class="{tone === 'warn' ? 'text-warn' : mark.color} {live && kind === 'running' ? 'motion-safe:animate-spin' : live && kind === 'active' ? 'motion-safe:animate-pulse' : ''}" />
