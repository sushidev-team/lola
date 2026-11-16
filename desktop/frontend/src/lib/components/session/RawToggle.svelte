<script lang="ts">
  // The conversation's raw form is the agent's own terminal. One toggle for both: beside the
  // activity bar on the conversation (off), and over the agent's terminal in the same corner (on).
  // Only the agent's terminal carries it; a shell, the review pane or Changes are not the raw
  // form of anything. Icon only, so the tooltip and the label carry the name. The prompt glyph
  // is drawn here rather than taken from the icon set, whose `>_` has no air between its parts.
  import { tooltip } from "$lib/tooltip";
  import Button from "../Button.svelte";

  let { on = false, onclick }: { on?: boolean; onclick: () => void } = $props();
</script>

<span class="inline-flex" use:tooltip={{ instant: true, text: on ? "Back to conversation view" : "Raw agent shell" }}>
  <Button icon size="sm" selected={on} aria-pressed={on} aria-label="Raw" class="w-10! border border-edge {on ? 'border-accent! text-ink!' : ''}" data-raw-toggle {onclick}>
    <svg width="20" height="20" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M4 6.5 8 10l-4 3.5" />
      <path d="M10.5 14h5" />
    </svg>
  </Button>
</span>
