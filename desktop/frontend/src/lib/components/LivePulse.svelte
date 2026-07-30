<script lang="ts">
  // "This agent is mid-turn." Extracted from <AgentActivity> so the compact
  // surfaces can carry the agent axis too: a grid tile header and a project's
  // session list have room for a 6px dot but not for a sentence, and without it
  // a tile reading `ci pending` gave no hint the agent was still typing — the
  // exact divergence the old `·wk` badge existed to show.
  //
  // aria-hidden with a visually-hidden word beside it: a bare animated dot
  // announces nothing. app.css stills the animation under prefers-reduced-motion,
  // and the dot itself stays, so the meaning survives without the movement.
  let { agentState = "" }: { agentState?: string } = $props();
  const live = $derived(agentState === "working" || agentState === "starting");
</script>

{#if live}
  <span class="relative flex h-1.5 w-1.5 shrink-0" aria-hidden="true">
    <span class="absolute inline-flex h-full w-full animate-ping rounded-full bg-info opacity-60"></span>
    <span class="relative inline-flex h-1.5 w-1.5 rounded-full bg-info"></span>
  </span>
  <span class="sr-only">agent running</span>
{/if}
