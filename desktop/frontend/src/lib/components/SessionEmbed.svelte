<script lang="ts">
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { terms, AGENT } from "$lib/terms.svelte";
  import StatusPill from "./StatusPill.svelte";
  import LiveTerminal from "./LiveTerminal.svelte";

  // `focused` = the expanded full-cockpit view ("minimize" toggle); otherwise the
  // compact detail panel. The two used to differ in terminal font size as well —
  // they no longer do, because the size is half of a matched metric set (see
  // TERM_FONT). Focus changes how much room the terminal gets, not how big its
  // type is, which is also how Ghostty behaves.
  //
  // Takes the session ID (a plain nav value) and resolves the session from the
  // store HERE rather than receiving the resolved object as a prop: the Cockpit
  // view container does not re-render on the async daemon push in the production
  // WKWebView, so a `store.sessionById(...)` computed up there stays frozen at
  // undefined ("select a session" forever). A leaf component's own read reacts.
  // See WKWEBVIEW_REACTIVITY in Cockpit.svelte.
  let { sessionId, focused = false }: { sessionId: string; focused?: boolean } = $props();
  const session = $derived(sessionId ? store.sessionById(sessionId) : undefined);

  // Terminal tabs. Every session shows its agent pane; any number of shell tabs
  // can be opened (each a real tmux session in the worktree — see $lib/terms). The
  // bar hides when there are no shells AND the panel is compact: nothing to switch,
  // so no chrome. Focus (the big/fullscreen view) always shows it, so a shell is
  // reachable there without the "s" shortcut.
  const shells = $derived(session ? terms.shellsFor(session.id) : []);
  const activeTab = $derived(session ? terms.activeTab(session.id) : AGENT);
  const showTabs = $derived(!!session && (shells.length > 0 || focused));

  // The tmux name the LiveTerminal attaches to for the active tab. Keying the
  // terminal on this (below) swaps agent ⇄ shell by re-attaching — the same
  // proven remount the selection change already does, never a live DOM toggle. A
  // shell tab IS its tmux name; the agent tab resolves to the session's pane.
  const activeName = $derived(!session ? "" : activeTab === AGENT ? session.tmuxName : activeTab);
  const activeIsShell = $derived(activeTab !== AGENT);

  // Discover this session's shell tabs from the tmux server on selection, then
  // poll so a shell opened in the TUI (or another window) appears here within a
  // few seconds — the two surfaces attach to the same tmux sessions.
  $effect(() => {
    const id = session?.id;
    if (!id) return;
    terms.refresh(id);
    const poll = setInterval(() => terms.refresh(id), 4000);
    return () => clearInterval(poll);
  });

  const canRevive = $derived(session && (session.status === "dead" || session.status === "session_ended"));
</script>

{#if !session}
  <div class="flex h-full flex-col items-center justify-center gap-1 text-faint">
    <div class="text-2xl opacity-40">⌘</div>
    <div class="text-sm">select a session</div>
    <div class="text-[11px] opacity-70">its live agent terminal shows here</div>
  </div>
{:else}
  <!-- z-10 header keeps the chrome above the WebGL terminal canvas. The terminal
       wrapper stays in normal flow (NO `isolate` on the root, NO `z-0` on the
       wrapper): wrapping the canvas in its own `isolate`+`z-0` stacking context made
       WKWebView paint the wrapper's opaque `bg-panel` over it — a blank terminal. -->
  <div class="flex h-full min-h-0 flex-col">
    <!-- header — z-10 keeps the minimize/focus button above the canvas layer. -->
    <div class="relative z-10 flex flex-wrap items-center gap-2 border-b border-edge/60 px-3 py-1.5 text-xs">
      <span class="font-semibold text-accent-ink">{session.issue || session.id.slice(0, 8)}</span>
      <span class="truncate text-faint">{session.title}</span>
      <span class="text-edge">·</span>
      <span class="text-faint">{session.project}</span>
      <StatusPill status={session.status} />
      {#if session.branch}<span class="font-mono text-[11px] text-faint">{session.branch}</span>{/if}
      <span class="ml-auto flex items-center gap-1.5">
        {#if focused}
          <button
            class="rounded bg-accent-fill px-2.5 py-[2px] font-medium text-accent-ink hover:bg-accent-fill-hover"
            title="exit fullscreen"
            onclick={() => nav.toggleFocusTerm(session.id)}>⤢ minimize</button
          >
        {:else}
          <button
            class="rounded border border-edge px-2 py-[1px] hover:border-accent hover:text-accent-ink"
            title="expand to fullscreen"
            onclick={() => nav.toggleFocusTerm(session.id)}>⛶ focus</button
          >
        {/if}
      </span>
    </div>

    <!-- Terminal tabs. Shown when a shell is open or the panel is focused/big:
         the agent tab, one tab per shell (each with a "×" to close), and a "+"
         that opens another shell. Collapses in the compact, agent-only case so
         the plain detail panel stays chrome-free. -->
    {#if showTabs}
      <div class="relative z-10 flex flex-wrap items-center gap-1 border-b border-edge/60 px-2 py-1 text-[11px]">
        <button
          class="rounded px-2 py-[2px] font-medium"
          class:bg-accent-fill={activeTab === AGENT}
          class:text-accent-ink={activeTab === AGENT}
          class:text-faint={activeTab !== AGENT}
          onclick={() => terms.select(session.id, AGENT)}>agent</button
        >
        {#each shells as sh (sh)}
          <span class="flex items-center rounded" class:bg-accent-fill={activeTab === sh}>
            <button
              class="rounded-l px-2 py-[2px] font-medium"
              class:text-accent-ink={activeTab === sh}
              class:text-faint={activeTab !== sh}
              onclick={() => terms.select(session.id, sh)}>{terms.labelFor(session.id, sh)}</button
            >
            <button
              class="rounded-r pr-1.5 py-[2px] text-faint hover:text-bad"
              title="close shell"
              onclick={() => terms.closeShell(session.id, sh)}>×</button
            >
          </span>
        {/each}
        <button
          class="rounded px-2 py-[2px] text-faint hover:text-accent-ink"
          title="open a shell in the worktree"
          onclick={() => terms.newShell(session.id, session.worktree)}>+ shell</button
        >
      </div>
    {/if}

    <!-- Live terminal (agent pane or worktree shell). p-4 = 16px, matching
         Ghostty's window-padding-x/y; p-2 gave it half the breathing room and
         contributed to the cramped read. bg-panel is the flavor's `base` — the
         exact colour LiveTerminal paints as its terminal background — so the
         padding gutter is seamless with the terminal and the OSC-11 background an
         agent reads is genuinely the colour surrounding it. There is no fontSize
         prop any more: the old `focused ? 14 : 12` broke the cell arithmetic
         (see TERM_FONT). -->
    <div class="min-h-0 flex-1 bg-panel p-4">
      {#if activeName}
        <!-- Keyed on the active tab's tmux name, which already carries the session
             identity: switching agent ⇄ shell (or moving the selection) re-attaches
             by remounting. Keyed on the NAME, not a focus flag — with one cell size
             for every terminal, focus changes nothing here, and rebuilding on it
             would drop the scrollback every time the panel expanded. -->
        {#key activeName}
          <LiveTerminal
            name={activeName}
            webgl
            interactive
            autofocus={activeIsShell || focused}
            onExit={activeIsShell ? () => terms.shellExited(session.id, activeName) : undefined}
          />
        {/key}
      {:else}
        <div class="flex h-full items-center justify-center text-sm text-faint">no tmux session (dead)</div>
      {/if}
    </div>

    <!-- actions -->
    <div class="flex flex-wrap items-center gap-1.5 border-t border-edge/60 px-3 py-1.5 text-xs">
      {#if session.prNumber > 0}
        <button class="rounded px-2 py-[1px] text-faint hover:text-accent-ink" onclick={() => store.openURL(session.prUrl)}>open PR ↗</button>
      {/if}
      <button class="rounded px-2 py-[1px] text-faint hover:text-accent-ink" onclick={() => store.coderabbit(session.id)}>coderabbit</button>
      <button class="rounded px-2 py-[1px] text-faint hover:text-accent-ink" onclick={() => store.review(session.id)}>review</button>
      {#if canRevive}
        <button class="rounded px-2 py-[1px] text-info hover:text-accent-ink" onclick={() => store.revive(session.id)}>revive</button>
      {/if}
      <span class="ml-auto">
        <!-- Opens the shared KillConfirm dialog (App.svelte) rather than an inline
             yes/no, so the 'x' shortcut and this button confirm the same way. -->
        <button class="rounded px-2 py-[1px] text-faint hover:text-bad" onclick={() => nav.confirmKill(session.id)}>kill</button>
      </span>
    </div>
  </div>
{/if}
