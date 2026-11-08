<script lang="ts">
  // Renders whatever destructive action is currently pending confirmation. Shown
  // whenever confirm.request is set — killing a session, stopping the daemon,
  // anything else that can't be undone. Escape/Enter are handled in App.svelte's
  // key handler; this component owns the buttons and copy.
  //
  // The confirming button is NOT autofocused: Modal focuses the first focusable
  // (the header ✕), so a stray Enter dismisses rather than destroys.
  import { confirm } from "$lib/confirm.svelte";
  import Modal from "./Modal.svelte";
  import Button from "./Button.svelte";

  const req = $derived(confirm.request);
  const descriptionId = $props.id();
</script>

{#if req}
  <Modal title={req.title} onClose={() => confirm.cancel()} width="480px" describedBy={descriptionId}>
    <div id={descriptionId} class="confirmation-copy">
      <p class="copy confirmation-body">{req.body}</p>
      {#if req.detail}<p class="copy confirmation-detail">{req.detail}</p>{/if}
    </div>
    {#snippet footer()}
      <div class="dialog-actions">
        <Button class="cancel" size="md" onclick={() => confirm.cancel()}>Cancel</Button>
        {#if req.alternative}
          <Button variant="secondary" size="md" onclick={() => confirm.acceptAlternative()}>{req.alternative.label}</Button>
        {/if}
        <Button variant="danger-solid" size="md" onclick={() => confirm.accept()}>{req.confirmLabel}</Button>
      </div>
    {/snippet}
  </Modal>
{/if}

<style>
  .confirmation-copy { min-width:0; overflow-wrap:anywhere; }
  .confirmation-body { font-size:14px; color:var(--color-ink); }
  .confirmation-detail { margin-top:10px; font-size:13px; color:var(--color-ink-2); }
</style>
