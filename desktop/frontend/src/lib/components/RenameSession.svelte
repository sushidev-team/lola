<script lang="ts">
  import { onMount, tick } from "svelte";
  import { overlayClose } from "$lib/overlayClose";
  import { nav } from "$lib/nav.svelte";
  import { store } from "$lib/store.svelte";
  import Modal from "./Modal.svelte";
  import Button from "./Button.svelte";
  const id = nav.overlayProject;
  const formId = $props.id();
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
<Modal title="Rename session" onClose={() => { if (!busy) nav.closeOverlay(); }} width="480px">
  <form id={formId} class="grid gap-3" onsubmit={(e) => { e.preventDefault(); void save(); }}>
    <label class="grid gap-1.5">Name<input bind:this={nameInput} class="lola-field" aria-describedby={`${formId}-hint`} bind:value={name} maxlength="100" disabled={busy} /></label>
    <p id={`${formId}-hint`} class="copy text-sm text-ink-2">A display name only. The branch and directory stay the same.</p>
    {#if error}<p role="alert" class="text-bad">{error}</p>{/if}
  </form>
  {#snippet footer()}
    <div class="dialog-actions">
      <Button class="cancel" size="md" onclick={close} disabled={busy}>Cancel</Button>
      <Button type="submit" form={formId} variant="primary" size="md" loading={busy}>Save name</Button>
    </div>
  {/snippet}
</Modal>
