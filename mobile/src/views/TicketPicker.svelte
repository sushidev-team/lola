<script lang="ts" module>
  // "Start a ticket": the Linear issues a project's team is carrying, and one
  // tap that turns one of them into a running session on the Mac.
  //
  // WHAT THIS SCREEN IS FOR. The Projects tab's detail says what a project IS;
  // this is the one thing it can DO that creates work rather than inspecting it.
  // A phone cannot write config.toml (every ConfigService method answers
  // `unsupported`, deliberately), so the detail's other half — polls, the
  // project form — has no mobile counterpart. Starting an issue does not touch
  // config at all: it is `cmd=openTicket`, allowed for a remote peer, and it is
  // the reason a person opens this app away from their desk.
  //
  // MODELLED ON desktop/frontend/src/lib/views/TicketPicker.svelte, and it is
  // worth saying which of that screen's parts did NOT come across and why,
  // because each omission is a decision and not an oversight:
  //
  //   * The FILTER FIELD. A text input over the list means a keyboard covering
  //     half of it, on the screen whose whole content is the list. The daemon
  //     already sorts into pick order (`sortTicketIssues`: what is moving, then
  //     by urgency, then the freshest), so the answer is at the top rather than
  //     behind a search. A team backlog of three hundred rows is still a long
  //     scroll; if that turns out to be the common case, a search belongs in a
  //     sheet like the sessions list's, not inline above the rows.
  //   * The AGENT SELECT. Choosing claude vs codex vs opencode per spawn is a
  //     decision about someone else's machine, taken on a phone, that the
  //     project's own `agent` setting already answers. Omitting it means
  //     `agentKind` is "" and the daemon uses the configured default, which is
  //     what a person tapping a row on a phone means.
  //   * PRIORITY, LABELS and ESTIMATE as columns. There is one 11px meta line
  //     here, not five table columns. Priority in particular is not lost: the
  //     daemon sorts by it inside each state bucket, so urgency is expressed as
  //     POSITION — the urgent work is what you land on.
  //
  // WHAT DID come across is the part that matters: the two scopes, the state
  // vocabulary, the already-running guard, and the rule that a refusal is shown
  // in the daemon's own words.

  /**
   * The scope chips' two states, as LITERAL class strings.
   *
   * Rule 4 of the brief and the Button invariant in CLAUDE.md: Tailwind v4
   * scans source TEXT, so a composed `bg-${state}` compiles to nothing — a chip
   * with no ground and no border, silently, with no error anywhere. Copied in
   * shape (not imported) from <FilterRail>'s own map so the two rails in this
   * app are visibly the same control; they are different vocabularies over
   * different data, and sharing one map would couple a Linear scope to a triage
   * bucket for the sake of four strings.
   */
  const CHIP = {
    on: "bg-sel border-edge text-ink",
    off: "bg-panel border-edge-soft text-subtext",
  } as const;

  /** The two scopes `protocol.TicketsArgs.Scope` accepts. */
  type Scope = "mine" | "team";

  /**
   * BOTH SCOPES ARE OFFERED, and that is a considered answer to "offer them
   * only if a phone can use them meaningfully".
   *
   * "mine" is the daemon's default and filters to the API key's viewer. But a
   * backlog is unassigned by convention on most boards, so a phone that only
   * ever asked for "mine" would show an empty list to anyone whose team plans
   * that way — and the empty state would then be a lie about the board. The
   * switch costs two chips and one re-fetch of a command a phone is already
   * allowed to send; it writes nothing anywhere. That is a much smaller price
   * than an honest-looking "nothing to start" over a full backlog.
   */
  const SCOPES: { id: Scope; label: string }[] = [
    { id: "mine", label: "Mine" },
    { id: "team", label: "Team" },
  ];

  /**
   * The colour of a Linear WORKFLOW STATE, by its stable type.
   *
   * THIS IS NOT A SECOND STATUS TABLE, and the distinction is the whole reason
   * it is allowed to exist. Rule 2 of the brief forbids a local palette for
   * lola's own session vocabulary — `statusLabel`, `statusText`, `pillKind` all
   * come from `$lib/theme`, the port of Go's internal/state that
   * desktop/state_parity_test.go pins across three surfaces. A Linear workflow
   * state is a DIFFERENT vocabulary owned by a different system: it is Linear's
   * `stateType` enum, theme.ts has no entry for any of its members, and asking
   * `statusText("started")` would be a category error that happens to return a
   * colour. The desktop's own TicketPicker keeps a local map for exactly this
   * reason; this is the same map at the phone's bare-word tier.
   *
   * WHY A BARE WORD RATHER THAN A CHIP. The design gives a compact row a status
   * WORD in the status's own colour and reserves chips for the hero card and
   * the detail header. That is also the right call for a list this long: three
   * hundred rows each wearing a filled chip is a wall, not a signal — the same
   * judgement the desktop's stateChip makes when it leaves the quiet states
   * unfilled.
   *
   * The NAME is the team's own text ("Ready for QA"), which is why the colour
   * comes off the type beside it: names are per-team and unknowable here.
   */
  const STATE_TONE: Record<string, string> = {
    /** Work in flight. The same blue family the shared `pill-work` uses. */
    started: "text-info",
    /** Unsorted and asking to be sorted — the one state that wants attention. */
    triage: "text-orange",
    /** Queued deliberately: todo, ready, up next. */
    unstarted: "text-subtext",
    /** The archive of intentions. Quiet by design. */
    backlog: "text-faint",
    completed: "text-good",
    canceled: "text-faint line-through",
  };

  /**
   * A state type this build has never heard of is drawn quietly rather than
   * dropped. A phone on the App Store outlives the Mac's daemon and Linear's
   * own enum; hiding an issue because its state is unfamiliar would hide work
   * that exists, while `text-faint` merely under-sells it.
   */
  const STATE_FALLBACK = "text-faint";
</script>

<script lang="ts">
  import { store } from "$lib/store.svelte";
  import type { TicketsData, TicketRow } from "@bindings/internal/protocol";
  import { nav } from "@mobile/lib/nav.svelte";
  import { DaemonService } from "@mobile/wailsshim";
  import BackIcon from "@mobile/lib/icons/BackIcon.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";

  let scope = $state<Scope>("mine");
  let data = $state<TicketsData | null>(null);
  /**
   * Starts TRUE, so the first frame is the loading state rather than a flash of
   * "nothing to start" before the request has left the phone. An empty list and
   * an unasked question look identical, and only one of them is worth a
   * sentence.
   */
  let loading = $state(true);
  /** The daemon's own sentence when the LIST could not be fetched. */
  let error = $state("");
  /** The daemon's own sentence when a START was refused. Distinct from `error`:
   *  the list is fine and still on screen, one row's tap was not. */
  let refusal = $state("");
  /** The identifier of the row whose spawn is in flight, or "". */
  let starting = $state("");

  /**
   * Ordered by RECENCY OF REQUEST, not by arrival.
   *
   * Two taps on the scope chips half a second apart are two `tickets` requests
   * over one socket, and nothing guarantees the answers come back in the order
   * they were asked — so a slow "mine" landing after a fast "team" would fill
   * the list with the wrong scope under a chip saying otherwise. Every response
   * checks that it is still the newest before it writes anything.
   */
  let seq = 0;

  const rows = $derived(data?.issues ?? []);

  // Never a bare `name`: a project has two names in this repository — `Name` is
  // identity (paths, tmux, every protocol field) and `Label` is display — and
  // CLAUDE.md's rule is that a UI renders the display one. `displayNameFor`
  // falls back to the id for a project the daemon has not described yet.
  const projectLabel = $derived(store.displayNameFor(nav.project));

  /**
   * The Linear team, for the subtitle — and the UUID is deliberately NOT a
   * fallback.
   *
   * `data.team` is what config keys by; a 36-character hex string where a name
   * belongs reads as a bug, and it was one on the desktop. The daemon's own
   * lookup fails open (`teamIdentity` returns empty strings rather than costing
   * the issue list), so an unresolvable team simply says nothing — the project
   * name beside it already says where you are.
   */
  const teamLabel = $derived.by(() => {
    if (!data) return "";
    const { teamName, teamKey } = data;
    if (teamName && teamKey) return `${teamName} (${teamKey})`;
    return teamName || teamKey || "";
  });

  /**
   * Fetch the issues for one project and scope.
   *
   * The arguments are passed in rather than read off the reactive state inside,
   * so the effect below decides WHAT to ask for and this function only asks —
   * which is what keeps the staleness check above honest about which request it
   * is comparing.
   */
  async function load(project: string, want: Scope): Promise<void> {
    if (project === "") return;
    const mine = ++seq;
    loading = true;
    error = "";
    try {
      const d = await DaemonService.Tickets(project, want);
      if (mine !== seq) return;
      data = d;
    } catch (e) {
      if (mine !== seq) return;
      // The daemon's own sentence, verbatim. It carries the only half a person
      // can act on: "project X has no Linear team — set team_id to browse
      // issues" is a configuration fact and "list issues for X: …" is a
      // reachability one, and a generic "could not load issues" throws that
      // distinction away. See the error state below for why the two are not
      // told apart by matching on the text.
      error = message(e, "Could not list issues.");
      data = null;
    } finally {
      if (mine === seq) loading = false;
    }
  }

  /**
   * ONE EFFECT DRIVES THE FETCH, covering both of its triggers — the screen
   * opening on a project, and the scope changing under it — with a single
   * staleness rule. `onMount` plus a manual reload in the chip handler would be
   * the same two calls with the ordering guarantee written twice.
   *
   * It reads exactly the two inputs and writes only state it does not read, so
   * there is no loop: `loading`, `error` and `data` are never read in this
   * body.
   */
  $effect(() => {
    const project = nav.project;
    const want = scope;
    void load(project, want);
  });

  /** An Error's own words, or a stated fallback. Never "[object Object]". */
  function message(e: unknown, fallback: string): string {
    return e instanceof Error && e.message ? e.message : fallback;
  }

  function pick(s: Scope): void {
    if (s === scope) return;
    refusal = "";
    scope = s;
  }

  /** The workflow state's word and colour, or nothing when the row carries none. */
  function stateTone(t: TicketRow): string {
    return STATE_TONE[t.stateType ?? ""] ?? STATE_FALLBACK;
  }

  /**
   * Show the sessions list, narrowed to this project.
   *
   * IT SETS THE SEARCH, NOT A SCOPE — the same call Projects.svelte's row makes,
   * for the same reason it gives: the phone has one free-text query where the
   * desktop has a project scope, because a phone list has no sidebar to put a
   * scope in. The NAME goes in rather than the label, because `Name` is the
   * identity every session of this project literally carries in its `project`
   * field.
   *
   * The picker is CLOSED on the way out. `nav.toTab` deliberately leaves the
   * Projects tab exactly where it was, which is right for a tab switch and
   * wrong here: the picker's job is finished the moment a ticket starts, and
   * coming back to Projects to find it still open — still listing the issue you
   * just started — would be the app disagreeing with itself. Closing the pick
   * leaves the project's DETAIL as that tab's position, which is where a person
   * came from.
   */
  function showSessions(): void {
    nav.toPick("");
    nav.query = nav.project;
    nav.triage = "";
    nav.toTab("sessions");
  }

  /**
   * Turn one issue into a running session, then go and look at it.
   *
   * A ROW THAT IS ALREADY RUNNING IS NOT A SPAWN BUTTON. `alreadyLive` is the
   * daemon's own answer — the issue is in its in-flight set, or a live session
   * holds its UUID — and sending `openTicket` for one earns the refusal "…is
   * already being worked on — check sessions". Offering a tap that can only
   * fail, in order to print advice the app could have followed itself, is worse
   * than doing what the advice says: such a row goes straight to the sessions
   * list instead.
   *
   * `branch` rides along even though it is optional. It is Linear's own
   * suggested branch name and the daemon puts it on the spawned session
   * (`linear.Issue.BranchName`); dropping it would give a phone-started session
   * a different branch from a desktop-started one for the same issue, which is
   * the kind of difference nobody thinks to look for.
   */
  async function start(t: TicketRow): Promise<void> {
    if (t.alreadyLive) {
      showSessions();
      return;
    }
    // One spawn at a time. The daemon answers only once tmux, git and the agent
    // have all started, which is seconds — long enough for a second tap to send
    // a second spawn, and the in-flight claim only dedups the SAME issue.
    if (starting !== "") return;
    starting = t.identifier;
    refusal = "";
    try {
      await DaemonService.OpenTicket({
        project: nav.project,
        identifier: t.identifier,
        uuid: t.uuid,
        branch: t.branch,
        title: t.title,
      });
      // The sessions list is the destination, so it is worth one round trip to
      // arrive with the new session already in it: navigating first would show
      // a list filtered to this project that does not yet contain the thing
      // just started. `refresh` settles its own reads and never throws.
      await store.refresh();
      showSessions();
    } catch (e) {
      // STAY, AND SAY WHY. A refusal here is a real answer about someone else's
      // machine — the runtime is not ready, the agent binary is missing, the
      // issue is already claimed — and leaving the picker would take the list
      // away from a person who now has to pick something else.
      refusal = message(e, `Could not start ${t.identifier}.`);
    } finally {
      starting = "";
    }
  }
</script>

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <!-- THE PUSHED-SCREEN HEADER, not the tab screens' one. This is a screen with
       a parent — the project's detail — so it leads with a back control and
       takes the design's `px-3` identity-row inset, the same shape the session
       detail uses. The list below keeps the compact row's `px-5`; the two
       insets differ because the 44-point button pays its own optical margin.

       The top padding ADDS to the safe-area inset rather than replacing it, and
       it is spelled out here rather than taken from `pt-safe-t`, because
       App.svelte sets `--lola-top-inset: 0px` on the container holding the
       screens when it has already paid that inset — a custom property is
       substituted on the element it is DECLARED on, so a var() baked into a
       spacing token could never see the override (see app.css). -->
  <header
    class="flex shrink-0 flex-col gap-0.5 px-3 pb-2"
    style="padding-top: calc(var(--lola-top-inset, env(safe-area-inset-top, 0px)) + 8px)"
  >
    <div class="flex items-center gap-2">
      <!-- `text-accent!` with the trailing `!`: a plain `text-accent` ties with
           the ghost variant's own `text-faint` and the winner would be decided
           by Tailwind's order in the compiled sheet rather than by the class
           attribute (CLAUDE.md's Button invariant).

           `nav.back()` rather than `nav.toPick("")`, even though on this screen
           the two do the same thing. back() is the app's ONE back action and it
           is ordered from the deepest out — a picker, then a project, then the
           tab — so a screen that hand-rolled its own step would be the one
           place that stopped agreeing with the hardware gesture and the
           terminal's own back button. -->
      <TouchButton
        icon
        aria-label="Back to {projectLabel}"
        class="text-accent!"
        onclick={() => nav.back()}
      >
        <BackIcon />
      </TouchButton>
      <h1 class="min-w-0 truncate text-lg text-ink">Start a ticket</h1>
    </div>

    <!-- ONE LINE, TRUNCATED, and it is the screen's whole context: which
         project, whose team, how many rows. The count is drawn only once there
         is an answer — "0 issues" while the request is still in flight is a
         claim the app cannot make yet. -->
    <span class="truncate text-sm text-faint">
      {projectLabel}
      {#if teamLabel}
        <span class="text-edge" aria-hidden="true">·</span>
        {teamLabel}
      {/if}
      {#if data}
        <span class="text-edge" aria-hidden="true">·</span>
        <span class="num">{rows.length}</span>
        {rows.length === 1 ? "issue" : "issues"}
      {/if}
    </span>
  </header>

  <!-- THE SCOPE RAIL, at <FilterRail>'s exact geometry so the two rails in this
       app read as the same control. The button is 50 points tall and the chip
       inside it is 33: the design draws a chip of 13px text with 7px of padding
       and a hairline, and rule 3 of the brief is 44, so the button owns the
       rail's height and the 13 points under the chip are invisible padding a
       thumb can still land on. Growing the chip itself would make a filter row
       taller than the row titles beneath it.

       `aria-pressed` rather than a styling prop: these are toggles, and a
       screen reader that cannot see the filled ground gets the state from the
       role. Two chips fit any phone, so unlike the triage rail this one needs
       no horizontal scroller and no scroll-into-view. -->
  <div class="h-[50px] shrink-0" role="group" aria-label="Which issues to list">
    <div class="flex h-full gap-2 px-5">
      {#each SCOPES as s (s.id)}
        <button
          type="button"
          class="flex h-full min-w-11 shrink-0 touch-manipulation items-start justify-center pt-1"
          aria-pressed={scope === s.id}
          onclick={() => pick(s.id)}
        >
          <span
            class="inline-flex items-center rounded-full border px-3 py-[7px] text-base font-medium
                   {scope === s.id ? CHIP.on : CHIP.off}"
          >
            {s.label}
          </span>
        </button>
      {/each}
    </div>
  </div>

  {#if refusal}
    <!-- A REFUSED START, in the daemon's own sentence, in the banner shape the
         terminal screen uses for the same job. Dismissible because it describes
         a MOMENT rather than a state: the list underneath is still correct and
         the next tap may well work. -->
    <div
      class="flex shrink-0 items-center gap-2 border-b border-bad/40 bg-bad/10 pl-5 text-sm text-bad"
      role="status"
    >
      <span class="min-w-0 flex-1 py-2">{refusal}</span>
      <TouchButton icon aria-label="Dismiss" class="text-bad!" onclick={() => (refusal = "")}>
        ×
      </TouchButton>
    </div>
  {/if}

  <!-- No bottom safe-area spacer: the tab bar below this screen pays that inset
       itself, and a screen that paid it too would leave a band of canvas
       between the last row and a bar already clear of the home indicator. -->
  <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain pb-4">
    {#if loading}
      <!-- THREE STATES THAT MUST NOT BE MISTAKEN FOR EACH OTHER, and this is
           the first: the question has been asked and has not been answered. It
           is deliberately not a spinner alone — a spinner and an empty list
           look the same at a glance on a screen this size. -->
      <div class="flex flex-col items-center gap-2 px-8 py-12 text-center">
        <span class="text-lg text-ink">Loading issues…</span>
        <span class="copy text-body text-faint">
          The Mac is asking Linear. This can take a few seconds.
        </span>
      </div>
    {:else if error}
      <!-- THE SECOND: the question was asked and could not be answered. This is
           the only one of the three that is a PROBLEM, which is why it is the
           only one wearing the bad colour and offering a retry.

           THE DAEMON'S SENTENCE IS PRINTED VERBATIM AND NOT CLASSIFIED. There
           are two very different causes behind it — a project with no
           `team_id`, which is a configuration fact a phone cannot fix, and
           Linear being unreachable, which is weather — and the daemon already
           words them differently. Matching on that wording to write a nicer
           heading would mean this screen quietly mis-labelling the day the
           daemon rephrases an error, so the heading stays generic and the
           sentence carries the distinction. -->
      <div class="flex flex-col items-center gap-3 px-8 py-12 text-center">
        <span class="text-lg text-ink">Could not list issues</span>
        <span class="copy text-body text-bad">{error}</span>
        <TouchButton variant="secondary" onclick={() => void load(nav.project, scope)}>
          Try again
        </TouchButton>
      </div>
    {:else if rows.length === 0}
      <!-- THE THIRD: the question was asked, answered, and the answer is
           "none". Not a fault, so it takes no bad colour and offers no retry —
           retrying a correct answer is how a working app looks broken. What it
           DOES offer is the other scope, because that is the actual next move
           for the commonest cause of an empty list: a board that plans its
           backlog unassigned. -->
      <div class="flex flex-col items-center gap-3 px-8 py-12 text-center">
        <span class="text-lg text-ink">Nothing to start</span>
        {#if scope === "mine"}
          <span class="copy text-body text-faint">
            No issue in {teamLabel || "this project's Linear team"} is assigned to you.
          </span>
          <TouchButton variant="secondary" onclick={() => pick("team")}>
            Show the whole team
          </TouchButton>
        {:else}
          <span class="copy text-body text-faint">
            {teamLabel || "This project's Linear team"} has no open issues. Nothing is wrong — there
            is simply nothing queued.
          </span>
        {/if}
      </div>
    {:else}
      <ul>
        {#each rows as t (t.uuid)}
          <li>
            <!-- The brief's compact row: the content on top at the row size,
                 the facts one tier down. `tap-row` rather than `tap` — a list
                 row is already full width, so only the height needs the 44pt
                 floor.

                 `disabled` only while ANOTHER row is spawning, not this one:
                 the row being started keeps its enabled colours because it is
                 the row that is doing something, and a whole list greyed out
                 including the one you pressed reads as a dead screen. -->
            <button
              type="button"
              class="tap-row flex w-full touch-manipulation flex-col gap-[3px] border-b border-edge-soft
                     px-5 py-[11px] text-left active:bg-sel disabled:opacity-40"
              disabled={starting !== "" && starting !== t.identifier}
              onclick={() => void start(t)}
            >
              <!-- WHAT THE TAP DOES, for a reader who cannot see the list. The
                   visible text is a title and some facts; nothing in it says
                   this row is a button that spawns an agent on another
                   machine. -->
              <span class="sr-only">
                {t.alreadyLive ? "Already running, show in sessions:" : "Start:"}
              </span>

              <!-- UNTRUSTED TEXT. A Linear issue title is written by whoever
                   filed the issue, so it is a text node and never markup or a
                   URL. Clamped to two lines because it is the only free text on
                   the row and the only thing that can be long. -->
              <span class="line-clamp-2 text-base font-medium text-ink">{t.title}</span>

              <div class="flex items-center gap-1.5 text-sm">
                <!-- `shrink-0`: the identifier is seven characters and is the
                     row's citation handle. "FE-…" is not a citation. -->
                <span class="num shrink-0 text-faint">{t.identifier}</span>

                {#if t.alreadyLive}
                  <!-- The design gives a compact row a 6px dot "only while the
                       agent is live", which is exactly what this is: lola
                       already holds this issue. It is the strongest signal on
                       the row because it changes what the tap means. -->
                  <span class="shrink-0 text-edge" aria-hidden="true">·</span>
                  <span
                    class="size-1.5 shrink-0 rounded-full bg-good"
                    aria-hidden="true"
                  ></span>
                  <span class="shrink-0 text-good">Running</span>
                {:else if t.state}
                  <span class="shrink-0 text-edge" aria-hidden="true">·</span>
                  <!-- The team's own word for the state, in the colour of the
                       stable type behind it. It is the ONE item on this line
                       allowed to give way, because a workflow state can be
                       "Ready for engineering review" on some boards. -->
                  <span class="min-w-0 truncate {stateTone(t)}">{t.state}</span>
                {/if}

                {#if scope === "team" && t.assignee}
                  <!-- Only in the team scope. In "mine" every row would name
                       the same person, which is a column of the reader's own
                       name. -->
                  <span class="shrink-0 text-edge" aria-hidden="true">·</span>
                  <span class="min-w-0 truncate text-faint">{t.assignee}</span>
                {/if}

                <span class="flex-1"></span>

                {#if t.updated}
                  <!-- Pre-formatted daemon-side in SessionInfo.Age's own format,
                       so no client parses a timestamp. `num` is tabular figures:
                       every age is rewritten on each fetch and proportional
                       digits would nudge the column on all of them. -->
                  <span class="num shrink-0 text-faint">{t.updated}</span>
                {/if}
              </div>
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>
