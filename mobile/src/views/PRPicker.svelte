<script lang="ts">
  import { onMount } from "svelte";
  import type { PrRow, PrsData } from "@bindings/internal/protocol";
  import { store } from "$lib/store.svelte";
  import { statusLabel } from "$lib/theme";
  import MetaPill from "@mobile/lib/components/MetaPill.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import BackIcon from "@mobile/lib/icons/BackIcon.svelte";
  import { nav } from "@mobile/lib/nav.svelte";
  import { statusTone } from "@mobile/lib/statustone";
  import { DaemonService } from "@mobile/wailsshim";

  // The project detail's "Open a PR": the repository's open pull requests, one
  // per row, and a tap puts a coding agent on one.
  //
  // IT IS A SCREEN, NOT A SHEET, and `nav.pick` is what says so. The desktop's
  // PRPicker is a table in a pane beside the rest of the cockpit; the same facts
  // on a phone are a full list that needs the whole display, and it has to be
  // somewhere a development link can name (`?tab=projects&project=x&pick=prs`)
  // because the Simulator has no gesture API and a screen only a tap can reach
  // is a screen no reviewer can photograph. The picker sits INSIDE the Projects
  // tab rather than over it, so the tab bar stays drawn and pays its own bottom
  // inset — which is why nothing here reserves one.
  //
  // WHAT CARRIES OVER FROM THE DESKTOP, unchanged, is the reasoning about the
  // cache. `cmd=prs` is served from a short-TTL snapshot the daemon refreshes by
  // exec'ing `gh pr list`, and the reply says how old that snapshot is
  // (`ageSeconds`) and whether it is past its TTL (`stale`). A stale list drawn
  // as a fresh one is the failure this whole field pair exists to prevent: the
  // PR you tap may have been merged, and the one you wanted may not be on the
  // screen at all. So the age is always stated and a stale list says so in a
  // band of its own, with the refresh that bypasses the TTL right beside it.
  //
  // WHAT DOES NOT CARRY OVER is every action but one. The desktop offers Shell
  // (a detached worktree for running a PR), Agent, and Browser; on this app the
  // daemon's remote policy allows `openPr` and not `open`, and a URL opened
  // through `cmd=openURL` opens on the MAC rather than on the phone, which is
  // not what a person tapping a PR on a train means. So a row does exactly one
  // thing, and that is the whole interaction.

  /**
   * The project this picker belongs to. Read from `nav` rather than taken as a
   * prop for the same reason the desktop's does: the picker is a PLACE, so the
   * place is the state, and a prop would give the routing screen a second copy
   * of it that could disagree with the link that opened it.
   */
  const project = $derived(nav.project);

  /**
   * The configured project, when the daemon's projects push has landed.
   *
   * `repoConfigured` is the daemon's own answer to exactly this screen's
   * question — the field's comment in protocol.go says "needed by the PR
   * picker" — so an unconfigured repo is read as a FACT here rather than
   * recognised from the wording of an error string. That matters twice: it
   * spares a request that is certain to fail, and it means the state survives a
   * future daemon rewording its message. When the push has not landed yet the
   * request goes out anyway and the daemon's own sentence is what gets shown;
   * if the push then arrives saying there is no repo, this takes over.
   */
  const info = $derived(store.projects.find((p) => p.name === project));
  const repoMissing = $derived(info !== undefined && !info.repoConfigured);

  let data = $state<PrsData | null>(null);
  let loading = $state(true);
  /** The daemon's sentence for a list that could not be fetched. */
  let error = $state("");
  /** The daemon's sentence for a PR it declined to open. Dismissible. */
  let refusal = $state("");
  /** The PR number whose agent is being launched, 0 for none. */
  let opening = $state(0);

  // `prs` is `PrRow[] | null` in the generated model — Go's encoder can emit a
  // null slice — so the coalesce is not defensive noise.
  const rows = $derived(data?.prs ?? []);

  /**
   * How old the served snapshot is, in words.
   *
   * The desktop prints raw seconds ("312s ago") because it sits beside a
   * refresh button someone is already looking at. On a phone this line is read
   * at a glance and is the only thing saying whether the list can be trusted,
   * and "5m ago" is a judgement a person can make while "312s ago" is
   * arithmetic. Rounded DOWN at every step: a snapshot claimed younger than it
   * is would be the same lie the `stale` flag exists to prevent.
   */
  function agePhrase(seconds: number): string {
    const s = Math.max(0, Math.trunc(seconds));
    if (s < 60) return `${s}s ago`;
    if (s < 3600) return `${Math.floor(s / 60)}m ago`;
    return `${Math.floor(s / 3600)}h ago`;
  }

  // repo · count · age. The repo is the one thing here that names WHERE these
  // PRs come from, and a picker that does not say so is one config mistake away
  // from listing a different team's work.
  const freshness = $derived.by(() => {
    if (!data) return "";
    const parts: string[] = [];
    if (data.repo) parts.push(data.repo);
    parts.push(rows.length === 1 ? "1 open" : `${rows.length} open`);
    parts.push(agePhrase(data.ageSeconds));
    return parts.join(" · ");
  });

  /**
   * Fetch the list. `refresh` bypasses the daemon's TTL and re-execs gh.
   *
   * The previous list is deliberately LEFT ON SCREEN while a refresh runs —
   * only a first load has nothing to show — because the commonest reason to
   * refresh is a stale banner, and blanking the rows to fetch a newer copy of
   * the same rows makes the screen flash for no information gained.
   */
  async function load(refresh = false) {
    loading = true;
    error = "";
    try {
      data = await store.prs(project, refresh);
    } catch (e) {
      // The daemon's own sentence, verbatim: it distinguishes "no repo
      // configured", "unknown project" and a gh failure, and this app has no
      // recovery to offer for the last one — so naming it exactly is the only
      // useful thing it can do. A composed "couldn't load PRs" would throw away
      // the only half a person can act on.
      error = e instanceof Error && e.message ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    if (repoMissing) {
      loading = false;
      return;
    }
    void load();
  });

  /**
   * Put a coding agent on this PR's head branch, then leave for the sessions
   * list filtered to this project.
   *
   * IT ASKS THE DAEMON EVEN WHEN IT EXPECTS A NO, and that is deliberate for
   * both refusals a row can carry. `alreadyOpen` is decorated onto a CACHED
   * list, so a client-side refusal based on it can be wrong in both directions
   * — the session may have been killed minutes ago, or another may have claimed
   * the branch since. And the daemon's refusal names the session holding it,
   * which is the actionable half a locally invented sentence would not have.
   * `isFork` never changes, so a local rule could be right about it, but the
   * phone has no fallback to offer either way (the detached "run and test" open
   * the daemon suggests is a Mac-only action, not in this client's allowed
   * set), so a second copy of that rule would only reword the same dead end.
   * The markers on the row are what make the outcome unsurprising.
   *
   * `DaemonService.OpenPR` rather than `store.openPr`, which is the shared
   * store's flash-wrapped version: this app draws no flash surface at all
   * (nothing renders `store.flash`), so going through it would swallow the
   * daemon's sentence entirely and a refusal would look like a tap that did
   * nothing. Same call, same wire frame; only the error handling differs.
   */
  async function openAgent(p: PrRow) {
    if (opening !== 0) return; // a second tap would spawn a second agent
    opening = p.number;
    refusal = "";
    try {
      await DaemonService.OpenPR({
        project,
        branch: p.branch,
        number: p.number,
        isFork: p.isFork,
      });
      // Not awaited: the bridge polls sessions on its own cadence anyway, and
      // holding the transition open on a second round trip would make a
      // successful tap feel slower than a refused one.
      void store.refresh();
      // The picker closes with the navigation — it belongs to the detail it was
      // opened over, and coming back to the Projects tab should land on that
      // detail rather than re-opening a list of PRs one of which was just
      // taken. The filter is the project's NAME, which is the identity every
      // session carries in its `project` field, and it is the same thing a tap
      // on a project row does; a triage bucket left standing would hide the
      // session that was just created.
      nav.toPick("");
      nav.query = project;
      nav.triage = "";
      nav.toTab("sessions");
    } catch (e) {
      refusal =
        e instanceof Error && e.message
          ? e.message
          : "The Mac refused to open this pull request.";
    } finally {
      opening = 0;
    }
  }
</script>

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <!-- The identity-row header shape rather than the large-title one: this
       screen is PUSHED, so a back control has to share the line, and the
       brief's 28px title beside two 44-point buttons leaves nothing for the
       title itself at 390 points. `text-lg` is the next step down that is still
       a heading. The top inset is spelled out at the point of use because
       App.svelte sets `--lola-top-inset: 0px` on the container when its
       development banner has already paid it, and a var() baked into a spacing
       token could never see that override (see app.css). -->
  <header
    class="flex shrink-0 flex-col gap-0.5 px-3 pb-2"
    style="padding-top: calc(var(--lola-top-inset, env(safe-area-inset-top, 0px)) + 8px)"
  >
    <div class="flex items-center gap-2">
      <!-- `text-accent!` with the trailing `!`: a plain `text-accent` ties with
           the ghost variant's own `text-faint` and the winner is decided by
           Tailwind's order in the compiled sheet (CLAUDE.md's Button
           invariant). `nav.back()` and not a hand-written assignment — back()
           is ordered deepest-first and closing the picker is its job. -->
      <TouchButton icon aria-label="Back to the project" class="text-accent!" onclick={() => nav.back()}>
        <BackIcon />
      </TouchButton>
      <h1 class="min-w-0 flex-1 truncate text-lg text-ink">Open a PR</h1>
      <!-- REFRESH IS IN THE HEADER, not only in the stale band, because the
           list can be quietly out of date without being past its TTL — a PR
           merged four seconds ago is inside every cache window there is. -->
      <TouchButton {loading} disabled={loading || repoMissing} onclick={() => load(true)}>
        Refresh
      </TouchButton>
    </div>
    <!-- ONE line, truncated: repo · count · age. The `num` run keeps the age
         from reflowing the line each time it is redrawn. -->
    {#if freshness}
      <span class="num truncate text-sm text-faint">{freshness}</span>
    {:else}
      <span class="truncate text-sm text-faint">{store.displayNameFor(project)}</span>
    {/if}
  </header>

  {#if data?.stale}
    <!-- THE STALE BAND. `stale` means the daemon served a snapshot past its own
         TTL, which happens when a refresh is running or has FAILED — so this is
         not merely "slightly old", it is "the Mac could not get a newer one".
         Saying it plainly, with the bypass right there, is the whole point of
         the daemon shipping the flag. `warn` rather than `bad`: the list is
         still usable, it is just not authoritative. -->
    <div
      class="flex shrink-0 items-center gap-2 border-b border-warn/40 bg-warn/10 px-4 py-2 text-sm text-warn"
      role="status"
    >
      <span class="min-w-0 flex-1">
        Cached list from {agePhrase(data.ageSeconds)} — the Mac has not refreshed it.
      </span>
      <TouchButton {loading} class="text-warn!" onclick={() => load(true)}>Refresh</TouchButton>
    </div>
  {/if}

  {#if refusal}
    <!-- A REFUSED TAP, in the daemon's own words. It names the session already
         holding the branch, or why a fork cannot be pushed back to, and a
         generic "could not open this PR" would throw exactly that away.
         Dismissible, because it describes a moment rather than a state. -->
    <div
      class="flex shrink-0 items-start gap-2 border-b border-warn/40 bg-warn/10 pl-4 text-sm text-warn"
      role="status"
    >
      <span class="min-w-0 flex-1 py-2">{refusal}</span>
      <TouchButton icon aria-label="Dismiss" class="text-warn!" onclick={() => (refusal = "")}>
        <span aria-hidden="true">×</span>
      </TouchButton>
    </div>
  {/if}

  <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain pb-4">
    {#if repoMissing}
      <!-- THE FIRST OF THREE DISTINCT EMPTINESSES. This one is a configuration
           fact, not a fetch outcome: the project has no GitHub `owner/name`, so
           there is nothing to list and no refresh that could change it. It is
           also the one this app cannot fix — a ConfigService write answers
           `unsupported` on mobile — so the sentence says where the fix lives
           instead of offering a button that would lie. -->
      <div class="flex flex-col items-center gap-2 px-8 py-12 text-center">
        <span class="text-lg text-ink">No repository configured</span>
        <span class="copy text-body text-faint">
          {store.displayNameFor(project)} has no GitHub repository set, so there are no pull
          requests to list. Set owner/name on the Mac, in config.toml — this app only reads it.
        </span>
      </div>
    {:else if loading && !data}
      <!-- Never a blank screen. A first load execs `gh pr list` on the Mac and
           can take seconds; an empty scroller for that long is indistinguishable
           from a repository with no open PRs, which is one of the states below
           and means something entirely different. -->
      <div class="flex flex-col items-center gap-2 px-8 py-12 text-center">
        <span class="text-lg text-ink">Loading pull requests…</span>
        <span class="copy text-body text-faint">
          Asking the Mac for {store.displayNameFor(project)}'s open pull requests.
        </span>
      </div>
    {:else if error}
      <!-- THE SECOND: the daemon answered, and the answer was a failure. gh is
           not authenticated, the network is down, the repo is gone — this app
           can do none of those from a phone, so it names the failure exactly as
           the daemon worded it and offers the one thing it can: try again. -->
      <div class="flex flex-col items-center gap-2 px-8 py-12 text-center">
        <span class="text-lg text-ink">Couldn't list the pull requests</span>
        <span class="copy text-body text-bad">{error}</span>
        <span class="copy text-body text-faint">
          This runs `gh` on the Mac. Nothing on the phone can fix it, but the Mac may have
          recovered since.
        </span>
        <TouchButton wide variant="primary" {loading} onclick={() => load(true)}>
          Try again
        </TouchButton>
      </div>
    {:else if rows.length === 0}
      <!-- THE THIRD: everything worked and there is genuinely nothing open. The
           refresh matters most here of all three — an empty list served from
           cache and an empty repository look identical — so the age is repeated
           in the sentence rather than left to the header. -->
      <div class="flex flex-col items-center gap-2 px-8 py-12 text-center">
        <span class="text-lg text-ink">No open pull requests</span>
        <span class="copy text-body text-faint">
          {data?.repo || store.displayNameFor(project)} had none {data ? agePhrase(data.ageSeconds) : ""}.
        </span>
        <TouchButton wide variant="primary" {loading} onclick={() => load(true)}>
          Refresh
        </TouchButton>
      </div>
    {:else}
      <ul>
        {#each rows as p (p.number)}
          <li>
            <!-- Hand-rolled as a <button> rather than a <TouchButton>:
                 CLAUDE.md's Button invariant names card-shaped rows as one of
                 the five things that stay hand-rolled on purpose, and this is
                 the same shape SessionRow draws. `tap-row` gives it the 44-point
                 floor without making it square — a list row is already full
                 width, so only the height needs one. -->
            <button
              type="button"
              class="tap-row flex w-full touch-manipulation flex-col gap-[3px] border-b border-edge-soft
                     px-5 py-[11px] text-left transition-colors active:bg-sel disabled:opacity-55"
              disabled={opening !== 0}
              onclick={() => openAgent(p)}
            >
              <!-- THE TITLE IS UNTRUSTED. It is whatever a pull request author
                   typed on GitHub, so it is a text node and nothing else — never
                   markup, never a URL something is built from.

                   Two lines rather than one truncated one, and this is the row's
                   whole reason for its shape: at 390 points one line of a PR
                   title is about five words, and a list of PRs you cannot tell
                   apart is not a picker. So the title gets the full width and
                   every fact that identifies it — number, author, branch — drops
                   to the line below, where each is short enough to survive. -->
              <span class="line-clamp-2 w-full text-base font-medium text-ink">{p.title}</span>

              <!-- The facts line, wrapping rather than crushing. At an
                   accessibility text size this is far wider than the screen, so
                   what may give way is CHOSEN: the number and the status word
                   are `shrink-0` (they are short, and the number is the row's
                   citation handle — "#1…" cites nothing), and the BRANCH is the
                   single item allowed to truncate, being the only free-form
                   string here that can be long. -->
              <span class="flex w-full flex-wrap items-center gap-x-1.5 gap-y-0.5 text-sm text-faint">
                <span class="num shrink-0">#{p.number}</span>
                <span aria-hidden="true">·</span>
                <!-- The delivery word, from the SHARED vocabulary. `p.status` is
                     `state.DeriveDelivery` run over this PR's facts on the
                     daemon (daemon.openPRStatus), so "ci failed" / "changes" /
                     "approved" here mean exactly what they mean on a session
                     row, and the checks and review glyphs the desktop's table
                     draws in columns of their own would only say the same thing
                     a second time. `statusTone` is mobile's one local rule: the
                     statuses theme.ts does not name step down to faint instead
                     of printing in the heading's ink. -->
                <span class="shrink-0 {statusTone(p.status)}">{statusLabel(p.status)}</span>
                {#if p.author}
                  <span aria-hidden="true">·</span>
                  <span class="shrink-0">{p.author}</span>
                {/if}
                <span aria-hidden="true">·</span>
                <span class="num min-w-0 truncate">{p.branch}</span>

                <!-- The markers that explain a tap BEFORE it is refused, wrapped
                     as one right-aligned group so they travel together when the
                     line breaks. `draft` is not implied by the status word —
                     a draft with failing checks derives `ci_failed`, so the two
                     facts are independent — and neither `fork` nor `already
                     open` is in the delivery vocabulary at all.

                     MetaPill renders a plain <span> unless it is given an
                     `onclick`, which is what lets it sit inside this button: a
                     nested <button> does not parse, and the parser would close
                     the outer one and take the row's tap with it. -->
                {#if p.isDraft || p.isFork || p.alreadyOpen}
                  <span class="ml-auto flex shrink-0 items-center gap-1">
                    {#if p.isDraft}<MetaPill tone="grey" ground="grey">draft</MetaPill>{/if}
                    {#if p.isFork}<MetaPill tone="grey" ground="grey">fork</MetaPill>{/if}
                    {#if p.alreadyOpen}<MetaPill tone="grey" ground="grey">already open</MetaPill>{/if}
                  </span>
                {/if}
              </span>
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</div>
