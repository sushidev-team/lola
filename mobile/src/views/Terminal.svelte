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
  import { FONT_DEFAULT, FONT_MAX, FONT_MIN, stepFont } from "@mobile/lib/viewport";

  // The terminal screen: a live agent pane, the accessory bar under it, and the
  // one piece of policy that does not belong in either.
  //
  // THE MID-TURN GUARD, and it is FRICTION rather than a gate. These two things
  // are different and the distinction is written here so that nobody later
  // deletes one believing it is the other:
  //
  //   lola's AtPrompt gate stops lola's OWN automation — reactions, review
  //   hand-offs, resolveConflict — from typing into an agent mid-turn and
  //   corrupting it. The live terminal deliberately bypasses it, because a human
  //   at a keyboard is the case that gate was never about, and the desktop app
  //   already works this way.
  //
  //   This is a UI confirmation, in the client, for a reason specific to the
  //   phone: the desktop human sees the full 200-column pane, while the phone
  //   human sees roughly 55 columns of it with panning. Typing into a mid-turn
  //   agent from a clipped view is materially more likely to be an accident than
  //   doing it at the Mac, and the damage is exactly what the gate exists to
  //   prevent.
  //
  // Escape and Ctrl-C are EXEMPT. Interrupting is the legitimate mid-turn
  // action, and adding friction to it would recreate the uselessness the bypass
  // exists to avoid. And the whole guard FAILS OPEN: an unknown session, an
  // aux pane, a store that has not loaded — none of those may stop a person
  // typing, because this is friction and friction that fires on a guess is just
  // an obstacle.

  let { onback }: { onback: () => void } = $props();

  let termRef = $state<ReturnType<typeof MobileTerminal> | undefined>();
  let barRef = $state<ReturnType<typeof AccessoryBar> | undefined>();

  let modes = $state<TerminalModes>(DEFAULT_MODES);
  // Seeded with the terminal's own default rather than 0: the font buttons read
  // this to decide whether they are at a limit, and a 0 would draw "smaller" as
  // disabled from the moment the screen opens until the first keystroke.
  let font = $state(FONT_DEFAULT);
  let geom = $state({ cols: 0, rows: 0, shown: 0, panning: false });
  let exited = $state(false);
  let error = $state("");
  let keyboardInset = $state(0);

  /** Set while a burst has been confirmed. Expires, so a session that goes back
   *  to working is protected again without the user doing anything. */
  let armedUntil = $state(0);
  let asking = $state(false);

  const session = $derived(store.sessionById(nav.paneSession));

  /**
   * Whether the daemon's own view of this pane says a human is expected.
   *
   * `atPrompt` is the send-keys gate the daemon itself consults, and
   * `waiting_input` is the pane classifier's waiting verdict. Either is enough.
   * An unknown session answers TRUE, which is the fail-open half.
   */
  const paneWaiting = $derived(
    !session || session.atPrompt === true || session.agentState === "waiting_input",
  );

  // `tick` exists only to make the expiry below recompute as the clock moves.
  // Nothing reads its value.
  let tick = $state(0);
  const armed = $derived.by(() => {
    void tick;
    return paneWaiting || Date.now() < armedUntil;
  });
  const needsConfirm = $derived(!armed);

  let ticker: ReturnType<typeof setInterval> | undefined;
  let offKeyboard: (() => void) | undefined;
  onMount(() => {
    // A coarse poll rather than a timer per confirmation: the expiry only has to
    // be about right, and a derived that re-reads the clock cannot go stale the
    // way a scheduled reset can when the screen is backgrounded mid-window.
    ticker = setInterval(() => (tick = Date.now()), 5000);
    offKeyboard = installKeyboardInset((px) => (keyboardInset = px));
  });
  onDestroy(() => {
    clearInterval(ticker);
    offKeyboard?.();
  });

  /**
   * The one place a byte is allowed out. Returns false to drop it.
   *
   * The interrupts are recognised by their bytes rather than by which control
   * sent them, so a hardware Ctrl-C through xterm's own key handling is exempt
   * exactly as the bar's ^C key is.
   */
  function guard(bytes: string): boolean {
    if (bytes === "\x1b" || bytes === "\x03") return true;
    if (armed) return true;
    asking = true;
    return false;
  }

  function confirmSend() {
    // Two minutes: long enough to answer a question and correct a typo, short
    // enough that putting the phone down re-arms the guard.
    armedUntil = Date.now() + 120_000;
    asking = false;
    termRef?.focus();
  }

  /**
   * Apply a latched modifier to whatever the SOFT keyboard produced. The letters
   * only exist there, so a latch that worked for bar keys alone would make
   * "ctrl then a letter" — the commonest chord there is — impossible.
   *
   * It PEEKS at the latch rather than consuming it. Consuming here would clear
   * ctrl before `guard` had run, so latching ctrl and typing a letter while the
   * mid-turn friction is up would drop the byte AND the latch, and after "Send
   * anyway" the user would have to latch again with nothing saying why. The
   * consume happens on the path that actually writes, in MobileTerminal.send,
   * through `onsent` below. AccessoryBar.press has always worked this way round.
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
    style="padding-top: calc(env(safe-area-inset-top, 0px) + 0.5rem)"
  >
    <TouchButton icon aria-label="Back to sessions" onclick={onback}>‹</TouchButton>
    <div class="flex min-w-0 flex-col">
      <span class="truncate font-medium text-ink">
        {session?.issue || nav.paneSession}
      </span>
      <span class="num truncate text-sm text-faint">
        {geom.cols > 0 ? `${geom.shown}/${geom.cols}×${geom.rows}` : nav.pane}
      </span>
    </div>
    <div class="ml-auto flex shrink-0 items-center gap-1">
      {#if session}<StatusPill status={session.status} />{/if}
      <TouchButton
        icon
        aria-label="Smaller text"
        disabled={font <= FONT_MIN}
        onclick={() => setFont(-1)}>A−</TouchButton
      >
      <TouchButton
        icon
        aria-label="Larger text"
        disabled={font >= FONT_MAX}
        onclick={() => setFont(1)}>A+</TouchButton
      >
    </div>
  </header>

  {#if error}
    <div class="shrink-0 border-b border-bad/40 bg-bad/10 px-4 py-2 text-sm text-bad" role="status">
      {error}
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
        {guard}
        onsent={() => barRef?.consumeLatch()}
        onexit={() => (exited = true)}
        onerror={(m) => (error = m)}
        onstate={(st) => {
          modes = st.modes;
          font = st.font;
          geom = { cols: st.cols, rows: st.rows, shown: st.shown, panning: st.panning };
        }}
      />
    {/key}
  </div>

  {#if asking}
    <!-- The friction, and it is one tap. It names WHY, because "are you sure"
         with no reason is a dialog people learn to dismiss without reading. -->
    <div class="shrink-0 border-t border-warn/40 bg-warn/10 px-4 py-3">
      <p class="copy mb-2 text-sm text-ink">
        This agent is mid-turn, and you can only see {geom.shown} of its {geom.cols} columns.
        Typing now may land in the middle of something.
      </p>
      <div class="flex gap-2">
        <TouchButton variant="primary" onclick={confirmSend}>Send anyway</TouchButton>
        <TouchButton onclick={() => (asking = false)}>Cancel</TouchButton>
        <span class="ml-auto self-center text-sm text-faint">Esc and ^C always work</span>
      </div>
    </div>
  {/if}

  <AccessoryBar
    bind:this={barRef}
    {modes}
    {needsConfirm}
    onsend={(bytes) => termRef?.send(bytes)}
    onconfirm={() => (asking = true)}
  />
</div>
