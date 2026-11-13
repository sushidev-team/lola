<script lang="ts" module>
  /**
   * The order the sections are drawn in, which is NOT the kanban board's.
   *
   * theme.ts's KANBAN_COLUMNS runs Needs You → Working → Fixing → In Review →
   * Done, left to right, because a board reads as a pipeline. A phone list reads
   * top to bottom as a queue, and the design swaps the middle pair: `Fixing`
   * comes before `Working` because a session whose delivered work regressed is
   * closer to needing a person than one that is quietly mid-turn. It is the same
   * judgement theme.ts's own `sortRank` already makes inside a bucket — tier 1
   * for the broken family, tier 2 for a working agent — so the two orderings
   * agree; only the board's left-to-right does not.
   *
   * The TITLES are still the shared ones. Nothing here renames a bucket: a title
   * that is not in TRIAGE_FILTERS is dropped from this list and its sessions
   * still get a section, appended after the known ones. That fallback is the
   * point of the derivation below — a bucket added to Go's state.KanbanColumns()
   * must never be able to make a session invisible on a phone.
   */
  const SECTION_ORDER = ["Needs You", "Fixing", "Working", "In Review", "Done"];
</script>

<script lang="ts">
  import { sortSessions, store } from "$lib/store.svelte";
  import { TRIAGE_FILTERS, triaged } from "$lib/filters";
  import { attention, attentionCount, isAttention } from "$lib/theme";
  import SessionCard from "@mobile/lib/components/SessionCard.svelte";
  import SessionRow from "@mobile/lib/components/SessionRow.svelte";
  import SectionHeader from "@mobile/lib/components/SectionHeader.svelte";
  import FilterRail from "@mobile/lib/components/FilterRail.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import FilterSheet from "@mobile/lib/components/FilterSheet.svelte";
  import FilterIcon from "@mobile/lib/icons/FilterIcon.svelte";
  import { connection } from "@mobile/lib/connection.svelte";
  import { nav, paneNameFor } from "@mobile/lib/nav.svelte";
  import { searchSessions } from "@mobile/lib/search";
  import { installKeyboardInset } from "@mobile/lib/keyboardinset";
  import { onDestroy, onMount } from "svelte";
  import type { SessionInfo } from "$lib/store.svelte";

  // The session list. READ-ONLY in M1: it shows what is happening and opens a
  // terminal. The answer card, the control tiers and every mutating action are
  // M3 and M4, and none of them is stubbed here — a disabled button for a feature
  // that does not exist is worse than its absence.
  //
  // WHERE THE DATA COMES FROM. Straight out of `$lib/store.svelte`, the desktop's
  // own store, unmodified: it subscribes to `daemon:sessions` and friends, and the
  // service shim synthesizes those events from the remote listener. So this list
  // is fed by the same code path the desktop app uses, which is the entire reuse
  // bet in one screen.
  //
  // SORTING, FILTERING AND BUCKETING ARE BORROWED, NOT REBUILT. `sortSessions` is
  // attention-first (theme.ts's sortRank, a port of the TUI's), `triaged` is the
  // kanban partition, and both are pinned against Go by
  // desktop/state_parity_test.go. Only the free-text search is new, because the
  // desktop replaces it with a project sidebar that does not fit here.
  //
  // THE LIST IS NOW PARTITIONED RATHER THAN FLAT, which is the redesign's one
  // structural change to this screen. A single sorted run was correct and
  // unreadable: attention-first ordering put the urgent sessions on top, but
  // nothing on the screen said WHERE the urgent ones stopped, so the fourth row
  // looked exactly as important as the first. Sections say it, and they say it
  // with the same partition the rail's chips count — `triaged` for both — so a
  // chip reading 2 above a section holding 3 is not a state this screen can
  // reach.

  const all = $derived(sortSessions(store.sessions));
  const rows = $derived(
    searchSessions(triaged(all, nav.triage), nav.query, (p) => store.displayNameFor(p)),
  );
  const needsYou = $derived(attentionCount(store.sessions));

  const filtered = $derived(nav.triage !== "" || nav.query !== "");

  /**
   * The sections, in the design's order, with the empty ones dropped.
   *
   * IT IS A COMPLETE COVER, and that is the property to preserve when touching
   * it: `triageOf` (inside `triaged`) answers `kanbanTitle`, which falls back to
   * "Working" for any pair it does not recognise, so every session lands in
   * exactly one TRIAGE_FILTERS bucket and no row can fall between two sections.
   * The order list is filtered THROUGH TRIAGE_FILTERS and any bucket it does not
   * name is appended, so a column added to Go's state.KanbanColumns() shows up
   * at the bottom of this screen rather than nowhere.
   *
   * SELECTING A BUCKET NEEDS NO SPECIAL CASE. `rows` is already narrowed by
   * `nav.triage`, so every other section computes empty and drops out on its
   * own, leaving the chosen bucket and its heading. Branching on `nav.triage`
   * here would be a second copy of the filter that could disagree with the
   * first.
   */
  const sections = $derived.by(() => {
    const known = SECTION_ORDER.filter((t) => TRIAGE_FILTERS.includes(t));
    const rest = TRIAGE_FILTERS.filter((t) => !SECTION_ORDER.includes(t));
    return [...known, ...rest]
      .map((title) => ({ title, rows: triaged(rows, title) }))
      .filter((section) => section.rows.length > 0);
  });

  /**
   * Which SHAPE a session takes: the hero card, or the compact row.
   *
   * The question is "is a human blocked on this", which is `$lib/theme`'s
   * `attention` — the axis-pair predicate — with `isAttention` as the fallback
   * for a record carrying no agent axis. That pairing is copied from
   * `sortSessions` itself (store.svelte.ts does the same thing with sortRank and
   * legacySortRank) rather than invented: both axes are optional on the wire, a
   * daemon predating the split sends neither, and asking `attention("", "")`
   * would answer false for every session on that push — turning the whole screen
   * into compact rows on exactly the daemon whose sessions are least understood.
   *
   * It is deliberately NOT `sections`. A card can therefore appear under a
   * heading other than "Needs You" or "Fixing" — a dead agent whose PR still has
   * red CI buckets as `Done` while `attention` is still true — and that is the
   * right answer both times: the section says where the session stands, the
   * shape says whether it wants a person. Both are read from the shared tables,
   * so neither is this screen's opinion.
   */
  function needsHuman(s: SessionInfo): boolean {
    return s.agentState ? attention(s.agentState, s.delivery ?? "") : isAttention(s.status ?? "");
  }

  /**
   * The Mac's name, for the header's third fact.
   *
   * `store.status.host` is the daemon's own answer to `cmd=status`, which is the
   * name of the machine somebody left work running on — an address describes a
   * network and changes between home and the office. `connection.label` is the
   * fallback for the window before the first status arrives, and for a daemon
   * too old to report one.
   *
   * WORTH A HUMAN'S EYE: those two can disagree. `connection.label` prefers a
   * name the user typed on this phone over the daemon's own, so a renamed Mac is
   * "work" on the connection button and its raw hostname here. Preferring the
   * label outright would fix that and lose the case this line is for, so the
   * order is left as the redesign specifies it and the tension is stated rather
   * than papered over.
   */
  const hostName = $derived(store.status?.host || connection.label);

  /**
   * The filter button's accessible name, and it changes with the filter.
   *
   * The bucket is now visible on the rail, but the SEARCH term is not — it lives
   * in the sheet this button opens — so the name still carries both. It is the
   * one guarantee this control owes the list: a filtered list must never be
   * mistakable for a short one, on either surface. The subtitle underneath makes
   * the same promise in numbers ("2 of 7 sessions").
   */
  const filterLabel = $derived.by(() => {
    if (!filtered) return "Filters";
    const parts: string[] = [];
    if (nav.triage) parts.push(`showing ${nav.triage}`);
    if (nav.query) parts.push(`searching ${nav.query}`);
    return `Filters active — ${parts.join(", ")}`;
  });

  let keyboardInset = $state(0);
  let offKeyboard: (() => void) | undefined;
  onMount(() => {
    offKeyboard = installKeyboardInset((px) => (keyboardInset = px));
  });
  onDestroy(() => {
    offKeyboard?.();
  });

  // WHICH SHEET IS OPEN LIVES IN `nav`, not here. It was two locals; naming the
  // state is what lets a development link land on an open sheet, which is the
  // only way those overlays can be photographed at all — the Simulator has no
  // gesture API, so a screen reachable solely by a tap is a screen a reviewer
  // must judge from unit tests. See lib/sheets.ts.
  const filterOpen = $derived(nav.sheet === "filter");
</script>

<!-- The list pays back the soft keyboard's height, exactly as the terminal
     screen does — `Keyboard.resize: KeyboardResize.None` means nothing else
     will.

     IT IS A BACKSTOP RATHER THAN A LIVE CONCERN, and the comment used to
     overstate it. This screen has no field of its own any more: the search box
     lives in <FilterSheet>, which mounts through `Sheet` as `fixed inset-0`, so
     it is outside this element's padding box and covers the list completely
     while it is up. Nothing a person can focus from here is behind this
     padding, so today it moves pixels nobody can see — and the tab bar below,
     which is a sibling of this whole screen in App.svelte, is covered by the
     keyboard whatever this element does about it. The wiring stays because a
     screen that later grows an inline field would otherwise reintroduce the
     original bug silently; if the sheet's own buttons need to clear the
     keyboard, that belongs in `Sheet`, which is where the field actually is. -->
<div class="flex h-full min-h-0 flex-col bg-canvas" style="padding-bottom: {keyboardInset}px">
  <!-- NO RULE UNDER THE HEADER. It had one, and the redesign's header block does
       not: the filter rail sits directly beneath, and a hairline above a row of
       chips turns the two into a toolbar — a band of chrome the list appears to
       hang off. The section headings do the separating now, each with a rule of
       its own, so a screen full of them was drawing four lines to say one thing.

       `pt-1.5` is the design's, and it is spent ON TOP of the status-bar inset
       rather than instead of it — hence the calc. `--lola-top-inset` is set to
       0px by the shell when something else above has already paid it (see the
       note in app.css).

       THE 6px IS A px LITERAL, not `0.375rem`, even though the two are equal at
       the default text size. app.css pins Tailwind's `--spacing` to px on
       purpose so that no gap, pad or inset follows Dynamic Type — the setting
       scales the TYPE, and a header that grew with it would reflow a screen
       that is already tight. A rem here reintroduces exactly that for this one
       value: the root runs 16 to 23px, so it would drift to ~8.6px while the
       Activity, Projects and Settings headers stayed at 6, and the three tabs
       would visibly disagree about where a screen starts. -->
  <header
    class="flex shrink-0 flex-col gap-0.5 px-5 pb-3"
    style="padding-top: calc(var(--lola-top-inset, env(safe-area-inset-top, 0px)) + 6px)"
  >
    <div class="flex h-11 items-center gap-1">
      <!-- ONE LARGE TITLE PER SCREEN — `text-2xl` is the ceiling of the phone's
           ladder and this is the only thing on the list that takes it. An <h1>
           rather than a styled span: it is the document's title, and it is what
           gives the section headings below something to be second-level to. -->
      <h1 class="truncate text-2xl text-ink">Sessions</h1>
      <span class="flex-1"></span>

      <!-- TWO ICONS, EACH WITH ONE SUBJECT. What was here before the redesign
           was a Refresh mark and the word "Disconnect", and neither named what it
           acted on, so both read as daemon controls — which this app has none of
           and must never grow (PLAN.md: "a phone that stops the daemon severs
           the only link it has back"). Refresh is gone outright: the list polls,
           and a manual button beside a live list is an invitation to distrust
           the polling. Disconnect moved into the settings sheet, where it can
           afford to name the Mac it leaves.

           `rounded-[10px]!` needs the `!`: a plain rounded value ties with
           Button's own `rounded-md` on specificity and the winner would be
           decided by Tailwind's order in the compiled sheet (the Button
           invariant in CLAUDE.md). -->
      <TouchButton
        icon
        class="rounded-[10px]!"
        aria-label={filterLabel}
        onclick={() => nav.openSheet("filter")}
      >
        <!-- THE ACTIVE MARK IS PART OF THE GLYPH, not a badge bolted beside it.
             The dot used to be a positioned span in this file, which meant the
             header owned a piece of the icon's drawing and the two could drift;
             `FilterIcon` takes the state instead. It is decoration either way —
             what a VoiceOver user gets is the button's name above. -->
        <FilterIcon active={filtered} width={20} height={20} />
      </TouchButton>

      <!-- NO CONNECTION BUTTON HERE ANY MORE. It opened a sheet holding the same
           three facts the Settings tab already carries — connected-to,
           disconnect, forget — plus the one it did not, the Mac's nickname. The
           nickname moved there and the sheet went with the button, so a machine
           is now managed in exactly one place instead of two that had to be kept
           in step by hand.

           The header keeps the FACTS it needs: the subtitle names the Mac, and
           the banner below says when the link or the daemon is down. Neither was
           ever behind that button. -->
    </div>

    <!-- ONE LINE, CLIPPED. The three facts are the only ones a person needs
         before they start reading rows: whether anything wants them, how much
         there is, and which Mac it is coming from.

         The count is a BARE TEXT NODE rather than a span, so the whole line is
         one element for anything asking what it says — the truncation and the
         base type live here and only the two exceptions (the orange count, the
         mono host) take a span of their own.

         `truncate` twice, deliberately: on the row it is the no-wrap and the
         clip, which is what stops a long host name from growing the header and
         pushing the list down; on the host span it is the ellipsis, because the
         host is the one item here that is neither a number nor a fixed word. -->
    <p class="flex min-w-0 items-center gap-1.5 truncate text-base font-medium text-faint">
      {#if needsYou > 0}
        <!-- ORANGE IS theme.ts's OWN COLOUR for needs_input, spent here on the
             count of them. It is omitted rather than shown as "0 need you",
             which is a sentence that draws the eye to say nothing. -->
        <span class="shrink-0 text-orange">{needsYou} need you</span>
        <span class="shrink-0 text-faint" aria-hidden="true">·</span>
      {/if}
      <!-- THE FILTERED COUNT NAMES BOTH NUMBERS. "2 sessions" over two rows is
           indistinguishable from a quiet morning; "2 of 7 sessions" is not, and
           it is the same promise the filter sheet's own tally makes from the
           other side. -->
      {#if filtered}
        {rows.length} of {store.sessions.length}
        {store.sessions.length === 1 ? "session" : "sessions"}
      {:else}
        {store.sessions.length}
        {store.sessions.length === 1 ? "session" : "sessions"}
      {/if}
      {#if hostName}
        <span class="shrink-0 text-faint" aria-hidden="true">·</span>
        <!-- `num` is tabular figures, which matters for an address: a hostname
             that has fallen back to 192.168.x.y is mostly digits, and this line
             is rewritten on every status push. -->
        <span class="num min-w-0 truncate text-sm">{hostName}</span>
      {/if}
    </p>
  </header>

  <!-- THE BUCKETS ARE INLINE AGAIN, and the search field is not. See the header
       of <FilterRail>: the buckets are what the list is now arranged BY, so the
       rail is a table of contents, while the search field stays behind the
       button as the rare action.

       WORTH A HUMAN'S EYE: <FilterSheet> still renders <TriageChips>, so the
       same filter is offered twice — here, and again inside the sheet. Both are
       bound to `nav.triage`, so they cannot disagree, but the sheet's copy is
       now redundant and should probably be dropped to leave it a search sheet.
       That file was outside this change's scope. -->
  <FilterRail bind:value={nav.triage} sessions={all} />

  {#if !connection.ready}
    <!-- PLAN.md: an off-network phone must say so in one line naming the actual
         reason, and must never be shown the pairing screen — that is what
         revocation looks like, and the two have to stay distinguishable. -->
    <div
      class="flex shrink-0 items-center gap-2 border-b border-warn/40 bg-warn/10 px-4 py-2"
      role="status"
    >
      <span class="min-w-0 flex-1">
        <span class="text-sm text-warn">{connection.diagnosis.title}.</span>
        <span class="text-sm text-faint"> Showing the last snapshot.</span>
      </span>
      <!-- RECONNECT, HERE, because this banner is where a person finds out.
           The ladder retries on its own and on every foreground, but its next
           attempt can be a minute away and the app gave no way to say "now" —
           so the only recovery anyone found was force-quitting the app, which
           is a worse version of the same request. Disabled while an attempt is
           in flight, so a second tap cannot restart the ladder underneath the
           first. -->
      <TouchButton
        class="shrink-0 px-2!"
        disabled={connection.busy || connection.reconnecting}
        onclick={() => void connection.reconnect()}
      >
        {connection.busy || connection.reconnecting ? "Connecting…" : "Reconnect"}
      </TouchButton>
    </div>
  {:else if !store.alive}
    <div class="shrink-0 border-b border-bad/40 bg-bad/10 px-4 py-2" role="status">
      <span class="text-sm text-bad">The daemon is not running.</span>
      <span class="text-sm text-faint">
        Nothing can be observed until it starts, and a phone cannot start it.</span
      >
    </div>
  {/if}

  <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain">
    {#if sections.length > 0}
      {#each sections as section (section.title)}
        <SectionHeader title={section.title} count={section.rows.length} />
        {#each section.rows as s (s.id)}
          <!-- TWO SHAPES, ONE LIST. A session a human is blocked on gets the
               hero card — bordered, shadowed, two lines of title, the agent's
               own sentence — and everything else gets the 42-point compact row.
               The budget is the argument: the screen can afford the card for the
               one or two sessions that want a person and cannot afford it for
               the twelve that do not, and a list where everything is emphasised
               emphasises nothing. -->
          {#if needsHuman(s)}
            <SessionCard
              session={s}
              projectLabel={store.displayNameFor(s.project)}
              onopen={() => nav.toTerminal(s.id, paneNameFor(s))}
            />
          {:else}
            <SessionRow
              session={s}
              projectLabel={store.displayNameFor(s.project)}
              onopen={() => nav.toTerminal(s.id, paneNameFor(s))}
            />
          {/if}
        {/each}
      {/each}
    {:else}
      <!-- Three genuinely different empty states. Collapsing them into "no
           sessions" is what hides a dead daemon behind what looks like an idle
           queue — and unlike the desktop, this app has no recovery action to
           offer, so saying which one it is matters more, not less. -->
      <div class="flex flex-col items-center gap-2 px-8 py-12 text-center">
        {#if !store.connected}
          <span class="text-faint">Connecting…</span>
        {:else if filtered}
          <span class="text-faint">Nothing matches that filter.</span>
          <TouchButton
            onclick={() => {
              nav.query = "";
              nav.triage = "";
            }}>Clear filters</TouchButton
          >
        {:else}
          <span class="text-lg text-ink">No sessions</span>
          <span class="copy text-sm text-faint">
            lola spawns one per matching Linear issue. Nothing is waiting on you.
          </span>
        {/if}
      </div>
    {/if}
    <!-- NO BOTTOM SPACER ANY MORE. This screen used to end with a div paying
         env(safe-area-inset-bottom), because it was the bottom of the window.
         It no longer is: the shell draws the tab bar beneath every screen, and
         that bar pays the home-indicator inset for all of them. Paying it here
         as well would leave a band of empty list above a bar that has already
         reserved the same space. -->
  </div>
</div>

{#if filterOpen}
  <FilterSheet
    bind:triage={nav.triage}
    bind:query={nav.query}
    sessions={all}
    matched={rows.length}
    onclose={() => nav.closeSheet()}
  />
{/if}

