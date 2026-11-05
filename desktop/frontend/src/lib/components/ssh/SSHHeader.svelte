<script lang="ts">
  // Where you are and whether it is up, then the one action that may matter right now.
  // Control is not a setting: stopping the agent or running a command makes you the
  // driver, and asking the agent something hands it back. Lola's voice throughout:
  // every part of it is a recorded fact.
  import { store } from "$lib/store.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import {
    SSH_UNSANDBOXED_NOTE,
    agentName,
    connCount,
    connView,
    grantActive,
    hostName,
    identity,
    isEnded,
    mmss,
    tierShort,
    type SSHConnInfo,
  } from "$lib/ssh";
  import { toMillis } from "$lib/time";
  import { allTabs } from "$lib/docklayout";
  import Button from "../Button.svelte";
  import MenuItem from "../MenuItem.svelte";
  import SSHTierTag from "./SSHTierTag.svelte";

  let {
    sessionId,
    conn,
    canShowTerminal = false,
  }: { sessionId: string; conn: SSHConnInfo; canShowTerminal?: boolean } = $props();

  const view = $derived(ssh.view(sessionId));
  const widgetCount = $derived(allTabs(view.dock).length);
  const agent = $derived(agentName(store.sessionById(sessionId)?.agent));
  const agentBusy = $derived(store.sessionById(sessionId)?.agentState === "working" || (conn.runningBlocks ?? 0) > 0);
  const session = $derived(store.sessionById(sessionId));
  const now = $derived(sshClock.now);
  const st = $derived(connView(conn, now));
  const hasGrant = $derived(grantActive(conn, now));
  const grantLeft = $derived(toMillis(conn.grant?.expiresAt) - now);
  const deadline = $derived(conn.state === "authenticating" && conn.connectDeadline ? toMillis(conn.connectDeadline) - now : NaN);
  const shellClosed = $derived(conn.state === "connected" && !conn.tabAlive);
  const ended = $derived(conn.state === "closed" || conn.state === "denied");

  let menu = $state(false);
  let menuAt = $state({ x: 0, y: 0 });
  function openMenu(e: MouseEvent) {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    menuAt = { x: Math.max(4, r.right - 192), y: r.bottom + 4 };
    menu = true;
  }
  function run(fn: () => void) {
    menu = false;
    fn();
  }
</script>

<header class="relative z-10 border-b border-edge/60">
  <div class="flex min-h-9 flex-wrap items-center gap-x-2 gap-y-1 px-3 py-1">
    <!-- Where you are, then how it is. Who you are and how the agent runs are one click
         away in ⋯, because they do not change from one glance to the next. -->
    <SSHTierTag tier={conn.tier} />
    <span class="min-w-0 truncate text-sm font-medium text-ink" title={identity(conn)}>{hostName(conn)}</span>
    <span class="ml-1 inline-flex items-center gap-1.5 text-sm whitespace-nowrap">
      <span
        class="inline-block h-1.5 w-1.5 rounded-full {st.hollow ? 'border' : ''} {st.dot} {st.pulse ? 'ssh-pulse' : ''}"
        aria-hidden="true"
      ></span>
      <span class={st.text}>{conn.state === "connected" ? "Connected" : st.word}</span>
      {#if !Number.isNaN(deadline)}<span class="num text-faint">Gives up in {mmss(deadline)}</span>{/if}
    </span>
    {#if conn.termElevated}
      <span class="rounded-sm border border-warn/60 px-1.5 text-sm whitespace-nowrap text-warn" title="Your shell is inside {conn.termElevated}. {agent} never has these rights: its commands run separately." data-root-shell>Root shell</span>
    {/if}
    {#if hasGrant}
      <!-- An open permission stays in plain sight until it ends. -->
      <span class="inline-flex items-center gap-1 text-sm whitespace-nowrap" title="The agent can run any command here without asking until this ends.">
        <span class="text-warn">Grant</span>
        <span class="num text-warn">{mmss(grantLeft)}</span>
        <Button size="xs" class="h-5!" onclick={() => ssh.revokeGrant(sessionId, conn.id)}>Revoke</Button>
      </span>
    {/if}

    <span class="ml-auto flex items-center gap-2">
      {#if !ended && conn.wheel === "human"}
        <span class="text-sm text-accent-ink" data-driving-chip title="{agent} waits until you ask it something.">You're driving</span>
      {:else if !ended && agentBusy}
        <Button variant="danger" size="xs" title="Stop {agent}'s turn and any command it has running. The host is then yours." onclick={() => ssh.stopAgent(sessionId, conn.id)}><span aria-hidden="true">■</span> Stop {agent}</Button>
      {/if}
      {#if view.dockCompact}
        <Button size="xs" variant="secondary" data-dock-opener aria-label="Show {widgetCount} widgets" onclick={() => (view.dockDrawer = true)}>Widgets {widgetCount}</Button>
      {/if}
      <Button size="xs" icon aria-label="More connection actions" aria-haspopup="menu" aria-expanded={menu} onclick={openMenu}>⋯</Button>
    </span>
  </div>
</header>

{#if menu}
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => (menu = false)} oncontextmenu={(e) => { e.preventDefault(); menu = false; }}></div>
  <div class="fixed z-50 min-w-56 rounded-lg border border-edge bg-panel p-1 shadow-xl" style="left:{menuAt.x}px;top:{menuAt.y}px" role="menu">
    <div class="grid gap-0.5 px-2 py-1.5 text-sm" data-conn-facts>
      <span class="selectable truncate font-mono text-faint">{identity(conn)}</span>
      <span class={conn.sandboxed ? "text-faint" : "text-warn"} title={conn.sandboxed ? "" : SSH_UNSANDBOXED_NOTE}>{conn.sandboxed ? "Agent runs in a container" : "Agent runs unsandboxed on this Mac"}</span>
      <span class="text-faint">{st.word}{#if shellClosed}{" · "}Shell closed{/if}</span>
    </div>
    <div class="my-1 h-px bg-edge/60"></div>
    <MenuItem onclick={() => run(() => void navigator.clipboard?.writeText(conn.alias))}>Copy ssh alias</MenuItem>
    {#if canShowTerminal}
      {#if view.narrow === "terminal"}
        <MenuItem onclick={() => run(() => ssh.showTimeline(sessionId))}>Back to timeline</MenuItem>
      {:else}
        <MenuItem onclick={() => run(() => ssh.showTerminal(sessionId))}>Open terminal</MenuItem>
      {/if}
    {/if}
    {#if conn.state === "connected" && !conn.tabAlive}
      <MenuItem onclick={() => run(() => void ssh.terminal(sessionId, conn.id))}>Open a new shell</MenuItem>
    {/if}
    {#if !ended && conn.wheel === "human"}
      <MenuItem onclick={() => run(() => void ssh.openHandback(sessionId, conn.id))}>Hand back with a note…</MenuItem>
    {/if}
    {#if !ended}
      <div class="my-1 h-px bg-edge/60"></div>
      <MenuItem variant="danger" onclick={() => run(() => ssh.askClose(sessionId, conn))}>Close connection…</MenuItem>
    {/if}
  </div>
{/if}
