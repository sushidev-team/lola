<script lang="ts">
  import { store, type ProjectInfo, type SessionInfo } from "$lib/store.svelte";
  import Select from "$lib/components/Select.svelte";
  import SectionHeader from "@mobile/lib/components/SectionHeader.svelte";
  import SessionRow from "@mobile/lib/components/SessionRow.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import BackIcon from "@mobile/lib/icons/BackIcon.svelte";
  import { nav, paneNameFor } from "@mobile/lib/nav.svelte";
  import { DaemonService } from "@mobile/wailsshim";

  // One project, drilled into from the Projects list. It is the phone's version
  // of the desktop's ProjectDetail: the same facts header and the same action
  // list, minus everything a phone is not allowed to do.
  //
  // WHAT IS MISSING, AND WHY IT IS MISSING RATHER THAN DISABLED. The desktop
  // offers two more rows — "Polls" and "Edit project" — and both open the same
  // config editor. Every ConfigService write answers `unsupported` through the
  // shim, deliberately: a phone cannot write config.toml, and PLAN.md's rule is
  // that it should not want to. So they are not drawn at all. Projects.svelte
  // states the reasoning for the whole app and it applies here: a control that
  // is dead for most of a session's life teaches people not to look at the
  // controls, which costs more than the missing feature does.
  //
  // WHAT IS EXTRA, AND WHY. "Poll now" and the polling toggle are NOT on the
  // desktop's detail list — over there they live on the project LIST row, which
  // this app does not have (a phone row does one thing, and that one thing is
  // opening this screen). They are here because they are the two daemon actions
  // a phone can usefully take ON A PROJECT rather than on a session: both are
  // forwarded by the shim and allowed by the daemon's remote policy, and
  // "the filter has stopped picking things up" is a thing you notice away from
  // the Mac.
  //
  // The pickers the first two actions open are NOT this file's. `nav.pick`
  // names them and the shell draws them over this screen, for the same reason
  // the terminal is a screen rather than a tab: a list of pull requests is a
  // full screen's worth of rows at 390 points.

  const project = $derived<ProjectInfo | undefined>(store.projectByName(nav.project));

  // Every session of this project, in the store's own attention-first order.
  // It includes the settled ones; SessionRow dims those itself (its `settled`
  // check is `kanbanColumn(status) === "Done"`), so the list stays honest about
  // what exists without shouting about what is finished.
  const sessions = $derived<SessionInfo[]>(store.sessionsForProject(nav.project));
  // The same cap the desktop uses. Past half a dozen rows this screen stops
  // being a summary and starts being a worse copy of the sessions list, which
  // is what the "show more" row hands over to.
  const SHOWN = 6;
  const shown = $derived(sessions.slice(0, SHOWN));
  const moreCount = $derived(Math.max(0, sessions.length - SHOWN));

  // NEVER A BARE `name`. A project has two names — `Name` is identity (paths,
  // tmux, every protocol field) and `Label` is display — and CLAUDE.md's rule is
  // that a UI renders the display one. `displayNameFor` falls back to the id, so
  // this still says something for a project the daemon has not described yet.
  const title = $derived(store.displayNameFor(nav.project));

  /**
   * The header's second line.
   *
   * It names the IDENTITY whenever that differs from the title, because the
   * identity is the string every session, worktree path and tmux name of this
   * project carries — and it is literally what the "Sessions" action below
   * filters the list by, so a person who wonders why the filter reads
   * `nori-app` has the answer on screen. When the project has no label the two
   * strings are the same and repeating it would say nothing, so it falls back
   * to the repository and then to the checkout.
   */
  const subtitle = $derived.by(() => {
    if (!project) return "Not configured on this Mac";
    if (title !== project.name) return project.name;
    return project.repo || project.path || "No repository configured";
  });

  /**
   * The launch gate.
   *
   * `agentOk` is the daemon's per-project health answer: the resolved coding
   * agent, plus tmux and git, all on PATH. The desktop SAYS "launch verbs
   * disabled" in its status box and then leaves every row enabled anyway; the
   * TUI actually gates them. This screen follows the TUI, because a sentence
   * that argues against its own buttons is worse than either behaviour alone —
   * and the daemon health-gates the spawn regardless, so an enabled row would
   * only buy a refusal notice a moment later.
   */
  const canLaunch = $derived(!!project?.agentOk);

  /**
   * Whether this project polls at all.
   *
   * `pollCount` is 0 when no Linear team is configured, which is the daemon's
   * own `Project.Polls()` — such a project has a filter to run neither now nor
   * on a timer. The two poll rows are gated on it and carry a hint, exactly as
   * the PR row is gated on a configured repository: the fault is in the config
   * on the Mac, and naming it is the only useful thing this screen can do about
   * it.
   */
  const polls = $derived((project?.pollCount ?? 0) > 0);
  const pollingOn = $derived((project?.pollsEnabled ?? 0) > 0);

  // ---------------------------------------------------------------------------
  // The notice line.
  //
  // THE FAILURE PATH IS THE POINT. The daemon refuses things for good reasons —
  // no worktree, a concurrency cap, an unconfigured repository, a branch that
  // already exists — and the sentence it returns is the only half a person can
  // act on. It is also the half this app would otherwise lose: `store.act`
  // reports every outcome through `store.flash`, and nothing in the mobile shell
  // draws that banner, so a refused action would look exactly like a tap that
  // did nothing. Same shape as Terminal.svelte's refused "+".
  //
  // Two tones and no more. A refusal is a `warn` — it describes a moment, not a
  // fault in the app — and a confirmation is `good`, because a poll that found
  // nothing and a poll that never ran look identical without one.
  // ---------------------------------------------------------------------------

  const NOTICE_CLASS = {
    warn: "border-warn/40 bg-warn/10 text-warn",
    good: "border-good/40 bg-good/10 text-good",
  } as const;

  let notice = $state<{ text: string; tone: keyof typeof NOTICE_CLASS } | null>(null);

  /** Which action is in flight, or "". One at a time: these are moves on the
   *  daemon, not idempotent reads, and the reply takes as long as tmux does. */
  let busy = $state("");

  /** A thrown error's own sentence, or a fallback. Never the raw object: a
   *  `[object Object]` in a banner is worse than a generic sentence. */
  function reason(e: unknown, fallback: string): string {
    return e instanceof Error && e.message ? e.message : fallback;
  }

  /** Run a daemon action, saying out loud what came back either way. */
  async function run(id: string, fn: () => Promise<string>): Promise<void> {
    if (busy !== "") return;
    busy = id;
    notice = null;
    try {
      notice = { text: await fn(), tone: "good" };
      await store.refresh();
    } catch (e) {
      notice = { text: reason(e, "The daemon refused that."), tone: "warn" };
    } finally {
      busy = "";
    }
  }

  // ---------------------------------------------------------------------------
  // The inline "new worktree" form.
  // ---------------------------------------------------------------------------

  let worktreeOpen = $state(false);
  let branch = $state("");
  let useAgent = $state(true);
  let selectedAgent = $state("");

  /**
   * Branch off and open a fresh worktree.
   *
   * FOLLOWS THE DESKTOP'S `startWorktree` EXACTLY, including the part that is
   * easy to lose: `openManual` resolves to `undefined` on failure, so the form
   * and the typed branch are torn down ONLY on success. A failed spawn that
   * cleared the field would make the retry a re-type, and the commonest failure
   * here is a branch name that already exists — i.e. one edit away from working.
   *
   * IT GOES THROUGH THE STORE rather than the shim, so the sessions list is
   * refreshed by `act` the moment the spawn lands. The cost is that the daemon's
   * refusal is swallowed into `store.flash`, which this app does not draw — so
   * it is read back off the store here. That is indirect and worth knowing
   * about: the alternative is calling `DaemonService.OpenManual` directly and
   * losing the shared refresh, which trades a visible failure for a stale list.
   */
  async function startWorktree(): Promise<void> {
    const b = branch.trim();
    if (!b || busy !== "") return;
    busy = "worktree";
    notice = null;
    try {
      const r = await store.openManual({
        project: nav.project,
        branch: b,
        agent: useAgent,
        agentKind: useAgent && selectedAgent ? selectedAgent : undefined,
      });
      if (r === undefined) {
        notice = { text: store.flash?.text || "The daemon refused that.", tone: "warn" };
        return;
      }
      worktreeOpen = false;
      branch = "";
      selectedAgent = "";
      openSessions();
    } finally {
      busy = "";
    }
  }

  /**
   * Filter the sessions list to this project and go there.
   *
   * IT SETS THE SEARCH, NOT A SCOPE — the same rule Projects.svelte's own
   * `open()` follows, and for the same reason: the desktop has a project scope
   * beside a sidebar, and a phone list has one free-text query. The NAME goes
   * in, not the label: it is the identity every session of this project carries
   * in its `project` field, so the filter can over-match and can never drop a
   * session it should have shown.
   */
  function openSessions(): void {
    nav.query = nav.project;
    nav.triage = "";
    nav.toTab("sessions");
  }

  type Action = {
    id: string;
    label: string;
    desc: string;
    enabled: boolean;
    /** Why it is off, said in the row rather than left to be guessed. */
    hint?: string;
    run: () => void;
  };

  const actions = $derived<Action[]>([
    {
      id: "prs",
      label: "Open a PR",
      desc: "Pick an open pull request and launch an agent on it",
      enabled: canLaunch && !!project?.repoConfigured,
      hint: !project?.repoConfigured ? "set a GitHub repo to list PRs" : "agent not ready",
      run: () => nav.toPick("prs"),
    },
    {
      id: "tickets",
      label: "Start a ticket",
      desc: "Pick a Linear issue and spawn a session for it",
      enabled: canLaunch,
      hint: "agent not ready",
      run: () => nav.toPick("tickets"),
    },
    {
      id: "worktree",
      label: "New worktree",
      desc: "Branch off and open a fresh worktree (agent or shell)",
      enabled: canLaunch,
      hint: "agent not ready",
      run: () => {
        worktreeOpen = !worktreeOpen;
        notice = null;
      },
    },
    {
      id: "sessions",
      label: "Sessions",
      desc: "Filter the sessions list to this project",
      enabled: true,
      run: openSessions,
    },
    {
      id: "pollOnce",
      label: "Poll now",
      // Says what it DOES, side effect included: this is not a dry run, and a
      // tick that spawns two sessions from a phone must not come as a surprise.
      desc: "Run this project's Linear filter once, spawning any matches",
      enabled: polls,
      hint: "no Linear filter configured",
      run: () =>
        void run("pollOnce", async () => {
          const d = await DaemonService.PollOnce(nav.project, false);
          const n = d?.matches?.length ?? 0;
          return n === 1 ? "Polled once: 1 match" : `Polled once: ${n} matches`;
        }),
    },
    {
      id: "polling",
      label: pollingOn ? "Stop polling" : "Start polling",
      desc: pollingOn
        ? "Stop spawning sessions from this project's filter"
        : "Spawn sessions from this project's filter again",
      enabled: polls,
      hint: "no Linear filter configured",
      run: () =>
        void run("polling", async () => {
          // `enable`/`disable` take a POLL name, and the daemon's `PollByName`
          // IS `ProjectByName` — a project has at most one polling config, its
          // own — so the project's identity is the right argument here.
          if (pollingOn) {
            await DaemonService.Disable(nav.project);
            return "Polling stopped";
          }
          await DaemonService.Enable(nav.project);
          return "Polling started";
        }),
    },
  ]);
</script>

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <!-- The redesign's screen header, the shape Activity, Projects and Settings
       all use — with the back control the other three do not need. The top inset
       is spelled out rather than taken from a `pt-safe-t` utility for the reason
       Sessions.svelte states at length: the design's 6px is spent ON TOP of the
       status bar, and it is a px literal so Dynamic Type scales the type without
       moving where the screen starts. -->
  <!-- THE BACK CONTROL IS ITS OWN ROW, ABOVE THE TITLE, and the title keeps the
       screen's left edge. The first version put the two side by side — the
       shape the terminal's identity row uses — which pushed the large title 52
       points in and left it hanging over a facts card and an action list that
       both start at the margin. A large title that does not line up with the
       content it names reads as a mistake at a glance, and it is the one thing
       on this screen a person looks at first.

       It is also what keeps the four tab screens and this one agreeing: Activity,
       Projects, Settings and Sessions all draw a `text-2xl` title on the margin,
       and a drill-in is not a reason for that to move. The terminal's header is
       the deliberate exception, because there the identity IS the row — an issue
       key, a status and a PR badge on one line — and it has no large title at
       all. -->
  <header
    class="flex shrink-0 flex-col px-5 pb-3"
    style="padding-top: calc(var(--lola-top-inset, env(safe-area-inset-top, 0px)) + 6px)"
  >
    <!-- `-ml-3` pulls the 44-point tap box back so the GLYPH sits on the
         margin: the box is bigger than the mark it draws, and aligning the box
         would leave the chevron visibly indented from the title under it. -->
    <TouchButton
      icon
      class="-ml-3 text-accent!"
      aria-label="Back to projects"
      onclick={() => nav.back()}
    >
      <BackIcon />
    </TouchButton>

    <!-- ONE LARGE TITLE PER SCREEN. `truncate` because a project label is free
         text and this is the widest thing on the header. -->
    <h1 class="truncate text-2xl text-ink">{title}</h1>
    <!-- `text-body`, the prose step, on the same left edge as the title. -->
    <span class="truncate text-body text-subtext">{subtitle}</span>
  </header>

  <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain">
    {#if !project}
      <!-- Two different absences, and they must not be collapsed. A push that
           has not landed is a moment; a project removed from config.toml on the
           Mac is a fact, and the name is worth showing because it is the one the
           row that got here was drawn from. -->
      <div class="flex flex-col items-center gap-2 px-8 py-12 text-center">
        {#if !store.connected}
          <span class="text-faint">Connecting…</span>
        {:else}
          <span class="text-lg text-ink">Project not found</span>
          <span class="copy text-sm text-faint">
            <span class="num">{nav.project || "(none)"}</span> is not in the Mac's config.toml any
            more. Projects are configured there; this app only reads them.
          </span>
        {/if}
      </div>
    {:else}
      {#if notice}
        <!-- The daemon's own sentence, dismissible because it describes a moment
             rather than a state. -->
        <div
          class="mx-3 mt-3 flex items-center gap-2 rounded-xl border pl-3.5 text-sm {NOTICE_CLASS[
            notice.tone
          ]}"
          role="status"
        >
          <span class="min-w-0 flex-1 py-2">{notice.text}</span>
          <TouchButton
            icon
            aria-label="Dismiss"
            class={notice.tone === "warn" ? "text-warn!" : "text-good!"}
            onclick={() => (notice = null)}>×</TouchButton
          >
        </div>
      {/if}

      <!-- THE FACTS CARD, the phone's version of the desktop's status box.
           That one is a single mono line — path · repo · agent · base — which at
           390 points wraps into an unreadable ribbon, so the same four facts
           become a definition list.

           GRID, NOT FLEX, and not for looks: WKWebView does not stretch a flex
           child inside a flex column (CLAUDE.md's WebKit note), so a two-column
           definition list built out of flex rows collapses to content width in
           the packaged app while looking correct in Chrome. -->
      <div class="mx-3 mt-3 rounded-xl border border-edge-soft bg-panel px-3.5 py-3">
        <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
          <dt class="text-faint">Path</dt>
          <!-- `break-all`, not `truncate`: a checkout path is the fact you are
               most likely to be checking character by character, and half of one
               is no use at all. -->
          <dd class="num break-all text-ink">{project.path || "(unset)"}</dd>
          <dt class="text-faint">Repo</dt>
          <dd class="num break-all text-ink">{project.repo || "(none)"}</dd>
          <dt class="text-faint">Agent</dt>
          <dd class="text-ink">{project.agent}</dd>
          <dt class="text-faint">Base</dt>
          <dd class="num break-all text-ink">{project.defaultBranch || "(default)"}</dd>
        </dl>

        <!-- The live half, under a hairline: the two health answers and the
             three counts. Wrapped rows rather than the desktop's one line, for
             the same width reason as above. -->
        <div
          class="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-edge-soft pt-3 text-sm"
        >
          {#if pollingOn}
            <span class="text-good">● Polling</span>
          {:else}
            <span class="text-faint">○ Not polling</span>
          {/if}
          <span class={project.agentOk ? "text-good" : "text-bad"}>
            {project.agentOk ? "✓ Agent ready" : "✗ Agent not ready"}
          </span>
          <!-- An em-dash rather than a number when the daemon is down: these
               counts come from its session snapshot, and a stale "2 live" from a
               daemon that is not running is a lie a glance cannot catch. -->
          <span class="num text-faint">{store.alive ? project.liveCounted : "—"} live</span>
          {#if project.needsYou > 0}
            <!-- Omitted at zero rather than drawn as "0 need you", which is a
                 sentence that draws the eye to say nothing. Orange is theme.ts's
                 own colour for the state. -->
            <span class="num text-orange">{project.needsYou} need you</span>
          {/if}
          {#if project.ciRed > 0}
            <span class="num text-bad">{project.ciRed} ci-red</span>
          {/if}
        </div>

        {#if !project.agentOk}
          <!-- The desktop's sentence, and unlike the desktop this screen means
               it: `canLaunch` above actually turns the three launch rows off. -->
          <p class="copy mt-2 text-sm text-bad">
            {project.agentErr
              ? `Agent not ready: ${project.agentErr}.`
              : "The coding agent, tmux or git could not be resolved on the Mac."}
            The launch actions are disabled until it resolves.
          </p>
        {/if}
      </div>

      <!-- THE ACTIONS. Each is a full-width row carrying the desktop's label and
           its one-line description, and NOT its keyboard hint — there is no
           keyboard here, and a letter in a box on a phone is a decoration that
           looks like a control. -->
      <div
        class="mx-3 mt-3 overflow-hidden rounded-xl border border-edge-soft bg-panel"
        role="group"
        aria-label="Project actions"
      >
        {#each actions as a (a.id)}
          <!-- `tap-row`, not `tap`: the row is already full width, so only the
               height needs the 44pt floor. -->
          <button
            type="button"
            class="tap-row flex w-full touch-manipulation items-center gap-3 border-b border-edge-soft
                   px-3.5 py-3 text-left last:border-b-0 {a.enabled
              ? 'active:bg-sel'
              : 'cursor-not-allowed opacity-40'}"
            disabled={!a.enabled || busy !== ""}
            onclick={a.run}
          >
            <span class="min-w-0 flex-1">
              <span class="block text-base font-medium text-ink">{a.label}</span>
              <span class="block text-sm text-faint">{a.desc}</span>
            </span>
            {#if busy === a.id}
              <span class="shrink-0 text-sm text-faint">…</span>
            {:else if !a.enabled && a.hint}
              <span class="shrink-0 text-right text-sm text-warn">{a.hint}</span>
            {/if}
          </button>

          {#if a.id === "worktree" && worktreeOpen}
            <!-- The form lives INSIDE the list, directly under the row that
                 opened it, so the thing being configured is never off screen
                 above the fields. It is a column rather than the desktop's
                 wrapped inline row: at 390 points a branch field, a segmented
                 pair, a picker and two buttons cannot share a line. -->
            <div class="flex flex-col gap-2 border-b border-edge-soft bg-canvas px-3.5 py-3">
              <!-- NO `text-*` HERE, deliberately. app.css pins every input to
                   16px in `@layer base` because iOS zooms the page when a field
                   under 16px takes focus, and a utility outranks a base layer —
                   so writing a size here would put that zoom back. -->
              <input
                class="w-full rounded border border-edge bg-canvas px-3 py-2.5 font-mono text-ink
                       outline-none focus:border-accent placeholder:text-placeholder"
                type="text"
                inputmode="text"
                autocapitalize="none"
                autocorrect="off"
                spellcheck="false"
                aria-label="Branch name"
                placeholder="branch name…"
                bind:value={branch}
                onkeydown={(e) => e.key === "Enter" && startWorktree()}
              />

              <!-- Agent or shell. A segmented pair rather than a checkbox: the
                   two options are equally ordinary, and "run an agent?" as a tick
                   box makes the shell the unnamed absence of the other. -->
              <div class="flex items-center gap-0.5 rounded-md border border-edge p-0.5" role="group" aria-label="What to run">
                <TouchButton class="flex-1" selected={useAgent} onclick={() => (useAgent = true)}>
                  Agent
                </TouchButton>
                <TouchButton class="flex-1" selected={!useAgent} onclick={() => (useAgent = false)}>
                  Shell
                </TouchButton>
              </div>

              {#if useAgent}
                <!-- WHICH agent, when it should not be the project's. The
                     shared <Select> keeps the native iOS picker (an AppKit/UIKit
                     menu outside the web view, with its own keyboard nav and
                     screen-reader semantics) and only replaces the chrome; the
                     child selector raises the 44pt floor without touching the
                     component. -->
                <Select
                  class="[&>select]:h-11"
                  bind:value={selectedAgent}
                  aria-label="Coding agent"
                >
                  <option value="">Project default ({project.agent || "claude"})</option>
                  <option value="claude">claude</option>
                  <option value="codex">codex</option>
                  <option value="opencode">opencode</option>
                </Select>
              {/if}

              <div class="flex items-center gap-2">
                <TouchButton
                  variant="primary"
                  class="flex-1"
                  disabled={!branch.trim() || busy !== ""}
                  onclick={startWorktree}
                >
                  {busy === "worktree" ? "Starting…" : "Start"}
                </TouchButton>
                <TouchButton class="flex-1" onclick={() => (worktreeOpen = false)}>
                  Cancel
                </TouchButton>
              </div>
            </div>
          {/if}
        {/each}
      </div>

      <!-- THIS PROJECT'S SESSIONS, drawn with the list's own row rather than a
           second arrangement of the same facts. <SessionRow> is the compact
           shape; the hero <SessionCard> deliberately is not used here, because
           this screen is a summary and a card that fills a third of it would
           make one session outrank the actions above. Tapping opens the
           terminal, exactly as it does on the sessions list. -->
      <SectionHeader title="Sessions" count={sessions.length} />
      {#if sessions.length === 0}
        <p class="copy px-5 py-6 text-center text-sm text-faint">
          No sessions in this project yet.
        </p>
      {:else}
        {#each shown as s (s.id)}
          <SessionRow
            session={s}
            projectLabel={title}
            onopen={() => nav.toTerminal(s.id, paneNameFor(s))}
          />
        {/each}
        {#if moreCount > 0}
          <!-- Hands over to the filtered sessions list rather than growing this
               one: everything past the cap is better read where the sections,
               the chips and the search already are. -->
          <div class="p-3">
            <TouchButton wide onclick={openSessions}>Show {moreCount} more</TouchButton>
          </div>
        {/if}
      {/if}
    {/if}
    <!-- No bottom safe-area spacer: the tab bar below pays that inset for every
         screen, and paying it twice leaves a band of empty list above it. -->
  </div>
</div>
