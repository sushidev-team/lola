<script lang="ts">
  import { store, scopedSessions } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { triaged } from "$lib/filters";
  import { isAttention } from "$lib/theme";
  import { reactionNote, reactionIsAlarm } from "$lib/reaction";
  import StatusPill from "./StatusPill.svelte";
  import AgentActivity from "./AgentActivity.svelte";
  import PrBadge from "./PrBadge.svelte";
  import SessionsEmpty from "./SessionsEmpty.svelte";

  let { dense = false }: { dense?: boolean } = $props();

  // Read the store directly here (a leaf component) rather than receiving `rows`
  // from the Cockpit view: the view container does not re-render on the async
  // daemon push in the production WKWebView, so a prop threaded from it stays
  // frozen empty. See WKWEBVIEW_REACTIVITY in Cockpit.svelte.
  //
  // `triaged` wraps the scoped list at EVERY call site (here, AutoSelect,
  // SessionsKanban, TerminalGrid, App's cockpitRows) — otherwise arrow-key
  // movement walks a different list than the one the table renders.
  const rows = $derived(triaged(scopedSessions(store.sessions, nav.scoped, nav.project), nav.triage));

  // Activity gets its own column once there is genuinely room for it, and rides
  // under the title below that. This is a matchMedia query rather than a pair of
  // `hidden 2xl:table-cell` / `2xl:hidden` copies on purpose: the CSS version
  // renders BOTH into every row and lets the stylesheet hide one, which doubles
  // the markup per row and puts the same sentence in the DOM twice. `display:none`
  // does keep the hidden copy out of the accessibility tree, so it was not a
  // correctness bug — but one element that moves is simpler than two that
  // alternate, and it means a query for the text finds exactly one node.
  const WIDE = "(min-width: 1536px)";
  let wide = $state(false);
  $effect(() => {
    const mq = window.matchMedia(WIDE);
    const sync = () => (wide = mq.matches);
    sync();
    mq.addEventListener("change", sync);
    return () => mq.removeEventListener("change", sync);
  });
</script>

<!-- No size class: the table inherits the 13px base from `body`. Only the two
     metadata columns (project, age) and the column heads step away
     from it, so the issue key and title are the row's primary read. -->
<div class="min-w-0">
  <table class="w-full border-separate border-spacing-0">
    <thead class="sticky top-0 bg-panel/90 backdrop-blur">
      <!-- `label` carries the 11px size, the 600 weight, the tracking and the
           uppercasing as one token — never spell those out at a call site. py-2
           lands the head at the same 30px as a body row. -->
      <tr class="label text-left text-faint">
        <th class="w-4 py-2 pl-2"></th>
        <th class="py-2 pr-2">Issue</th>
        {#if !dense}
          <th class="py-2 pr-2">Title</th>
          {#if wide}<th class="py-2 pr-2">Activity</th>{/if}
        {/if}
        <th class="py-2 pr-2">Project</th>
        <th class="py-2 pr-2">Status</th>
        <th class="py-2 pr-2">PR</th>
        <th class="py-2 pr-2 text-right">Age</th>
      </tr>
    </thead>
    <tbody>
      {#each rows as s (s.id)}
        {@const sel = nav.selectedId === s.id}
        {@const note = reactionNote(s.reacting)}
        <!-- The separator is on the CELLS, not the row. The table is
             `border-separate`, and in the separated-borders model the UA must
             ignore border properties on rows — the `border-b` that used to sit
             on this <tr> never painted a single pixel. Backgrounds on a row DO
             paint, which is why the hover / selected bands always worked and
             hid the fact. edge/30 keeps the rule quiet enough that the bands
             still do most of the work. -->
        <tr
          class="cursor-pointer hover:bg-sel/60 [&>td]:border-b [&>td]:border-edge/30"
          class:bg-sel={sel}
          onclick={() => nav.select(s.id)}
          ondblclick={() => nav.toggleFocusTerm(s.id)}
          oncontextmenu={(e) => {
            nav.select(s.id);
            sessionMenu.open(s.id, e);
          }}
        >
          <td class="py-1.5 pl-2 text-center align-middle">
            {#if sel}<span class="font-medium text-accent-ink">›</span>
            {:else if isAttention(s.status) && s.status === "needs_input"}<span class="text-warn">!</span>{/if}
          </td>
          <!-- The row's ONE 500: the issue key is what the row is about. -->
          <td class="py-1.5 pr-2 align-middle font-medium whitespace-nowrap" class:text-accent-ink={sel}>{s.issue || s.id.slice(0, 8)}</td>
          {#if !dense}
            <!-- Primary too, so it reads `text-ink` at the base size; the tier
                 below it is size + colour (12px faint), never faint alone.
                 The interpreter's one-line judgement (or the agent's last
                 notification) rides UNDER the title instead of living in a
                 `title=` tooltip on the status cell: a tooltip is discoverable
                 only by hovering the exact right 60px of the row, so the single
                 most useful thing on screen — what the agent is doing RIGHT NOW —
                 was effectively hidden. It is untrusted, display-only text (see
                 [statusagent]), so it is marked with the same "≈" the pill uses
                 and never styled as fact.

                 Every cell is align-middle, so on a two-line row the single-line
                 columns centre against the pair rather than hanging off the
                 title's first line. -->
            <td class="max-w-[26rem] py-1.5 pr-2 align-middle">
              <!-- min-h is the two-line height (18px title + 16px sub-line), so
                   EVERY row is that tall whether or not it has a second line.
                   Without it the list jitters: a row grows the moment the
                   interpreter produces a headline and shrinks when it clears, so
                   rows shift under the cursor while you are reading them. This
                   cell is the tallest in the row, so it sets the row height and
                   the align-middle cells beside it centre against it.
                   justify-center keeps a single-line title centred in the box. -->
              <!-- Built as a string, not a `class:` directive: `min-h-[34px]` is
                   not a legal class-directive name (the brackets), and Tailwind's
                   scanner needs to see the literal. -->
              <div class="flex flex-col justify-center {wide ? '' : 'min-h-[34px]'}">
                <div class="truncate text-ink">{s.title}</div>
                {#if !wide}<AgentActivity session={s} />{/if}
              </div>
            </td>
            {#if wide}
              <td class="max-w-[24rem] py-1.5 pr-2 align-middle"><AgentActivity session={s} /></td>
            {/if}
          {/if}
          <td class="py-1.5 pr-2 align-middle text-sm whitespace-nowrap text-faint">{store.displayNameFor(s.project)}</td>
          <!-- The reaction posture rides WITH the status instead of in a column
               of its own: it is a qualifier on that status ("ci failed, and lola
               has spent 1 of 2 auto-retries"), not an independent axis. Only the
               two informative postures survive the filter — see $lib/reaction. -->
          <td class="py-1.5 pr-2 align-middle">
            <span class="inline-flex items-center gap-1.5">
              <StatusPill status={s.status} interpreted={s.interpretedState} />
              {#if note}
                <span
                  class="num whitespace-nowrap text-sm {reactionIsAlarm(note) ? 'text-bad' : 'text-faint'}"
                  title={reactionIsAlarm(note)
                    ? "lola has spent its CI retry budget — this needs a human"
                    : "lola is re-prompting the agent to fix CI"}>{note}</span
                >
              {/if}
            </span>
          </td>
          <td class="py-1.5 pr-2 align-middle"><PrBadge session={s} status={s.status} /></td>
          <!-- `num` — the age reflows on every 30s observer push otherwise. -->
          <td class="num py-1.5 pr-2 text-right align-middle text-sm whitespace-nowrap text-faint">{s.age}</td>
        </tr>
      {/each}
    </tbody>
  </table>

  {#if rows.length === 0}
    <SessionsEmpty>
      {#snippet idle()}
        <div class="px-3 py-6 text-center text-faint">no sessions observed</div>
      {/snippet}
    </SessionsEmpty>
  {/if}
</div>
