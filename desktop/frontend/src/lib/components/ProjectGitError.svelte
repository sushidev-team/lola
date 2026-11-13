<script lang="ts">
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  import {gitErrorInfo} from '$lib/giterror';
  let {error,operation='',busy=false,recovery,onrefresh,ondismiss}:{error:string;operation?:string;busy?:boolean;recovery?:{label:string;run:()=>void};onrefresh:()=>void;ondismiss:()=>void}=$props();
  let details=$state(false);
  const info=$derived(gitErrorInfo(error,operation));
</script>
<div class="git-error" class:warning={info.kind==='warn'}>
  <div class="error-summary">
    <Icon name="circle-alert" size={16}/>
    <span role="alert" class="error-copy"><strong>{info.title}</strong><span>{info.description}</span></span>
    {#if recovery}<Button size="xs" variant="secondary" disabled={busy} onclick={recovery.run}>{recovery.label}</Button>{:else if info.recovery==='refresh' || !operation}<Button size="xs" disabled={busy} onclick={onrefresh}>Refresh</Button>{/if}
    <Button size="xs" aria-expanded={details} onclick={()=>details=!details}>Details</Button>
    <Button size="xs" icon aria-label="Dismiss Git error" onclick={ondismiss}><Icon name="x" size={14}/></Button>
  </div>
  {#if details}<pre class="selectable" aria-label="Git error details">{error}</pre>{/if}
</div>
<style>
  .git-error { padding:7px 12px; color:var(--color-bad); background:color-mix(in srgb,var(--color-bad) 7%,var(--color-panel)); border-bottom:1px solid color-mix(in srgb,var(--color-bad) 25%,var(--color-edge)); }
  .git-error.warning { color:var(--color-warn); background:color-mix(in srgb,var(--color-warn) 7%,var(--color-panel)); border-color:color-mix(in srgb,var(--color-warn) 25%,var(--color-edge)); }
  .error-summary { display:flex; gap:8px; align-items:center; }
  .error-summary > :global(svg) { flex-shrink:0; }
  .error-copy { display:flex; flex:1; min-width:0; align-items:baseline; gap:8px; font-size:13px; }
  .error-copy strong { flex-shrink:0; font-weight:600; }
  .error-copy > span { color:var(--color-ink-2); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  pre { margin-top:8px; max-height:180px; overflow:auto; white-space:pre-wrap; overflow-wrap:anywhere; font-size:12px; color:var(--color-ink-2); }
</style>
