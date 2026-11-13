<script lang="ts">
  // The session context menu (right-click on any session surface). Mounted once
  // in App.svelte; sessionmenu.svelte.ts holds the pending request. Resolves the
  // session from the store HERE (leaf read) — see WKWEBVIEW_REACTIVITY in
  // Cockpit.svelte — and closes itself if the session vanished mid-open.
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { bestOf } from "$lib/bestof.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { isEnded, urgentConn } from "$lib/ssh";
  import MenuItem from "./MenuItem.svelte";

  const req = $derived(sessionMenu.request);
  const session = $derived(req ? store.sessionById(req.id) : undefined);
  const canRevive = $derived(!!session && (session.status === "dead" || session.status === "session_ended"));
  const AGENT_KINDS = ["claude", "codex", "opencode"] as const;
  const otherAgents = $derived(session?.location ? [] : AGENT_KINDS.filter((k) => k !== (session?.agent || "claude")));

  const sshConn = $derived(urgentConn(session?.ssh?.connections));
  const sshLive = $derived(!!sshConn && !isEnded(sshConn));

  let el = $state<HTMLDivElement | null>(null);

  // Clamp into the viewport once the menu has a measurable size, so a click near
  // the bottom/right edge doesn't push items off-screen.
  $effect(() => {
    const r = req;
    const node = el;
    if (!r || !node) return;
    const { width, height } = node.getBoundingClientRect();
    node.style.left = `${Math.max(4, Math.min(r.x, window.innerWidth - width - 4))}px`;
    node.style.top = `${Math.max(4, Math.min(r.y, window.innerHeight - height - 4))}px`;
  });

  // Close FIRST, then run: the action may open a dialog (kill) or navigate.
  // The session is resolved BEFORE closing — the `session` derived nulls the
  // moment the request clears, so an action reading it lazily would see nothing.
  function run(action: (s: SessionInfo) => void) {
    const s = session;
    sessionMenu.close();
    if (s) action(s);
  }

</script>

{#if req && session}
  <!-- Backdrop: any click (or another right-click) outside the menu dismisses
       it without falling through to the surface underneath. -->
  <div
    class="fixed inset-0 z-40"
    role="presentation"
    onclick={() => sessionMenu.close()}
    oncontextmenu={(e) => {
      e.preventDefault();
      sessionMenu.close();
    }}
  ></div>

  <!-- p-1, not py-1: the items are rounded chips inside the popover, so they need
       a gutter on all four sides or the hover fill runs into the border. -->
  <div
    bind:this={el}
    class="fixed z-50 max-h-[calc(100vh-8px)] min-w-[12rem] overflow-y-auto rounded-lg border border-edge bg-panel p-1 shadow-xl"
    style="left:{req.x}px;top:{req.y}px"
    role="menu"
  >
    <div class="label truncate px-2 pt-1 pb-1.5 text-faint">
      {session.issue || session.id.slice(0, 8)}
    </div>
    <MenuItem icon="✎" onclick={() => run(s => nav.openOverlay("rename-session", s.id))}>Rename session…</MenuItem>
    {#if session.worktree}
      <MenuItem icon="◆" title={session.worktree} onclick={() => run(s => store.openFolder(s.worktree))}>Open in Finder</MenuItem>
    {/if}
    {#if sshConn && sshLive}
      <div class="my-1 h-px bg-edge/60"></div>
      <MenuItem icon="⌁" onclick={() => run(s => ssh.wheelKey(s))}>{sshConn.wheel === "human" ? "Hand back…" : "Take the wheel"}</MenuItem>
      <MenuItem onclick={() => run(s => { if (sshConn) ssh.askClose(s.id, sshConn); })}>Close connection…</MenuItem>
    {/if}
    {#if session.status === "merge_conflict"}
      <!-- The same action the status pill offers on hover, for anyone who reaches
           a session by right-clicking it rather than by hovering its badge. -->
      <MenuItem
        variant="accent"
        icon="⇄"
        title={`merge ${store.defaultBranchFor(session.project) || "the default branch"} into this branch and resolve the conflicts (the session's agent does the work)`}
        onclick={() => run((s) => store.resolveConflict(s.id))}
      >
        Resolve conflicts
      </MenuItem>
    {/if}
    {#if session.bestOf}
      <!-- One attempt of a best-of group: the comparison is where the race ends
           (keep one, discard the rest), so it gets the accent until a pick. -->
      <MenuItem
        icon="★"
        variant={session.bestOf.winner ? "default" : "accent"}
        title="compare this issue's best-of attempts side by side and keep one"
        onclick={() => run((s) => bestOf.open(s.id))}
      >
        Compare attempts ({session.bestOf.attempt}/{session.bestOf.of})
      </MenuItem>
    {/if}
    {#if canRevive}
      <MenuItem variant="accent" icon="↻" onclick={() => run((s) => store.revive(s.id))} title={session.location ? "Reopen this directory; stopped agents start a new conversation" : "Resume the session"}>{session.location ? "Reopen session" : "Revive"}</MenuItem>
    {/if}
    {#if session.status !== "shell" && otherAgents.length}
      <div class="my-1 h-px bg-edge/60"></div>
      {#each otherAgents as kind (kind)}
        <MenuItem icon="⇄" onclick={() => run((s) => store.askSwitchAgent(s.id, kind))}>
          Switch to {kind}
        </MenuItem>
      {/each}
    {/if}
    <div class="my-1 h-px bg-edge/60"></div>
    <!-- Routes through the shared confirm dialog, same as the 'x' shortcut. -->
    <MenuItem variant="danger" icon="■" onclick={() => run((s) => store.askKill(s.id))}>Kill…</MenuItem>
  </div>
{/if}
