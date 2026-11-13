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
  import Checkbox from "./Checkbox.svelte";

  const req = $derived(confirm.request);
  const descriptionId = $props.id();
</script>

{#if req}
  <Modal title={req.title} onClose={() => confirm.cancel()} width="480px" describedBy={descriptionId}>
    <div id={descriptionId} class="confirmation-copy">
      {#if req.subject}<p class="selectable confirmation-subject">{req.subject}</p>{/if}
      <p class="copy confirmation-body">{req.body}</p>
      {#if req.detail}<p class="copy confirmation-detail">{req.detail}</p>{/if}
    </div>
    {#if req.choices?.items.length}
      <ul class="mt-3 max-h-56 overflow-auto rounded border border-edge" aria-label={req.choices.label}>
        {#each req.choices.items as item (item.id)}
          <li class="border-b border-edge last:border-b-0">
            <label class="flex cursor-pointer items-center gap-2 px-2 py-1.5 text-ink">
              <Checkbox bind:checked={item.checked} aria-label={`${req.choices.label}: ${item.label}`} />
              <span class="min-w-0 truncate">{item.label}</span>
            </label>
          </li>
        {/each}
      </ul>
    {/if}
    {#snippet footer()}
      <div class="dialog-actions confirmation-actions">
        <Button class={req.alternative ? "cancel" : ""} size="md" onclick={() => confirm.cancel()}>Cancel</Button>
        {#if req.alternative}
          <Button variant="secondary" size="md" onclick={() => confirm.acceptAlternative()}>{req.alternative.label}</Button>
        {/if}
        <Button variant={req.confirmVariant ?? "danger-solid"} size="md" onclick={() => confirm.accept()}>{req.confirmLabel}</Button>
      </div>
    {/snippet}
  </Modal>
{/if}

<style>
  .confirmation-actions { justify-content:flex-end; }
  .confirmation-copy { min-width:0; overflow-wrap:anywhere; }
  .confirmation-subject { margin-bottom:12px; font-family:var(--font-mono); font-size:13px; line-height:1.5; color:var(--color-ink-2); white-space:pre-wrap; }
  .confirmation-body { font-size:14px; color:var(--color-ink); }
  .confirmation-detail { margin-top:10px; font-size:13px; color:var(--color-ink-2); }
</style>
