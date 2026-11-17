<script lang="ts">
  import { tick } from 'svelte';
  import Button from './Button.svelte';
  import Checkbox from './Checkbox.svelte';
  import Select from './Select.svelte';
  import Icon from './Icon.svelte';
  import Skeleton from './Skeleton.svelte';
  import { emptySectionFilter, filterActive, regexError, type SectionFilter } from '$lib/gitsectionfilters';

  let { name, count, total, loading = false, filter = $bindable(), statuses, statusLabel = 'Checkout status', searchLabel = 'Name contains' }: {
    name: string; count: number; total: number; loading?: boolean; filter: SectionFilter;
    statuses: {value:string;label:string}[]; statusLabel?: string; searchLabel?: string;
  } = $props();
  const uid = $props.id();
  const active = $derived(filterActive(filter));
  let open = $state(false);
  let trigger: HTMLSpanElement;
  let popup = $state<HTMLDivElement>();
  let upwards = false;
  let position = $state({left:0,top:0,height:400});
  function close(focus = false) { open = false; if (focus) trigger?.querySelector('button')?.focus(); }
  async function reveal() {
    if (open) { close(); return; }
    const rect = trigger.getBoundingClientRect();
    const width = Math.min(320, innerWidth - 16);
    const below = innerHeight - rect.bottom - 8, above = rect.top - 8;
    const up = below < 350 && above > below;
    upwards = up;
    const height = Math.min(480, Math.max(120, up ? above : below));
    position = {left:Math.max(8, Math.min(rect.right - width, innerWidth - width - 8)), top:up ? Math.max(8, rect.top - height - 4) : rect.bottom + 4, height};
    open = true;
    await tick();
    popup?.querySelector('input')?.focus();
  }
  async function addRegex() {
    filter.regexes = [...filter.regexes, ''];
    await tick();
    popup?.querySelector<HTMLInputElement>(`[id="${uid}-regex-${filter.regexes.length - 1}"]`)?.focus();
  }
  function outside(event: PointerEvent) {
    if (!open || !(event.target instanceof Element) || event.target.closest('.lola-select-menu')) return;
    if (!trigger.contains(event.target) && !popup?.contains(event.target)) close();
  }
  function key(event: KeyboardEvent) {
    if (open && event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close(true); }
  }
  function portal(node: HTMLDivElement) {
    document.body.appendChild(node);
    const observer = new ResizeObserver(() => {
      if (upwards) position = {...position,top:Math.max(8,trigger.getBoundingClientRect().top - node.getBoundingClientRect().height - 4)};
    });
    observer.observe(node);
    return {destroy:() => { observer.disconnect(); node.remove(); }};
  }
</script>

<svelte:window onpointerdown={outside} onkeydown={key} onresize={() => close()} />
<span class="filter-trigger" class:active bind:this={trigger}>
  <Button variant="bare" size="xs" class="filter-count" aria-label={`Filter ${name}`} aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? `${uid}-popover` : undefined} title={`${count} of ${total}${active ? ' · Filters active' : ''} · Filter ${name.toLowerCase()}`} onclick={reveal}>
    <span class="count">{#if loading}<Skeleton width="12px" height="12px"/>{:else}{active ? `${count}/${total}` : total}{/if}</span>
    <span class="filter-icon" aria-hidden="true"><Icon name="list-filter" size={14}/></span>
  </Button>
</span>
{#if open}
  <div use:portal bind:this={popup} id={`${uid}-popover`} class="filter-popover" role="dialog" aria-label={`Filter ${name}`} tabindex="-1" style={`left:${position.left}px;top:${position.top}px;max-height:${position.height}px`}>
    <header><strong>Filter {name.toLowerCase()}</strong><Button icon size="xs" aria-label="Close filters" onclick={() => close(true)}><Icon name="x" size={14}/></Button></header>
    <label for={`${uid}-search`}>{searchLabel}</label>
    <input id={`${uid}-search`} type="text" bind:value={filter.search} placeholder="Search…" />
    <label for={`${uid}-status`}>{statusLabel}</label>
    <Select id={`${uid}-status`} bind:value={filter.status}>{#each statuses as status}<option value={status.value}>{status.label}</option>{/each}</Select>
    <div class="regex-heading"><strong>Regular expressions</strong><Button size="xs" aria-label="Add regular expression" onclick={addRegex}><Icon name="plus" size={13}/>Add</Button></div>
    {#each filter.regexes as pattern, index}
      {@const error = pattern ? regexError(pattern) : ''}
      <div class="regex-row">
        <label class="sr-only" for={`${uid}-regex-${index}`}>Regular expression {index + 1}</label>
        <input id={`${uid}-regex-${index}`} type="text" spellcheck="false" bind:value={filter.regexes[index]} placeholder="^feature/" aria-invalid={!!error} aria-describedby={error ? `${uid}-error-${index}` : undefined}/>
        <Button icon size="xs" aria-label={`Remove regular expression ${index + 1}`} onclick={() => filter.regexes = filter.regexes.filter((_, i) => i !== index)}><Icon name="x" size={14}/></Button>
      </div>
      {#if error}<p id={`${uid}-error-${index}`} class="filter-error" role="status">{error}. Regex filtering is paused until corrected.</p>{/if}
    {/each}
    {#if filter.regexes.length}
      <label for={`${uid}-match`}>Combine expressions</label>
      <Select id={`${uid}-match`} bind:value={filter.match}><option value="any">Match any expression</option><option value="all">Match all expressions</option></Select>
      <label class="check"><Checkbox bind:checked={filter.exclude}/>Exclude matching items</label>
      <label class="check"><Checkbox bind:checked={filter.ignoreCase}/>Ignore case</label>
    {:else}<p class="filter-help">Add patterns to match full names. Filters combine with the search and status above.</p>{/if}
    <footer><span>{count} of {total} shown</span><Button size="xs" disabled={!active && !filter.regexes.length} onclick={() => filter = emptySectionFilter()}>Clear filters</Button></footer>
  </div>
{/if}

<style>
  .filter-trigger { display:block; }
  .filter-trigger :global(.filter-count) { position:relative; height:24px; min-width:24px; padding:1px 5px; border:1px solid transparent; border-radius:4px; background:var(--color-canvas); color:var(--color-ink-2); font-size:12px; font-weight:500; font-variant-numeric:tabular-nums; }
  .filter-icon { position:absolute; inset:0; display:grid; place-items:center; opacity:0; }
  .filter-trigger:hover .count, .filter-trigger:focus-within .count { opacity:0; }
  .filter-trigger:hover .filter-icon, .filter-trigger:focus-within .filter-icon { opacity:1; }
  .active :global(.filter-count) { color:var(--color-accent-ink); border-color:var(--color-accent); }
  .filter-trigger :global(button:focus-visible) { outline:2px solid var(--color-accent); outline-offset:1px; }
  .filter-popover { position:fixed; z-index:81; display:grid; gap:8px; width:min(320px,calc(100vw - 16px)); overflow:auto; overscroll-behavior:contain; padding:12px; border:1px solid var(--color-edge); border-radius:8px; background:var(--color-panel); color:var(--color-ink); font-size:13px; box-shadow:0 10px 28px #0004; }
  header, footer, .regex-heading { display:flex; align-items:center; justify-content:space-between; gap:8px; }
  header { margin-bottom:4px; }
  header strong { font-size:15px; }
  .filter-popover > label:not(.check) { margin-top:4px; }
  input { min-width:0; width:100%; height:30px; padding:4px 8px; border:1px solid var(--color-edge); border-radius:5px; background:var(--color-canvas); color:var(--color-ink); }
  input:focus-visible { outline:2px solid var(--color-accent); outline-offset:1px; }
  input[aria-invalid="true"] { border-color:var(--color-bad); }
  .regex-heading { margin-top:12px; }
  .regex-row { display:grid; grid-template-columns:minmax(0,1fr) 24px; align-items:center; gap:4px; }
  .check { display:flex; align-items:center; gap:8px; }
  .filter-error { color:var(--color-bad); }
  .filter-help, footer { color:var(--color-ink-2); }
  .filter-help, .filter-error { margin:0; line-height:1.5; }
  footer { margin-top:8px; padding-top:8px; border-top:1px solid var(--color-edge); }
</style>
