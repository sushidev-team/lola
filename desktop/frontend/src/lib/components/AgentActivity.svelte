<script lang="ts">
  import { showAgentBadge } from "$lib/theme";
  import LivePulse from "./LivePulse.svelte";
  import type { SessionInfo } from "$lib/store.svelte";

  // The secondary "what is this agent doing right now" line. It replaces two
  // things that used to be tooltips or cryptic glyphs:
  //
  //  1. the `·wk` / `·?!` / `·en` badge that hung off the status pill. Those
  //     two-letter abbreviations are the TUI's (agentBadge in
  //     internal/tui/sessionview.go), where every column is precious. The app has
  //     room the TUI does not, so the agent axis is rendered here in words and
  //     motion instead.
  //  2. the `title=` tooltip carrying the interpreter's headline, which was
  //     discoverable only by hovering the right ~60px of a row.
  //
  // The text is UNTRUSTED, display-only ([statusagent] / the agent's own
  // notification): it is "≈"-marked exactly like the interpreted pill and never
  // styled as fact. Nothing here feeds the control loop.
  let { session }: { session: SessionInfo } = $props();

  // The pulse surfaces the AGENT axis on rows whose pill is showing a DELIVERY
  // state — "CI is running and the agent is still typing" is two facts, and the
  // pill only has room for one. It is not fresher than the pill: both read the
  // same ~30s observer snapshot.
  const live = $derived(session.agentState === "working" || session.agentState === "starting");

  // "agent exited" stays gated on showAgentBadge, i.e. only when the agent
  // disagrees with an open PR's delivery state. Ungated it would restate the
  // status pill on every row, which is the noise that rule was written to avoid.
  //
  // There is deliberately NO "waiting for you" branch. It looks like it belongs
  // here, and it is unreachable: Rollup (internal/state/rollup.go) maps
  // AgentWaitingInput to "needs_input" ahead of every delivery state except
  // merged, and showAgentBadge rejects both needs_input and merged — so the gate
  // is false for every waiting_input session that could reach it. The pill
  // already says needs_input, which is the whole message. (The TUI carries the
  // same structurally-dead "?!" glyph in sessionview.go; it was not worth
  // porting the dead half.)
  const diverged = $derived(showAgentBadge(session.status, session.agentState, session.delivery));
  const note = $derived(diverged && session.agentState === "exited" ? "agent exited" : "");

  const text = $derived(session.headline || session.lastNotification || "");
</script>

{#if live || note || text}
  <div class="flex min-w-0 items-center gap-1.5 text-sm">
    <LivePulse agentState={session.agentState} />
    {#if note}
      <span class="shrink-0 whitespace-nowrap {session.agentState === 'waiting_input' ? 'text-orange' : 'text-faint'}"
        >{note}</span
      >
    {/if}
    {#if text}
      <!-- The interpreter's headline reads as a live-progress note, not a
           warning, so it carries the pulse dot's `info` blue. Orange is spoken
           for by "you are needed" (needsYou, the waiting_input note above). -->
      <span class="truncate {session.headline ? 'text-info' : 'text-faint'}"
        >{session.headline ? `≈ ${text}` : text}</span
      >
    {/if}
  </div>
{/if}
