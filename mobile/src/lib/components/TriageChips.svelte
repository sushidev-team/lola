<script lang="ts">
  import { TRIAGE_FILTERS } from "$lib/filters";
  import { kanbanColumn } from "$lib/theme";
  import { overflowFade } from "@mobile/lib/edgefade";
  import TouchButton from "./TouchButton.svelte";
  import type { SessionInfo } from "$lib/store.svelte";

  // The triage filter, as a scrolling strip of chips.
  //
  // The buckets are NOT invented here: TRIAGE_FILTERS is derived from theme.ts's
  // KANBAN_COLUMNS, which is a verbatim port of Go's state.KanbanColumns(), and
  // desktop/state_parity_test.go fails the build when the two drift. A phone-side
  // partition would be a third mirror of a list the repository deliberately keeps
  // in exactly two — which is also why the kanban BOARD is not ported (five
  // horizontally-swiped columns is worse than five chips) while its buckets are.

  let {
    value = $bindable(""),
    sessions,
  }: {
    /** "" means everything. */
    value?: string;
    /** For the per-chip counts. A chip showing 0 is worth seeing: it says the
     *  bucket is empty rather than that the filter is broken. */
    sessions: SessionInfo[];
  } = $props();

  /**
   * Keep the ACTIVE chip on screen.
   *
   * Six buckets are about 610 points of chips, so the strip is always scrolled
   * on a phone — and scrolled to the end, the only chip with a filled
   * background is off to the left, so nothing on the screen says what the list
   * below is filtered to. The chip is scrolled back into view whenever the
   * filter changes, which is the one moment it is certain the user wants to see
   * it. Purely cosmetic: `scrollIntoView` is guarded because jsdom and an
   * older WebView both lack the options overload.
   */
  let strip = $state<HTMLDivElement | undefined>();
  $effect(() => {
    const selected = value;
    const el = strip?.querySelector<HTMLElement>("[data-triage-selected='true']");
    if (!el || typeof el.scrollIntoView !== "function") return;
    void selected;
    try {
      el.scrollIntoView({ block: "nearest", inline: "nearest" });
    } catch {
      /* an implementation without the options overload; the chip stays put */
    }
  });

  const counts = $derived.by(() => {
    const m: Record<string, number> = {};
    for (const s of sessions) {
      const c = kanbanColumn(s.status);
      m[c] = (m[c] ?? 0) + 1;
    }
    return m;
  });
</script>

<!-- ONE ROW, and it scrolls. Six fixed buckets are about 610 points of chips,
     so wrapping cost two or three stacked lines above a list that has none to
     spare on a phone.

     It scrolled once before and was reverted, and the reason is worth keeping:
     with no scrollbar (scrollbar-width: none), no fade and no chevron the strip
     simply ended mid-word on "In Review" with "Done" off-screen, and those
     happened to be the only two buckets holding a session — so every count a
     phone user could see read 0 above a list of two rows. An unselected chip is
     a ghost Button, i.e. bare text with no border, so a sliced one reads as a
     layout bug rather than as more content. `use:overflowFade` is what fixes
     that and what makes one row honest: text that fades out continues, text
     that is chopped is broken — and the fade is dropped at whichever end has
     nothing left to hide, so the last chip is never dimmed for no reason.

     `shrink-0` is not spelled here because Button's own base classes carry it
     along with `whitespace-nowrap`. Without those a flex scroller would squash
     the chips to their min-content width and wrap the labels inside them
     instead of overflowing, which is the failure this markup looks like it
     should have. -->
<div class="shrink-0 border-b border-edge" role="group" aria-label="Filter sessions">
  <div
    bind:this={strip}
    class="flex gap-1.5 overflow-x-auto overscroll-x-contain px-3 py-2 [scrollbar-width:none]"
    use:overflowFade
  >
    <TouchButton
      selected={value === ""}
      data-triage-selected={value === ""}
      onclick={() => (value = "")}
    >
      All <span class="num text-sm opacity-70">{sessions.length}</span>
    </TouchButton>
    {#each TRIAGE_FILTERS as title (title)}
      <TouchButton
        selected={value === title}
        data-triage-selected={value === title}
        onclick={() => (value = value === title ? "" : title)}
      >
        {title} <span class="num text-sm opacity-70">{counts[title] ?? 0}</span>
      </TouchButton>
    {/each}
  </div>
</div>
