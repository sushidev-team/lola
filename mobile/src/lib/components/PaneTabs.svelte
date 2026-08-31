<script lang="ts">
  import TouchButton from "./TouchButton.svelte";
  import { DaemonService } from "@mobile/wailsshim";
  import type { PaneInfo } from "@mobile/wire";
  import { overflowFade, type OverflowEdges } from "@mobile/lib/edgefade";

  // The tab strip over one session's panes: agent, shells, dev tabs, review.
  //
  // WHY THE STRIP OWNS ITS OWN DATA rather than taking a `panes` prop. The
  // inventory is derived from tmux on every `cmd=panes` call — it is the live
  // truth about what exists, not a projection of the session record — and the
  // one thing that invalidates it is this component's own "+" button. Handing
  // the fetch to the screen would put a request, a refusal and a reload in a
  // file whose subject is a terminal, and would make the strip untestable
  // without mounting xterm. So the whole tab concern lives here and the screen
  // hands it two strings and a callback.
  //
  // WHY THERE IS NO "REFRESH". Same reason the sessions header lost one: a
  // manual button implies the automatic path cannot be trusted. The inventory
  // is reloaded when the session changes and after a shell is created, which
  // are the two moments it can change because of something this app did. A tab
  // created on the Mac appears the next time this screen is opened; that is a
  // known and deliberate gap, written up rather than papered over with a poll
  // that costs a tmux listing every few seconds for a screen that is usually
  // showing one pane.
  //
  // A FAILED LOAD IS NOT ONE THING, and the strip used to treat it as one.
  // Every failure rendered nothing at all, silently — which is right for a
  // session that is gone (the terminal's own banner already says so) and wrong
  // for the commonest cause by far: a phone NEWER THAN THE MAC'S DAEMON.
  // `cmd=panes` landed after the rest of the remote protocol, so an older `lola
  // run` answers `unknown cmd "panes"`, the strip and its "+" both vanish, and
  // nothing anywhere says a capability is missing rather than a feature being
  // broken. That is a fixable misconfiguration presented as an absence, and it
  // cost a reviewer a whole pass.
  //
  // So the two are now distinguished. A capability gap draws the row with a
  // sentence and a disabled "+", because the fix is one `go install` away on
  // the Mac and the user cannot guess that from a blank screen. Everything else
  // stays silent, because the terminal is already saying it. Note what is NOT
  // done here: no enabled "+" is offered in the gap. `cmd=shellCreate` shipped
  // in the same commit as `cmd=panes`, so a daemon that refuses one refuses the
  // other — a live-looking button whose only possible outcome is a second
  // refusal is worse than a disabled one that names the reason.
  //
  // A REFUSED CREATE is a different case again and is always surfaced: the user
  // pressed a button and is owed the reason it did nothing.

  let {
    /** The session whose panes these are. */
    session,
    /** The pane the screen is currently attached to. */
    active,
    /**
     * The id of the region these tabs control, for `aria-controls`. Optional:
     * the strip is perfectly usable without it, and a wrong id is worse than
     * none, so a caller that has no single element wrapping the pane omits it.
     */
    panelId,
    /** Attach to a pane. */
    onselect,
    /**
     * Something the user needs told — today, only a refused shell creation.
     * The daemon's own sentence is passed through verbatim: "session X has no
     * worktree", "…already has 16 shells, which is the cap". A generic "could
     * not create shell" throws away the only actionable half.
     */
    onnotice,
  }: {
    session: string;
    active: string;
    panelId?: string;
    onselect: (pane: string) => void;
    onnotice?: (message: string) => void;
  } = $props();

  let panes = $state<PaneInfo[]>([]);
  let canCreateShell = $state(false);
  let creating = $state(false);
  /**
   * The Mac's daemon does not know `cmd=panes`.
   *
   * Kept apart from a plain empty inventory because the two need opposite
   * treatments: this one is a message, an empty one is silence.
   */
  let unsupported = $state(false);

  const NONE: OverflowEdges = { left: false, right: false };
  let edges = $state<OverflowEdges>(NONE);
  let strip = $state<HTMLDivElement | undefined>();

  /**
   * Whether a rejection means "this daemon has never heard of that command".
   *
   * Matched on the daemon's own wire sentence — `unknown cmd "panes"` — which
   * is the only signal there is: the remote protocol carries no capability
   * list, and a version number would not help either, since what matters is
   * what this build's dispatcher actually answers. Anchored at the start so a
   * findings string or a session name that happens to contain the words cannot
   * trip it, and deliberately narrow: anything unrecognised falls through to
   * silence, which is the behaviour that was already there.
   */
  function isUnknownCommand(err: unknown): boolean {
    const m = err instanceof Error ? err.message : "";
    return /^unknown cmd\b/i.test(m);
  }

  /**
   * Load the inventory for `id`, discarding the answer if the screen has since
   * moved on.
   *
   * The guard is not theoretical: opening one session's terminal, going back
   * and opening another is two overlapping requests over one connection, and
   * the slower one answering last would draw the wrong session's tabs.
   */
  async function load(id: string): Promise<void> {
    if (id === "") {
      panes = [];
      canCreateShell = false;
      unsupported = false;
      return;
    }
    try {
      const d = await DaemonService.Panes(id);
      if (id !== session) return;
      panes = d.panes;
      canCreateShell = d.canCreateShell;
      unsupported = false;
    } catch (err) {
      if (id !== session) return;
      panes = [];
      canCreateShell = false;
      unsupported = isUnknownCommand(err);
    }
  }

  $effect(() => {
    void load(session);
  });

  /**
   * Keep the attached tab on screen.
   *
   * A session with several shells and a dev tab is wider than a phone, so the
   * tab the terminal below is actually showing can easily be scrolled off —
   * and then nothing on the screen says which pane is on screen. Cosmetic, and
   * guarded the same way the triage chips are: jsdom and older WebViews lack
   * the options overload.
   */
  $effect(() => {
    void active;
    void panes;
    const el = strip?.querySelector<HTMLElement>("[data-pane-selected='true']");
    if (!el || typeof el.scrollIntoView !== "function") return;
    if (takeFocus) {
      takeFocus = false;
      // Arrowing along the strip has to carry focus with the selection, or the
      // next arrow key arrives at a button that is no longer in the tab order
      // and the walk stops after one step.
      try {
        el.focus({ preventScroll: true });
      } catch {
        el.focus();
      }
    }
    try {
      el.scrollIntoView({ block: "nearest", inline: "nearest" });
    } catch {
      /* no options overload; the tab stays put */
    }
  });

  /** Set by the keyboard walk so the effect above moves focus, not by a tap. */
  let takeFocus = false;

  /**
   * Arrow-key movement along the strip.
   *
   * The tabs carry a ROVING TABINDEX — only the selected one is in the tab
   * order — which is the right pattern and was also, on its own, a trap: with
   * no key handler beside it every unselected pane became unreachable by a
   * hardware keyboard, which iPads have and which this app already handles
   * elsewhere (Sheet's Escape, MobileTerminal's shift+enter). So the walk is
   * implemented rather than the roving tabindex dropped.
   *
   * ACTIVATION IS AUTOMATIC — an arrow both moves and attaches — because that
   * is what these tabs already are: tapping one attaches its pane, the cost is
   * one subscription, and it is undone by pressing the other arrow. Manual
   * activation would need a focus cursor separate from the selection, which is
   * more machinery than the behaviour is worth.
   *
   * Bound to each TAB rather than to the tablist. That is where the event
   * actually originates — only a tab is focusable — and a listener on the
   * container would oblige the container to be focusable too, which is what
   * svelte's a11y_interactive_supports_focus rule is pointing at.
   */
  function onkeydown(e: KeyboardEvent): void {
    if (e.altKey || e.ctrlKey || e.metaKey || panes.length === 0) return;
    const here = panes.findIndex((p) => p.name === active);
    let next = -1;
    switch (e.key) {
      case "ArrowRight":
        next = here < 0 ? 0 : Math.min(panes.length - 1, here + 1);
        break;
      case "ArrowLeft":
        next = here < 0 ? 0 : Math.max(0, here - 1);
        break;
      case "Home":
        next = 0;
        break;
      case "End":
        next = panes.length - 1;
        break;
      default:
        return;
    }
    e.preventDefault();
    const target = panes[next];
    if (!target || target.name === active) return;
    takeFocus = true;
    onselect(target.name);
  }

  /**
   * Start a shell and attach to it.
   *
   * The daemon allocates the index and answers with the pane name; nothing here
   * invents one, because two phones and a desktop can be racing for
   * "-shell-2" and only the daemon sees all three.
   */
  async function createShell(): Promise<void> {
    if (creating || !canCreateShell) return;
    creating = true;
    try {
      const d = await DaemonService.ShellCreate(session);
      onnotice?.("");
      await load(session);
      onselect(d.pane);
    } catch (err) {
      onnotice?.(err instanceof Error && err.message ? err.message : "Could not start a shell.");
    } finally {
      creating = false;
    }
  }

  // The one sentence a disabled "+" gets, and there are two of them because
  // there are two reasons. `canCreateShell` is a single boolean by design — the
  // daemon folds a cap and a missing worktree into it — so the honest
  // explanation names both possibilities rather than guessing which. The
  // alternative was mirroring the cap constant here, which is precisely the
  // re-derivation the wire contract forbids. The capability gap is a different
  // sentence entirely: nothing about this session is wrong, the Mac is simply
  // running an older lola.
  const CANNOT_CREATE =
    "A new shell is not available for this session: it has no worktree, or it has reached the shell limit.";
  const NEEDS_NEWER_DAEMON =
    "New shells need a newer lola on the Mac. Update and restart it there, then reopen this session.";

  // Same three load-bearing classes as the accessory bar's key rows, for the
  // same reasons: `min-w-0` so a flex child may shrink below its content and
  // `overflow-x-auto` can engage at all, `py-1` because `overflow-x: auto`
  // forces `overflow-y` to compute to `auto` and would otherwise slice the
  // tabs' pressed state, and `overscroll-x-contain` so a swipe past the last
  // tab stays in the strip instead of becoming the WebView's back gesture.
  // Horizontal padding stays on the ROW: WebKit drops a horizontal scroller's
  // trailing padding, so `px-2` here would put the last tab against the clip.
  const STRIP =
    "flex min-w-0 flex-1 gap-1 overflow-x-auto overscroll-x-contain py-1 [scrollbar-width:none]";
</script>

<!-- Nothing to draw until the inventory has answered. An empty strip that fills
     in a moment later reads as a layout fault. The one failure that DOES draw
     is the capability gap, which would otherwise be an absence with no
     explanation anywhere. -->
{#if panes.length > 0 || unsupported}
  <div class="relative flex shrink-0 items-center border-b border-edge bg-panel px-2">
    {#if unsupported}
      <!-- The strip's place, holding the reason it is not a strip. Faint and
           one line, because it is a note about the Mac rather than an error in
           this session — the pane below is streaming perfectly well. -->
      <span class="min-w-0 flex-1 truncate py-3 text-sm text-faint">
        Panes need a newer lola on the Mac
      </span>
    {:else}
      <div
        bind:this={strip}
        class={STRIP}
        role="tablist"
        aria-label="Session panes"
        aria-orientation="horizontal"
        use:overflowFade={{ onedges: (e) => (edges = e) }}
      >
        {#each panes as p (p.name)}
          {@const on = p.name === active}
          <!-- Hand-rolled rather than a <TouchButton>, which is the documented
               exception: Button sets `aria-pressed` for `selected`, and
               aria-pressed on a role="tab" is an invalid pairing. The desktop's
               Tabs.svelte is hand-rolled for the same reason. The 44pt floor is
               kept here instead, as `h-11`.

               `onclick`, not the accessory bar's settled-press gesture. That
               gate exists because a bar key fires bytes into a live coding
               agent on pointerdown and a scroll of the row necessarily starts
               on a key; selecting a tab sends nothing and is reversible by
               selecting another, and WebKit already suppresses the click when a
               touch turns into a scroll. -->
          <button
            type="button"
            role="tab"
            aria-selected={on}
            aria-controls={panelId}
            data-pane-selected={on}
            tabindex={on ? 0 : -1}
            class="flex h-11 shrink-0 touch-manipulation items-center justify-center rounded-md
                   px-3 text-base whitespace-nowrap transition-colors select-none
                   {on
              ? 'bg-accent-fill font-medium text-accent-ink'
              : 'text-faint active:bg-sel'}"
            onclick={() => onselect(p.name)}
            {onkeydown}
          >
            {p.label || p.name}
          </button>
        {/each}
      </div>

      <!-- Painted on the ROW, over the strip's own background, and inset past
           the "+" so it marks the strip's edge rather than the row's. The mask
           alone is not enough on a strip that ends on clean tab boundaries: it
           can only dim what it covers, so a clip landing in a gap reads as a
           complete strip with nothing after it — which is the "the list
           silently ends" bug the accessory bar already had to fix once. -->
      {#if edges.left}
        <div
          class="pointer-events-none absolute inset-y-0 left-2 flex w-7 items-center justify-start
                 bg-gradient-to-r from-panel via-panel/80 to-transparent text-sm text-faint"
          aria-hidden="true"
        >
          ‹
        </div>
      {/if}
      {#if edges.right}
        <div
          class="pointer-events-none absolute inset-y-0 right-13 flex w-7 items-center justify-end
                 bg-gradient-to-l from-panel via-panel/80 to-transparent text-sm text-faint"
          aria-hidden="true"
        >
          ›
        </div>
      {/if}
    {/if}

    <!-- OUTSIDE the scroller, pinned. This is the whole reason the row is split
         in two, and it is the accessory bar's chevron lesson applied: a "+" that
         is the last item of the strip scrolls away exactly when there are enough
         tabs to need it, which is the only moment it matters. The `ml-1` stands
         in for the `gap-1` the strip owns; TouchButton's own `shrink-0` keeps it
         from being squeezed by a full strip.

         A REAL <TouchButton>, unlike the tabs above it. The hand-roll there is
         the documented exception — Button sets `aria-pressed` for `selected`,
         which is invalid on a `role="tab"` — and that reason does not reach this
         button, which is an ordinary action outside the tablist. It was
         hand-rolled anyway and re-stated four things the shared control already
         owns: the 44pt square, the disabled fade, the in-flight spinner, and the
         rule that a working control must not wear the 40% of a dead one. -->
    <TouchButton
      icon
      variant="secondary"
      loading={creating}
      disabled={!canCreateShell}
      aria-label={canCreateShell ? "New shell" : unsupported ? NEEDS_NEWER_DAEMON : CANNOT_CREATE}
      title={canCreateShell ? "New shell" : unsupported ? NEEDS_NEWER_DAEMON : CANNOT_CREATE}
      class="ml-1 text-base! select-none"
      onclick={createShell}
    >
      <!-- The glyph steps aside while the spinner is up, which is what the
           shared Button asks of a call site that draws its own state mark: the
           spinner takes that slot, and "+" beside it in a 44pt square is two
           marks fighting over one. -->
      {#if !creating}+{/if}
    </TouchButton>
  </div>
{/if}
