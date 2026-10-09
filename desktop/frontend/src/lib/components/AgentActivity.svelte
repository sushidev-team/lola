<script lang="ts">
  import LivePulse from "./LivePulse.svelte";
  import type { SessionInfo } from "$lib/store.svelte";

  // The secondary "what is this agent doing right now" line: a liveness dot plus
  // whatever prose the session has about itself.
  //
  // It used to carry a THIRD thing — an "agent exited" note gated on
  // theme.ts's showAgentBadge, which asked whether the agent axis DISAGREED with
  // an open PR's delivery state. That question only existed because the status
  // pill was the delivery state and the agent axis had nowhere to go. The pill
  // is the agent axis now, so the note is the pill restated, and the gate it
  // depended on has been deleted along with the badge it named.
  //
  // The pulse stays, on both surfaces that render this component. On the
  // desktop it is motion beside a pill that only has words; on the phone (which
  // renders this component verbatim through mobile's $lib alias) it is the ONLY
  // agent-axis signal on the row — that list still prints the rolled-up status
  // word, which post-PR is the delivery word and says nothing about the runner.
  //
  // The text is UNTRUSTED, display-only ([statusagent] / the agent's own
  // notification): it is "≈"-marked exactly like the interpreted pill and never
  // styled as fact. Nothing here feeds the control loop.
  // `pulse={false}` where the row already animates the agent axis itself (the
  // desktop list's AgentGlyph): two moving marks a few pixels apart is noise.
  let { session, pulse = true }: { session: SessionInfo; pulse?: boolean } = $props();

  const live = $derived(pulse && (session.agentState === "working" || session.agentState === "starting"));

  // The interpreter's headline, or — failing that — the agent's own last
  // notification. The daemon clears lastNotification on any transition off
  // waiting_input, so this is never a stale sentence about a turn that ended.
  const text = $derived(session.headline || session.lastNotification || "");

  // The agent's OWN report (`lola report`) fills the line when nothing above
  // has a sentence: a reported blocker first — it is the one part a human must
  // act on, so it also outranks the interpreter's guess — else the plan item it
  // says it is on. Also the agent's claim, so it is never styled as fact: the
  // blocker carries "⏸", and the plan item rides bare beside the pulse dot.
  const blocked = $derived(session.board?.blocked || "");
  const current = $derived(session.board?.current || "");
</script>

{#if live || text || blocked || current}
  <div class="flex min-w-0 items-center gap-1.5 text-sm">
    {#if pulse}<LivePulse agentState={session.agentState} />{/if}
    {#if blocked}
      <span class="truncate text-orange" title="the agent reports it is blocked">⏸ {blocked}</span>
    {:else if text}
      <!-- The interpreter's headline reads as a live-progress note, not a
           warning, so it carries the pulse dot's `info` blue. Orange is spoken
           for by "you are needed" — the pill's own colour. -->
      <span class="truncate {session.headline ? 'text-info' : 'text-faint'}"
        >{session.headline ? `≈ ${text}` : text}</span
      >
    {:else if current}
      <span class="truncate text-faint" title="the plan item the agent reports it is on">{current}</span>
    {/if}
  </div>
{/if}
