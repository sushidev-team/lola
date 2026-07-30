<script lang="ts">
  import type { Snippet } from "svelte";

  // The single definition of sidebar row density, so the triage rows and the
  // project rows cannot drift apart. 28px tall (h-7) against 13px type — the
  // generous-row / small-type ratio the Linear sidebar gets its calm from.
  //
  // Active state is `bg-sel` + weight 500 and NOTHING else: no accent bar, no
  // accent text, no larger size. Hierarchy spends at most two of
  // size / weight / colour on one row (see the type map's rule 5).
  //
  // Structure note: the row is a <div> wrapper around a real <button>, not a
  // button carrying the whole row, because the trailing actions are themselves
  // buttons and a nested button is invalid HTML (and unfocusable in WebKit).
  // The wrapper owns the background so hover/active still cover the actions.
  let {
    label,
    count,
    glyph = "",
    glyphCls = "",
    active = false,
    dim = false,
    title,
    onclick,
    badges,
    actions,
  }: {
    label: string;
    /** Rendered even when 0 — a nav whose counts appear and vanish is jumpy. */
    count?: number;
    /** ≤1-char leading glyph. The slot is always reserved so labels line up. */
    glyph?: string;
    glyphCls?: string;
    active?: boolean;
    /** Quieten the label (a project whose polls are off), never the whole row. */
    dim?: boolean;
    title?: string;
    onclick: () => void;
    /** Always-visible trailing content (counts/badges that must not hide). */
    badges?: Snippet;
    /** Trailing controls revealed on row hover / keyboard focus. */
    actions?: Snippet;
  } = $props();

  // Built as a string rather than `class:` directives: `hover:bg-sel/60` is not
  // a legal class-directive name, and Tailwind's scanner sees the literal here.
  //
  // Active spends THREE signals, not two: band + weight + foreground. `sel` on
  // `canvas` measures 1.40:1 on mocha and only 1.27:1 on latte, so on the light
  // flavor the band is very nearly invisible and a 400→500 weight step on 13px
  // text was carrying the entire selected state on its own. The `text-ink` /
  // `text-faint` split is the one signal that survives both flavors.
  const rowCls = $derived(
    "group/row flex h-7 w-full items-center rounded-md transition-colors " +
      (active ? "bg-sel text-ink" : "text-faint hover:bg-sel/60 hover:text-ink"),
  );
</script>

<div class={rowCls} {title}>
  <button
    class="flex h-full min-w-0 grow items-center gap-2 rounded-md px-2 text-left"
    aria-current={active ? "true" : undefined}
    {onclick}
  >
    <span class="w-3.5 shrink-0 text-center text-sm {glyphCls}" aria-hidden="true">{glyph}</span>
    <span class="truncate" class:font-medium={active} class:text-faint={dim && !active}>{label}</span>
    {#if count !== undefined}
      <span class="num ml-auto shrink-0 text-sm text-faint">{count}</span>
    {/if}
  </button>
  {#if badges}
    <span class="flex shrink-0 items-center gap-1 pr-1">{@render badges()}</span>
  {/if}
  {#if actions}
    <span
      class="flex shrink-0 items-center pr-1 opacity-0 transition-opacity group-hover/row:opacity-100 focus-within:opacity-100"
      >{@render actions()}</span
    >
  {/if}
</div>
