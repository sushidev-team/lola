<script lang="ts">
  // Between the stream and the composer, where it can't scroll away. One card: the oldest
  // approval, then a go. Answering advances. Sign-in is on the stream's own card, not here.
  //
  // It NEVER takes focus by appearing, so a keystroke meant for the terminal or the
  // composer cannot land on it. Only ⌘⇧A, `A`, `t` or a click gives a card focus.
  import { ssh } from "$lib/ssh.svelte";
  import { dockCards } from "$lib/ssh";
  import SSHApproval from "./SSHApproval.svelte";
  import SSHGoCard from "./SSHGoCard.svelte";
  import SSHConnCard from "./SSHConnCard.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const view = $derived(ssh.view(sessionId));
  const active = $derived(view.active());
  const cards = $derived(dockCards(active ? [active] : [], view.tabItems, active?.id ?? ""));
  const card = $derived(cards[0]);

  let root = $state<HTMLDivElement | null>(null);
  $effect(() => {
    const n = view.dockFocus;
    if (n === view.seen.dock || !root) return;
    view.seen.dock = n;
    // The CARD takes focus, not this wrapper: its keys live on it, and a keydown on
    // the wrapper would never reach them. An answered card unmounts and focus goes
    // with it, so the next one needs its own explicit focus. A held key cannot walk
    // the queue.
    (root.querySelector<HTMLElement>("[data-card][tabindex]") ?? root).focus();
  });
</script>

{#if card}
  <!-- role="alert" announces a new decision. tabindex -1: focusable only on request. -->
  <div
    bind:this={root}
    role="alert"
    tabindex="-1"
    class="ssh-dock grid min-h-0 grid-rows-[minmax(0,1fr)] overflow-hidden border-t border-edge/60 p-2.5 focus-visible:outline-2"
  >
    {#key card.id}
      {#if card.kind === "approval"}
        <SSHApproval {sessionId} conn={card.conn} block={card.block} index={card.index} of={card.of} />
      {:else if card.kind === "go"}
        <SSHGoCard {sessionId} conn={card.conn} index={card.index} of={card.of} />
      {:else}
        <div class="min-h-0 overflow-y-auto">
          {#if card.kind === "reconnect"}
            <SSHConnCard {sessionId} conn={card.conn} kind="reconnect" running={card.running} />
          {:else}
            <SSHConnCard {sessionId} conn={card.conn} kind="failed" />
          {/if}
        </div>
      {/if}
    {/key}
  </div>
{/if}
