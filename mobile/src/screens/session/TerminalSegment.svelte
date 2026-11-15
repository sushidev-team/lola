<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import { onDestroy, onMount, untrack } from "svelte";
  import type MobileTerminal from "@mobile/lib/components/MobileTerminal.svelte";
  import AccessoryBar from "@mobile/lib/components/AccessoryBar.svelte";
  import PaneTabs from "@mobile/lib/components/PaneTabs.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { viewClippingNotice } from "@mobile/lib/components/ViewSettings.svelte";
  import { installAppBackground } from "@mobile/lib/appstate";
  import { connection } from "@mobile/lib/connection.svelte";
  import { copy } from "@mobile/lib/copy";
  import { installKeyboardInset } from "@mobile/lib/keyboardinset";
  import { PIN_STUCK_MESSAGE, PanePin } from "@mobile/lib/panepin";
  import { router } from "@mobile/lib/router.svelte";
  import { terminalView } from "@mobile/lib/terminalgeometry.svelte";
  import { typedBytes } from "@mobile/lib/terminalinput";
  import { canWritePane } from "@mobile/lib/tiers.svelte";
  import { DaemonService } from "@mobile/wailsshim";

  // The Terminal segment of the session screen: a live pane with the tab strip
  // over it and the accessory bar under it. It is mounted only while the segment
  // is on screen, so the terminal attaches only then; xterm is a lazy chunk.
  //
  // Invariants:
  //  - This screen types whatever the human types. lola's AtPrompt gate guards
  //    lola's own automation, not a human holding the session. The one gate here
  //    is the `shell` tier: without it the pane is shown and nothing is typed.
  //  - The pane size pin is owned here, not by the terminal. The terminal is
  //    rebuilt on every tab switch (`{#key pane}`); only this screen outlives
  //    the switch, so only it can keep two panes from being pinned at once.
  //    Every condition that makes a pin wrong is in the one `want` expression,
  //    and every way out releases (leaving, backgrounding, switching, exit).
  //  - The pane is the route's: `/session/<id>/terminal/<pane>`, the agent pane
  //    being the session's own name. Attaching another pane replaces the route.

  let { id: sessionId }: { id?: string } = $props();

  const PANE_PANEL_ID = "session-pane";

  const route = $derived(router.top);
  const id = $derived(sessionId ?? (route.name === "session" ? route.id : ""));
  const pane = $derived(route.name === "session" && route.id === id && route.pane ? route.pane : id);
  const canType = $derived(canWritePane());

  const loadTerminal = import("@mobile/lib/components/MobileTerminal.svelte");

  let termRef = $state<ReturnType<typeof MobileTerminal> | undefined>();
  let barRef = $state<ReturnType<typeof AccessoryBar> | undefined>();
  let exited = $state(false);
  let error = $state("");
  /** A refused shell, or a pin that could not be released, in plain words. */
  let notice = $state("");
  /** Bumped when this screen learns something the tab strip cannot. */
  let paneRefresh = $state(0);
  let keyboardUp = $state(false);

  /** The one sentence a dead pane gets: the code is the least useful half. */
  function humanError(message: string): string {
    if (/^unknown_pane\b/.test(message)) return copy.terminal.gone.body;
    if (/^not connected\b/.test(message)) return copy.terminal.offline.body;
    return message;
  }

  const view = terminalView;
  const viewLabel = $derived.by(() => {
    const clip = viewClippingNotice(view.geom);
    return clip ? `View settings. ${clip}` : "View settings";
  });

  // --- Following the route's pane ----------------------------------------------
  // Defined before the pin effect so a pane change clears the stale capacity
  // first. The release goes with it: no capacity means no pin wanted.
  $effect(() => {
    const p = pane;
    untrack(() => {
      if (view.pane === p) return;
      exited = false;
      error = "";
      notice = "";
      view.switchPane(p);
    });
  });

  // --- The pane size pin --------------------------------------------------------
  const pin = new PanePin({
    resize: (session, p, cols, rows) => DaemonService.PaneResize(session, p, cols, rows),
    // "" withdraws the warning, and is matched against the exact sentence so a
    // pin that resolves never swallows a refusal the strip is reporting.
    report: (m) => {
      if (m !== "") {
        notice = m;
        return;
      }
      if (notice === PIN_STUCK_MESSAGE) notice = "";
    },
  });

  $effect(() => {
    const cap = view.capacity;
    const want =
      view.pinActive &&
      canType &&
      connection.ready &&
      !exited &&
      id !== "" &&
      view.pane === pane &&
      pane !== "" &&
      cap.cols > 0 &&
      cap.rows > 0
        ? { session: id, pane, cols: cap.cols, rows: cap.rows }
        : null;
    pin.want(want);
  });

  // A new socket knows nothing of what the old one sent: assert the intent again
  // on the false -> true edge and sweep any breadcrumb a lost release left. A
  // plain `let`, not `$state`: an effect that reads and writes one loops.
  let pinWasReady = connection.ready;
  $effect(() => {
    const ready = connection.ready;
    const back = ready && !pinWasReady;
    pinWasReady = ready;
    if (!back) return;
    void pin.reassert();
    void pin.recover();
  });

  let offKeyboard: (() => void) | undefined;
  let offBackground: (() => void) | undefined;
  onMount(() => {
    offKeyboard = installKeyboardInset((px) => (keyboardUp = px > 0));
    // Backgrounding closes the socket, so this release races a teardown and
    // sometimes loses; the breadcrumb covers the times it does.
    offBackground = installAppBackground(() => void pin.release());
    view.actions = {
      setFont: (size) => termRef?.setFont(size),
      toggleFit: () => termRef?.toggleFit(),
    };
    // Sweep a pin an earlier run left, exempting the pane about to be pinned.
    // Only with a socket: an offline sweep fails and warns about something the
    // person cannot act on. The edge above runs it when a connection arrives.
    if (connection.ready) {
      void pin.recover(view.pinActive && id !== "" && pane !== "" ? { session: id, pane } : null);
    }
  });
  onDestroy(() => {
    offKeyboard?.();
    offBackground?.();
    // Leaving the session is the commonest way out. Svelte does not wait on a
    // promise here; the request only has to reach the socket, and `release` is
    // idempotent.
    pin.stop();
    void pin.release();
    view.reset();
  });

  /** A latched modifier applies to what the soft keyboard produced. */
  function transform(data: string): string {
    return typedBytes(data, barRef?.latched() ?? {});
  }

  function attach(next: string): void {
    if (next === pane || route.name !== "session") return;
    // A navigation closes the open sheet, but retargeting the pane is not one:
    // a development link names the session, not its pane, so the inventory's
    // first answer replaces the route while that link's sheet is already up.
    const open = router.sheet;
    router.replace({ name: "session", id, seg: "terminal", pane: next });
    if (open !== "") router.open(open);
  }
</script>

<div class="flex h-full max-w-full min-h-0 min-w-0 flex-col overflow-x-clip bg-canvas">
  {#if error}
    <div class="shrink-0 border-b border-bad/40 bg-bad/10 px-4 py-2 text-sm text-bad" role="status">
      {humanError(error)}
    </div>
  {/if}

  {#if exited}
    <div class="shrink-0 border-b border-edge bg-panel px-4 py-2 text-sm text-faint" role="status">
      {copy.terminal.ended.body}
    </div>
  {/if}

  {#if notice}
    <div
      class="flex shrink-0 items-center gap-2 border-b border-warn/40 bg-warn/10 pl-4 text-sm text-warn"
      role="status"
    >
      <span class="min-w-0 flex-1 py-2">{notice}</span>
      <TouchButton icon aria-label="Dismiss" class="text-warn!" onclick={() => (notice = "")}
        ><Icon name="x" size={14} /></TouchButton
      >
    </div>
  {/if}

  <PaneTabs
    session={id}
    active={pane}
    panelId={PANE_PANEL_ID}
    refreshKey={paneRefresh}
    capacity={view.capacity}
    canShell={canType}
    onselect={attach}
    onnotice={(m) => (notice = m)}
    onmenu={() => router.open("paneMenu")}
    onpanes={(session, names) => void pin.forgetMissing(session, names)}
  >
    {#snippet trailing()}
      <!-- A grid wider than the screen looks like an agent that stopped writing
           mid-line, so the clipping lives in the button's name (viewLabel), not
           in a dot: the glyph stays a plain `type` icon. -->
      <TouchButton
        icon
        aria-label={viewLabel}
        aria-haspopup="dialog"
        class="h-11! w-11! text-subtext!"
        onclick={() => router.open("viewSettings")}
      >
        <Icon name="type" size={18} />
      </TouchButton>
    {/snippet}
  </PaneTabs>

  <div id={PANE_PANEL_ID} role="tabpanel" aria-label="Terminal" class="min-h-0 min-w-0 max-w-full flex-1 overflow-x-clip">
    {#key pane}
      {#await loadTerminal then m}
        {@const Terminal = m.default}
        <Terminal
          bind:this={termRef}
          {pane}
          {transform}
          readonly={!canType}
          onsent={() => barRef?.consumeLatch()}
          onexit={() => {
            exited = true;
            // The pane's tmux session is gone, so the inventory that listed it
            // is now wrong by exactly one tab.
            paneRefresh++;
          }}
          onerror={(m) => {
            error = m;
            // An attach refused `unknown_pane` says the same thing as an exit.
            if (/^unknown_pane\b/.test(m)) paneRefresh++;
          }}
          onstate={(st) => view.apply(st)}
        />
      {/await}
    {/key}
  </div>

  {#if !canType}
    <div class="shrink-0 border-t border-edge bg-panel px-4 py-2 text-sm text-faint" role="status">
      {copy.notices.noTier.body}
    </div>
  {/if}

  <AccessoryBar
    bind:this={barRef}
    modes={view.modes}
    disabled={!canType || exited || error !== ""}
    raised={keyboardUp}
    onsend={(bytes) => termRef?.send(bytes)}
  />
</div>
