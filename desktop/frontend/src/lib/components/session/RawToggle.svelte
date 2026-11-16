<script lang="ts">
  // The conversation's raw form is the agent's own terminal. One toggle for both: beside the
  // activity bar on the conversation (off), and over the agent's terminal in the same corner (on).
  // Only the agent's terminal carries it; a shell, the review pane or Changes are not the raw
  // form of anything. Icon only, so the tooltip and the label carry the name.
  import Icon from "$lib/components/Icon.svelte";
  import { tooltip } from "$lib/tooltip";
  import Button from "../Button.svelte";

  let { on = false, onclick }: { on?: boolean; onclick: () => void } = $props();
</script>

<span class="inline-flex" use:tooltip={{ instant: true, heading: "Raw", text: on ? "Back to the conversation" : "Raw agent shell" }}>
  <Button icon size="xs" selected={on} aria-pressed={on} aria-label="Raw" class="border border-edge {on ? 'border-accent! text-ink!' : ''}" data-raw-toggle {onclick}><Icon name="terminal" size={14} /></Button>
</span>
