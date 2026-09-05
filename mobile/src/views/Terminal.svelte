<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { store } from "$lib/store.svelte";
  import { statusLabel } from "$lib/theme";
  import { statusTone } from "@mobile/lib/statustone";
  import AccessoryBar from "@mobile/lib/components/AccessoryBar.svelte";
  import ContextCard from "@mobile/lib/components/ContextCard.svelte";
  import MetaPill from "@mobile/lib/components/MetaPill.svelte";
  import MobileTerminal from "@mobile/lib/components/MobileTerminal.svelte";
  import PaneTabs from "@mobile/lib/components/PaneTabs.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import ViewSettings, {
    viewClippingNotice,
    viewIsClipped,
    type ViewGeometry,
  } from "@mobile/lib/components/ViewSettings.svelte";
  import DevLinksSheet from "@mobile/lib/components/DevLinksSheet.svelte";
  import BackIcon from "@mobile/lib/icons/BackIcon.svelte";
  import BranchIcon from "@mobile/lib/icons/BranchIcon.svelte";
  import OverflowIcon from "@mobile/lib/icons/OverflowIcon.svelte";
  import { DEFAULT_MODES, isBracketedPaste, textBytes, type TerminalModes } from "@mobile/lib/keybytes";
  import { installKeyboardInset } from "@mobile/lib/keyboardinset";
  import { nav } from "@mobile/lib/nav.svelte";
  import { openExternal } from "@mobile/lib/openurl";
  import { loadFontSize } from "@mobile/lib/prefs";
  import { installAppBackground } from "@mobile/lib/appstate";
  import { connection } from "@mobile/lib/connection.svelte";
  import {
    PIN_STUCK_MESSAGE,
    PanePin,
    forgetOwnShell,
    isOwnShell,
    loadPinEnabled,
    savePinEnabled,
  } from "@mobile/lib/panepin";
  import { DaemonService } from "@mobile/wailsshim";

  // The terminal screen: a live pane with a tab strip over it and the accessory
  // bar under it.
  //
  // THIS SCREEN TYPES WHATEVER THE HUMAN TYPES. It carried a mid-turn
  // confirmation once — a client-side banner asking "send anyway?" whenever the
  // daemon's view of the pane said the agent was busy — and that has been
  // removed because it fired constantly and was answered reflexively, which is
  // the state in which a confirmation protects nobody.
  //
  // Removing it changed NOTHING on the daemon. The two are different things and
  // the distinction is written here so nobody re-adds one believing it restores
  // the other:
  //
  //   lola's AtPrompt gate stops lola's OWN automation — reactions, review
  //   hand-offs, resolveConflict — from typing into an agent mid-turn and
  //   corrupting it. That gate is untouched and still guards every one of those
  //   paths. The live terminal has always deliberately bypassed it (see
  //   internal/protocol/frame.go on the pty write), because a human holding the
  //   session is the case the gate was never about, and the desktop app works
  //   the same way.
  //
  //   What was removed was client-side friction, in this file, in front of that
  //   same human. Ctrl-C and Escape were already exempt from it; now everything
  //   is.

  let { onback }: { onback: () => void } = $props();

  /**
   * The id tying the tab strip to the pane it switches.
   *
   * A constant rather than a generated id: there is exactly one terminal on
   * this screen and the screen is a singleton, so a unique id buys nothing and
   * a stable one is readable in a snapshot.
   */
  const PANE_PANEL_ID = "session-pane";

  let termRef = $state<ReturnType<typeof MobileTerminal> | undefined>();
  let barRef = $state<ReturnType<typeof AccessoryBar> | undefined>();

  let modes = $state<TerminalModes>(DEFAULT_MODES);
  /**
   * The one sentence a dead pane gets, in English.
   *
   * `unknown_pane: pane is not available` is the daemon's own wire vocabulary
   * and was rendered verbatim in a red banner. A person holding a phone cannot
   * act on a protocol code, and the code is the least useful half of it: the
   * fact is that this session's pane is gone, which is ordinary — a session was
   * killed, or a worktree cleaned up — rather than an error in the app.
   *
   * Only the codes with a plain-English equivalent are translated. Anything
   * else is shown as it came, because inventing a sentence for a failure lola
   * does not recognize is how a diagnosable problem becomes an undiagnosable
   * one.
   */
  function humanError(message: string): string {
    if (/^unknown_pane\b/.test(message)) {
      return "This session's terminal is gone. It was closed on the Mac, or the session was cleaned up.";
    }
    if (/^not connected\b/.test(message)) {
      return "Not connected to the Mac.";
    }
    return message;
  }
  // Seeded from the SAME source the terminal seeds itself from, rather than 0
  // or the bare default: the popover's font buttons read this to decide whether
  // they are at a limit, so a 0 would draw "smaller" as disabled from the
  // moment the screen opens, and the plain default would mis-draw the limits
  // for one tick for anyone whose remembered size is at the floor or the
  // ceiling. It self-corrects on the terminal's first `onstate`; seeding it
  // right just means there is no frame where it is wrong.
  let font = $state(loadFontSize());
  let geom = $state<ViewGeometry>({
    cols: 0,
    rows: 0,
    shown: 0,
    shownRows: 0,
    first: 1,
    panning: false,
    canFit: false,
    fitActive: false,
    fitSize: 0,
  });
  let exited = $state(false);
  let error = $state("");
  /** A refused shell creation, in the daemon's own words. See PaneTabs. */
  let notice = $state("");
  /**
   * Bumped whenever this screen learns something the tab strip cannot: the pane
   * stream reported an exit, or an attach was refused because the pane is gone.
   *
   * The strip owns the inventory and reloads it on its own for everything it can
   * see (see PaneTabs). These two facts arrive on the SUBSCRIPTION, which only
   * this screen holds, so they are handed over as one number rather than by
   * giving the strip a second data source. A shell that exits ends its tmux
   * session — shells get no `remain-on-exit` — so this is the moment the daemon
   * stops listing it and the app has to ask again.
   */
  let paneRefresh = $state(0);
  let keyboardInset = $state(0);

  // --- THE PANE SIZE PIN ----------------------------------------------------
  //
  // While this screen is looking at a pane, the pane's window ON THE MAC can be
  // held at this phone's size, so a 200-column agent redraws at the ~50 columns
  // a phone can show instead of being panned over. It is the one thing the app
  // does that changes somebody else's screen, which is why the whole of it is
  // opt-in, is labelled for the Mac rather than for the phone, and is released
  // on every way out.
  //
  // THE LIFECYCLE LIVES HERE RATHER THAN IN MobileTerminal, and that is not a
  // preference. The terminal is wrapped in `{#key nav.pane}`, so switching tabs
  // DESTROYS it and builds another; a pin owned from inside would have to
  // release from a component that is already being torn down while its
  // replacement is already pinning, and the ordering that keeps two panes from
  // being pinned at once could not be guaranteed across that boundary. This
  // screen outlives every tab switch, so it is the only place that can own it.
  // panepin.ts owns the serialisation, the breadcrumb and the release rules; it
  // is given a way to send a request and a way to speak.

  /** The toggle, remembered per device and OFF unless it was turned on. */
  let pinEnabled = $state(loadPinEnabled());

  /**
   * The pin is ALSO on for a shell this phone started, whatever the toggle says.
   *
   * The toggle defaults to off because pinning reshapes somebody else's screen:
   * an agent pane belongs to the work and a developer may be watching it on the
   * Mac. A shell the phone created has none of that history — nothing on the
   * Mac is looking at it, and its tmux window is born at whatever size tmux
   * picks, so it opens as a prompt on row 0 with a void beneath. Sizing that
   * one to the phone takes nothing from anybody, and it is the difference
   * between a shell tab that behaves like a terminal and one that does not.
   *
   * Read through `paneEpoch` so it re-evaluates when the pane changes and when
   * the switch is used; localStorage is not reactive on its own.
   */
  let paneEpoch = $state(0);
  const autoPin = $derived.by(() => {
    void paneEpoch;
    return isOwnShell(nav.pane);
  });
  /** What the pin actually does, and what the switch shows. */
  const pinActive = $derived(pinEnabled || autoPin);

  /**
   * What this phone can SHOW, which is the only size worth pinning to.
   *
   * NOT `geom.cols`/`geom.rows`, which are the grid the MAC is sending: pinning
   * that window to its own dimensions is a request that changes nothing, and
   * the toggle would look broken rather than wrong. `shown`/`shownRows` are the
   * capacity — how much of that grid actually fits on this screen — and they are
   * what the Mac's window has to become for the two to agree.
   */
  let capacity = $state({ cols: 0, rows: 0 });

  const pin = new PanePin({
    resize: (session, pane, cols, rows) => DaemonService.PaneResize(session, pane, cols, rows),
    // Into the same dismissible banner a refused shell uses. `""` is the
    // WITHDRAWAL — see PIN_STUCK_MESSAGE — and it is matched against that exact
    // sentence rather than clearing whatever happens to be up, so a pin that
    // resolves can never silently swallow a refusal the strip is reporting.
    report: (m) => {
      if (m !== "") {
        notice = m;
        return;
      }
      if (notice === PIN_STUCK_MESSAGE) notice = "";
    },
  });

  function setPin(on: boolean) {
    pinEnabled = on;
    savePinEnabled(on);
    // Turning it OFF has to reach the auto-pin too, or the switch would be a
    // lie: this pane would keep pinning itself and the control would look like
    // it had flipped back on by itself.
    if (!on) forgetOwnShell(nav.pane);
    paneEpoch++;
  }

  // THE ONLY THING THAT EVER ASKS FOR A PIN. Every condition that makes one
  // wrong is in this one expression, so there is no path that pins without
  // passing through it and no second place to forget a case: the toggle is off,
  // the socket is gone, the pane has died, or the box has not been measured yet
  // — each of them yields `null`, which is the release.
  $effect(() => {
    const want =
      pinActive &&
      connection.ready &&
      !exited &&
      nav.paneSession !== "" &&
      nav.pane !== "" &&
      capacity.cols > 0 &&
      capacity.rows > 0
        ? { session: nav.paneSession, pane: nav.pane, cols: capacity.cols, rows: capacity.rows }
        : null;
    pin.want(want);
  });

  // A NEW SOCKET KNOWS NOTHING ABOUT WHAT THE OLD ONE SENT, so the intent is
  // asserted again on the false -> true edge and any breadcrumb left by a
  // release that never got out is swept. A PLAIN `let` rather than `$state`,
  // for the reason MobileTerminal's own edge detector gives: an effect that
  // both reads and writes the same rune re-invalidates itself forever.
  let pinWasReady = connection.ready;
  $effect(() => {
    const ready = connection.ready;
    const back = ready && !pinWasReady;
    pinWasReady = ready;
    if (!back) return;
    void pin.reassert();
    void pin.recover();
  });

  // Read for the status text and the PR button in the header. Nothing on this
  // screen gates a keystroke on it.
  const session = $derived(store.sessionById(nav.paneSession));

  // A PR button ONLY when there is a PR, and only when the daemon actually
  // handed over an address for it. `prNumber > 0` with an empty `prUrl` happens
  // — the number comes from the PR facts, the URL from the same fetch — and a
  // button that opens nothing is worse than no button.
  const prUrl = $derived(session?.prUrl ?? "");
  const prNumber = $derived(session?.prNumber ?? 0);
  const hasPR = $derived(prNumber > 0 && prUrl !== "");

  /**
   * What the header row leads with, and the line under it.
   *
   * THE ISSUE KEY LEADS HERE, WHICH IS THE OPPOSITE OF THE LIST. Both list
   * components argue at length that the title has to lead a ROW — a list of
   * NOR-401 / NOR-329 / NOR-311 is a list of three things you cannot tell
   * apart. That argument is about telling sessions apart, and on this screen
   * there is only one: the question a person opening a terminal is answering is
   * "which ticket am I in", they arrived by tapping a title they have just
   * read, and the key is the handle they will quote in a commit or a comment. So
   * the key is the identity and the title is the subtitle, which is also what
   * the design draws.
   *
   * The fallback is the session id rather than the pane name, so a record the
   * list has not caught up with still names something a person can act on. The
   * pane name is a debugging handle and lives in the tab strip and its menu.
   */
  const issueKey = $derived(session?.issue || nav.paneSession);
  /**
   * ONE TRUNCATED LINE, and empty is a perfectly good answer. `title` is "" for
   * older and adopted records, and a header that filled the gap with the tmux
   * name would lead a screen whose subject is a ticket with
   * `lola-nori-app-nor-311`.
   */
  const subtitle = $derived(session?.title ?? "");

  /**
   * The overflow menu.
   *
   * A NAMED `nav` SHEET, like the other three this screen can put up. It landed
   * as a plain local on the argument that `sheets.ts`'s vocabulary is for
   * surfaces a gesture cannot reach — the view settings, the pane menu's long
   * press — and that an ordinary tap on a 44-point button is not that case.
   * That reads the rule one step too narrowly: what a name buys is a
   * development link, and a link is how a SCRIPT reaches a surface at all. The
   * Simulator can no more tap a button than it can long-press a tab, so this
   * menu — which now holds most of this screen's actions — was the one overlay
   * in the app that could be unit-tested and never photographed.
   *
   * Naming it also deletes work: `nav.toSessions()` and `nav.toTab()` already
   * close whatever sheet is up, because a sheet belongs to the screen it was
   * opened over, so leaving the terminal can no longer strand this one open.
   */
  const menuOpen = $derived(nav.sheet === "menu");

  /**
   * Whether the grid is wider than the screen, and the sentence that says so.
   *
   * BOTH FROM ViewSettings' MODULE, never re-derived here. The sections that
   * report the clipping now live inside the session sheet, so the button that
   * opens that sheet is the only always-visible sign of it — and a second copy
   * of the rule here is exactly how the mark and the readout would come to
   * disagree. See `viewClippingNotice` for why the guarantee matters at all.
   */
  const clipped = $derived(viewIsClipped(geom));
  const menuLabel = $derived.by(() => {
    const notice = viewClippingNotice(geom);
    return notice ? `Session actions. ${notice}` : "Session actions";
  });

  /**
   * The status word under the issue key, and the status it is drawn for.
   *
   * THE ERROR WINS IT, and it has to. The status describes the SESSION and the
   * banner below describes the PANE, and a reader sees one screen: with the
   * status drawn unconditionally, a live session whose aux pane had been closed
   * on the Mac printed "needs you" directly above "This session's terminal is
   * gone". That contradiction is the exact confusion `humanError` exists to
   * remove, so the word and the sentence agree by construction.
   *
   * NOTHING IS DRAWN ONCE THE PANE HAS EXITED: "This session ended" is already
   * the banner immediately below, and the store's status word lags a dead pane
   * by up to an observer cycle — so the one moment it is most likely to be wrong
   * is the one moment something else is already stating the truth.
   *
   * `statusFor` is what colours it. "no terminal" is not a status the shared
   * vocabulary names, and asking `statusTone` for one would answer the faint
   * default — which is the right answer here anyway, since the banner beneath is
   * already coloured and this is a statement about what is missing rather than a
   * second alarm about it.
   */
  const statusFor = $derived(error ? "" : (session?.status ?? ""));
  const statusWord = $derived.by(() => {
    if (error) return "no terminal";
    if (!session || exited) return "";
    return statusLabel(session.status);
  });

  /**
   * The session's dev servers, as THIS PHONE can reach them.
   *
   * `devForwards`, not `devUrls`. The latter are what the Mac sees —
   * `http://127.0.0.1:8000` — and on a phone that address is the phone's own
   * loopback, which reaches nothing. The daemon republishes them on a private
   * interface while the session is ACTIVE, and only when [remote].dev_forward
   * is set, so an empty list is the ordinary state and the button is absent
   * rather than dead.
   */
  const devLinks = $derived(session?.devForwards ?? []);
  let devSheet = $state(false);

  /**
   * Whether this session can run dev commands at all, and whether it is the one
   * doing so.
   *
   * `devCommands` is its project's configured list; a session whose project has
   * none can never be activated, so the control is absent rather than dead.
   */
  const canDev = $derived((session?.devCommands ?? []).length > 0);
  const devActive = $derived(session?.devActive === true);
  let devBusy = $state(false);

  /**
   * Start or stop this session's dev commands.
   *
   * IT IS A MOVE, NOT A TOGGLE, and the confirmation says so: only one session
   * per project may run them (they bind ports), so activating this one STOPS
   * another's servers. That is a heavier thing than it looks from a phone,
   * where the session that loses its dev tabs is not on screen.
   *
   * The flag lives here rather than in the store because the daemon answers
   * only when tmux has finished, which is seconds — long enough that a second
   * tap would send a second move.
   */
  async function toggleDev() {
    const id = nav.paneSession;
    if (devBusy || id === "") return;
    devBusy = true;
    try {
      await DaemonService.Dev(id, !devActive);
      await store.refresh();
    } catch (e) {
      error = e instanceof Error && e.message ? e.message : "Could not change the dev commands.";
    } finally {
      devBusy = false;
    }
  }

  /**
   * One address opens straight away; several open the sheet.
   *
   * A session's dev commands print more than one address as a rule — an app and
   * a bundler — so picking is the interaction, and a button that guessed would
   * open the asset server instead of the app about half the time.
   */
  async function openDev() {
    // THE MENU CLOSES FIRST, WHICHEVER BRANCH THIS TAKES. Both of them put
    // something in front of the user that the menu would otherwise be sitting
    // on top of: the link sheet is a second modal, and a failed open writes into
    // the banner under the header, which the sheet covers. `Sheet` is not
    // stacked anywhere else in this app and there is no z-order story for two.
    nav.closeSheet();
    if (devLinks.length === 1) {
      await openLink(devLinks[0].url);
      return;
    }
    devSheet = true;
  }

  /**
   * Hand one address to the phone's browser, and SAY SO IF IT DID NOT OPEN.
   *
   * A button whose entire purpose is to open something must report that it
   * could not — silence is what "it shows, but nothing happens on click" is
   * made of, and it is indistinguishable from a broken feature.
   */
  async function openLink(url: string) {
    if (await openExternal(url)) return;
    error = `Could not open ${url}. No browser accepted the link.`;
  }

  let offKeyboard: (() => void) | undefined;
  let offBackground: (() => void) | undefined;
  onMount(() => {
    offKeyboard = installKeyboardInset((px) => (keyboardInset = px));
    // A PHONE GOING INTO A POCKET IS A WAY OUT TOO. The plugin closes the socket
    // on the way into the background, so this release is racing a teardown that
    // is already queued and will sometimes lose — see installAppBackground. It
    // is sent anyway because it usually wins, and the breadcrumb below is what
    // covers the times it does not.
    offBackground = installAppBackground(() => void pin.release());
    // Sweep a pin an earlier run or an earlier connection left behind. The pane
    // this screen is about to pin is EXEMPTED, or reopening the same pane would
    // release it and pin it again a moment later for nothing.
    //
    // ONLY WHEN THERE IS A SOCKET. A sweep with none cannot succeed, and its
    // failure is reported — so an offline open would greet the user with a
    // warning about a pin they can do nothing about, next to a banner already
    // saying the Mac is unreachable. The edge below runs the same sweep the
    // moment a connection arrives, so nothing is skipped, only deferred.
    if (connection.ready) {
      void pin.recover(
        pinActive && nav.paneSession !== "" && nav.pane !== ""
          ? { session: nav.paneSession, pane: nav.pane }
          : null,
      );
    }
  });
  onDestroy(() => {
    offKeyboard?.();
    offBackground?.();
    // LEAVING THE SESSION VIEW IS THE COMMONEST WAY OUT, and this is it: App
    // swaps this screen out on `onback`, which destroys it. Fire and forget —
    // Svelte will not wait for a promise here, and the request only has to reach
    // the socket. `release` is idempotent, so this costs nothing when the pin
    // was already handed back by the tab switch or the pane's own exit.
    pin.stop();
    void pin.release();
  });

  /**
   * Apply a latched modifier to whatever the SOFT keyboard produced. The letters
   * only exist there, so a latch that worked for bar keys alone would make
   * "ctrl then a letter" — the commonest chord there is — impossible.
   *
   * It PEEKS at the latch rather than consuming it. The consume happens on the
   * path that actually writes, in MobileTerminal.send, through `onsent` below,
   * so the latch is cleared by the byte that used it and not by the attempt.
   * AccessoryBar.press has always worked this way round.
   *
   * A BRACKETED PASTE is passed through untouched. xterm wraps a paste itself,
   * in CoreService, so the payload arriving here already begins with CSI 200~;
   * a latched alt would put ESC in front of the wrapper and a latched ctrl would
   * apply to the wrapper's first byte instead of the pasted text.
   */
  function transform(data: string): string {
    if (isBracketedPaste(data)) return data;
    const mods = barRef?.latched() ?? {};
    return textBytes(data, mods);
  }

  function setFont(size: number) {
    termRef?.setFont(size);
  }

  /**
   * Attach to another pane of this session.
   *
   * The screen's per-pane state is reset here rather than left to the remount.
   * `{#key nav.pane}` rebuilds the terminal and its next successful attach
   * clears `error` on its own, but `exited` is latched by this file: without the
   * reset, switching away from a pane that had died would carry "This session
   * ended" onto a live shell and leave the accessory bar disabled over it.
   */
  function attach(pane: string) {
    if (pane === nav.pane) return;
    exited = false;
    error = "";
    notice = "";
    // THE PIN GOES BEFORE THE PANE DOES. Clearing the capacity makes the effect
    // above ask for nothing, which releases the pane being left in the same
    // step that stops wanting it — so the new pane is never pinned on top of the
    // old one, and the stale measurement is never sent as the new pane's size.
    // The replacement terminal reports its own capacity a frame later and the
    // pin follows it.
    capacity = { cols: 0, rows: 0 };
    nav.pane = pane;
  }

  /**
   * Open the PR — on the PHONE, and only if the address is http(s).
   *
   * `openExternal`, deliberately NOT the daemon's `cmd=openURL` that the desktop
   * uses for the same button. That command runs `open` on the DAEMON's machine,
   * so a tap here would launch Safari on an unattended Mac in another room; the
   * guard it is valued for — a two-scheme allowlist — is applied by this module
   * too, on this device, before anything is handed to an opener. mobile/PLAN.md
   * settles this and openurl.ts is the module that exists for it.
   */
  function openPR() {
    nav.closeSheet();
    void openExternal(prUrl);
  }
</script>

<div class="flex h-full min-h-0 flex-col bg-canvas" style="padding-bottom: {keyboardInset}px">
  <!-- THE HEADER IS TWO LINES AND ONE OF THEM IS A SENTENCE. It used to be a
       single row carrying, from left to right: a back chevron, the issue key
       over a status word, a Dev toggle, a dev-links button, a PR button and the
       view-settings trigger — six controls and two facts on a 393-point line.
       Everything shrank to fit, so the identity of the session read smaller than
       the row of grey glyphs beside it, and the two controls a person actually
       reaches for on a phone (open the PR, open the dev server) were the two
       narrowest.

       The redesign splits it: the top row is IDENTITY — who this is, how it is
       doing, whether it has a PR — and the actions move behind the overflow
       button into a sheet, where they get full-width rows and their real names
       instead of a 16-point glyph. `px-3` rather than the old `px-2` is the
       design's; the top padding still adds to the safe-area inset rather than
       replacing it, for the reason app.css spells out about --lola-top-inset.

       `8px` rather than `0.5rem`, which is the same thing only at the default
       text size. app.css pins spacing to px so that Dynamic Type scales the
       TYPE and not the layout; in rem this one padding would grow to ~11.5px at
       the largest setting, taking three points off the pane on the screen with
       the least room to give and leaving this header disagreeing with the four
       tab screens, which all write theirs as a px literal. -->
  <!-- NO BOTTOM RULE ON THIS HEADER. The pane-tab strip 44 points below draws
       its own, and the design has exactly one hairline between the header and
       the pane — two of them boxed the identity row into a band of its own,
       which reads as a toolbar rather than as the top of one screen. -->
  <header
    class="flex shrink-0 flex-col gap-0.5 px-3 pb-2"
    style="padding-top: calc(var(--lola-top-inset, env(safe-area-inset-top, 0px)) + 8px)"
  >
    <div class="flex items-center gap-2">
      <!-- `text-accent!` with the trailing `!` because a plain `text-accent`
           ties with the ghost variant's own `text-faint` and the winner would be
           decided by Tailwind's order in the compiled sheet (CLAUDE.md's Button
           invariant). Back is the one control on this screen that is always
           there and always safe, which is what earns it the accent. -->
      <TouchButton icon aria-label="Back to sessions" class="text-accent!" onclick={onback}>
        <BackIcon />
      </TouchButton>

      <!-- THE ONE ITEM ALLOWED TO GIVE WAY. Everything else on this line is
           fixed-width and cannot truncate — a chip and a badge are
           `whitespace-nowrap` by construction and a 44-point button is a floor,
           not a preference — so at the narrowest phone widths and at large
           Dynamic Type sizes the key is what shortens. That is the right
           casualty: the full title is on the line directly beneath it, and a
           clipped button is a control a person cannot reach. -->
      <span class="num min-w-0 truncate text-base font-medium text-ink">{issueKey}</span>

      <span class="flex-1"></span>

      {#if prNumber > 0}
        <!-- THE BADGE IS THE WAY TO THE PR, and it is the only one. It was a
             statement with the action parked in the sheet — a number you could
             read and not press, two taps from the page it names — which is the
             wrong shape for the thing this session exists to produce. Pressing
             the number that says "#352" and landing on #352 is what a person
             expects of it, so the sheet's "Open pull request" row is gone rather
             than kept as a second door onto the same page.

             IT IS A STATEMENT WHEN IT CANNOT BE A DOOR. `prNumber > 0` with an
             empty `prUrl` is a real state — the number and the address come from
             the same gh fetch and can arrive apart — so the badge still DRAWS on
             the number alone and only becomes pressable once there is somewhere
             to go. A button that opens nothing is worse than a fact.

             <MetaPill> makes an `onclick` badge a transparent 44-point button
             around the chip rather than growing the chip itself, so the row's
             height is unchanged. The name is spelled out because "#352" alone is
             announced as a loose number belonging to nothing.

             The colour comes from the CHECKS rather than the delivery word,
             exactly as on the hero card: a red number is the fastest way to see
             that the PR that exists is not the PR you wanted. -->
        <MetaPill
          tone={session?.checks === "fail" ? "bad" : "magenta"}
          onclick={hasPR ? openPR : undefined}
          ariaLabel="Open pull request #{prNumber} in the browser"
        >
          {#snippet leading()}<BranchIcon />{/snippet}
          {#if !hasPR}<span class="sr-only">Pull request&nbsp;</span>{/if}#{prNumber}
        </MetaPill>
      {/if}

      <!-- ONE BUTTON, WHERE THERE WERE TWO. The header used to carry a view-
           settings glyph beside this one — 88 points of controls on the screen
           with the least of it to give, on a row where the ISSUE KEY was the
           only item allowed to shorten. The key is what says which session this
           is; a second glyph is not worth its first character. So the view
           settings became the first section of this sheet, and this button opens
           all of it.

           IT INHERITS THE CLIPPING GUARANTEE ALONG WITH THE SECTIONS. A phone
           shows roughly 55 of a developer's 200 columns, and a pane clipped at
           column 55 looks exactly like an agent that stopped writing mid-line —
           so the old trigger wore a dot and carried the live column range in its
           name. Both facts come from ViewSettings' own module (`viewIsClipped`,
           `viewClippingNotice`), which is what stops the button and the readout
           inside it from ever disagreeing. Dropping either half here would
           silently undo the reason that component exists.

           `relative` for the dot, appended after the shared Button's classes,
           which set no positioning — so no `!`, unlike the geometry overrides. -->
      <TouchButton
        icon
        aria-label={menuLabel}
        aria-haspopup="dialog"
        aria-expanded={menuOpen}
        class="relative text-subtext!"
        onclick={() => nav.openSheet("menu")}
      >
        <OverflowIcon />
        {#if clipped}
          <!-- `warn` rather than `bad`: a clipped pane is not an error, it is
               the normal state of a 200-column grid on a phone. It is the state
               a reader has to know about before believing the right-hand edge of
               what they can see. -->
          <span
            class="pointer-events-none absolute top-1.5 right-1.5 h-2 w-2 rounded-full bg-warn ring-2 ring-canvas"
            aria-hidden="true"
          ></span>
        {/if}
      </TouchButton>
    </div>

    <!-- ONE LINE, TRUNCATED, and no clamp. The card in the list gives a title two
         lines because it is asking the reader to choose; here they have already
         chosen, and every row this header takes comes off the pane.

         THE STATUS IS A WORD ON THIS LINE, NOT A CHIP ON THE ONE ABOVE. It used
         to be a <StatusChip> between the issue key and the spacer, which put a
         filled badge — up to 126 points of it for "waiting for you" — in the
         middle of the row whose remaining space the key was competing for. Here
         it costs a word, and it costs the key nothing.

         It is deliberately the same shape the COMPACT ROW in the list uses: the
         status word in its own tone, leading a line of quieter facts, from
         `statusTone` + `$lib/theme`'s statusLabel. A reader tapping a row now
         sees the same two things in the same order one screen further in.

         `shrink-0` on the word and `truncate` on the title: the status is three
         words at most and is the reason the line exists, while the title is free
         text and is the only thing here that can be long. -->
    {#if statusWord || subtitle}
      <div class="flex min-w-0 items-center gap-1.5 text-sm">
        {#if statusWord}
          <span class="shrink-0 {statusTone(statusFor)}">{statusWord}</span>
          {#if subtitle}<span class="shrink-0 text-edge" aria-hidden="true">·</span>{/if}
        {/if}
        {#if subtitle}<span class="min-w-0 truncate text-faint">{subtitle}</span>{/if}
      </div>
    {/if}
  </header>

  {#if error}
    <div class="shrink-0 border-b border-bad/40 bg-bad/10 px-4 py-2 text-sm text-bad" role="status">
      {humanError(error)}
    </div>
  {/if}

  {#if exited}
    <!-- A pane death arrives as a resync carrying the last screen, and nothing
         follows it. Saying so is worth a line: an agent pane that simply stops
         updating is indistinguishable from one that is thinking. -->
    <div class="shrink-0 border-b border-edge bg-panel px-4 py-2 text-sm text-faint" role="status">
      This session ended. The screen above is its last frame.
    </div>
  {/if}

  {#if notice}
    <!-- A REFUSED "+", in the daemon's own sentence. It names the reason — no
         worktree, or the shell cap — and a generic "could not start a shell"
         would throw away the only half a person can act on. Dismissible, because
         it describes a moment rather than a state. -->
    <div
      class="flex shrink-0 items-center gap-2 border-b border-warn/40 bg-warn/10 pl-4 text-sm text-warn"
      role="status"
    >
      <span class="min-w-0 flex-1 py-2">{notice}</span>
      <TouchButton icon aria-label="Dismiss" class="text-warn!" onclick={() => (notice = "")}
        >×</TouchButton
      >
    </div>
  {/if}

  <!-- THE TAB STRIP, above the pane and below every banner.
       `shrink-0` in this column takes its height off the terminal's `flex-1`,
       so it pushes neither the pane nor the accessory bar off the screen, and
       the safe areas stay where they already were: the header pays the top
       inset, the screen pays the keyboard, the bar pays the home indicator. -->
  <!-- `bind:menuPane` against `nav` rather than the strip's own local state,
       through a function binding, for the reason ViewSettings' `bind:open` is
       wired the same way: a menu only a long press can open is a menu no
       screenshot can reach, and the Simulator has no gesture API. With the pane
       named in `nav`, a development link asking for `sheet=pane` lands on
       it. The getter falls back to the ATTACHED pane so such a link needs no
       second field; the strip still works uncontrolled everywhere else. -->
  <PaneTabs
    session={nav.paneSession}
    active={nav.pane}
    panelId={PANE_PANEL_ID}
    refreshKey={paneRefresh}
    {capacity}
    bind:menuPane={
      () => (nav.sheet === "pane" ? nav.menuPane || nav.pane : ""),
      (v) => {
        if (v === "") {
          nav.closeSheet();
          return;
        }
        nav.menuPane = v;
        nav.openSheet("pane");
      }
    }
    onselect={attach}
    onnotice={(m) => (notice = m)}
    onpanes={(id, names) => void pin.forgetMissing(id, names)}
  />

  <!-- WHAT THE AGENT IS DOING, between the strip and the pane it labels.
       Renders nothing at all — not an empty card — when the session has neither
       an activity line nor a fact worth a chip, which is most sessions most of
       the time; see ContextCard for why an empty box above a terminal is worse
       than the space it takes. `session` may be undefined while the list has
       not caught up with the pane, which is the same nothing. -->
  <ContextCard {session} />

  <!-- The region the tab strip controls. `role="tabpanel"` plus the id the
       strip points `aria-controls` at is what turns two adjacent widgets into
       one relationship for assistive technology: without it a screen reader
       announces a tab list and, separately, a terminal, with nothing saying the
       second is what the first switches. Named rather than labelled by the
       selected tab, because the tab labels are the daemon's ("shell 2") and are
       not a name for the thing below them. -->
  <div id={PANE_PANEL_ID} role="tabpanel" aria-label="Terminal" class="min-h-0 flex-1">
    {#key nav.pane}
      <MobileTerminal
        bind:this={termRef}
        pane={nav.pane}
        {transform}
        onsent={() => barRef?.consumeLatch()}
        onexit={() => {
          exited = true;
          // The pane's tmux session is gone, so the inventory that listed it is
          // now wrong by exactly one tab.
          paneRefresh++;
        }}
        onerror={(m) => {
          error = m;
          // An attach refused `unknown_pane` says the same thing one moment
          // earlier: the pane was already gone before the subscribe. Matched on
          // the daemon's own wire code, the same prefix `humanError` translates.
          if (/^unknown_pane\b/.test(m)) paneRefresh++;
        }}
        onstate={(st) => {
          modes = st.modes;
          font = st.font;
          // Assigned only on a real change: this is read by the pin effect, and a
          // fresh object every time would restart its settle timer on every
          // state push and could starve the pin entirely.
          // THE UNCLAMPED CAPACITY, not `shown`/`shownRows`. Those are clamped
          // to the current grid, so pinning to them makes each pin's target
          // depend on the previous pin's result — which climbed a shell one row
          // per pin, over twenty seconds, reflowing it on the Mac every time.
          if (capacity.cols !== st.capCols || capacity.rows !== st.capRows) {
            capacity = { cols: st.capCols, rows: st.capRows };
          }
          geom = {
            cols: st.cols,
            rows: st.rows,
            shown: st.shown,
            shownRows: st.shownRows,
            first: st.first,
            panning: st.panning,
            canFit: st.canFit,
            fitActive: st.fitActive,
            fitSize: st.fitSize,
          };
        }}
      />
    {/key}
  </div>

  <!-- DEAD MEANS DEAD, and the bar has to say so. A full-strength row of keys
       under a pane that cannot receive a byte is the app claiming an ability it
       does not have; `disabled` fades it and refuses every press, including the
       repeat. -->
  <AccessoryBar
    bind:this={barRef}
    {modes}
    disabled={exited || error !== ""}
    raised={keyboardInset > 0}
    onsend={(bytes) => termRef?.send(bytes)}
  />
</div>

{#if menuOpen}
  <!-- THE ACTIONS THAT USED TO BE GLYPHS IN THE HEADER, with their names back.
       Nothing about any of them changed — the same handlers, the same
       accessible names, the same absent-rather-than-disabled rule — only the
       address. Each is still drawn ONLY when it can do something: a control that
       is dead for most of a session's life teaches people not to look at that
       corner, and a menu of dead rows teaches them not to open the menu.

       The app's one modal shape, so this sheet gets Escape, the dismissible
       backdrop and the Dynamic Type height cap from Sheet.svelte rather than a
       fifth copy of them. -->
  <Sheet
    label="Session actions"
    dismissLabel="Close the session menu"
    onclose={() => nav.closeSheet()}
  >
    <!-- THE VIEW SETTINGS LEAD, because they are the reason this sheet is opened
         most often: a text size and a column readout are adjusted while reading,
         while the dev toggle and the PR link are things a person does once. They
         had their own header button and their own sheet until the header ran out
         of room for both glyphs — see the note on the trigger above.

         `ondone` closes this sheet, and only "Fit the width" spends it: that
         control changes what is on screen BEHIND the sheet, so leaving it up
         hides the thing just asked for. Everything else here is adjusted and
         re-adjusted with the sheet open, which is the whole reason A− / A+ are
         worth having in a sheet at all. -->
    <ViewSettings
      {font}
      {geom}
      onfont={setFont}
      onfit={() => termRef?.toggleFit()}
      pinned={pinActive}
      onpin={setPin}
      ondone={() => nav.closeSheet()}
    />

    <div class="h-px bg-edge/60" aria-hidden="true"></div>

    {#if canDev}
      <!-- IT IS A MOVE, NOT A TOGGLE, and the sheet is where that can finally be
           said: only one session per project may run the dev commands (they bind
           ports), so starting these STOPS another session's servers — and that
           session is not on screen. The glyph in the header had no room for the
           sentence and the aria-label carried it alone, which meant it reached
           VoiceOver users and nobody else.

           The label text is the sheet's; the ACCESSIBLE NAME is deliberately the
           header button's, word for word, so the control is the same control to
           anything that was addressing it before. -->
      <section class="flex flex-col gap-2">
        <span class="label text-faint">Dev servers</span>
        <TouchButton
          wide
          variant="secondary"
          aria-label={devActive
            ? "Stop this session's dev commands"
            : "Run this session's dev commands here"}
          aria-pressed={devActive}
          loading={devBusy}
          onclick={toggleDev}
        >
          <!-- Hidden while the spinner is up: the shared Button asks a call site
               that draws its own state glyph to step aside, because the spinner
               takes that slot and two marks fight over one. -->
          {#if !devBusy}
            <span class={devActive ? "text-good" : "text-faint"} aria-hidden="true">●</span>
          {/if}
          {devActive ? "Stop the dev commands" : "Run the dev commands here"}
        </TouchButton>
        {#if devLinks.length > 0}
          <TouchButton
            wide
            aria-label={devLinks.length === 1
              ? `Open the dev server at ${devLinks[0].from} on this phone`
              : `Open one of ${devLinks.length} dev server links on this phone`}
            onclick={openDev}
          >
            {devLinks.length === 1 ? "Open the dev server" : `Open a dev server (${devLinks.length})`}
          </TouchButton>
        {/if}
        <!-- KEPT, AND SHORTENED TO ONE LINE. The rest of this sheet's captions
             are gone; this one states a consequence that happens to a session
             which is not on screen, so a reader cannot discover it from the
             control. Two sentences became one. -->
        <span class="copy text-sm text-faint">
          Starting these stops another session's servers.
        </span>
      </section>
    {:else if devLinks.length > 0}
      <!-- A session can publish addresses without this app being able to offer
           the toggle: `devCommands` is the project's configured list and an
           older daemon does not ship it. The link is still worth having. -->
      <TouchButton
        wide
        variant="secondary"
        aria-label={devLinks.length === 1
          ? `Open the dev server at ${devLinks[0].from} on this phone`
          : `Open one of ${devLinks.length} dev server links on this phone`}
        onclick={openDev}
      >
        {devLinks.length === 1 ? "Open the dev server" : `Open a dev server (${devLinks.length})`}
      </TouchButton>
    {/if}

    <TouchButton wide onclick={() => nav.closeSheet()}>Done</TouchButton>
  </Sheet>
{/if}

{#if devSheet}
  <DevLinksSheet
    forwards={devLinks}
    onopen={(url) => {
      devSheet = false;
      void openLink(url);
    }}
    onclose={() => (devSheet = false)}
  />
{/if}
