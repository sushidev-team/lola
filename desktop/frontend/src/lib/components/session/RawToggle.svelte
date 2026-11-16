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

<span class="inline-flex" use:tooltip={{ instant: true, heading: "Raw", text: on ? "Back to the conversation" : "Raw agent shell" }}>
  <Button icon size="sm" selected={on} aria-pressed={on} aria-label="Raw" class="w-9! border border-edge {on ? 'border-accent! text-ink!' : ''}" data-raw-toggle {onclick}>
    <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M3.5 5.5 6.5 8l-3 2.5" />
      <path d="M9 11h3.5" />
    </svg>
  </Button>
</span>
