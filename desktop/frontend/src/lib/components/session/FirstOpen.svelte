<script lang="ts">
  // One line, once, across the whole app, the first time anyone sees a conversation: the view
  // changed and the agent did not, and the old way is one chord away. No tour. It sits in the action
  // zone, where the eye already is, and not up against the strip's rule.
  //
  // A session that opens on its terminal keeps this waiting behind it, so `covered` holds the
  // claim until the line can actually be read.
  import { untrack } from "svelte";
  import { terms, AGENT } from "$lib/terms.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { store } from "$lib/store.svelte";
  import Button from "../Button.svelte";

  let { sessionId, covered = false }: { sessionId: string; covered?: boolean } = $props();

  let shown = $state(false);
  $effect(() => {
    if (covered) return;
    const sid = sessionId;
    untrack(() => {
      if (terms.claimIntro(sid)) shown = true;
    });
  });

  function terminal() {
    const s = store.sessionById(sessionId);
    if (s) ssh.toggleTerminalView(s);
  }
  function always() {
    terms.setGlobalView("terminal");
    terms.dismissIntro();
    shown = false;
    terms.select(sessionId, AGENT);
    terms.focusTerminal(sessionId);
  }
</script>

{#if shown}
  <p class="flex h-6 min-w-0 items-center gap-3 text-sm text-faint" data-first-open>
    <span class="min-w-0 truncate">Your agent still runs in its terminal, unchanged.</span>
    <Button size="xs" class="h-5! shrink-0" onclick={terminal}>Terminal <span class="text-faint">⌘\</span></Button>
    <Button size="xs" class="h-5! shrink-0" onclick={always}>Always open there</Button>
  </p>
{/if}
