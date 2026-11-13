<script lang="ts" module>
  // The triage filter as a permanent rail under the header — the redesign's
  // replacement for a strip that lived inside a sheet.
  //
  // WHY IT CAME BACK OUT OF THE SHEET. <FilterSheet>'s own comment argues that
  // the chips and the search field together took a fifth of a phone screen above
  // a list that usually holds three sessions, and that filtering is the rare
  // action while reading the list is what the screen is for. Half of that is
  // still true and half of it is not, and the design splits them: the SEARCH
  // field is the rare action and stays behind the button, while the buckets are
  // now what the list is ARRANGED by — every row on the screen sits under a
  // section heading naming its bucket — so a rail of buckets is a table of
  // contents rather than a control panel. It costs one 50pt row, which is the
  // price of not having to open a sheet to answer "is anything waiting on me".
  //
  // NOTHING HERE NAMES A BUCKET. KANBAN_COLUMNS is theme.ts's port of Go's
  // state.KanbanColumns(), pinned key-for-key and title-for-title by
  // desktop/state_parity_test.go, and the membership question is `triageOf` from
  // $lib/filters — the SAME function `triaged` filters the list with. That is
  // the property worth protecting: a chip counting three sessions above a
  // section holding two is a filter nobody can trust again.

  /**
   * The chip's two states, as LITERAL class strings.
   *
   * Rule 4 of the brief and the Button invariant in CLAUDE.md: Tailwind v4
   * scans source TEXT, so a composed `bg-${state}` compiles to nothing and the
   * chip renders as bare text with no ground and no border — silently, with no
   * error anywhere.
   *
   * The unselected chip carries `edge-soft` and the selected one plain `edge`,
   * which is the one place on this screen the heavier border survives: a
   * selected chip IS a small panel, and at 1px the difference is what separates
   * "this is the filter" from "these are the filters".
   */
  const CHIP = {
    on: "bg-sel border-edge text-ink",
    off: "bg-panel border-edge-soft text-subtext",
  } as const;

  /**
   * The count badge's ground. The attention bucket is the only one that gets
   * the loud pill; every other count is a number, not news.
   *
   * "The attention bucket" is `needs` alone rather than theme.ts's
   * ATTENTION_STATUSES (which also spans the broken family, i.e. `fixing`),
   * because the design draws exactly one loud count and the two buckets are
   * different questions: `needs` is nothing-happens-until-you-act, `fixing` is
   * lola is already reacting. The dot before the label still separates them —
   * orange against bad — so nothing is lost by keeping one badge quiet.
   */
  const COUNT = {
    urgent: "bg-pill-urgent text-pill-urgent-fg",
    quiet: "bg-pill-grey text-pill-grey-fg",
  } as const;
</script>

<script lang="ts">
  import { KANBAN_COLUMNS, kanbanDotText } from "$lib/theme";
  import { triageOf } from "$lib/filters";
  import { overflowFade } from "@mobile/lib/edgefade";
  import StatusDot from "./StatusDot.svelte";
  import type { SessionInfo } from "$lib/store.svelte";

  let {
    /** The selected bucket title, or "" for everything. Bound to nav.triage. */
    value = $bindable(""),
    /**
     * Every session in scope, for the per-chip counts. A chip showing 0 is
     * worth drawing: it says the bucket is empty rather than that the filter is
     * broken — and with the list now partitioned by these same buckets, a 0
     * chip is also the only thing on screen saying a section is missing because
     * it holds nothing.
     */
    sessions,
  }: { value?: string; sessions: SessionInfo[] } = $props();

  const counts = $derived.by(() => {
    const m: Record<string, number> = {};
    for (const s of sessions) {
      const t = triageOf(s);
      m[t] = (m[t] ?? 0) + 1;
    }
    return m;
  });

  /**
   * Keep the SELECTED chip on screen.
   *
   * Six chips are wider than any phone, so the rail is always scrolled — and
   * scrolled to the right, the one chip with a filled ground is off to the left
   * and nothing visible says what the list below is narrowed to. It is scrolled
   * back whenever the selection changes, which is the one moment it is certain
   * somebody wants to see it. Purely cosmetic, and guarded: jsdom and an older
   * WebView both lack the options overload, and a missing scroll must never
   * throw inside an effect.
   */
  let strip = $state<HTMLDivElement | undefined>();
  $effect(() => {
    const selected = value;
    const el = strip?.querySelector<HTMLElement>("[data-rail-selected='true']");
    if (!el || typeof el.scrollIntoView !== "function") return;
    void selected;
    try {
      el.scrollIntoView({ block: "nearest", inline: "nearest" });
    } catch {
      /* no options overload; the chip stays where it is */
    }
  });

  /** Tapping the selected chip clears the filter, which is what a toggle does. */
  function pick(title: string): void {
    value = value === title ? "" : title;
  }
</script>

<!-- THE BUTTON IS 50pt TALL AND THE CHIP INSIDE IT IS 33, which is the whole
     reason the chip is a <span> rather than the control itself. The design draws
     a chip of 13px text with 7px of padding and a hairline — 33 points — and
     rule 3 of the brief is 44. Growing the chip to 44 would make a filter row
     taller than the card titles beneath it; so the button owns the rail's full
     height, `items-start` + `pt-1` puts the chip at the design's offset, and the
     13 points below it are invisible padding a thumb can still land on. It is
     the same trade <MetaPill> makes for a tappable PR badge.

     `aria-pressed` rather than a `selected` styling prop: these are toggles, and
     a screen reader that cannot see the filled ground gets the state from the
     role. It is also what keeps these chips distinguishable from the identical
     ones still inside <FilterSheet> — see the note in Sessions.svelte about the
     two surfaces offering the same filter. -->
<div class="h-[50px] shrink-0" role="group" aria-label="Filter sessions by bucket">
  <div
    bind:this={strip}
    class="flex h-full gap-2 overflow-x-auto overscroll-x-contain px-5 [scrollbar-width:none]"
    use:overflowFade
  >
    <!-- "All" carries no count and no dot, and that is the design's `px-3` bare
         chip. The total is already the second thing the header says ("7
         sessions"), so a badge here would be the same number twice, 30 points
         apart. -->
    <button
      type="button"
      class="flex h-full min-w-11 shrink-0 touch-manipulation items-start justify-center pt-1"
      aria-pressed={value === ""}
      data-rail-selected={value === ""}
      onclick={() => (value = "")}
    >
      <span
        class="inline-flex items-center rounded-full border px-3 py-[7px] text-base font-medium
               {value === '' ? CHIP.on : CHIP.off}"
      >
        All
      </span>
    </button>

    {#each KANBAN_COLUMNS as column (column.key)}
      {@const on = value === column.title}
      <button
        type="button"
        class="flex h-full min-w-11 shrink-0 touch-manipulation items-start justify-center pt-1"
        aria-pressed={on}
        data-rail-selected={on}
        onclick={() => pick(column.title)}
      >
        <!-- `pl-3 pr-2`, lopsided on purpose: the count badge carries its own
             1.5 of padding, so an equal 3 either side leaves it visibly further
             from the chip's edge than the label is from the other. -->
        <span
          class="inline-flex items-center gap-1.5 rounded-full border py-[7px] pr-2 pl-3
                 text-base font-medium {on ? CHIP.on : CHIP.off}"
        >
          <!-- The bucket's colour, from theme.ts's own table — the same one the
               desktop's sidebar rows read, so the board and the rail can never
               disagree about what "Fixing" is coloured. It is passed as a whole
               literal class because that is the only shape <StatusDot> can
               accept safely (rule 4 again). -->
          <StatusDot size={5} tone={kanbanDotText(column.key)} />
          {column.title}
          <span
            class="num rounded-full px-1.5 py-px text-sm font-bold
                   {column.key === 'needs' ? COUNT.urgent : COUNT.quiet}"
          >
            {counts[column.title] ?? 0}
          </span>
        </span>
      </button>
    {/each}
  </div>
</div>
