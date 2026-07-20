<script lang="ts">
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import StatusPill from "./StatusPill.svelte";
  import LiveTerminal from "./LiveTerminal.svelte";

  // `focused` = the expanded full-cockpit view ("minimize" toggle); otherwise the
  // compact detail panel. The two used to differ in terminal font size as well —
  // they no longer do, because the size is half of a matched metric set (see
  // TERM_FONT). Focus changes how much room the terminal gets, not how big its
  // type is, which is also how Ghostty behaves.
  let { session, focused = false }: { session: SessionInfo | undefined; focused?: boolean } = $props();

  let answer = $state("");
  let confirmKill = $state(false);

  function send() {
    if (!session || !answer.trim()) return;
    store.answer(session.id, answer);
    answer = "";
  }
  function doKill() {
    if (!session) return;
    store.kill(session.id);
    confirmKill = false;
  }

  const canRevive = $derived(session && (session.status === "dead" || session.status === "session_ended"));
</script>

{#if !session}
  <div class="flex h-full flex-col items-center justify-center gap-1 text-faint">
    <div class="text-2xl opacity-40">⌘</div>
    <div class="text-sm">select a session</div>
    <div class="text-[11px] opacity-70">its live agent terminal shows here</div>
  </div>
{:else}
  <div class="flex h-full min-h-0 flex-col">
    <!-- header -->
    <div class="flex flex-wrap items-center gap-2 border-b border-edge/60 px-3 py-1.5 text-xs">
      <span class="font-semibold text-accent-ink">{session.issue || session.id.slice(0, 8)}</span>
      <span class="truncate text-faint">{session.title}</span>
      <span class="text-edge">·</span>
      <span class="text-faint">{session.project}</span>
      <StatusPill status={session.status} />
      {#if session.branch}<span class="font-mono text-[11px] text-faint">{session.branch}</span>{/if}
      <span class="ml-auto flex items-center gap-1.5">
        <button class="rounded border border-edge px-2 py-[1px] hover:border-accent hover:text-accent-ink" onclick={() => nav.toggleFocusTerm(session.id)}>
          {focused ? "⤢ minimize" : "⛶ focus"}
        </button>
      </span>
    </div>

    <!-- Live agent terminal. p-4 = 16px, matching Ghostty's window-padding-x/y;
         p-2 gave it half the breathing room and contributed to the cramped read.
         bg-panel is the flavor's `base` — the exact colour LiveTerminal paints as
         its terminal background — so the padding gutter is seamless with the
         terminal and the OSC-11 background an agent reads is genuinely the colour
         surrounding it. There is no fontSize prop any more: the old
         `focused ? 14 : 12` broke the cell arithmetic (see TERM_FONT). -->
    <div class="min-h-0 flex-1 bg-panel p-4">
      {#if session.tmuxName}
        <!-- Keyed on the session ONLY. The old key also carried a focus flag, to
             force a rebuild when the removed `fontSize={focused ? 14 : 12}` prop
             changed; with one cell size for every terminal, focus changes nothing
             here and rebuilding on it would detach and re-attach the tmux PTY —
             and drop the scrollback — every time the user moved the selection. -->
        {#key session.id}
          <LiveTerminal name={session.tmuxName} webgl interactive />
        {/key}
      {:else}
        <div class="flex h-full items-center justify-center text-sm text-faint">no tmux session (dead)</div>
      {/if}
    </div>

    <!-- answer card for needs_input -->
    {#if session.status === "needs_input"}
      <div class="flex items-center gap-2 border-t border-orange/40 bg-orange/5 px-3 py-2">
        <span class="text-orange">?</span>
        <input
          class="min-w-0 flex-1 rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent placeholder:text-placeholder"
          placeholder="type a reply to the agent…"
          bind:value={answer}
          onkeydown={(e) => e.key === "Enter" && send()}
        />
        <button class="rounded bg-accent-fill px-2 py-1 text-xs text-accent-ink hover:bg-accent-fill-hover" onclick={send}>send</button>
      </div>
    {/if}

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
        {#if confirmKill}
          <span class="text-warn">kill {session.issue || session.id.slice(0, 8)}?</span>
          <button class="ml-1 rounded px-1.5 text-bad hover:underline" onclick={doKill}>yes</button>
          <button class="rounded px-1.5 text-faint hover:underline" onclick={() => (confirmKill = false)}>no</button>
        {:else}
          <button class="rounded px-2 py-[1px] text-faint hover:text-bad" onclick={() => (confirmKill = true)}>kill</button>
        {/if}
      </span>
    </div>
  </div>
{/if}
