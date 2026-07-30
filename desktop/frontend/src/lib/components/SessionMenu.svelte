<script lang="ts">
  // The session context menu (right-click on any session surface). Mounted once
  // in App.svelte; sessionmenu.svelte.ts holds the pending request. Resolves the
  // session from the store HERE (leaf read) — see WKWEBVIEW_REACTIVITY in
  // Cockpit.svelte — and closes itself if the session vanished mid-open.
  import { sessionMenu } from "$lib/sessionmenu.svelte";
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { terms } from "$lib/terms.svelte";

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

  <div
    bind:this={el}
    class="fixed z-50 min-w-[11rem] rounded-md border border-edge bg-panel py-1 shadow-lg"
    style="left:{req.x}px;top:{req.y}px"
    role="menu"
  >
    <div class="label truncate px-3 py-1 text-faint">
      {session.issue || session.id.slice(0, 8)}
    </div>
    <button
      class="block w-full px-3 py-1.5 text-left hover:bg-sel disabled:opacity-40"
      role="menuitem"
      disabled={!session.worktree}
      title={session.worktree ? "open a shell in the worktree" : "session has no worktree"}
      onclick={() => run((s) => addShell(s))}>+ add shell</button
    >
    <button
      class="block w-full px-3 py-1.5 text-left hover:bg-sel"
      role="menuitem"
      title="force a QA review pass now"
      onclick={() => run((s) => store.review(s.id))}>trigger review</button
    >
    <button
      class="block w-full px-3 py-1.5 text-left hover:bg-sel"
      role="menuitem"
      onclick={() => run((s) => store.coderabbit(s.id))}>coderabbit</button
    >
    {#if session.prNumber > 0}
      <button
        class="block w-full px-3 py-1.5 text-left hover:bg-sel"
        role="menuitem"
        onclick={() => run((s) => store.openURL(s.prUrl))}>open PR ↗</button
      >
    {/if}
    {#if canRevive}
      <button
        class="block w-full px-3 py-1.5 text-left text-info hover:bg-sel"
        role="menuitem"
        onclick={() => run((s) => store.revive(s.id))}>revive</button
      >
    {/if}
    <div class="my-1 border-t border-edge/60"></div>
    <!-- Routes through the shared confirm dialog, same as the 'x' shortcut. -->
    <button
      class="block w-full px-3 py-1.5 text-left text-faint hover:bg-sel hover:text-bad"
      role="menuitem"
      onclick={() => run((s) => store.askKill(s.id))}>kill…</button
    >
  </div>
{/if}
