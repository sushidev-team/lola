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
</script>

{#if req}
  <Modal title={req.title} onClose={() => confirm.cancel()} width="420px">
    <!-- `copy` is the only sanctioned line-height deviation: multi-line prose
         gets 1.55 and a 62ch measure. -->
    <p class="copy text-ink">{req.body}</p>
    {#if req.detail}
      <p class="mt-2 text-sm text-faint">{req.detail}</p>
    {/if}
    {#snippet footer()}
      <div class="flex justify-end gap-2">
        <Button variant="secondary" size="md" onclick={() => confirm.cancel()}>Cancel</Button>
        <Button variant="danger-solid" size="md" onclick={() => confirm.accept()}>{req.confirmLabel}</Button>
      </div>
    {/snippet}
  </Modal>
{/if}
