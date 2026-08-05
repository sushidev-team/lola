<script lang="ts" module>
  // A row in a popover menu (the session context menu). Not a <Button>: a menu
  // item is full-bleed, left-aligned, carries role="menuitem", and reserves a
  // leading glyph column so labels line up whether or not an item has an icon.
  // It shares the button's hover language — a `bg-sel` chip on approach — so the
  // two read as one system.
  export type MenuItemVariant = "default" | "accent" | "danger";

  const VARIANTS: Record<MenuItemVariant, string> = {
    default: "text-ink enabled:hover:bg-sel",
    accent: "text-info enabled:hover:bg-sel enabled:hover:text-accent-ink",
    danger: "text-faint enabled:hover:bg-bad/15 enabled:hover:text-bad",
  };
</script>

<script lang="ts">
  import type { Snippet } from "svelte";
  import type { HTMLButtonAttributes } from "svelte/elements";

  let {
    variant = "default",
    /** ≤1-char leading glyph. The column is reserved even when this is empty. */
    icon = "",
    /** Trailing affordance (↗ for "leaves the app", ⌘-chords, …). */
    trailing = "",
    children,
    ...rest
  }: {
    variant?: MenuItemVariant;
    icon?: string;
    trailing?: string;
    children: Snippet;
  } & HTMLButtonAttributes = $props();
</script>

<button
  type="button"
  role="menuitem"
  {...rest}
  class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-40 {VARIANTS[
    variant
  ]}"
>
  <span class="w-3.5 shrink-0 text-center text-sm text-faint" aria-hidden="true">{icon}</span>
  <span class="min-w-0 flex-1 truncate">{@render children()}</span>
  {#if trailing}<span class="shrink-0 text-sm text-faint" aria-hidden="true">{trailing}</span>{/if}
</button>
