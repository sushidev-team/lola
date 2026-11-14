<script lang="ts">
  // The open session, under the top bar: the strip, then one grid cell that holds whichever view is
  // on show, then the footer strip. Always mounted, with `sessionId` possibly empty, because this
  // component's template must never flip a branch with a terminal inside it. Session changes update
  // sessionId, while TerminalCell and Workspace stay rendered and each holds its own
  // {#if}{#key} (see both, and CockpitLayout.svelte for what the freeze was).
  //
  // Takes the session ID (a plain nav value) and resolves the session from the store HERE rather
  // than receiving the resolved object as a prop: the Cockpit view container does not re-render on
  // the async daemon push in the production WKWebView. A leaf component's own read reacts. See
  // WKWEBVIEW_REACTIVITY in Cockpit.svelte.
  //
  // No `isolate` or `z-0` around the terminal: wrapping the canvas in its own stacking context made
  // WKWebView paint the wrapper's opaque background over it. The dock's `isolate` is inside
  // Workspace, and TerminalCell is its sibling, outside it.
  import type { Rect } from "$lib/docklayout";
  import { tick, untrack } from "svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { terms, AGENT, SSH, isChangesTab, isViewTab } from "$lib/terms.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { talk } from "$lib/talk.svelte";
  import { changes } from "$lib/changes.svelte";
  import { changesOffered, conversationOffered, feedKnownFrom, isAgentless, stripCells, stripShown } from "$lib/sessionview";
  import ChangesView from "./session/ChangesView.svelte";
  import EndedView from "./session/EndedView.svelte";
  import DevClashBanner from "./DevClashBanner.svelte";
  import Button from "./Button.svelte";
  import TerminalCell from "./session/TerminalCell.svelte";
  import Workspace from "./session/Workspace.svelte";
  import ViewStrip from "./session/ViewStrip.svelte";
  import SessionFooter from "./session/SessionFooter.svelte";
  import { widthOf } from "./session/measure";

  let { sessionId = "" }: { sessionId?: string } = $props();
  const session = $derived(sessionId ? store.sessionById(sessionId) : undefined);
  // The workspace stays mounted when focus returns to the sidebar.
  const open = $derived(!!sessionId && nav.focusedTerm === sessionId);
  const id = $derived(session?.id ?? "");

  const shells = $derived(id ? terms.shellsFor(id) : []);
  const activeTab = $derived(id ? terms.activeTab(id) : AGENT);
  const view = $derived(ssh.view(id));
  const offered = $derived(!!session && conversationOffered(session));
  const onHostShell = $derived(activeTab === SSH && view.narrow === "terminal");
  const onConversation = $derived(activeTab === SSH && view.narrow !== "terminal");
  const onChanges = $derived(isChangesTab(activeTab));
  let width = $state(0);
  let body = $state<HTMLElement | null>(null);
  let mainBox = $state<Rect | null>(null);
  function measureMain(rect: Rect, host: HTMLElement) {
    if (!body) return;
    const h = host.getBoundingClientRect(), b = body.getBoundingClientRect();
    mainBox = { ...rect, x: h.left - b.left + rect.x, y: h.top - b.top + rect.y };
  }
  const rightChanges = $derived(width >= 1180 && offered);
  const mainStyle = $derived(offered && mainBox ? `position:absolute;left:${mainBox.x}px;top:${mainBox.y}px;width:${mainBox.w}px;height:${mainBox.h}px` : 'grid-area:1/1');

  const strip = $derived(!!session && stripShown(session, stripCells(session, shells).machines.length));

  // Attached only while on screen. Home and the project views are an opaque overlay on a mounted
  // cockpit, and a terminal left attached under one would defer every automated send.
  const onScreen = $derived(nav.view === "cockpit");
  const agentPane = $derived(session?.tmuxName ?? "");
  const terminalName = $derived(!session || isViewTab(activeTab) || !onScreen ? "" : activeTab === AGENT ? agentPane : activeTab);
  const activeIsShell = $derived(activeTab !== AGENT && !isViewTab(activeTab));
  const covered = $derived(activeTab !== SSH && !(onChanges && rightChanges));
  const noPane = $derived(!!session && activeTab === AGENT && !agentPane);

  const localShell = $derived(!!session && isAgentless(session) && ["temporary", "main", "checkout"].includes(session.location ?? ""));

  async function terminalExited(sid: string, name: string, closeSession: boolean, shellTab: boolean) {
    if (shellTab) terms.shellExited(sid, name);
    if (!closeSession) return;
    const removed = await store.closeExitedTemporary(sid);
    if (removed) {
      // The user may have moved elsewhere while the liveness check ran.
      if (nav.focusedTerm === sid || (!nav.focusedTerm && nav.selectedId === sid)) {
        nav.goCockpit(nav.scoped ? nav.project : "");
        nav.select("");
      }
    } else {
      await terms.refresh(sid);
      const remaining = terms.shellsFor(sid);
      if (!shellTab && remaining.length && terms.activeTab(sid) === AGENT) terms.select(sid, remaining[0]);
    }
  }

  // Pinned once per open: an answer about the log that arrives after paint must not flip a view
  // someone is reading. See terms.arrive.
  $effect(() => {
    const sid = id;
    if (!sid) return;
    untrack(() => terms.arrive(sid, feedKnownFrom(talk.state(sid)?.health)));
  });

  // Pinned for this open only. Leaving unpins what nobody chose, so the next open lands by what is
  // known by then, and a log that could not be read is the terminal's.
  $effect(() => {
    const sid = id;
    if (sid) return () => untrack(() => terms.leave(sid));
  });
  const feedHealth = $derived(id ? talk.state(id)?.health : undefined);
  $effect(() => {
    const health = feedHealth;
    const agent = session?.agent;
    untrack(() => terms.noteFeedHealth(agent, health));
  });

  // What the footer's Stop and the strip read. Two views of a session share one poll.
  $effect(() => {
    if (id) return talk.poll(id);
  });

  // Discover this session's terminals from the tmux server, then poll, so one opened in the TUI
  // appears here within a few seconds. The two surfaces attach to the same tmux sessions.
  $effect(() => {
    const sid = id;
    if (!sid) return;
    terms.refresh(sid);
    const poll = setInterval(() => terms.refresh(sid), 4000);
    return () => clearInterval(poll);
  });

  // Being here is what seeing a finished turn means, so the roster's ● goes when it arrives.
  const stateSince = $derived(session?.agentStateSince);
  $effect(() => {
    const sid = id;
    stateSince;
    if (sid) untrack(() => nav.markSeen(sid));
  });

  // The strip's `Changes +84 −12`, once on arrival and once each time the agent comes to rest. On
  // Changes itself the diff that is already loading reports the same figure.
  const agentState = $derived(session?.agentState ?? "");
  const diffable = $derived(!!session && changesOffered(session));
  $effect(() => {
    const sid = id;
    agentState;
    if (sid && diffable && !onChanges && store.alive) untrack(() => void changes.load(sid));
  });

  // A session that left the list while open: merged and cleaned up, usually. Its record was
  // written before it left, so asking now is what lands on its story and not on an empty column.
  const missing = $derived(!!sessionId && !session);
  const endedRecord = $derived(missing ? talk.ended(sessionId) : undefined);
  $effect(() => {
    if (missing) untrack(() => void talk.loadEnded());
  });
  // An id the store has never listed has not arrived yet, usually: a new session is opened before
  // the refresh that lists it lands. "No longer running" would be false for that round trip, so it
  // takes having been seen, or a wait long enough that it is not on its way.
  const ARRIVAL_MS = 3000;
  let seenHere = $state("");
  let waitedFor = $state("");
  $effect(() => {
    if (session) seenHere = session.id;
  });
  $effect(() => {
    const sid = sessionId;
    if (!missing || !sid) return;
    const t = setTimeout(() => (waitedFor = sid), ARRIVAL_MS);
    return () => clearTimeout(t);
  });
  const gone = $derived(missing && (seenHere === sessionId || waitedFor === sessionId || !store.alive));

  // Focus on arrival: the composer in the conversation. A terminal takes the keyboard by mounting,
  // and Overview takes nothing. A jump that already chose a card says so through holdArrival.
  $effect(() => {
    const sid = id;
    const here = onConversation;
    if (!sid || !here || !open || nav.sidebarBrowsing) return;
    untrack(() => {
      const v = ssh.view(sid);
      if (v.holdArrival) v.holdArrival = false;
      else v.composerFocus++;
    });
  });
  // Enter on a band showing its terminal: nothing mounts, so the keyboard has to be asked for.
  $effect(() => {
    if (open && terminalName && !nav.sidebarBrowsing) untrack(() => terms.focusTerminal(id));
  });
  // A request made on a session already on its conversation has no arrival to hold back. Left
  // set, it would swallow the next real one, and a person's first words would fire bare keys. So
  // it lasts for the flush it was made in, which is when an arrival it did belong to has run.
  $effect(() => {
    const v = view;
    if (v.holdArrival) void tick().then(() => (v.holdArrival = false));
  });

  const leave = () => {
    if (session) ssh.leaveTerminal(session);
  };
</script>

<div class="grid h-full min-h-0 min-w-0 grid-cols-[minmax(0,1fr)] grid-rows-[auto_auto_auto_minmax(0,1fr)_auto] bg-canvas" {@attach widthOf(w => width = w)} data-session-view>
  <!-- Each wrapper always renders, or auto-placement would shift the body out of its row. -->
  <div class="min-w-0">

  </div>
  <div class="min-w-0">
    {#if !offered && session && strip}<ViewStrip {session} changesInSide={rightChanges} />{/if}
  </div>
  <div class="min-w-0">
    <!-- Why a dev tab died, when the reason is a port someone else holds. Absent while healthy. -->
    {#if !offered && session && !onConversation}<DevClashBanner {session} />{/if}
  </div>

  <div bind:this={body} class="relative grid min-h-0 min-w-0 grid-rows-[minmax(0,1fr)] grid-cols-[minmax(0,1fr)]">
      <!-- The conversation stays mounted behind a terminal or Changes, so a widget does not reload
           on the way there and back. Hidden with visibility: it keeps its measured size. -->
      <div class="min-h-0 min-w-0 {offered ? '' : 'hidden'} {onChanges && !rightChanges ? 'invisible' : ''}" style="grid-area:1/1" inert={onChanges && !rightChanges}>
        <Workspace sessionId={offered ? id : ""} {covered} changesInSide={rightChanges} onmain={measureMain}>
          {#snippet header()}
            {#if session && strip}<ViewStrip {session} changesInSide={rightChanges} />{/if}
            {#if session && !onConversation}<DevClashBanner {session} />{/if}
          {/snippet}
        </Workspace>
      </div>

      <TerminalCell
        name={terminalName}
        autofocus={open && !nav.sidebarBrowsing}
        style={mainStyle}
        focusRequest={id ? terms.terminalFocusAsks(id) : 0}
        onExit={session && (activeIsShell || localShell) ? () => terminalExited(id, terminalName, localShell, activeIsShell) : undefined}
        onLeave={leave}
      />

      {#if onChanges && session && !rightChanges}
        <div class="grid min-h-0 min-w-0 grid-rows-[auto_minmax(0,1fr)] bg-panel" style="grid-area:1/1"><div>{#if offered && strip}<ViewStrip {session} />{/if}</div><ChangesView sessionId={session.id} /></div>
      {/if}

      {#if noPane}
        <div class="grid min-h-0 place-items-center bg-panel text-faint" style={mainStyle}>
          <div class="grid justify-items-center gap-2 text-center">
            <p>This session has no terminal. Its agent is gone.</p>
            {#if session?.status === "dead" || session?.status === "session_ended"}
              <Button variant="secondary" size="sm" onclick={() => session && store.revive(session.id)}>{session.location ? "Reopen" : "Revive"}</Button>
            {/if}
          </div>
        </div>
      {/if}

      {#if !sessionId}
        <div class="grid min-h-0 place-items-center text-sm text-faint" style="grid-area:1/1">Select a session from the sidebar.</div>
      {/if}

      {#if endedRecord}
        <EndedView sessionId={endedRecord.id} style="grid-area:1/1" />
      {:else if gone}
        <!-- A session that went while open and left no record: a shell, usually. The top bar's ← and
             Esc still lead out. -->
        <div class="grid min-h-0 place-items-center text-faint" style="grid-area:1/1">
          <p>{store.alive ? "This session is no longer running." : "The daemon is down, and this session was not in its last record."}</p>
        </div>
      {/if}
  </div>

  <div class="min-w-0">
    {#if session && (!onConversation || onHostShell)}<SessionFooter sessionId={session.id} />{/if}
  </div>
</div>
