<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import StatusPill from "$lib/components/StatusPill.svelte";
  import { store } from "$lib/store.svelte";
  import AccessoryBar from "@mobile/lib/components/AccessoryBar.svelte";
  import MobileTerminal from "@mobile/lib/components/MobileTerminal.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { DEFAULT_MODES, isBracketedPaste, textBytes, type TerminalModes } from "@mobile/lib/keybytes";
  import { installKeyboardInset } from "@mobile/lib/keyboardinset";
  import { nav } from "@mobile/lib/nav.svelte";
  import { FONT_MAX, FONT_MIN, stepFont } from "@mobile/lib/viewport";
  import { loadFontSize } from "@mobile/lib/prefs";

  // The terminal screen: a live agent pane with the accessory bar under it.
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
  // or the bare default: the font buttons read this to decide whether they are
  // at a limit, so a 0 would draw "smaller" as disabled from the moment the
  // screen opens, and the plain default would mis-draw the limits for one tick
  // for anyone whose remembered size is at the floor or the ceiling. It
  // self-corrects on the terminal's first `onstate`; seeding it right just
  // means there is no frame where it is wrong.
  let font = $state(loadFontSize());
  let geom = $state({
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
  let keyboardInset = $state(0);

  // Read for the status pill in the header only. Nothing on this screen gates a
  // keystroke on it.
  const session = $derived(store.sessionById(nav.paneSession));

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

  function setFont(delta: number) {
    termRef?.setFont(stepFont(font, delta));
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
      <!-- THE RANGE, NOT THE COUNT. `43/211x44` says how much is on screen and
           nothing about where: five consecutive screens of a 211-column grid
           read identically. `44-86 of 211` says where the window is, which is
           the only question panning raises. It collapses to the plain grid size
           when nothing is clipped. -->
      <span class="num truncate text-sm text-faint">
        {#if geom.cols > 0 && geom.panning}
          {geom.first}–{Math.min(geom.cols, geom.first + geom.shown - 1)} of {geom.cols}×{geom.rows}
        {:else if geom.cols > 0}
          {geom.cols}×{geom.rows}
        {:else if error}
          <!-- The tmux name is a debugging handle, not a subtitle. With no grid
               to report it was all this line had, so a screen whose whole point
               is "this pane is gone" led with `lola-nori-app-nor-311`. -->
          No terminal
        {:else}
          {nav.pane}
        {/if}
      </span>
    </div>
    <div class="ml-auto flex shrink-0 items-center gap-1">
      {#if session && !exited}<StatusPill status={session.status} />{/if}
      <!-- The fit-width zoom lives HERE rather than over the pane. As a chip
           pinned to the pane's top-right corner it permanently covered the
           first line of live output — a phone always clips a developer's grid,
           so it was never not up — and it was drawn identically whether it was
           a button or a caption. In the header it is unmistakably a control,
           and it is hidden rather than disabled when there is no smaller size
           to offer, because a dead control is worse than none. -->
      {#if !exited && (geom.canFit || geom.fitActive)}
        <TouchButton
          icon
          aria-label={geom.fitActive
            ? "Back to the reading text size"
            : `Zoom out to ${geom.fitSize} point text to see more of the grid. This changes only this phone's view; the pane keeps its size.`}
          onclick={() => termRef?.toggleFit()}>{geom.fitActive ? "⤢" : "⤡"}</TouchButton
        >
      {/if}
      <TouchButton
        icon
        aria-label="Smaller text"
        disabled={exited || font <= FONT_MIN}
        onclick={() => setFont(-1)}>A−</TouchButton
      >
      <TouchButton
        icon
        aria-label="Larger text"
        disabled={exited || font >= FONT_MAX}
        onclick={() => setFont(1)}>A+</TouchButton
      >
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

  <div class="min-h-0 flex-1">
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
