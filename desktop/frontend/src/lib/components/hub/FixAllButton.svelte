<script lang="ts">
  // "Fix all N" with a menu half: the main part claims a group's chores (its
  // advisories for one agent, each failing bump on its own); the arrow offers
  // the wider "Update all dependencies…", which needs at least one advisory.
  import { tick } from "svelte";
  import Button from "$lib/components/Button.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import MenuItem from "$lib/components/MenuItem.svelte";

  let {
    label,
    title = "",
    disabled = false,
    loading = false,
    updateDisabled = false,
    onFix,
    onUpdateAll,
  }: {
    label: string;
    title?: string;
    disabled?: boolean;
    loading?: boolean;
    /** No advisory to update around: the menu entry says so instead of failing later. */
    updateDisabled?: boolean;
    onFix: () => void;
    onUpdateAll: () => void;
  } = $props();

  let open = $state(false);
  let root = $state<HTMLDivElement>();
  let menu = $state<HTMLDivElement>();
  let trigger = $state<HTMLButtonElement>();
  const id = $props.id();

  async function toggle(e: MouseEvent) {
    trigger = e.currentTarget as HTMLButtonElement;
    open = !open;
    if (open) {
      await tick();
      menu?.querySelector<HTMLElement>('[role="menuitem"]')?.focus();
    }
  }
  function close(focus = false) {
    open = false;
    if (focus) trigger?.focus();
  }
  function onKey(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close(true);
    } else if (e.key === "Tab") {
      close();
    }
  }
</script>

<svelte:window onpointerdown={(e) => { if (open && root && !root.contains(e.target as Node)) close(); }} />

<div class="relative flex items-stretch" bind:this={root}>
  <Button variant="primary" size="sm" class="rounded-r-none!" {disabled} {loading} {title} onclick={onFix}>{label}</Button>
  <Button variant="primary" size="sm" icon class="rounded-l-none! border-l! border-canvas/60!" {disabled}
    aria-label="More ways to fix" aria-haspopup="menu" aria-expanded={open} aria-controls={`${id}-menu`} onclick={toggle}>
    <Icon name="chevron-down" size={14} />
  </Button>
  {#if open}
    <div bind:this={menu} id={`${id}-menu`} role="menu" tabindex="-1" aria-label="More ways to fix"
      class="absolute right-0 top-full z-50 mt-1 w-60 rounded-lg border border-edge bg-panel p-1 shadow-xl" onkeydown={onKey}>
      <MenuItem disabled={updateDisabled} onclick={() => { close(); onUpdateAll(); }}>Update all dependencies…</MenuItem>
      {#if updateDisabled}<p class="px-2 pb-1 text-sm text-faint">Needs at least one open security advisory.</p>{/if}
    </div>
  {/if}
</div>
