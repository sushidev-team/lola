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

  const req = $derived(confirm.request);
</script>

{#if req}
  <Modal title={req.title} onClose={() => confirm.cancel()} width="420px">
    <p class="text-sm leading-relaxed text-ink">{req.body}</p>
    {#if req.detail}
      <p class="mt-2 text-xs text-faint">{req.detail}</p>
    {/if}
    {#snippet footer()}
      <div class="flex justify-end gap-2">
        <button
          class="rounded border border-edge px-3 py-1 text-xs text-faint hover:text-ink"
          onclick={() => confirm.cancel()}>Cancel</button
        >
        <button
          class="rounded bg-bad px-3 py-1 text-xs font-medium text-on-bad hover:opacity-90"
          onclick={() => confirm.accept()}>{req.confirmLabel}</button
        >
      </div>
    {/snippet}
  </Modal>
{/if}
