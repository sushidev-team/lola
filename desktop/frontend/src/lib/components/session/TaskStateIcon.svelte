<script lang="ts">
  import Icon from '../Icon.svelte';
  let { state }: { state: string } = $props();
</script>
<span class="task-state-icon" data-task-state={state} aria-hidden="true" style={`--fill:${state === 'done' ? 360 : state === 'doing' ? 240 : 120}deg`}><Icon name={state === 'checking' ? 'circle-help' : 'circle'} size={15} /></span>
<style>
  .task-state-icon { position: relative; display: inline-flex; width: 15px; height: 15px; flex-shrink: 0; color: var(--color-blue); }
  .task-state-icon::after { content: ''; position: absolute; inset: 4px; border-radius: 50%; background: conic-gradient(currentColor var(--fill), transparent 0); }
  [data-task-state='doing'] { color: var(--color-accent); }
  [data-task-state='done'] { color: var(--color-good); }
  [data-task-state='checking'] { color: var(--color-warn); }
  [data-task-state='checking']::after { display: none; }
</style>
