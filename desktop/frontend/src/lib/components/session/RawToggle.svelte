<script lang="ts">
  // The conversation's raw form is the agent's own terminal. A switch, because it is the same
  // session shown two ways: on is the terminal, off is the conversation. The terminal view has
  // no copy of it; the strip's Conversation tab is the way back.
  import Button from "../Button.svelte";

  let { on = false, onclick }: { on?: boolean; onclick: () => void } = $props();
</script>

<Button size="xs" role="switch" aria-checked={on} aria-pressed={undefined} title="Show the raw terminal instead of the conversation" data-raw-toggle {onclick}>
  <span class="relative h-3.5 w-6 shrink-0 rounded-full border border-edge transition-colors {on ? 'bg-accent' : 'bg-canvas'}" aria-hidden="true">
    <span class="absolute top-px size-2.5 rounded-full bg-ink transition-[left] {on ? 'left-3' : 'left-px'}"></span>
  </span>
  Raw
</Button>
