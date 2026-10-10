<script lang="ts">
  import { store, scopedSessions } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { triaged } from "$lib/filters";
  import { displayFor, inputReasonLabel } from "$lib/theme";
  import AgentActivity from "./AgentActivity.svelte";
  import AgentGlyph from "./AgentGlyph.svelte";
  import StageChip from "./StageChip.svelte";
  import BoardChip from "./BoardChip.svelte";
  import ClaimFlag from "./ClaimFlag.svelte";
  import Button from "./Button.svelte";
  import SessionsEmpty from "./SessionsEmpty.svelte";
  import { chipRelevant } from "$lib/board";
  import UsageMark from "./UsageMark.svelte";

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
  // The Tokens column appears only once some listed session has a figure, so a
  // fleet running only codex/opencode keeps its columns.
  const anyCost = $derived(rows.some((s) => !!s.usage));

  // The row reads left to right as one sentence about the session:
  //
  //   [glyph] ISSUE  Title / activity  Project  [#PR  Stage chip]  Age
  //
  // The AGENT axis is an icon in the leading column (AgentGlyph): only a
  // working agent moves, every other state is a distinct static shape, and the
  // word lives in its tooltip. That column used to hold the selection caret and
  // an attention "!", but the selected row already has its band, and attention
  // now shows where it belongs — an orange glyph when the agent waits on you, a
  // red chip when the delivery regressed.
  //
  // The Status cell holds the DELIVERY axis as ONE lifecycle chip ($lib/stage),
  // with lola's reaction posture folded into its wording ("CI failing · needs
  // you" instead of "× ci failed" + "escalated"). The PR number comes FIRST in a
  // fixed-width slot, grey and monospaced like any identifier, so every chip
  // starts at the same x. Before a PR exists, the agent's own plan chip stands in
  // (chipRelevant): the plan is the progress until a PR supersedes it. An agent
  // waiting on you adds a "Needs you" chip naming what it is asking.

  // Activity gets its own column once there is genuinely room for it, and rides
  // under the title below that — so a wide window keeps single-line rows. This
  // is a matchMedia query rather than a pair of `hidden 2xl:table-cell` /
  // `2xl:hidden` copies on purpose: the CSS version renders BOTH into every row
  // and lets the stylesheet hide one, which doubles the markup per row and puts
  // the same sentence in the DOM twice. One element that moves is simpler than
  // two that alternate, and a query for the text finds exactly one node.
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
<!-- px-1 lands the first cell's own pl-2 on the app's 12px inset now that the
     table sits on the canvas instead of inside a padded panel. -->
<div class="min-w-0 px-1">
  <table class="w-full border-separate border-spacing-0">
    <!-- The sticky head must match what it sticks over: canvas, not the panel
         tint the surrounding card used to paint. -->
    <thead class="sticky top-0 bg-canvas/90 backdrop-blur">
      <!-- `label` carries the 11px size, the 600 weight, the tracking and the
           uppercasing as one token — never spell those out at a call site. py-2
           lands the head at the same 30px as a body row. -->
      <tr class="label text-left text-faint">
        <th class="w-4 py-2 pl-2"></th>
        <th class="py-2 pr-2">Issue</th>
        {#if !dense}<th class="py-2 pr-2">Title</th>{/if}
        {#if !dense && wide}<th class="py-2 pr-2">Activity</th>{/if}
        <th class="py-2 pr-2">Project</th>
        <th class="py-2 pr-2">Status</th>
        {#if anyCost}<th class="py-2 pr-2 text-right">Cost</th>{/if}
        <th class="py-2 pr-2 text-right">Age</th>
      </tr>
    </thead>
    <tbody>
      {#each rows as s (s.id)}
        {@const sel = nav.selectedId === s.id}
        {@const needsYou = !!s.agentState && displayFor(s.agentState) === "needs_you"}
        {@const reason = needsYou ? inputReasonLabel(s.inputReason) : ""}
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
            <AgentGlyph
              agentState={s.agentState}
              inputReason={s.inputReason}
              status={s.status}
              interpreted={s.interpretedState}
            />
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
                {#if !wide}<AgentActivity session={s} pulse={false} />{/if}
              </div>
            </td>
            {#if wide}
              <td class="max-w-[24rem] py-1.5 pr-2 align-middle"><AgentActivity session={s} pulse={false} /></td>
            {/if}
          {/if}
          <!-- The dot marks the session running this project's dev_commands.
               It rides on the PROJECT cell because that is the scope of the
               exclusivity — one dot per project name in the whole table. -->
          <td class="py-1.5 pr-2 align-middle text-sm whitespace-nowrap text-faint">
            {store.displayNameFor(s.project)}{#if s.devActive}<span
                class="ml-1 text-good"
                title="running this project's dev commands">●</span
              >{/if}
          </td>
          <td class="py-1.5 pr-2 align-middle">
            <span class="inline-flex items-center gap-2 whitespace-nowrap">
              <!-- Fixed width (room for "#9999"), present on every row, so the
                   chips beside it line up down the column. The number opens the
                   PR: a <td> is not inside a button, so it may be a control. -->
              <span class="num inline-block w-[3.25rem] text-sm text-faint">
                {#if s.prNumber > 0}
                  {#if s.prUrl}
                    <Button
                      variant="bare"
                      size="xs"
                      title="open pull request #{s.prNumber}"
                      class="num h-auto! rounded! px-0! py-0! text-faint! underline-offset-2 enabled:hover:text-ink! enabled:hover:underline"
                      onclick={(e: MouseEvent) => {
                        e.stopPropagation();
                        store.openURL(s.prUrl);
                      }}>#{s.prNumber}</Button
                    >
                  {:else}#{s.prNumber}{/if}
                {/if}
              </span>
              {#if s.prNumber > 0}
                <StageChip
                  delivery={s.delivery}
                  reacting={s.reacting}
                  resolveBranch={store.defaultBranchFor(s.project)}
                  onResolve={() => store.resolveConflict(s.id)}
                />
              {:else if chipRelevant(s.board, s.prNumber)}
                <BoardChip session={s} />
              {/if}
              <ClaimFlag session={s} />
              {#if needsYou}
                <span
                  class="inline-flex items-center gap-1.5 rounded-full bg-orange/12 px-2 py-[1px] text-sm font-medium whitespace-nowrap text-orange"
                  title="the agent is waiting on you"
                  >Needs you{#if reason}<span class="font-normal opacity-80">· {reason}</span>{/if}</span
                >
              {/if}
            </span>
          </td>
          {#if anyCost}
            <!-- Tokens + size glyph + burn flame (UsageMark); an unknown figure
                 is blank rather than "0". -->
            <td
              class="py-1.5 pr-2 text-right align-middle text-sm whitespace-nowrap text-faint"
              title={s.usage ? undefined : "no usage recorded for this agent"}
              >{#if s.usage}<UsageMark usage={s.usage} />{/if}</td
            >
          {/if}
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
