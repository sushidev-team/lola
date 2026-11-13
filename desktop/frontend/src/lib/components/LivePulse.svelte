<script lang="ts">
  // "This agent is mid-turn." It was extracted from <AgentActivity> because the
  // compact surfaces had no other way to carry the agent axis at all: a tile
  // reading `ci pending` gave no hint the agent was still typing, which is the
  // exact divergence the old `·wk` badge existed to show.
  //
  // The status pill IS the agent axis now, so on the desktop this is no longer
  // the only signal — it is the MOTION channel beside a static word, which is
  // what a glance across a full grid actually reads. It stays for two reasons
  // beyond that: on the phone (which renders <AgentActivity> verbatim through
  // mobile's $lib alias) the row still prints the rolled-up status word, so the
  // dot remains the only thing on it that says the runner is alive; and it is
  // the bullet that anchors the activity line's text.
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
