<script lang="ts">
  // The "{ }" button on a field that accepts variables, and its popover: a
  // filter box over grouped rows, each naming a variable and the exact text it
  // inserts. Picking a row hands that text to the field; nothing is typed here.
  //
  // The popover is portaled to <body> and fixed, like the git action menu: the
  // project form's body scrolls and its shell clips, so an in-flow popover
  // would be cut off at the dialog's edge.
  import { tick, untrack } from "svelte";
  import Button from "./Button.svelte";
  import Icon from "./Icon.svelte";
  import MenuItem from "./MenuItem.svelte";
  import type { VariableGroup } from "$lib/fieldVariables";

  let {
    label,
    groups,
    onpick,
    class: klass = "",
  }: {
    /** The field's caption, for the accessible names. */
    label: string;
    groups: VariableGroup[];
    onpick: (token: string) => void;
    class?: string;
  } = $props();

  let open = $state(false);
  let query = $state("");
  let anchor = $state<HTMLSpanElement>();
  let menu = $state<HTMLDivElement>();
  let search = $state<HTMLInputElement>();

  const shown = $derived.by(() => {
    const q = query.trim().toLowerCase();
    if (!q) return groups;
    return groups
      .map((g) => ({ ...g, items: g.items.filter((v) => `${v.label} ${v.token}`.toLowerCase().includes(q)) }))
      .filter((g) => g.items.length > 0);
  });

  // Right-aligned under the button, flipped above it when the space below is
  // short, and kept inside the window either way.
  $effect(() => {
    const node = menu, from = anchor;
    if (!open || !node || !from) return;
    untrack(() => {
      const a = from.getBoundingClientRect();
      const m = node.getBoundingClientRect();
      const below = a.bottom + 4;
      const top = below + m.height > innerHeight - 6 ? a.top - 4 - m.height : below;
      node.style.left = `${Math.max(6, Math.min(a.right - m.width, innerWidth - m.width - 6))}px`;
      node.style.top = `${Math.max(6, top)}px`;
      void tick().then(() => search?.focus());
    });
  });

  function close(refocus: boolean) {
    open = false;
    query = "";
    if (refocus) anchor?.querySelector("button")?.focus();
  }

  function pick(token: string) {
    close(false);
    onpick(token);
  }

  function rows(): HTMLButtonElement[] {
    return Array.from(menu?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? []);
  }

  // Every key stops here: the form underneath owns Escape (it would close the
  // whole overlay) and the cockpit owns bare keys.
  function key(event: KeyboardEvent) {
    event.stopPropagation();
    if (event.key === "Escape" || event.key === "Tab") {
      event.preventDefault();
      close(true);
      return;
    }
    const items = rows();
    const inSearch = document.activeElement === search;
    if (event.key === "Enter" && inSearch) {
      event.preventDefault();
      items[0]?.click();
      return;
    }
    if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
    event.preventDefault();
    const index = items.indexOf(document.activeElement as HTMLButtonElement);
    const next = index + (event.key === "ArrowDown" ? 1 : -1);
    // Up from the first row returns to the filter box.
    if (next < 0 || !items.length) search?.focus();
    else items[Math.min(next, items.length - 1)].focus();
  }

  function portal(node: HTMLDivElement) {
    document.body.appendChild(node);
    return { destroy: () => node.remove() };
  }
</script>

<svelte:window onresize={() => open && close(false)} />

<span bind:this={anchor} class={klass}>
  <Button icon size="xs" aria-label={`Insert variable into ${label}`} aria-haspopup="menu" aria-expanded={open}
    title="Insert variable" selected={open} onclick={() => { open = !open; }}>
    <Icon name="braces" size={14} />
  </Button>
</span>

{#if open}
  <div use:portal>
    <div class="fixed inset-0 z-[80]" role="presentation" onclick={() => close(false)}></div>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div bind:this={menu} onkeydown={key}
      class="fixed z-[81] flex max-h-80 w-72 max-w-[calc(100vw-12px)] flex-col overflow-hidden rounded-lg border border-edge bg-panel text-ink shadow-2xl">
      <label class="flex items-center gap-2 border-b border-edge px-2.5 py-2 text-faint">
        <Icon name="search" size={14} />
        <input bind:this={search} bind:value={query} spellcheck="false" autocomplete="off"
          aria-label={`Search variables for ${label}`} placeholder="Search variables…"
          class="min-w-0 flex-1 bg-transparent text-ink outline-none placeholder:text-placeholder" />
      </label>
      <div role="menu" aria-label={`Variables for ${label}`} class="min-h-0 flex-1 overflow-y-auto p-1">
        {#each shown as group, i (group.title)}
          <div role="group" aria-label={group.title} class={i > 0 ? "mt-1 border-t border-edge pt-1" : ""}>
            <div class="label px-2 pt-1.5 pb-1 text-xs text-faint">{group.title}</div>
            {#if group.note}<p class="px-2 pb-1 text-sm text-faint">{group.note}</p>{/if}
            {#each group.items as v (v.token)}
              <MenuItem onclick={() => pick(v.token)}>
                <span class="block truncate">{v.label}</span>
                <code class="block truncate font-mono text-sm text-faint">{v.token}</code>
              </MenuItem>
            {/each}
          </div>
        {:else}
          <p class="px-2 py-3 text-sm text-faint">No variable matches.</p>
        {/each}
      </div>
    </div>
  </div>
{/if}
