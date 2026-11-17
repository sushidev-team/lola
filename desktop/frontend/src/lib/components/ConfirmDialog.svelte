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
  import Icon from "./Icon.svelte";

  const req = $derived(confirm.request);
  const descriptionId = $props.id();
  const danger = $derived((req?.confirmVariant ?? "danger-solid") === "danger-solid");
  const files = $derived(req?.subjectKind === "files" ? req.subject?.split("\n").filter(Boolean) ?? [] : []);
  // One sentence per row; the irreversible one is set apart so it is the last thing read.
  const notes = $derived((req?.detail?.match(/[^.!?]+[.!?]+/g) ?? (req?.detail ? [req.detail] : [])).map(text => text.trim()));
  const irreversible = (text: string) => /cannot be undone|permanent/i.test(text);
  const dir = (path: string) => (path.includes("/") ? path.slice(0, path.lastIndexOf("/") + 1) : "");
  const leaf = (path: string) => path.slice(path.lastIndexOf("/") + 1);
  // `data-confirm*` are styling hooks for the phone shell, which restyles this as an action sheet
  // (mobile/src/app.css); the desktop's markup and classes are unchanged.
</script>

{#if req}
  <Modal title={req.title} onClose={() => confirm.cancel()} width="480px" describedBy={descriptionId}>
    <div id={descriptionId} class="confirmation-copy" data-confirm>
      {#if files.length}
        <ul class="confirmation-files" aria-label="Affected files">
          {#each files as path (path)}
            <li class="confirmation-file" title={path}><Icon name="file-text" size={15} /><span class="confirmation-path"><span class="confirmation-dir">{dir(path)}</span><strong>{leaf(path)}</strong></span></li>
          {/each}
        </ul>
      {:else if req.subject}<p class="selectable confirmation-subject">{req.subject}</p>{/if}
      {#if req.body}<p class="copy confirmation-body">{req.body}</p>{/if}
      {#if notes.length}
        <ul class="confirmation-notes">
          {#each notes as note (note)}
            {#if irreversible(note)}<li class="confirmation-note warn"><Icon name="triangle-alert" size={15} /><span>{note}</span></li>
            {:else}<li class="confirmation-note"><Icon name="info" size={15} /><span>{note}</span></li>{/if}
          {/each}
        </ul>
      {/if}
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
        <Button class={req.alternative ? "cancel" : ""} size="md" data-confirm-cancel onclick={() => confirm.cancel()}>Cancel</Button>
        {#if req.alternative}
          <Button variant="secondary" size="md" onclick={() => confirm.acceptAlternative()}>{req.alternative.label}</Button>
        {/if}
        <Button variant={req.confirmVariant ?? "danger-solid"} size="md" data-confirm-ok onclick={() => confirm.accept()}>{req.confirmLabel}</Button>
      </div>
    {/snippet}
  </Modal>
{/if}

<style>
  .confirmation-actions { justify-content:flex-end; gap:8px; }
  .confirmation-copy { min-width:0; overflow-wrap:anywhere; display:grid; gap:12px; }
  .confirmation-subject { padding:9px 12px; border:1px solid var(--color-edge); border-radius:8px; background:color-mix(in srgb,var(--color-ink) 5%,var(--color-canvas)); font-family:var(--font-mono); font-size:13px; line-height:1.5; color:var(--color-ink); white-space:pre-wrap; }
  .confirmation-files { display:grid; margin:0; padding:0; list-style:none; max-height:208px; overflow:auto; border:1px solid var(--color-edge); border-radius:10px; background:color-mix(in srgb,var(--color-ink) 4%,var(--color-canvas)); }
  .confirmation-file { display:flex; align-items:center; gap:10px; padding:9px 12px; min-width:0; color:var(--color-ink-2); }
  .confirmation-file + .confirmation-file { border-top:1px solid var(--color-edge); }
  .confirmation-path { min-width:0; font-family:var(--font-mono); font-size:13px; line-height:1.4; overflow-wrap:anywhere; }
  .confirmation-dir { color:var(--color-faint); }
  .confirmation-path strong { font-weight:600; color:var(--color-ink); }
  .confirmation-body { font-size:14px; line-height:1.5; color:var(--color-ink); }
  .confirmation-notes { display:grid; gap:6px; margin:0; padding:0; list-style:none; }
  .confirmation-note { display:flex; align-items:flex-start; gap:9px; padding:7px 2px; font-size:13px; line-height:1.45; color:var(--color-ink-2); }
  .confirmation-note :global(svg) { margin-top:2px; color:var(--color-faint); }
  .confirmation-note.warn { padding:9px 12px; border-radius:8px; color:var(--color-bad); background:color-mix(in srgb,var(--color-bad) 11%,transparent); border:1px solid color-mix(in srgb,var(--color-bad) 28%,transparent); }
  .confirmation-note.warn :global(svg) { color:var(--color-bad); }
</style>
