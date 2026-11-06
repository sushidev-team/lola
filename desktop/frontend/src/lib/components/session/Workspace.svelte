<script lang="ts">
  // The conversation: band and host bar, then the column with the story and the action zone,
  // inside the dock. Always rendered, with `sessionId` possibly empty, and it holds its own
  // {#if}{#key} for the same reason TerminalCell does: Enter and Esc would otherwise flip a
  // branch in a parent's template with a terminal inside it, the parked one below.
  //
  // CSS grid throughout. WKWebView does not stretch a flex child in a flex column, so every fill
  // here is a grid cell. The band and each wrapper always render, or auto-placement would shift
  // the `minmax(0,1fr)` row out from under the body.
  //
  // A host's xterm stays mounted behind the column, parked at its last measured size, because
  // FitAddon would otherwise tell tmux the pane is two columns wide and reflow the human's shell.
  // Its session is never in `Shells`, and it is mounted only here: one tmux session with two
  // xterm clients would fight over its size.
  import { untrack } from "svelte";
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { talk } from "$lib/talk.svelte";
  import { deriveSide, sideHasContent } from "$lib/sessionside";
  import { inScope } from "$lib/turns";
  import { bandTier, dockCards, isEnded, tierBandClass } from "$lib/ssh";
  import SSHTerminalPane from "../ssh/SSHTerminalPane.svelte";
  import DockArea from "../ssh/dock/DockArea.svelte";
  import HostBar from "../ssh/HostBar.svelte";
  import TimelineSlot from "./TimelineSlot.svelte";
  import ActionZone from "./ActionZone.svelte";
  import SessionSide from "./SessionSide.svelte";

  let { sessionId = "", covered = false }: { sessionId?: string; covered?: boolean } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(sessionId ? store.sessionById(sessionId) : undefined);

  // The push carries the glance every two seconds, and is always current. A moving lastSeq is
  // the signal to fetch what changed.
  const pushed = $derived(session?.ssh);
  $effect(() => {
    const conns = pushed?.connections;
    if (conns) view.connections = conns;
  });
  const seq = $derived(pushed?.lastSeq ?? 0);
  // The daemon coming back counts too: no seq moves for that, and a fetch that failed while it was
  // down would otherwise stay failed for good.
  $effect(() => {
    const s = seq;
    const up = store.alive;
    if (!sessionId) return;
    untrack(() => {
      view.resetIfBehind(s);
      if (up || !view.loaded) void view.fetch();
    });
  });
  // While a block runs, its row's preview and clock are worth one read a second.
  const anyRunning = $derived(view.items.some((i) => i.block?.status === "running"));
  $effect(() => {
    if (!anyRunning || !sessionId) return;
    const t = setInterval(() => void view.fetch(), 1000);
    return () => clearInterval(t);
  });
  // "Predates" came from whichever daemon answered last. A restart moves no seq, so nothing else
  // would ask again, and the message would outlive the daemon it was about.
  const predates = $derived(view.predates);
  $effect(() => {
    if (!predates || !sessionId) return;
    const t = setInterval(() => void view.fetch(), 3000);
    return () => clearInterval(t);
  });
  // A prompt is not in the store, so no seq moves when one appears. While a connection comes up,
  // a question is worth one read a second.
  const signingIn = $derived(view.connections.some((c) => c.state === "connecting" || c.state === "authenticating"));
  $effect(() => {
    if (!signingIn || !sessionId) return;
    const t = setInterval(() => void view.fetch(), 1000);
    return () => clearInterval(t);
  });
  $effect(() => {
    if (sessionId) return talk.poll(sessionId);
  });

  const conn = $derived(view.active());
  // Fallback picks by urgency, which moves. Pin it, so a request arriving on another host never
  // flips the one being read.
  $effect(() => {
    if (conn && conn.id !== view.activeConn) view.activeConn = conn.id;
  });
  const cards = $derived(dockCards(conn ? [conn] : [], view.tabItems, conn?.id ?? ""));
  const deciding = $derived(cards[0]?.kind === "approval" || cards[0]?.kind === "go");

  const shown = $derived(view.connections.filter((c) => c.state !== "denied"));
  const band = $derived(tierBandClass(bandTier(shown, view.scope)));

  // Which host's shell is parked: the one the strip picked, else the active connection's.
  const shell = $derived(view.connections.find((c) => c.id === view.shellConn && !isEnded(c)) ?? conn);
  const hasTerminal = $derived(!!shell && !isEnded(shell));
  const showTerminal = $derived(hasTerminal && view.narrow === "terminal");
  // The shell on show ended. Falling through to another host's would hand that host the next
  // keystrokes with no one having asked, so this goes back to the story, and arriving there is
  // what returns the keyboard to the composer.
  $effect(() => {
    if (view.narrow === "terminal" && (!hasTerminal || (view.shellConn && shell?.id !== view.shellConn))) view.narrow = "stream";
  });
  // Report and the drawer hold window-level Esc handlers, and Esc on a terminal is its program's.
  // Neither is on show from one, so neither stays open behind it.
  $effect(() => {
    if (covered || showTerminal) {
      untrack(() => {
        view.reportOpen = false;
        view.dockDrawer = false;
      });
    }
  });

  let paneEl = $state<HTMLDivElement | null>(null);
  let parked = $state({ w: 640, h: 480 });
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

  // The agent's account: a sidebar once it has used one and the column has room. Below that, and
  // when hidden, it stays one line over the story. Measured, never a guess: the band under the list
  // and the open session are the same width, so they make the same choice.
  const SIDE_MIN_WIDTH = 1040;
  let rootWidth = $state(0);
  const attachWidth = (el: HTMLElement) => {
    if (typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => (rootWidth = el.clientWidth));
    ro.observe(el);
    return () => ro.disconnect();
  };
  const sideHas = $derived(sideHasContent(deriveSide(inScope(view.items, view.scope), session?.board), session?.board));
  const sideFits = $derived(rootWidth >= SIDE_MIN_WIDTH && !showTerminal && sideHas);
  const side = $derived(sideFits && !view.sideHidden);
  // The menu's Report asks for the account. With a sidebar there is no overlay to open.
  $effect(() => {
    if (view.reportOpen && sideFits) {
      untrack(() => {
        view.reportOpen = false;
        view.sideHidden = false;
      });
    }
  });

  // Clicking the story without selecting anything returns the keyboard to the composer. A person
  // who clicked to read should be able to type next, and a selection they just made is theirs.
  function clickStory(e: MouseEvent) {
    const t = e.target as HTMLElement | null;
    if (!t || t.closest("button,a,input,textarea,select,summary,[role='button'],[tabindex]")) return;
    const sel = window.getSelection?.();
    if (sel && !sel.isCollapsed) return;
    view.composerFocus++;
  }
</script>

{#if sessionId}
  {#key sessionId}
    <div class="grid h-full min-h-0 w-full grid-cols-[minmax(0,1fr)] grid-rows-[auto_auto_minmax(0,1fr)] bg-canvas" data-ssh-workspace {@attach attachWidth}>
      <div class="{shown.length > 0 ? 'h-0.5' : 'h-0'} {band}" aria-hidden="true" data-tier-band></div>
      <div>
        {#if shown.length > 0}<HostBar {sessionId} />{/if}
      </div>
      <div class="grid min-h-0 min-w-0 grid-rows-[minmax(0,1fr)]" style="grid-template-columns:minmax(0,1fr){side ? ' 300px' : ''}">
      <DockArea {sessionId} {covered}>
        {#if !showTerminal}
          <!-- A size container, so the dock's cap is a share of THIS column. The rows live on an
               inner grid, because a container query cannot restyle its own container. -->
          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
          <div class="relative min-h-0 min-w-0 bg-canvas [container-type:size] {store.alive ? '' : 'ssh-offline'}" onclick={clickStory} data-conversation>
            <div class="ssh-col absolute inset-0 grid" data-carded={cards.length > 0 ? "" : undefined} data-deciding={deciding ? "" : undefined}>
              <TimelineSlot {sessionId} {covered} {side} onside={sideFits ? () => (view.sideHidden = false) : undefined} />
              <ActionZone {sessionId} {covered} />
            </div>
          </div>
        {/if}

        {#if hasTerminal && shell}
          <!-- Parked, not unmounted. See the note at the top of this file. -->
          <div
            bind:this={paneEl}
            class="min-h-0 min-w-0 {showTerminal ? '' : 'pointer-events-none invisible absolute top-0 left-0'}"
            style={showTerminal ? "" : `width:${parked.w}px;height:${parked.h}px`}
            inert={!showTerminal}
          >
            <SSHTerminalPane {sessionId} conn={shell} chrome={false} />
          </div>
        {/if}
      </DockArea>
      {#if side}<SessionSide {sessionId} />{/if}
      </div>
    </div>
  {/key}
{/if}
