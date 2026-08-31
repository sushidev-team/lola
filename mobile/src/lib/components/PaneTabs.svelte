<script lang="ts">
  import { onDestroy, untrack } from "svelte";
  import PaneMenuSheet from "./PaneMenuSheet.svelte";
  import TouchButton from "./TouchButton.svelte";
  import { DaemonService } from "@mobile/wailsshim";
  import { PANE_KIND_AGENT, type PaneInfo } from "@mobile/wire";
  import { connection } from "@mobile/lib/connection.svelte";
  import { overflowFade, type OverflowEdges } from "@mobile/lib/edgefade";
  import { LONG_PRESS_MS, beginKey, cancelKey, moveKey, type KeyGesture } from "@mobile/lib/keygesture";
  import { loadPaneLabels, prunePaneLabels, savePaneLabel } from "@mobile/lib/prefs";

  // The tab strip over one session's panes: agent, shells, dev tabs, review.
  //
  // WHY THE STRIP OWNS ITS OWN DATA rather than taking a `panes` prop. The
  // inventory is derived from tmux on every `cmd=panes` call — it is the live
  // truth about what exists, not a projection of the session record — and
  // everything that invalidates it is either this component's own doing or a
  // signal it can be handed as one number. Putting the fetch in the screen would
  // spread a request, a refusal and a reload through a file whose subject is a
  // terminal, and would make the strip untestable without mounting xterm.
  //
  // WHEN IT RELOADS, AND WHY THERE IS STILL NO TIMER. The inventory used to be
  // fetched exactly twice: on the session prop changing, and after this
  // component created a shell. That left a real hole, because a shell that EXITS
  // ends its tmux session — shells get no `remain-on-exit`, only dev tabs do, so
  // that a crashed dev server stays readable — and `cmd=panes` derives from
  // tmux, so the daemon stops listing it the instant it goes while the app kept
  // drawing its tab forever. The fix is more MOMENTS, not a poll:
  //
  //   * the session changed, or this component created or closed a pane;
  //   * `refreshKey` was bumped by the screen, which does it when the pane
  //     stream reports an exit and when an attach is refused `unknown_pane`;
  //   * the connection came back, since anything could have happened while the
  //     phone was in somebody's pocket;
  //   * the screen was re-entered, which needs no code at all — App.svelte
  //     mounts the terminal behind `{#if}`, so returning to it remounts this
  //     component and the load effect runs.
  //
  // A poll was rejected for the reason it always is here: it costs a tmux
  // listing every few seconds for a screen that is usually showing one pane and
  // usually correct.
  //
  // AND WHEN THE VIEWED PANE IS THE ONE THAT WENT, the strip moves the user off
  // it. A dead terminal with a live strip above it is the app claiming the tab
  // still means something. See `reconcileActive` for the three rules that keeps
  // — most importantly that an EMPTY inventory navigates nowhere, because the
  // last frame is the only artefact left and yanking the user off it destroys
  // the one thing worth reading.
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
    /**
     * Bumped by the screen when something it can see — and this component
     * cannot — has invalidated the inventory: a pane exiting, an attach refused
     * because the pane is gone. A NUMBER rather than an exported `reload()`
     * because this file has no imperative handle and a `bind:this` would be the
     * first one, for a signal that is a single integer.
     */
    refreshKey = 0,
    /**
     * The pane whose menu is open, or "". Bindable so the screen can put it in
     * `nav` — a menu only a long press can open is a menu no screenshot can
     * reach, and the Simulator has no gesture API. It works uncontrolled too.
     */
    menuPane = $bindable(""),
    /** Attach to a pane. */
    onselect,
    /**
     * Something the user needs told: a refused shell creation, a refused close,
     * or the fact that the pane they were reading has gone and this is where
     * they went instead. The daemon's own sentence is passed through verbatim
     * for the refusals — "session X has no worktree", "…already has 16 shells,
     * which is the cap" — because a generic "could not do that" throws away the
     * only actionable half.
     */
    onnotice,
    /**
     * The live inventory, after every SUCCESSFUL load: which panes of `session`
     * the daemon says exist.
     *
     * IT EXISTS FOR THE SIZE PIN, which has no other way to learn that a pane it
     * is holding has gone. A release names a pane whose tmux window may already
     * be dead, and the daemon refuses such a release — it validates the pane by
     * name convention and then asks tmux to resize something that is not there —
     * so without this the app warns forever that a window is squashed when no
     * window exists. This component is the one that knows, because it is the one
     * that asks; see panepin.ts's forgetMissing for what is done with it.
     *
     * Only on SUCCESS, for the same reason `prunePaneLabels` is: a refused or
     * unsupported `cmd=panes` means the inventory is unknown, not empty, and
     * "nothing exists" is the one answer that must never be inferred from a
     * failure.
     */
    onpanes,
  }: {
    session: string;
    active: string;
    panelId?: string;
    refreshKey?: number;
    menuPane?: string;
    onselect: (pane: string) => void;
    onnotice?: (message: string) => void;
    onpanes?: (session: string, names: string[]) => void;
  } = $props();

  let panes = $state<PaneInfo[]>([]);
  let canCreateShell = $state(false);
  let creating = $state(false);
  /** The pane a close is in flight for, or "". */
  let closing = $state("");
  /**
   * The Mac's daemon does not know `cmd=panes`.
   *
   * Kept apart from a plain empty inventory because the two need opposite
   * treatments: this one is a message, an empty one is silence.
   */
  let unsupported = $state(false);

  /**
   * The nicknames this device holds, keyed by tmux pane name.
   *
   * Reassigned wholesale by every successful load, from `prunePaneLabels`'s
   * return value — which is the read and the garbage collection in one call.
   * See prefs.ts for why a label whose pane has gone must not survive: the
   * daemon allocates the lowest free shell index, so the next shell opened
   * would inherit it.
   */
  let labels = $state<Record<string, string>>(loadPaneLabels());

  const NONE: OverflowEdges = { left: false, right: false };
  let edges = $state<OverflowEdges>(NONE);
  let strip = $state<HTMLDivElement | undefined>();

  /** The pane the open menu is about, or undefined when none is. */
  const menuFor = $derived(panes.find((p) => p.name === menuPane));

  /** What a tab shows: this device's nickname, else the daemon's own label. */
  function displayName(p: PaneInfo): string {
    return labels[p.name] || p.label || p.name;
  }

  /**
   * The accessible name for a tab.
   *
   * A RENAMED TAB CARRIES BOTH NAMES, because the nickname is this phone's and
   * the daemon's label is what anybody at the Mac can see. "notes (shell 2)"
   * keeps a renamed tab identifiable across the two machines; a bare "notes"
   * would leave a screen-reader user holding a word that appears nowhere else.
   * Undefined when nothing was renamed, so an ordinary tab keeps its text
   * content as its name and nothing about it changes.
   */
  function tabName(p: PaneInfo): string | undefined {
    const nick = labels[p.name];
    if (!nick) return undefined;
    const from = p.label || p.name;
    return nick === from ? nick : `${nick} (${from})`;
  }

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
   *
   * `announce` says whether a pane vanishing from under the user is NEWS. It is
   * false when the user did the vanishing — closing the tab they were on — and
   * true when the pane died on its own, which is the case where somebody
   * suddenly looking at a different terminal is owed a sentence.
   */
  /**
   * Which load is the current one.
   *
   * The session guard below is not enough on its own: two loads for the SAME
   * session overlap routinely — a reconnect landing beside a `refreshKey` bump,
   * a create followed by an exit — and nothing makes a socket answer them in
   * order. The older answer arriving last redrew the strip from a list that
   * predates a shell it does not mention, resurrecting a tab the daemon has
   * killed and pruning that shell's nickname. It now also decides what the size
   * pin believes exists, where a stale list is the difference between a record
   * retired correctly and a developer's window left squashed with nothing
   * naming it. So a superseded answer is dropped whole.
   */
  let loadSeq = 0;

  async function load(id: string, announce = false): Promise<void> {
    if (id === "") {
      panes = [];
      canCreateShell = false;
      unsupported = false;
      return;
    }
    // Captured BEFORE the await: where the attached pane sat in the old list is
    // the only thing that says where to go if it is not in the new one.
    const wasAt = panes.findIndex((p) => p.name === active);
    const seq = ++loadSeq;
    try {
      const d = await DaemonService.Panes(id);
      if (id !== session || seq !== loadSeq) return;
      panes = d.panes;
      canCreateShell = d.canCreateShell;
      unsupported = false;
      // Only on the SUCCESS path. A refused or unsupported call means the
      // inventory is unknown, not empty, and pruning against nothing would wipe
      // every nickname the moment the Mac's daemon was too old.
      labels = prunePaneLabels(d.panes.map((p) => p.name));
      onpanes?.(id, d.panes.map((p) => p.name));
      // A menu cannot outlive the tab it is about.
      if (menuPane !== "" && !d.panes.some((p) => p.name === menuPane)) menuPane = "";
      reconcileActive(wasAt, announce);
    } catch (err) {
      if (id !== session || seq !== loadSeq) return;
      panes = [];
      canCreateShell = false;
      unsupported = isUnknownCommand(err);
      menuPane = "";
    }
  }

  /**
   * Move off a pane that is no longer there.
   *
   * Three rules, in the order they matter:
   *
   *   1. AN EMPTY INVENTORY NAVIGATES NOWHERE. A session whose panes are all
   *      gone still has its last frame on screen and the terminal's own "this
   *      session ended" banner under it, which is the only artefact left;
   *      yanking the reader somewhere else destroys it to no purpose.
   *   2. A pane that is still listed is left alone, whichever other tab went.
   *      A redrawn strip is fine; a navigation nobody asked for is not.
   *   3. Otherwise take the tab that inherited the position — same index,
   *      clamped — falling back to the first, which the daemon's ordering
   *      guarantees is the agent pane. Closing "shell 2" of three shells should
   *      land on the shell that is now second, not at the far end of the strip.
   *
   * `onselect` comes BEFORE `onnotice` deliberately: the screen's `attach`
   * clears its notice banner on the way through, so a sentence sent first would
   * be wiped by the very move it describes.
   */
  function reconcileActive(wasAt: number, announce: boolean): void {
    if (panes.length === 0 || active === "") return;
    if (panes.some((p) => p.name === active)) return;
    const i = Math.min(Math.max(wasAt, 0), panes.length - 1);
    const next = panes[i] ?? panes[0];
    if (!next) return;
    onselect(next.name);
    if (announce) onnotice?.(`That pane closed. Showing ${displayName(next)} instead.`);
  }

  // ONE EFFECT FOR BOTH TRIGGERS, not two. A second effect on `refreshKey`
  // would fetch the inventory twice on mount, which is a wasted round trip at
  // the slowest moment of the screen's life. The plain `let` is an edge
  // detector and must not be `$state`: an effect that reads and writes the same
  // rune re-invalidates itself forever.
  let lastSession = "";
  $effect(() => {
    const id = session;
    void refreshKey;
    const switched = id !== lastSession;
    lastSession = id;
    // UNTRACKED, and it has to be. `load` reads `panes` and `active`
    // synchronously — it captures where the attached tab sat before the fetch —
    // and everything before an effect's first `await` runs inside its tracking
    // scope. Without this the effect depends on the very state it goes on to
    // write, so one mount fetched the inventory twice and every tab switch
    // fetched it again. The dependencies here are `session` and `refreshKey`,
    // and those are the two read above.
    //
    // A session switch is not news — the user asked for it, and the panes of
    // the session they left are not gone, merely elsewhere.
    void untrack(() => load(id, !switched));
  });

  // BACK FROM THE POCKET. iOS tears the socket down when the app backgrounds
  // (see appstate.ts and Connection#reconnect), and anything at all can have
  // happened on the Mac meanwhile: shells exited, dev tabs started, sessions
  // cleaned up. A reconnect is the one moment worth a fetch that is not a timer.
  // Fires only on the false -> true EDGE, so the ordinary `ready` that the first
  // connection settles into does not double the load effect's request.
  let wasReady = connection.ready;
  $effect(() => {
    const ready = connection.ready;
    const back = ready && !wasReady;
    wasReady = ready;
    if (back && session !== "") void untrack(() => load(session, true));
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

  // A HOLD MUST NOT OUTLIVE THE STRIP. The timer is half a second long and the
  // thing it does is assign `menuPane`, which the terminal screen binds to
  // `nav` — so a press begun a moment before the user taps Back opened a pane
  // sheet over the sessions list, for a pane on a screen that no longer exists.
  // Tearing the strip down is exactly as good a reason to abandon a press as the
  // finger travelling.
  onDestroy(clearHold);

  // -------------------------------------------------------------------------
  // The long press
  // -------------------------------------------------------------------------
  //
  // A PRESS THAT TRAVELS IS A SCROLL, and this strip has exactly the geometry
  // that makes that a hazard rather than a nicety: `overflow-x-auto` with
  // wall-to-wall tabs, so a sideways swipe to reach the review tab NECESSARILY
  // begins on some other tab. The accessory bar solved this once already — a
  // drag begun on its ^Z key suspended a live Claude Code session — so its gate
  // is reused rather than a second one written. `beginKey`/`moveKey`/`cancelKey`
  // own the 8px slop; only the timer is this file's, and only because a hold
  // means something different from a press (see LONG_PRESS_MS).
  //
  // WHAT IS DELIBERATELY NOT COPIED from AccessoryKey: it calls
  // `preventDefault` on pointerdown to keep the soft keyboard up. A tab has no
  // such obligation, and preventing the default there suppresses the click the
  // ordinary tap path depends on.

  let gesture: KeyGesture | undefined;
  let holdTimer: ReturnType<typeof setTimeout> | undefined;
  /**
   * The lift that ends a long press must not also select the tab.
   *
   * Cleared by the NEXT press rather than by the click it suppresses: a menu
   * opened by a hold is usually dismissed by a tap on the sheet's backdrop, so
   * the click it is waiting for never arrives, and a flag cleared only there
   * would swallow the following genuine tap on that tab.
   */
  let suppressClick = false;

  function clearHold(): void {
    if (holdTimer === undefined) return;
    clearTimeout(holdTimer);
    holdTimer = undefined;
  }

  function holdStart(e: PointerEvent, p: PaneInfo): void {
    // THE FIRST FINGER ONLY, and the main button only. A second finger landing
    // on another tab used to replace the first one's gesture wholesale: it
    // cleared the pending hold, reset `suppressClick` — so the first finger's
    // lift then SELECTED the tab whose menu had just opened — and handed its own
    // hold to whichever finger lifted first. A phone with two thumbs on a
    // scrolling strip is not an exotic case, and one gesture at a time is the
    // whole of the fix. `button` guards the mouse an iPad can have attached;
    // the browser's own context menu is prevented separately.
    //
    // COMPARED AGAINST `false`, NOT COERCED. An engine or a synthesized event
    // that carries no `isPrimary` at all would fail a truthiness test and
    // disable the long press entirely, which is a far worse outcome than
    // honouring a second finger: this guard exists to drop an extra pointer, not
    // to decide whether the feature is available.
    if (e.isPrimary === false || (e.button ?? 0) !== 0) return;
    clearHold();
    suppressClick = false;
    gesture = beginKey(e.clientX, e.clientY);
    holdTimer = setTimeout(() => {
      holdTimer = undefined;
      if (!gesture || gesture.cancelled) return;
      suppressClick = true;
      menuPane = p.name;
    }, LONG_PRESS_MS);
  }

  function holdMove(e: PointerEvent): void {
    if (!gesture) return;
    const next = moveKey(gesture, e.clientX, e.clientY);
    if (next === gesture) return;
    gesture = next;
    // It is a scroll. Nothing has opened and there is nothing to undo, which is
    // the entire point of waiting for the finger to settle.
    if (next.cancelled) clearHold();
  }

  /** The finger lifted, or the browser took the gesture over for its scroll. */
  function holdEnd(): void {
    if (gesture) gesture = cancelKey(gesture);
    gesture = undefined;
    clearHold();
  }

  /**
   * Arrow-key movement along the strip, and the keyboard's own way in to a
   * tab's menu.
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
   * ContextMenu and Shift+F10 open the menu, which are the two keys every
   * platform's context menu answers to. Without them the whole feature is
   * pointer-only, and a long press is a gesture a hardware keyboard cannot make
   * at all.
   *
   * Bound to each TAB rather than to the tablist. That is where the event
   * actually originates — only a tab is focusable — and a listener on the
   * container would oblige the container to be focusable too, which is what
   * svelte's a11y_interactive_supports_focus rule is pointing at.
   */
  function onkeydown(e: KeyboardEvent, p: PaneInfo): void {
    if (e.altKey || e.ctrlKey || e.metaKey || panes.length === 0) return;
    if (e.key === "ContextMenu" || (e.shiftKey && e.key === "F10")) {
      e.preventDefault();
      menuPane = p.name;
      return;
    }
    const here = panes.findIndex((x) => x.name === active);
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

  /**
   * Close a pane on the Mac, then re-read what is left.
   *
   * NEVER CALLED FOR THE AGENT PANE: the menu does not draw the control there,
   * because `handlePaneClose` refuses it outright — that pane is the session —
   * and a button whose only possible outcome is a refusal is worse than no
   * button. The guard below is the second lock on the same door, for the
   * keyboard path and for a future caller.
   *
   * The reload is what makes this honest. `cmd=paneClose` answers `closed:true`
   * and the tabs are drawn from `cmd=panes`, so without the second call the
   * strip would keep showing a tab the daemon has already killed — which is the
   * exact bug that let a closed shell linger in the first place.
   *
   * Silent about the move it may cause. The user pressed the button; being told
   * that the pane they just closed has closed is noise, and landing on the
   * neighbouring tab is self-evidently the consequence of what they did.
   */
  async function closePane(p: PaneInfo): Promise<void> {
    if (closing !== "" || p.kind === PANE_KIND_AGENT) return;
    closing = p.name;
    try {
      await DaemonService.PaneClose(session, p.name);
      onnotice?.("");
      menuPane = "";
      await load(session);
    } catch (err) {
      // The daemon's own sentence, exactly as a refused "+" gets. "pane X does
      // not belong to session Y" and "the agent pane cannot be closed; it is
      // the session" are both actionable; "could not close that pane" is not.
      onnotice?.(err instanceof Error && err.message ? err.message : "Could not close that pane.");
      menuPane = "";
    } finally {
      closing = "";
    }
  }

  /** Store or forget a nickname, and redraw the strip from what was stored. */
  function rename(p: PaneInfo, label: string): void {
    savePaneLabel(p.name, label);
    labels = loadPaneLabels();
    menuPane = "";
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

               `onclick` STILL CARRIES THE TAP, unlike the accessory bar, whose
               keys fire on a settled pointerdown because a bar key sends bytes
               into a live coding agent. Selecting a tab sends nothing and is
               undone by selecting another, and WebKit already suppresses the
               click when a touch turns into a scroll. The pointer handlers
               beside it are the LONG PRESS only — they never select — which is
               why a press that travels simply lets the strip scroll, and the
               click, if one follows, does what it always did.

               `aria-haspopup="dialog"` because holding the tab opens one, and
               `-webkit-touch-callout` so WebKit's own text callout does not
               race the sheet to the same gesture. -->
          <button
            type="button"
            role="tab"
            aria-selected={on}
            aria-controls={panelId}
            aria-label={tabName(p)}
            aria-haspopup="dialog"
            data-pane-selected={on}
            tabindex={on ? 0 : -1}
            class="flex h-11 shrink-0 touch-manipulation items-center justify-center rounded-md
                   px-3 text-base whitespace-nowrap transition-colors select-none
                   [-webkit-touch-callout:none]
                   {on
              ? 'bg-accent-fill font-medium text-accent-ink'
              : 'text-faint active:bg-sel'}"
            onclick={() => {
              // CONSUMED, not merely read. It is cleared by the next press as
              // well (a menu dismissed on the sheet's backdrop never produces
              // the click this is waiting for), but leaving it set once the
              // click HAS arrived left it armed for anything that clicks
              // without a pointer first — Enter or Space on a focused tab,
              // which is how a hardware keyboard selects one. That swallowed
              // the first keystroke after every long press.
              if (suppressClick) {
                suppressClick = false;
                return;
              }
              onselect(p.name);
            }}
            onpointerdown={(e) => holdStart(e, p)}
            onpointermove={holdMove}
            onpointerup={holdEnd}
            onpointercancel={holdEnd}
            onpointerleave={holdEnd}
            oncontextmenu={(e) => e.preventDefault()}
            onkeydown={(e) => onkeydown(e, p)}
          >
            {displayName(p)}
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

<!-- Mounted only while it is open, which is what lets the sheet seed its text
     field once from the current nickname. `menuFor` resolves against the LIVE
     inventory, so a menu whose pane disappeared under it closes itself rather
     than offering a close on something that has already gone. -->
{#if menuFor}
  <PaneMenuSheet
    name={menuFor.name}
    defaultName={menuFor.label || menuFor.name}
    label={labels[menuFor.name] ?? ""}
    canClose={menuFor.kind !== PANE_KIND_AGENT}
    closing={closing === menuFor.name}
    onrename={(l) => rename(menuFor, l)}
    onclosepane={() => void closePane(menuFor)}
    ondismiss={() => (menuPane = "")}
  />
{/if}
