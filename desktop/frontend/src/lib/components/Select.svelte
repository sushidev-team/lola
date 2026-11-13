<script lang="ts">
  import { tick } from 'svelte';
  import type { Snippet } from 'svelte';
  import type { HTMLSelectAttributes } from 'svelte/elements';
  import Icon from './Icon.svelte';

  // Keep the native field for labels, form submission, validation, bindings and
  // existing change handlers. Only its OS popup is replaced by our listbox.
  let { children, class: klass = '', value = $bindable(), minMenuWidth = 160, ...rest }:
    { children: Snippet; class?: string; value?: any; minMenuWidth?: number } & HTMLSelectAttributes = $props();
  const uid = $props.id();
  let field = $state<HTMLSelectElement>();
  let menu = $state<HTMLDivElement>();
  let open = $state(false);
  let options = $state<{ index: number; label: string; selectedLabel: string; description: string; disabled: boolean; hidden: boolean; group: string }[]>([]);
  let active = $state(-1);
  let selected = $state(-1);
  let position = $state({ left: 0, top: 0, width: 0, height: 280 });
  let search = '';
  let searchedAt = 0;

  function sync() {
    if (!field) return;
    options = Array.from(field.options, (option, index) => {
      const group = option.parentElement instanceof HTMLOptGroupElement ? option.parentElement : null;
      return { index, label: option.label, selectedLabel: option.dataset.selectedLabel || option.label, description: option.dataset.description || '', disabled: option.disabled || !!group?.disabled, hidden: option.hidden, group: group?.label ?? '' };
    });
    selected = field.selectedIndex;
  }
  $effect(() => {
    const node = field;
    if (!node) return;
    sync();
    const observer = new MutationObserver(sync);
    observer.observe(node, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ['value', 'label', 'disabled', 'hidden', 'selected', 'data-description', 'data-selected-label'] });
    return () => observer.disconnect();
  });
  $effect(() => { value; if (field) void tick().then(sync); });
  $effect(() => {
    // Expose one option list at a time, retaining native closed-field semantics.
    for (const option of Array.from(field?.options ?? [])) {
      if (open) option.setAttribute('aria-hidden', 'true');
      else option.removeAttribute('aria-hidden');
    }
  });
  $effect(() => { if (rest.disabled) open = false; });

  function place() {
    if (!field) return;
    const rect = field.getBoundingClientRect();
    const width = Math.min(Math.max(rect.width, minMenuWidth), window.innerWidth - 16);
    const below = window.innerHeight - rect.bottom - 8;
    const above = rect.top - 8;
    const desired = Math.min(288, options.filter(o => !o.hidden).reduce((height, option) => height + (option.description ? 52 : 32), 10));
    const up = below < desired && above > below;
    const height = Math.max(40, Math.min(desired, up ? above : below));
    position = { left: Math.max(8, Math.min(rect.left, window.innerWidth - width - 8)), top: up ? rect.top - height - 4 : rect.bottom + 4, width, height };
  }
  async function reveal() {
    await tick();
    menu?.querySelector<HTMLElement>(`[data-option-index="${active}"]`)?.scrollIntoView?.({ block: 'nearest' });
  }
  function show() {
    if (!field || field.matches(':disabled')) return;
    sync(); search = '';
    active = options.find(o => o.index === field?.selectedIndex && !o.disabled && !o.hidden)?.index ?? options.find(o => !o.disabled && !o.hidden)?.index ?? -1;
    place(); open = true; void reveal();
  }
  function choose(index: number) {
    const option = field?.options[index];
    if (!field || !option || options[index]?.disabled || option.hidden) return;
    field.selectedIndex = index;
    value = '__value' in option ? option.__value : option.value;
    selected = index; open = false;
    field.dispatchEvent(new Event('input', { bubbles: true }));
    field.dispatchEvent(new Event('change', { bubbles: true }));
    field.focus();
  }
  function move(direction: number, edge?: 'first' | 'last') {
    const enabled = options.filter(o => !o.disabled && !o.hidden);
    if (!enabled.length) return;
    const at = enabled.findIndex(o => o.index === active);
    active = enabled[edge === 'first' ? 0 : edge === 'last' ? enabled.length - 1 : (at + direction + enabled.length) % enabled.length].index;
    void reveal();
  }
  function key(event: KeyboardEvent & { currentTarget: HTMLSelectElement }) {
    rest.onkeydown?.(event);
    if (event.defaultPrevented) return;
    if (event.key === 'Tab') { if (open) choose(active); return; }
    if (event.key === 'Escape' && open) { event.preventDefault(); event.stopPropagation(); open = false; return; }
    if (['ArrowDown', 'ArrowUp', 'Home', 'End', 'Enter', ' '].includes(event.key)) {
      event.preventDefault(); event.stopPropagation();
      if (!open) { show(); if (event.key === 'Home' || event.key === 'End') move(0, event.key === 'Home' ? 'first' : 'last'); return; }
      if (event.key === 'Enter' || event.key === ' ') choose(active);
      else move(event.key === 'ArrowUp' ? -1 : 1, event.key === 'Home' ? 'first' : event.key === 'End' ? 'last' : undefined);
      return;
    }
    if (event.key.length === 1 && !event.metaKey && !event.ctrlKey && !event.altKey) {
      event.preventDefault(); event.stopPropagation();
      sync();
      const now = Date.now(); search = now - searchedAt > 700 ? event.key : search + event.key; searchedAt = now;
      const term = search.toLocaleLowerCase();
      const repeated = [...term].every(c => c === term[0]);
      const start = open ? active : selected;
      const ordered = [...options.slice(start + 1), ...options.slice(0, start + 1)];
      const match = ordered.find(o => !o.disabled && !o.hidden && o.label.toLocaleLowerCase().startsWith(repeated ? term[0] : term));
      if (match) { if (open) { active = match.index; void reveal(); } else choose(match.index); }
    }
  }
  function portal(node: HTMLDivElement) {
    document.body.appendChild(node);
    return { destroy: () => node.remove() };
  }
  $effect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => { if (!field?.contains(event.target as Node) && !menu?.contains(event.target as Node)) open = false; };
    const scroll = (event: Event) => { if (!menu?.contains(event.target as Node)) open = false; };
    document.addEventListener('pointerdown', outside, true);
    window.addEventListener('scroll', scroll, true);
    window.addEventListener('resize', place);
    return () => { document.removeEventListener('pointerdown', outside, true); window.removeEventListener('scroll', scroll, true); window.removeEventListener('resize', place); };
  });
</script>

<span class="lola-select {klass}" class:has-description={!!options[selected]?.description}>
  <select {...rest} bind:this={field} bind:value class="lola-field appearance-none w-full pr-8" aria-haspopup="listbox" aria-expanded={open} aria-controls={open ? `${uid}-list` : undefined} aria-activedescendant={open && active >= 0 ? `${uid}-${active}` : undefined}
    onmousedown={(event) => { if (event.button === 0) { event.preventDefault(); field?.focus(); } rest.onmousedown?.(event); }}
    onclick={(event) => { event.preventDefault(); if (open) open = false; else show(); rest.onclick?.(event); }}
    onkeydown={key} onblur={(event) => { open = false; rest.onblur?.(event); }}>
    {@render children()}
  </select>
  {#if options[selected]?.description}<span class="select-value" aria-hidden="true"><span>{options[selected].selectedLabel}</span></span>{/if}
  <Icon name="chevron-down" class="lola-select-caret pointer-events-none" size={14} />
</span>

{#if open}
  <div use:portal bind:this={menu} id={`${uid}-list`} role="listbox" aria-label={rest['aria-label'] || field?.labels?.[0]?.textContent?.trim() || 'Options'} class="lola-select-menu" style={`left:${position.left}px;top:${position.top}px;width:${position.width}px;max-height:${position.height}px`}>
    {#each options as option, i (option.index)}
      {#if !option.hidden}
        {#if option.group && (i === 0 || options[i - 1].group !== option.group)}<div class="lola-select-group">{option.group}</div>{/if}
        <button type="button" tabindex="-1" role="option" id={`${uid}-${option.index}`} aria-selected={selected === option.index} aria-disabled={option.disabled} data-option-index={option.index} class="lola-select-option" class:highlighted={active === option.index}
          onpointermove={() => { if (!option.disabled) active = option.index; }}
          onmousedown={(event) => event.preventDefault()} onclick={() => choose(option.index)}>
          <span class="lola-select-check">{#if selected === option.index}<Icon name="check" size={14} />{/if}</span>
          <span class="option-copy"><span>{option.label}</span>{#if option.description}<small>{option.description}</small>{/if}</span>
        </button>
      {/if}
    {/each}
  </div>
{/if}

<style>
  .has-description select { color:transparent; -webkit-text-fill-color:transparent; }
  .select-value { position:absolute; inset:0 32px 0 12px; pointer-events:none; display:grid; align-content:center; gap:2px; font-size:13px; color:var(--color-ink); }
  .select-value > span { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .option-copy small { display:block; font-size:11px; line-height:16px; color:var(--color-ink-2); }
  .option-copy { display:grid; gap:0; min-width:0; }
</style>
