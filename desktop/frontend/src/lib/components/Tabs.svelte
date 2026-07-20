<script lang="ts">
  // The tab strip shared by the config overlays (project editor, settings).
  // Purely presentational: the parent owns which tab is active, so a caller can
  // deep-link to one (nav.overlayTab) without this component knowing about nav.
  let {
    tabs,
    active,
    onSelect,
  }: {
    tabs: { id: string; label: string }[];
    active: string;
    onSelect: (id: string) => void;
  } = $props();

  // ←/→ walk the strip, the usual tablist affordance. The handler sits on the
  // buttons (interactive elements) rather than the tablist div.
  function onKey(e: KeyboardEvent, i: number) {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    const step = e.key === "ArrowRight" ? 1 : -1;
    onSelect(tabs[(i + step + tabs.length) % tabs.length].id);
  }
</script>

<div role="tablist" class="mb-3 flex flex-wrap items-center gap-1 border-b border-edge/60">
  {#each tabs as t, i (t.id)}
    <button
      type="button"
      role="tab"
      aria-selected={active === t.id}
      tabindex={active === t.id ? 0 : -1}
      class="-mb-px border-b-2 px-2.5 py-1.5 text-[11px] tracking-wide uppercase transition-colors {active === t.id
        ? 'border-accent text-accent-ink'
        : 'border-transparent text-faint hover:text-ink'}"
      onclick={() => onSelect(t.id)}
      onkeydown={(e) => onKey(e, i)}>{t.label}</button
    >
  {/each}
</div>
