<script lang="ts">
  import type { PRChoice } from '$lib/gitpullrequest';
  import { disclosureMotion } from '$lib/disclosureMotion';
  import Checkbox from './Checkbox.svelte';
  import Icon from './Icon.svelte';
  let {label,options,selected=$bindable([]),disabled=false}:{label:string;options:PRChoice[];selected?:string[];disabled?:boolean}=$props();
  let query=$state('');
  const shown=$derived(options.filter(o=>`${o.label} ${o.value}`.toLowerCase().includes(query.toLowerCase())));
  function toggle(value:string,checked:boolean){selected=checked?[...selected,value]:selected.filter(v=>v!==value);}
</script>
<details use:disclosureMotion class="pr-choices">
  <summary><span>{label}</span><span class="choice-count">{selected.length || 'None'}</span><Icon name="chevron-down" size={13}/></summary>
  <div class="choice-content">
    <input aria-label={`Search ${label.toLowerCase()}`} placeholder="Search…" bind:value={query} disabled={disabled}/>
    <div class="choice-list">
      {#each shown as option (option.value)}<label><Checkbox checked={selected.includes(option.value)} onchange={e=>toggle(option.value,e.currentTarget.checked)} {disabled}/><span>{option.label}</span></label>{:else}<p>{options.length?'No matches':'No options available'}</p>{/each}
    </div>
  </div>
</details>
<style>
  .pr-choices { min-width:0; border:1px solid var(--color-edge); border-radius:6px; background:var(--color-canvas); }
  summary { display:flex; align-items:center; gap:8px; padding:7px 9px; list-style:none; cursor:pointer; color:var(--color-ink-2); font-size:13px; }
  summary::-webkit-details-marker { display:none; }
  summary:focus-visible { outline:2px solid var(--color-accent); outline-offset:1px; }
  .choice-count { margin-left:auto; color:var(--color-faint); font-size:12px; }
  .choice-content { padding:0 8px 7px; }
  input { width:100%; padding:5px 7px; background:var(--color-panel); color:var(--color-ink); border:1px solid var(--color-edge); border-radius:4px; font-size:13px; }
  .choice-list { max-height:130px; overflow:auto; margin-top:6px; }
  label { display:flex; gap:7px; align-items:center; padding:4px 0; color:var(--color-ink); font-size:13px; cursor:pointer; }
  label span { overflow-wrap:anywhere; }
  p { color:var(--color-faint); font-size:12px; padding:5px 0; }
</style>
