<script lang="ts">
  import { store, scopedSessions } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { triaged, triageOf } from "$lib/filters";
  import { KANBAN_COLUMNS, attention, displayFor, displayLabel, displayText } from "$lib/theme";
  import PrBadge from "./PrBadge.svelte";
  import SessionsEmpty from "./SessionsEmpty.svelte";

  // Reads the store directly (leaf component) — the Cockpit view can't pass live
  // rows in the production WKWebView. See WKWEBVIEW_REACTIVITY in Cockpit.svelte.
  // Triaged like every other session surface. A triage filter and the board are
  // the same partition (TRIAGE_FILTERS is derived from KANBAN_COLUMNS), so an
  // active filter simply leaves the other columns empty.
  const rows = $derived(triaged(scopedSessions(store.sessions, nav.scoped, nav.project), nav.triage));
  // Membership is a function of BOTH axes (state.KanbanKeyFor), not of a list of
  // collapsed status words: a column set could not express "working agent, red
  // CI" landing in Fixing while its agent axis stays visible on the card.
  // `triageOf` is the same predicate the sidebar filter and the list use, so an
  // active filter still simply leaves the other columns empty.
  const cols = $derived(
    KANBAN_COLUMNS.map((c) => ({
      title: c.title,
      items: rows.filter((s) => triageOf(s) === c.title),
    })),
  );
</script>

{#if store.connected && store.alive}
  <div class="flex h-full min-h-0 overflow-x-auto py-2">
    {#each cols as col (col.title)}
      <!-- Columns are divided by a hairline rather than by a gap: with the panel
           gone the board is a boxed grid, and a rule is what tells two adjacent
           empty columns apart. `last:border-r-0` so the rightmost one doesn't
           draw a second edge against the window.
           min-h-0 so the card list below can bound itself and scroll rather than
           stretching the column past the band and clipping its bottom cards. -->
      <div class="flex min-h-0 min-w-[13rem] flex-1 flex-col border-r border-edge/40 px-3 last:border-r-0">
        <!-- `label` — the app's column-head level (same as the table's <thead>),
             not a section title. At text-lg these five heads were the largest type
             on a screen whose actual content is the cards under them, and the
             board read as five headlines over some fine print. Uppercase + 600 +
             tracking still out-ranks a card at half the visual weight. -->
        <div class="mb-1.5 flex items-baseline gap-1.5 border-b border-edge/60 pb-2">
          <span class="label text-faint">{col.title}</span>
          <span class="num text-sm text-faint">{col.items.length}</span>
        </div>
        <!-- min-h-0 flex-1: a tall column scrolls inside itself; without them it
             overflows the panel and the last cards are cut off. -->
        <div class="flex min-h-0 flex-1 flex-col gap-1 overflow-auto">
          {#each col.items as s (s.id)}
            {@const sel = nav.selectedId === s.id}
            <button
              class="rounded border px-2.5 py-1.5 text-left transition-colors hover:border-accent/60"
              class:border-accent={sel}
              class:border-edge={!sel}
              class:bg-sel={sel}
              onclick={() => nav.select(s.id)}
              ondblclick={() => nav.toggleFocusTerm(s.id)}
              oncontextmenu={(e) => {
                nav.select(s.id);
                sessionMenu.open(s.id, e);
              }}
            >
              <!-- The TITLE is the card's primary read, not the issue key. The
                   two were inverted: a 13px medium accent key over a 12px faint
                   title made every card lead with a string you cannot skim
                   ("NOR-343" tells you nothing about the work) while the sentence
                   that identifies it receded. Now the key is metadata — 12px,
                   faint, above — and the title carries the base size and `ink`.
                   Selection still tints the key, since that is the row's stable
                   identifier and the title colour is already spent. -->
              <div class="flex items-center gap-1.5 text-sm">
                <!-- The marker covers the WHOLE attention predicate. It used to
                     fire on needs_input alone, which left the three delivery
                     regressions — red CI, requested changes, a conflict — with no
                     mark on any surface even though they were in the attention
                     set from the start. -->
                {#if attention(s.agentState, s.delivery) && !sel}<span
                    class={s.agentState === "waiting_input" ? "text-warn" : "text-bad"}>!</span
                  >{/if}
                <span class="num" class:text-accent-ink={sel} class:text-faint={!sel}
                  >{s.issue || s.id.slice(0, 8)}</span
                >
                <!-- The AGENT axis, in words. It was the two-letter status glyph
                     ("!x", "rv") ported from the TUI, where a column is precious;
                     a card is not a column, and the Display vocabulary is six
                     words long, so it fits as itself.
                     `label`, not mono: a state is a word, not an identifier. -->
                <span class="label ml-auto {displayText(displayFor(s.agentState))}"
                  >{displayLabel(displayFor(s.agentState))}</span
                >
              </div>
              {#if s.title}<div class="truncate text-ink">{s.title}</div>{/if}
              <!-- No `onOpen`: this card IS a <button>, and a nested button is
                   not parseable — the parser closes the outer one and the card
                   stops being clickable. -->
              <div class="mt-0.5"><PrBadge session={s} delivery={s.delivery} status={s.status} /></div>
            </button>
          {/each}
        </div>
      </div>
    {/each}
  </div>
{:else}
  <!-- Empty columns look identical whether the daemon is dead or connecting, so
       hand off to the shared placeholder rather than showing a silent board. -->
  <SessionsEmpty />
{/if}
