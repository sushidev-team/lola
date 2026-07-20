<script lang="ts">
  import { store, type SessionInfo } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import StatusPill from "./StatusPill.svelte";
  import LiveTerminal from "./LiveTerminal.svelte";

  // `focused` = the expanded full-cockpit view (bigger font, "minimize" toggle);
  // otherwise the compact detail panel (smaller font, padded terminal).
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
      <span class="font-semibold text-accent">{session.issue || session.id.slice(0, 8)}</span>
      <span class="truncate text-faint">{session.title}</span>
      <span class="text-edge">·</span>
      <span class="text-faint">{session.project}</span>
      <StatusPill status={session.status} />
      {#if session.branch}<span class="font-mono text-[11px] text-faint">{session.branch}</span>{/if}
      <span class="ml-auto flex items-center gap-1.5">
        <button class="rounded border border-edge px-2 py-[1px] hover:border-accent hover:text-accent" onclick={() => nav.toggleFocusTerm(session.id)}>
          {focused ? "⤢ minimize" : "⛶ focus"}
        </button>
      </span>
    </div>

    <!-- live agent terminal -->
    <div class="min-h-0 flex-1 p-2">
      {#if session.tmuxName}
        {#key session.id + (focused ? ":f" : "")}
          <LiveTerminal name={session.tmuxName} webgl interactive fontSize={focused ? 13 : 12} />
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
          class="min-w-0 flex-1 rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent"
          placeholder="type a reply to the agent…"
          bind:value={answer}
          onkeydown={(e) => e.key === "Enter" && send()}
        />
        <button class="rounded bg-accent/20 px-2 py-1 text-xs text-accent hover:bg-accent/30" onclick={send}>send</button>
      </div>
    {/if}

    <!-- actions -->
    <div class="flex flex-wrap items-center gap-1.5 border-t border-edge/60 px-3 py-1.5 text-xs">
      {#if session.prNumber > 0}
        <button class="rounded px-2 py-[1px] text-faint hover:text-accent" onclick={() => store.openURL(session.prUrl)}>open PR ↗</button>
      {/if}
      <button class="rounded px-2 py-[1px] text-faint hover:text-accent" onclick={() => store.coderabbit(session.id)}>coderabbit</button>
      <button class="rounded px-2 py-[1px] text-faint hover:text-accent" onclick={() => store.review(session.id)}>review</button>
      {#if canRevive}
        <button class="rounded px-2 py-[1px] text-info hover:text-accent" onclick={() => store.revive(session.id)}>revive</button>
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
