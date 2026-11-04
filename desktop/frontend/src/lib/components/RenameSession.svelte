<script lang="ts">
  import { onMount, tick } from "svelte";
  import { overlayClose } from "$lib/overlayClose";
  import { nav } from "$lib/nav.svelte";
  import { store } from "$lib/store.svelte";
  import Modal from "./Modal.svelte";
  import Button from "./Button.svelte";
  const id = nav.overlayProject;
  let name = $state(store.sessionById(id)?.title || "");
  let nameInput: HTMLInputElement;
  let busy = $state(false);
  let error = $state("");
  function close() { if (!busy) nav.closeOverlay(); }
  onMount(() => { overlayClose.register(close); void tick().then(() => { nameInput?.focus(); nameInput?.select(); }); return () => overlayClose.unregister(close); });
  async function save() {
    if (busy) return;
    busy = true;
    const ok = await store.renameSession(id, name.trim());
    busy = false;
    if (ok) nav.closeOverlay(); else error = store.flash?.text || "Could not rename session";
  }
</script>
<Modal title="Rename session" onClose={() => { if (!busy) nav.closeOverlay(); }} width="420px">
  <form class="grid gap-3" onsubmit={(e) => { e.preventDefault(); void save(); }}>
    <label class="grid gap-1.5">Name<input bind:this={nameInput} class="rounded border border-edge bg-canvas px-3 py-2" bind:value={name} maxlength="100" disabled={busy} /></label>
    <p class="text-sm text-faint">A display name only. The branch and directory stay the same.</p>
    {#if error}<p role="alert" class="text-bad">{error}</p>{/if}
    <Button type="submit" variant="primary" loading={busy}>Save name</Button>
  </form>
</Modal>
