<script lang="ts">
  // Pickup labels and match mode for the selected projects, in one atomic save.
  // Blank fields are left alone. Preview shows what each project would change
  // without writing; the daemon refuses the whole change if any project cannot
  // resolve a label, so a half-configured workspace is never left behind.
  import { bulkPickup } from "$lib/bulkpickup.svelte";
  import { store } from "$lib/store.svelte";
  import { displayName } from "$lib/slug";
  import Modal from "./Modal.svelte";
  import Button from "./Button.svelte";
  import Checkbox from "./Checkbox.svelte";
  import Select from "./Select.svelte";

  const names = $derived(bulkPickup.projects ?? []);
  const label = (n: string) => {
    const p = store.projectByName(n);
    return p ? displayName(p) : n;
  };
  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder";
  const id = $props.id();
</script>

{#if bulkPickup.projects}
  <Modal title="Pickup labels for {names.length} project{names.length === 1 ? '' : 's'}" onClose={() => bulkPickup.close()} width="560px">
    <div class="grid gap-3">
      <p class="text-sm text-faint">
        Leave a field blank to keep each project's current value. A name ("agent/agent-ready", or "*/agent-ready") is looked up in each
        project's own team.
      </p>
      <label class="grid gap-1" for="{id}-match">
        <span class="text-faint">Match labels (one per line)</span>
        <textarea id="{id}-match" class="{inputCls} resize-y font-mono" rows="3" spellcheck="false"
          placeholder={"*/agent-ready"} bind:value={bulkPickup.draft.matchLabels}></textarea>
      </label>
      <label class="grid gap-1" for="{id}-mode">
        <span class="text-faint">Match mode</span>
        <Select id="{id}-mode" bind:value={bulkPickup.draft.matchMode}>
          <option value="">(keep)</option>
          <option value="any">any label</option>
          <option value="all">every label</option>
        </Select>
      </label>
      <label class="grid gap-1" for="{id}-sent">
        <span class="text-faint">On-sent label</span>
        <input id="{id}-sent" class="{inputCls} font-mono" placeholder="agent/agent-picked-up" bind:value={bulkPickup.draft.onSent} />
      </label>
      <label class="grid gap-1" for="{id}-blocked">
        <span class="text-faint">Blocked label</span>
        <input id="{id}-blocked" class="{inputCls} font-mono" placeholder="agent/blocked" bind:value={bulkPickup.draft.blocked} />
      </label>
      <label class="flex cursor-pointer items-start gap-2 text-ink">
        <Checkbox bind:checked={bulkPickup.draft.keepNames} aria-label="Store names instead of ids" />
        <span>
          Store names instead of ids
          <span class="block text-sm text-faint">
            Keeps config.toml readable and lets each team resolve the name itself. Names are still checked against every project's team now.
          </span>
        </span>
      </label>

      {#if bulkPickup.error}
        <p role="alert" class="rounded border border-edge bg-canvas px-3 py-2 text-sm text-bad">{bulkPickup.error}</p>
      {/if}
      {#if bulkPickup.changes}
        <ul class="max-h-48 overflow-auto rounded border border-edge" aria-label={bulkPickup.applied ? "Saved changes" : "Preview"}>
          {#each bulkPickup.changes as c (c.project)}
            <li class="flex gap-2 border-b border-edge px-2 py-1.5 text-sm last:border-b-0">
              <span class="min-w-0 truncate text-ink">{label(c.project)}</span>
              <span class="ml-auto shrink-0 {c.error ? 'text-bad' : 'text-faint'}">
                {c.error || (c.changed?.length ? c.changed.join(", ") : "no change")}
              </span>
            </li>
          {/each}
        </ul>
        {#if bulkPickup.applied}<p class="text-sm text-good">Saved.</p>{/if}
      {/if}
    </div>
    {#snippet footer()}
      <div class="dialog-actions" style="justify-content:flex-end">
        <Button size="md" onclick={() => bulkPickup.close()}>{bulkPickup.applied ? "Close" : "Cancel"}</Button>
        <Button size="md" disabled={bulkPickup.busy || bulkPickup.applied} onclick={() => void bulkPickup.run(true)}>Preview</Button>
        <Button variant="primary" size="md" disabled={bulkPickup.busy || bulkPickup.applied} onclick={() => void bulkPickup.run(false)}>Apply</Button>
      </div>
    {/snippet}
  </Modal>
{/if}
