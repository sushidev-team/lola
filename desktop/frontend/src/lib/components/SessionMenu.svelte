<script lang="ts">
  // The session context menu (right-click on any session surface). Mounted once
  // in App.svelte; sessionmenu.svelte.ts holds the pending request. Resolves the
  // session from the store HERE (leaf read) — see WKWEBVIEW_REACTIVITY in
  // Cockpit.svelte — and closes itself if the session vanished mid-open.
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { terms } from "$lib/terms.svelte";
  import MenuItem from "./MenuItem.svelte";

  const req = $derived(sessionMenu.request);
  const session = $derived(req ? store.sessionById(req.id) : undefined);
  const canRevive = $derived(!!session && (session.status === "dead" || session.status === "session_ended"));

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

  // Opens a worktree shell tab for the session. The shell tab lives in the
  // detail SessionEmbed, which the grid lens doesn't render — leave the grid
  // first (same move as opening a tile), then select so the new tab is visible.
  function addShell(s: SessionInfo) {
    if (nav.lens === "grid") nav.setLens("list");
    nav.select(s.id);
    terms.newShell(s.id, s.worktree);
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
    class="fixed z-50 min-w-[12rem] rounded-lg border border-edge bg-panel p-1 shadow-xl"
    style="left:{req.x}px;top:{req.y}px"
    role="menu"
  >
    <div class="label truncate px-2 pt-1 pb-1.5 text-faint">
      {session.issue || session.id.slice(0, 8)}
    </div>
    <MenuItem
      icon="+"
      disabled={!session.worktree}
      title={session.worktree ? "open a shell in the worktree" : "session has no worktree"}
      onclick={() => run((s) => addShell(s))}>Add shell</MenuItem
    >
    {#if session.devCommands?.length}
      <!-- The project's dev processes run in ONE session at a time, so this is a
           toggle, not an "open": switching it on here takes them off whoever had
           them. The label says where they are, not what the click does — "Active"
           with a filled dot IS the state. -->
      <MenuItem
        icon={session.devActive ? "●" : "○"}
        variant={session.devActive ? "accent" : "default"}
        title={session.devActive
          ? `stop ${session.devCommands.join(", ")}`
          : `run ${session.devCommands.join(", ")} here (stops them in any other session of this project)`}
        onclick={() => run((s) => store.dev(s.id, !s.devActive))}
      >
        {session.devActive ? "Active — stop dev" : "Make active"}
      </MenuItem>
    {/if}
    <MenuItem icon="◈" title="force a QA review pass now" onclick={() => run((s) => store.review(s.id))}>
      Trigger review
    </MenuItem>
    <MenuItem icon="◇" onclick={() => run((s) => store.coderabbit(s.id))}>CodeRabbit</MenuItem>
    {#if session.prNumber > 0}
      <MenuItem icon="⑂" trailing="↗" onclick={() => run((s) => store.openURL(s.prUrl))}>Open PR</MenuItem>
    {/if}
    {#if canRevive}
      <MenuItem variant="accent" icon="↻" onclick={() => run((s) => store.revive(s.id))}>Revive</MenuItem>
    {/if}
    <div class="my-1 h-px bg-edge/60"></div>
    <!-- Routes through the shared confirm dialog, same as the 'x' shortcut. -->
    <MenuItem variant="danger" icon="■" onclick={() => run((s) => store.askKill(s.id))}>Kill…</MenuItem>
  </div>
{/if}
