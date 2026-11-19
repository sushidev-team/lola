<script lang="ts">
  // The repositories a stack session gets, as a list to tick (appchooser.svelte.ts).
  // Mounted once in App.svelte. The ones already included are shown ticked and
  // fixed, so the list reads as the whole session rather than as a delta.
  import Button from "$lib/components/Button.svelte";
  import Checkbox from "$lib/components/Checkbox.svelte";
  import Modal from "$lib/components/Modal.svelte";
  import { appChooser } from "$lib/appchooser.svelte";

  const req = $derived(appChooser.request);
  let ticked = $state<Record<string, boolean>>({});
  $effect(() => {
    void req;
    ticked = {};
  });
  const picked = $derived((req?.choices ?? []).filter((c) => !c.included && ticked[c.name]).map((c) => c.name));
  const anyIncluded = $derived((req?.choices ?? []).some((c) => c.included));
  const blocked = $derived(!!req && picked.length === 0 && (req.requireOne || !anyIncluded));
</script>

{#if req}
  <Modal title={req.title} onClose={() => appChooser.finish(null)} width="480px">
    <div class="grid gap-3">
      <p class="text-ink">{req.intro}</p>
      <ul class="grid gap-1" aria-label="Repositories">
        {#each req.choices as c (c.name)}
          <li>
            <label class="flex items-center gap-2 rounded px-1 py-1 {c.included ? '' : 'cursor-pointer hover:bg-sel/50'}">
              <Checkbox
                checked={c.included || !!ticked[c.name]}
                disabled={c.included}
                onchange={(e) => (ticked[c.name] = (e.currentTarget as HTMLInputElement).checked)}
              />
              <span class="min-w-0 truncate text-ink">{c.label}</span>
              {#if c.note}<span class="ml-auto shrink-0 text-sm text-faint">{c.note}</span>{/if}
            </label>
          </li>
        {/each}
      </ul>
    </div>
    {#snippet footer()}
      <div class="dialog-actions">
        <Button class="cancel" size="md" onclick={() => appChooser.finish(null)}>Cancel</Button>
        <Button variant="primary" size="md" disabled={blocked} onclick={() => appChooser.finish(picked)}>{req.confirm}</Button>
      </div>
    {/snippet}
  </Modal>
{/if}
