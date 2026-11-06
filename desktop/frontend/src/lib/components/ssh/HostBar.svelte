<script lang="ts">
  // A host is a scope inside a session, never a place you go. All is the default, because an
  // agent's conversation is one story: a host filters it and names where a Run goes. Picking a
  // production target must stay a human act, so under All with several live hosts the bar acts
  // on none of them and says so.
  //
  // The right side belongs to the host in scope, or to the only live one. Each action sits beside
  // what it affects, which is why these are here and not in the session's ⋯.
  //
  // A fixed 32px, one line, never a wrap: a host arriving must not reflow what is below.
  import { store } from "$lib/store.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import {
    FILTERS,
    SSH_UNSANDBOXED_NOTE,
    agentName,
    connView,
    grantActive,
    hostName,
    identity,
    isEnded,
    mmss,
    sshTabs,
    type SSHConnInfo,
  } from "$lib/ssh";
  import { toMillis } from "$lib/time";
  import { widthOf } from "../session/measure";
  import Button from "../Button.svelte";
  import Chevron from "../Chevron.svelte";
  import MenuItem from "../MenuItem.svelte";
  import SSHTierTag from "./SSHTierTag.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const agent = $derived(agentName(session?.agent));
  const tabs = $derived(sshTabs(view.connections));
  const live = $derived(view.connections.filter((c) => c.state !== "denied" && !isEnded(c)));
  const scoped = $derived(view.connections.find((c) => c.id === view.scope));
  // Under All the bar has a host to act on only when there is exactly one.
  const target = $derived(scoped ?? (live.length === 1 ? live[0] : undefined));
  const filterLabel = $derived(FILTERS.find((f) => f.id === view.filter)?.label ?? "Everything");

  // Any host's, not only the one in scope: under All an agent may be running anything on one of
  // them without asking, and an open permission stays in plain sight until it ends.
  const now = $derived(view.connections.some((c) => c.grant) ? sshClock.now : Date.now());
  const granted = $derived(view.connections.filter((c) => !isEnded(c) && grantActive(c, now)));
  const elevated = $derived(view.connections.filter((c) => !isEnded(c) && c.termElevated));
  const where = $derived(target ? [target.user, target.cwd].filter(Boolean).join(":") : "");
  const wheelMine = $derived(target?.wheel === "human");

  let width = $state(0);
  // 0 means not measured yet, which is the wide layout. Below 900 of main column: one picker.
  const narrow = $derived(width > 0 && width < 876);

  function setScope(id: string) {
    view.scope = id;
    if (id) {
      view.activeConn = id;
      view.shellConn = id; // the shell of a host looked at earlier must not be what a Run opens
    }
  }

  type Pop = { kind: "more" | "filter" | "scope"; x: number; y: number };
  let pop = $state<Pop | null>(null);
  function openPop(kind: Pop["kind"], e: MouseEvent, w = 224) {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    pop = { kind, x: Math.max(4, Math.min(kind === "scope" ? r.left : r.right - w, window.innerWidth - w - 4)), y: r.bottom + 4 };
  }
  function run(fn: () => void) {
    pop = null;
    fn();
  }

  // The record of a host's commands and output goes for good, so it asks. What it keeps is said
  // in the sentence, because "delete" alone reads as if the conversation goes too.
  function removeHost(c: SSHConnInfo) {
    const running = c.runningBlocks ?? 0;
    confirm.ask({
      title: `Remove ${hostName(c)} and its record?`,
      body: "Removes this host, its commands and their output. Your messages stay.",
      detail: running > 0 ? `${running} still running. The remote process may keep running.` : undefined,
      confirmLabel: "Remove host",
      onConfirm: () => ssh.confirmTrash(sessionId, c),
    });
  }
</script>

<div class="relative z-10 grid h-8 min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-edge/60 bg-canvas px-3" {@attach widthOf((w) => (width = w))} data-host-bar>
  <div class="flex min-w-0 items-center gap-1 text-sm">
    {#if narrow}
      <Button size="xs" class="min-w-0 shrink!" aria-haspopup="menu" aria-expanded={pop?.kind === "scope"} onclick={(e) => openPop("scope", e)} data-host-picker>
        {#if scoped}<SSHTierTag tier={scoped.tier} short /><span class="min-w-0 truncate text-ink">{hostName(scoped)}</span>{:else}<span class="text-ink">All</span>{/if}
        <span class="text-faint"><Chevron open /></span>
      </Button>
    {:else}
      <div role="radiogroup" aria-label="Scope" class="flex min-w-0 items-center gap-1 overflow-hidden">
        <Button size="xs" role="radio" aria-checked={!scoped} class={!scoped ? "bg-sel! text-ink!" : ""} onclick={() => setScope("")}>All</Button>
        {#each tabs as t (t.conn.id)}
          {@const on = scoped?.id === t.conn.id}
          {@const st = connView(t.conn)}
          <span class="group flex min-w-0 items-center rounded-md {on ? 'bg-sel' : ''}" data-host-chip={t.conn.id}>
            <Button variant="bare" size="xs" role="radio" aria-checked={on} class="min-w-0 shrink! {on ? 'text-ink' : 'text-faint enabled:hover:text-ink'}" title={`${t.label}: ${st.word}`} onclick={() => setScope(t.conn.id)}>
              <span class={on ? "" : "opacity-80"}><SSHTierTag tier={t.conn.tier} short /></span>
              <span class="min-w-0 truncate">{t.label}</span>
              <span class="inline-block size-1.5 shrink-0 rounded-full {st.hollow ? 'border' : ''} {st.dot}" aria-hidden="true"></span>
              {#if t.count > 0}<span class="num text-orange">{t.count}</span>{/if}
            </Button>
            {#if on}
              <!-- Clears the filter. Nothing about the host changes: taking its record away is in ⋯, behind a question. -->
              <Button variant="bare" size="xs" icon class="h-5! w-4! text-faint opacity-70 group-hover:opacity-100 hover:text-ink focus-visible:opacity-100" title="Back to All" aria-label="Back to All" onclick={() => setScope("")}>×</Button>
            {/if}
          </span>
        {/each}
        <Button size="xs" title="Connect this session to another host" onclick={() => ssh.connectHost(sessionId)}><span aria-hidden="true">+</span> Connect</Button>
      </div>
    {/if}
  </div>

  <div class="flex items-center gap-2 text-sm">
    {#if !narrow && where}<span class="selectable max-w-56 truncate font-mono text-faint" title={target ? identity(target) : ""}>{where}</span>{/if}
    {#each elevated as c (c.id)}
      <span class="whitespace-nowrap text-warn" title="Your shell is inside {c.termElevated}. {agent} never has these rights: its commands run separately." data-root-shell>Root shell{c.id === target?.id ? "" : ` on ${hostName(c)}`}</span>
    {/each}
    {#each granted as c (c.id)}
      <span class="inline-flex items-center gap-1 whitespace-nowrap" title="The agent can run any command here without asking until this ends.">
        <span class="text-warn">Grant{c.id === target?.id ? "" : ` on ${hostName(c)}`}</span>
        <span class="num text-warn">{mmss(toMillis(c.grant?.expiresAt) - now)}</span>
        <Button size="xs" class="h-5!" onclick={() => ssh.revokeGrant(sessionId, c.id)}>Revoke</Button>
      </span>
    {/each}
    <Button size="xs" aria-haspopup="menu" aria-expanded={pop?.kind === "filter"} title="Filter the timeline (F)" onclick={(e) => openPop("filter", e)}>{filterLabel} <span class="text-faint"><Chevron open /></span></Button>
    {#if wheelMine && target}
      <Button size="xs" title="{agent} waits until you ask it something." onclick={() => ssh.openHandback(sessionId, target.id)}>Hand back</Button>
    {:else if target && !isEnded(target)}
      <!-- Under All with several hosts there is nothing to take, and the bar itself says to pick. -->
      <Button
        size="xs"
        title={`Stop ${agent}'s turn and any command it has running on ${hostName(target)}. The host is then yours.`}
        onclick={() => ssh.stopAgent(sessionId, target.id)}>Take the wheel</Button
      >
    {/if}
    {#if target}
      <Button icon size="xs" aria-label="More host actions" aria-haspopup="menu" aria-expanded={pop?.kind === "more"} onclick={(e) => openPop("more", e)}>⋯</Button>
    {/if}
  </div>
</div>

{#if pop}
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => (pop = null)} oncontextmenu={(e) => { e.preventDefault(); pop = null; }}></div>
  <div class="fixed z-50 w-56 rounded-lg border border-edge bg-panel p-1 shadow-xl" style="left:{pop.x}px;top:{pop.y}px" role="menu">
    {#if pop.kind === "filter"}
      {#each FILTERS as f (f.id)}
        <MenuItem icon={view.filter === f.id ? "•" : ""} onclick={() => run(() => (view.filter = f.id))}>{f.label}</MenuItem>
      {/each}
    {:else if pop.kind === "scope"}
      <MenuItem icon={!scoped ? "•" : ""} onclick={() => run(() => setScope(""))}>All</MenuItem>
      {#each tabs as t (t.conn.id)}
        <MenuItem icon={scoped?.id === t.conn.id ? "•" : ""} onclick={() => run(() => setScope(t.conn.id))}>{t.label}</MenuItem>
      {/each}
      <div class="my-1 h-px bg-edge/60"></div>
      <MenuItem icon="+" onclick={() => run(() => ssh.connectHost(sessionId))}>Connect</MenuItem>
    {:else if target}
      {@const t = target}
      <div class="grid gap-0.5 px-2 py-1.5 text-sm" data-conn-facts>
        <span class="selectable truncate font-mono text-faint">{identity(t)}</span>
        <span class={t.sandboxed ? "text-faint" : "text-warn"} title={t.sandboxed ? "" : SSH_UNSANDBOXED_NOTE}>{t.sandboxed ? "Agent runs in a container" : "Agent runs unsandboxed on this Mac"}</span>
        <span class="text-faint">{connView(t).word}{#if t.state === "connected" && !t.tabAlive}{" "}· Shell closed{/if}</span>
        {#if t.termElevated}<span class="text-warn" title="Your shell is inside {t.termElevated}. {agent} never has these rights: its commands run separately." data-root-shell>Root shell</span>{/if}
      </div>
      <div class="my-1 h-px bg-edge/60"></div>
      <MenuItem onclick={() => run(() => void navigator.clipboard?.writeText(t.alias))}>Copy ssh alias</MenuItem>
      {#if t.state === "connected" && !t.tabAlive}
        <MenuItem onclick={() => run(() => void ssh.terminal(sessionId, t.id))}>Open a new shell</MenuItem>
      {/if}
      {#if !isEnded(t) && t.wheel === "human"}
        <MenuItem onclick={() => run(() => void ssh.openHandback(sessionId, t.id))}>Hand back with a note…</MenuItem>
      {/if}
      <div class="my-1 h-px bg-edge/60"></div>
      {#if !isEnded(t)}
        <MenuItem variant="danger" onclick={() => run(() => ssh.askClose(sessionId, t))}>Close connection…</MenuItem>
      {/if}
      <MenuItem variant="danger" onclick={() => run(() => removeHost(t))}>Remove host and its record…</MenuItem>
    {/if}
  </div>
{/if}
