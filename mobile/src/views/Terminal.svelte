<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import { statusLabel } from "$lib/theme";
  import { store } from "$lib/store.svelte";
  import AccessoryBar from "@mobile/lib/components/AccessoryBar.svelte";
  import MobileTerminal from "@mobile/lib/components/MobileTerminal.svelte";
  import PaneTabs from "@mobile/lib/components/PaneTabs.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import ViewSettings, { type ViewGeometry } from "@mobile/lib/components/ViewSettings.svelte";
  import { DEFAULT_MODES, isBracketedPaste, textBytes, type TerminalModes } from "@mobile/lib/keybytes";
  import { installKeyboardInset } from "@mobile/lib/keyboardinset";
  import { nav } from "@mobile/lib/nav.svelte";
  import { openExternal } from "@mobile/lib/openurl";
  import { statusTone } from "@mobile/lib/statustone";
  import { loadFontSize } from "@mobile/lib/prefs";

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
  let keyboardInset = $state(0);

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

  let offKeyboard: (() => void) | undefined;
  onMount(() => {
    offKeyboard = installKeyboardInset((px) => (keyboardInset = px));
  });
  onDestroy(() => {
    offKeyboard?.();
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
    void openExternal(prUrl);
  }
</script>

<div class="flex h-full min-h-0 flex-col bg-canvas" style="padding-bottom: {keyboardInset}px">
  <header
    class="flex shrink-0 items-center gap-2 border-b border-edge px-2 pb-2"
    style="padding-top: calc(var(--lola-top-inset, env(safe-area-inset-top, 0px)) + 0.5rem)"
  >
    <TouchButton icon aria-label="Back to sessions" onclick={onback}>‹</TouchButton>
    <div class="flex min-w-0 flex-col">
      <span class="truncate font-medium text-ink">
        {session?.issue || nav.paneSession}
      </span>
      <span class="num flex min-w-0 items-center gap-1 truncate text-sm text-faint">
        <!-- THE STATUS, WOVEN IN rather than worn as a badge. It is the same
             word and the same colour the sessions list and the desktop use —
             `$lib/theme`'s statusLabel + statusText, which are the port of Go's
             internal/state vocabulary that desktop/state_parity_test.go pins —
             at the weight of a caption instead of a filled pill. A pill up here
             competed with the title for the eye and told a person who has just
             tapped into a session something they already knew.

             THE GRID READOUT THAT USED TO LIVE HERE IS NOT GONE, it moved into
             the view-settings popover, whose trigger carries the column range
             in its accessible name and wears a dot whenever the pane is
             clipped. That number is the only signal that a line stops at the
             screen edge rather than because the agent stopped writing, so it
             was moved rather than dropped. -->
        {#if error}
          <!-- THE ERROR WINS, and it has to. This branch used to come second,
               behind `session && !exited` — and a session the list still knows
               about ALWAYS takes that branch, so the fallback was very nearly
               dead code and the subtitle could contradict the banner directly
               beneath it: "working" printed over "This session's terminal is
               gone" whenever a live session's aux pane had been closed on the
               Mac. That contradiction is the exact confusion humanError exists
               to remove, so the sentence below and the word up here now agree
               by construction.

               The tmux name is a debugging handle, not a subtitle. With nothing
               else to report it was all this line had, so a screen whose whole
               point is "this pane is gone" led with `lola-nori-app-nor-311`. -->
          <span class="truncate">No terminal</span>
        {:else if session && !exited}
          <span class="shrink-0 {statusTone(session.status)}">{statusLabel(session.status)}</span>
        {:else}
          <span class="truncate">{nav.pane}</span>
        {/if}
      </span>
    </div>
    <div class="ml-auto flex shrink-0 items-center gap-1">
      <!-- ONLY WHEN THERE IS A PR. An always-drawn button that is dead for most
           of a session's life teaches people not to look at that corner. -->
      {#if hasPR}
        <TouchButton
          aria-label="Open pull request #{prNumber} in the browser"
          class="gap-1! px-2!"
          onclick={openPR}
        >
          <svg viewBox="0 0 16 16" class="size-4 shrink-0" fill="currentColor" aria-hidden="true">
            <path
              d="M1.5 3.25a2.25 2.25 0 1 1 3 2.122v5.256a2.251 2.251 0 1 1-1.5 0V5.372A2.25 2.25 0 0 1 1.5 3.25Zm5.677-.177L9.573.677A.25.25 0 0 1 10 .854V2.5h1A2.5 2.5 0 0 1 13.5 5v5.628a2.251 2.251 0 1 1-1.5 0V5a1 1 0 0 0-1-1h-1v1.646a.25.25 0 0 1-.427.177L7.177 3.427a.25.25 0 0 1 0-.354ZM3.75 2.5a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Zm0 9.5a.75.75 0 1 0 0 1.5.75.75 0 0 0 0-1.5Zm8.25.75a.75.75 0 1 0 1.5 0 .75.75 0 0 0-1.5 0Z"
            />
          </svg>
          <span class="num text-sm">#{prNumber}</span>
        </TouchButton>
      {/if}
      <!-- The fit-width toggle and A−/A+ used to sit here as three loose
           controls. They are inside this one, unchanged; see ViewSettings for
           why the column readout had to go with them and what replaces it in
           the header. Deliberately NOT disabled on `exited`: a dead pane still
           has its last frame on screen, and enlarging that frame or reading how
           much of it is off to the right is the one thing left worth doing. -->
      <!-- `bind:open` against `nav.sheet` rather than the component's own local
           state, through a function binding. The sheet is a place the app can
           be in, which is what lets a development link land with it open — the
           column readout inside it could not otherwise be photographed at all,
           since nothing but a tap opens it and the Simulator has no gesture
           API. The component still works uncontrolled everywhere else. -->
      <ViewSettings
        {font}
        {geom}
        bind:open={() => nav.sheet === "view", (v) => (v ? nav.openSheet("view") : nav.closeSheet())}
        onfont={setFont}
        onfit={() => termRef?.toggleFit()}
      />
    </div>
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
  <PaneTabs
    session={nav.paneSession}
    active={nav.pane}
    panelId={PANE_PANEL_ID}
    onselect={attach}
    onnotice={(m) => (notice = m)}
  />

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
        onexit={() => (exited = true)}
        onerror={(m) => (error = m)}
        onstate={(st) => {
          modes = st.modes;
          font = st.font;
          geom = {
            cols: st.cols,
            rows: st.rows,
            shown: st.shown,
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
