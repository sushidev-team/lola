<script lang="ts">
  // The tab strip shared by the config overlays (project editor, settings).
  // Purely presentational: the parent owns which tab is active, so a caller can
  // deep-link to one (nav.overlayTab) without this component knowing about nav.
  let {
    tabs,
    active,
    onSelect,
    vertical = false,
  }: {
    tabs: { id: string; label: string; group?: string }[];
    vertical?: boolean;
    active: string;
    onSelect: (id: string) => void;
  } = $props();

  // The button elements, so arrow-key selection can move focus onto the newly
  // active tab (the roving-tabindex tablist pattern). $state so `bind:this` into
  // the array is a reactive write (Svelte warns otherwise).
  let btns: HTMLButtonElement[] = $state([]);

  // ←/→ walk the strip, the usual tablist affordance. The handler sits on the
  // buttons (interactive elements) rather than the tablist div.
  function onKey(e: KeyboardEvent, i: number) {
    const previous = vertical ? "ArrowUp" : "ArrowLeft";
    const forward = vertical ? "ArrowDown" : "ArrowRight";
    if (![previous, forward, "Home", "End"].includes(e.key)) return;
    e.preventDefault();
    const step = e.key === forward ? 1 : -1;
    const next = e.key === "Home" ? 0 : e.key === "End" ? tabs.length - 1 : (i + step + tabs.length) % tabs.length;
    onSelect(tabs[next].id);
    // Keep keyboard focus with the selection so the next arrow keeps walking.
    btns[next]?.focus();
  }
</script>

<div role="tablist" aria-orientation={vertical ? "vertical" : "horizontal"} class={vertical ? "space-y-1" : "mb-3 flex flex-wrap items-center gap-1 border-b border-edge/60"}>
  {#each tabs as t, i (t.id)}
    {#if vertical && t.group && t.group !== tabs[i - 1]?.group}
      <div class="label px-2.5 pt-4 pb-1 text-faint first:pt-0">{t.group}</div>
    {/if}
    <button
      bind:this={btns[i]}
      type="button"
      role="tab"
      aria-selected={active === t.id}
      tabindex={active === t.id ? 0 : -1}
      class="px-2.5 py-2 transition-colors {vertical ? 'w-full rounded-md text-left' : '-mb-px border-b-2'} {active === t.id
        ? (vertical ? 'bg-sel font-medium text-accent-ink' : 'border-accent font-medium text-accent-ink')
        : 'border-transparent text-faint hover:text-ink'}"
      onclick={() => onSelect(t.id)}
      onkeydown={(e) => onKey(e, i)}>{t.label}</button
    >
  {/each}
</div>
