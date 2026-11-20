<script lang="ts">
  import { nav } from "$lib/nav.svelte";
  import { store } from "$lib/store.svelte";
  import Modal from "./Modal.svelte";
  import Button from "./Button.svelte";

  let { project }: { project: string } = $props();
  let message = $state("");
  let sending = $state(false);
  let result = $state("");

  async function send() {
    const text = message.trim();
    if (!text || sending) return;
    sending = true;
    try {
      const response = await store.broadcast(project, text);
      if (response) {
        result = response.message || "";
        message = "";
      }
    } finally {
      sending = false;
    }
  }
</script>

<Modal title={`Broadcast to ${store.displayNameFor(project)}`} onClose={() => nav.closeOverlay()}>
  <form class="grid gap-3" onsubmit={(e) => { e.preventDefault(); void send(); }}>
    <label class="grid gap-2 text-sm">Message every live agent<textarea class="min-h-20 rounded border border-edge bg-panel p-3" bind:value={message} maxlength="4000" aria-label="Broadcast message"></textarea></label>
    <div class="dialog-actions"><Button type="submit" loading={sending} disabled={!message.trim()}>Send to agents</Button><Button onclick={() => nav.closeOverlay()}>Cancel</Button></div>
    {#if result}<p role="status" class="text-sm text-faint">{result}</p>{/if}
  </form>
</Modal>
