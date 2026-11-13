<script lang="ts">
  // Keep this mounted outside the tray's prompt-hash key: resizing and navigating a live
  // dialog changes that hash. Answering the dialog or leaving the view detaches it.
  import Button from "../Button.svelte";
  import TerminalCell from "./TerminalCell.svelte";
  import type { Snippet } from "svelte";

  let { name = "", agent, focusRequest = 0, onLeave, onExpand, onClose, pager }: {
    name?: string;
    agent: string;
    focusRequest?: number;
    onLeave: () => void;
    onExpand: () => void;
    onClose?: () => void;
    pager?: Snippet;
  } = $props();
</script>

<!-- The terminal owns Escape and every other key; Ctrl-Q is its explicit way back. -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<section
  aria-label="{agent} terminal controls"
  data-inline-terminal
  hidden={!name}
  class="h-full min-h-0 max-h-full grid-rows-[auto_minmax(0,1fr)] overflow-hidden rounded-panel border border-accent/50 bg-panel {name ? 'grid' : 'hidden'}"
  onkeydown={(event) => event.stopPropagation()}
>
  <div class="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-edge px-3 py-1">
    <h3 class="mr-auto font-medium text-ink">{agent} terminal</h3>
    {#if name && pager}{@render pager()}{/if}
    <span class="text-sm text-ink-2">Ctrl-Q to conversation</span>
    <Button size="xs" onclick={onExpand}>Expand terminal</Button>
    {#if onClose}<Button size="xs" onclick={onClose}>Close controls</Button>{/if}
  </div>
  <TerminalCell {name} autofocus={false} {focusRequest} {onLeave} style="height: 100%; max-height: 100%; min-height: 0; padding: 8px 10px;" />
</section>
