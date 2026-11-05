<script lang="ts">
  // One column. The timeline is where you read, talk, run commands and answer the shell's
  // questions, and the live terminal takes the same place when something needs a real TTY:
  // vim, a REPL. Two side-by-side panes made the terminal read as the place the agent works,
  // when its commands never go through it.
  //
  // CSS grid throughout. WKWebView does not stretch a flex child in a flex column, so
  // every fill here is a grid cell. The band and each wrapper always render, or
  // auto-placement would shift the `minmax(0,1fr)` row out from under the body.
  //
  // Widgets open in a dock of panels around that column (DockArea). The column is its main
  // slot, a fixed DOM node that only changes size, so nothing in it remounts on a move.
  //
  // The xterm stays mounted behind the timeline, parked at its last measured size,
  // because FitAddon would otherwise tell tmux the pane is two columns wide and reflow
  // the human's shell.
  import { untrack } from "svelte";
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { dockCards, isEnded, tierBandClass } from "$lib/ssh";
  import SSHHeader from "./SSHHeader.svelte";
  import SSHBrief from "./SSHBrief.svelte";
  import SSHStream from "./SSHStream.svelte";
  import SSHDock from "./SSHDock.svelte";
  import SSHComposer from "./SSHComposer.svelte";
  import SSHHandback from "./SSHHandback.svelte";
  import SSHEnded from "./SSHEnded.svelte";
  import SSHOutput from "./SSHOutput.svelte";
  import SSHTerminalPane from "./SSHTerminalPane.svelte";
  import DockArea from "./dock/DockArea.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));

  // The push carries the glance every two seconds, and is always current. A moving
  // lastSeq is the signal to fetch what changed.
  const pushed = $derived(session?.ssh);
  $effect(() => {
    const conns = pushed?.connections;
    if (conns) view.connections = conns;
  });
  const seq = $derived(pushed?.lastSeq ?? 0);
  $effect(() => {
    const s = seq;
    untrack(() => {
      view.resetIfBehind(s);
      void view.fetch();
    });
  });
  // While a block runs, its row's preview and clock are worth one read a second.
  const anyRunning = $derived(view.items.some((i) => i.block?.status === "running"));
  $effect(() => {
    if (!anyRunning) return;
    const t = setInterval(() => void view.fetch(), 1000);
    return () => clearInterval(t);
  });

  // "Predates" came from whichever daemon answered last. A restart moves no seq, so
  // nothing else would ask again, and the message would outlive the daemon it was about.
  const predates = $derived(view.predates);
  $effect(() => {
    if (!predates) return;
    const t = setInterval(() => void view.fetch(), 3000);
    return () => clearInterval(t);
  });

  const conn = $derived(view.active());
  // Fallback picks by urgency, which moves. Pin it, so a request arriving in another tab
  // never flips the one being read.
  $effect(() => {
    if (conn && conn.id !== view.activeConn) view.activeConn = conn.id;
  });
  // A prompt is not in the store, so no seq moves when one appears. While a connection
  // comes up, a question is worth one read a second.
  const signingIn = $derived(view.connections.some((c) => c.state === "connecting" || c.state === "authenticating"));
  $effect(() => {
    if (!signingIn) return;
    const t = setInterval(() => void view.fetch(), 1000);
    return () => clearInterval(t);
  });
  const cards = $derived(dockCards(conn ? [conn] : [], view.tabItems, conn?.id ?? ""));
  const deciding = $derived(cards[0]?.kind === "approval" || cards[0]?.kind === "go");
  const hasTerminal = $derived(!!conn && !isEnded(conn));
  const ended = $derived(!!conn && isEnded(conn));
  const maxBlock = $derived.by(() => {
    const b = view.outputMax ? view.block(view.outputMax) : undefined;
    return b && b.conn === conn?.id ? b : undefined;
  });

  let paneEl = $state<HTMLDivElement | null>(null);
  let parked = $state({ w: 640, h: 480 });
  const showTerminal = $derived(hasTerminal && view.narrow === "terminal");

  $effect(() => {
    const el = paneEl;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => {
      if (!showTerminal || !el.clientWidth) return;
      parked = { w: el.clientWidth, h: el.clientHeight };
    });
    ro.observe(el);
    return () => ro.disconnect();
  });
</script>

<div class="grid h-full min-h-0 w-full grid-rows-[auto_auto_minmax(0,1fr)] bg-canvas" data-ssh-workspace>
  <div class="h-0.5 {conn ? tierBandClass(conn.tier) : ''}" aria-hidden="true"></div>
  <div>
    {#if conn}<SSHHeader {sessionId} {conn} canShowTerminal={hasTerminal} />{/if}
  </div>

  <DockArea {sessionId}>
    {#if !showTerminal}
      <!-- A size container, so the dock's cap is a share of THIS column. The rows live
           on an inner grid, because a container query cannot restyle its own container:
           .ssh-col in app.css gives the card what is left, keeps the composer's row, and
           below 420px lets a decision take the stream's row too. -->
      <div class="relative min-h-0 min-w-0 bg-canvas [container-type:size] {store.alive ? '' : 'ssh-offline'}">
       <div class="ssh-col absolute inset-0 grid" data-carded={cards.length > 0 ? "" : undefined} data-deciding={deciding ? "" : undefined}>
        <div>
          {#if !store.alive}
            <p class="border-b border-edge/60 px-3 py-1.5 text-sm text-bad" role="status" data-ssh-offline>The daemon is down. This is the last record it sent, and nothing here can act until it is back. Your terminal still works.</p>
          {/if}
          <SSHBrief {sessionId} />
        </div>
        <SSHStream {sessionId} />
        <div class="grid min-h-0 grid-rows-[minmax(0,1fr)]"><SSHDock {sessionId} /></div>
        <div class="max-h-[70cqh] overflow-y-auto">
          {#if ended}
            <SSHEnded {sessionId} />
          {:else if view.handback && view.handback.conn === conn?.id}
            <SSHHandback {sessionId} />
          {:else}
            <SSHComposer {sessionId} />
          {/if}
        </div>
        {#if maxBlock}
          <div class="absolute inset-0 z-20 grid min-h-0 grid-rows-[minmax(0,1fr)] bg-canvas p-3">
            <SSHOutput {sessionId} block={maxBlock} full />
          </div>
        {/if}
       </div>
      </div>
    {/if}

    {#if hasTerminal && conn}
      <!-- Parked, not unmounted. See the note at the top of this file. -->
      <div
        bind:this={paneEl}
        class="min-h-0 min-w-0 {showTerminal ? '' : 'pointer-events-none invisible absolute top-0 left-0'}"
        style={showTerminal ? "" : `width:${parked.w}px;height:${parked.h}px`}
        inert={!showTerminal}
      >
        <SSHTerminalPane {sessionId} {conn} />
      </div>
    {/if}
  </DockArea>
</div>
