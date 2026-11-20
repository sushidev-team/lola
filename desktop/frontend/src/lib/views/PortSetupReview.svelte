<script lang="ts">
  import { onMount } from "svelte";
  import { ProjectGitService } from "@bindings/desktop";
  import type { PortSetupChange } from "@bindings/desktop/models";
  import Modal from "$lib/components/Modal.svelte";
  import Button from "$lib/components/Button.svelte";
  import Chevron from "$lib/components/Chevron.svelte";
  import Checkbox from "$lib/components/Checkbox.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import { overlayClose } from "$lib/overlayClose";
  import { store } from "$lib/store.svelte";

  let { project, label, path, range, onClose }: {
    project: string; label: string; path: string; range: string; onClose: () => void;
  } = $props();
  let loading = $state(true);
  let applying = $state(false);
  let error = $state("");
  let note = $state("");
  let token = "";
  let rows = $state<(PortSetupChange & { selected: boolean })[]>([]);
  let selected = $derived(rows.filter((r) => r.selected));
  let invalid = $derived(selected.some((r) => !r.after.trim() || (r.kind === "ports" && !/^(?:[1-9]|1\d|20)$/.test(r.after))));
  let alive = true;

  function close() { if (!applying) onClose(); }
  async function generate() {
    loading = true;
    error = "";
    try {
      const plan = await ProjectGitService.SuggestPortSetup(project);
      if (!alive) return;
      token = plan.token;
      note = plan.note;
      rows = (plan.changes ?? []).map((r) => ({ ...r, selected: true }));
    } catch (e) {
      error = String(e);
    } finally { loading = false; }
  }
  onMount(() => {
    overlayClose.register(close);
    void generate();
    return () => { alive = false; overlayClose.unregister(close); };
  });

  async function apply() {
    if (applying || !selected.length || invalid) return;
    applying = true;
    error = "";
    try {
      await ProjectGitService.ApplyPortSetup(project, token, selected.map(({ selected: _, ...row }) => row));
      store.setFlash(`Applied port setup for ${label}`, "good");
      onClose();
    } catch (e) {
      error = String(e);
    } finally { applying = false; }
  }

  function title(row: PortSetupChange) {
    if (row.kind === "ports") return "Ports per session";
    if (row.kind === "env") return row.target;
    if (row.kind === "command") return row.line ? `Dev command ${row.line}` : "New dev command";
    return `${row.target}:${row.line}`;
  }
</script>

<Modal title="Review port setup" width="660px" onClose={close}>
  <div class="grid min-w-0 gap-4">
    <div class="flex min-w-0 items-center gap-2 text-sm">
      <Icon name="folder" size={14} class="text-faint" />
      <span class="shrink-0 font-medium text-ink">{label}</span>
      <span class="truncate font-mono text-xs text-faint" title={path}>{path}</span>
    </div>
    {#if loading}
      <p role="status" class="flex items-center gap-2 py-3 text-sm text-ink-2">
        <Icon name="loader-circle" size={16} class="animate-spin text-accent-ink" />
        Checking dev servers…
      </p>
    {:else}
      {#if !rows.length && !error}
        <p class="text-sm text-ink">{note || "No port changes suggested."}</p>
      {/if}
      {#each ["settings", "files"] as group}
        {@const items = rows.filter((r) => (r.kind === "file") === (group === "files"))}
        {#if items.length}
          <section aria-label={group === "files" ? "Repository edits" : "Lola settings"}>
            <h3 class="mb-1 font-semibold text-ink">{group === "files" ? "Repository edits" : "Lola settings"}</h3>
            <div class="divide-y divide-edge/70 border-y border-edge/70">
              {#each items as row}
                {@const wide = row.kind === "file" || row.kind === "command"}
                <div class="change-row" class:wide>
                  <label class="flex min-w-0 items-baseline gap-2 text-sm text-ink">
                    <Checkbox class="self-center" bind:checked={row.selected} disabled={applying} aria-label={`Include ${title(row)}`} />
                    <span class="break-all" class:font-mono={row.kind === "file" || row.kind === "env"}>{title(row)}</span>
                  </label>
                  <div class="change-value">
                    {#if row.before}
                      <div class="mb-1 break-all text-xs text-faint">Current <code class="whitespace-pre-wrap">{row.before}</code></div>
                    {/if}
                    {#if row.kind === "ports"}
                      <div class="flex flex-wrap items-center gap-2 text-xs text-faint">
                        <input class="port-input w-16!" type="number" min="1" max="20" step="1" aria-label="Proposed ports per session"
                          value={row.after} oninput={(e) => row.after = e.currentTarget.value} disabled={!row.selected || applying} />
                        <span>{range && range !== "auto" ? range : "Automatic"}</span>
                      </div>
                    {:else}
                      <textarea class="port-input block resize-y font-mono" aria-label={`Proposed ${title(row)}`} rows={row.after.length > (wide ? 70 : 38) ? 2 : 1}
                        bind:value={row.after} disabled={!row.selected || applying} spellcheck="false"></textarea>
                    {/if}
                  </div>
                </div>
              {/each}
            </div>
            {#if group === "files"}
              <p class="mt-2 text-xs text-faint">Commit these edits to your base branch for new worktrees.</p>
            {/if}
          </section>
        {/if}
      {/each}
      {#if rows.length && (note || rows.some((row) => row.reason))}
        <details class="text-sm text-faint">
          <summary class="cursor-pointer"><Chevron class="disclosure-chevron" />Why these changes?</summary>
          <div class="mt-2 grid gap-2 pl-5">
            {#if note}<p>{note}</p>{/if}
            {#each rows.filter((row) => row.reason) as row}
              <p><strong class="font-medium text-ink-2">{title(row)}</strong> · {row.reason}</p>
            {/each}
          </div>
        </details>
      {/if}
      {#if error}
        <div role="alert" class="grid justify-items-start gap-2 text-sm text-bad">
          <p class="break-words">{error}</p>
          <Button variant="secondary" onclick={generate}>Generate again</Button>
        </div>
      {/if}
    {/if}
  </div>
  {#snippet footer()}
    <div class="flex items-center justify-end gap-2">
      <Button size="md" disabled={applying} onclick={close}>{rows.length || loading ? "Cancel" : "Close"}</Button>
      {#if rows.length && !loading}
        <Button variant="primary" size="md" disabled={!selected.length || invalid} loading={applying} onclick={apply}>Apply {selected.length} {selected.length === 1 ? "change" : "changes"}</Button>
      {/if}
    </div>
  {/snippet}
</Modal>

<style>
  .change-row { display:grid; grid-template-columns:minmax(0,2fr) minmax(0,3fr); align-items:center; gap:6px 16px; padding:10px 0; }
  .change-value { min-width:0; }
  .change-row.wide { grid-template-columns:minmax(0,1fr); }
  .wide .change-value { margin-left:22px; }
  @media (max-width:480px) {
    .change-row { grid-template-columns:minmax(0,1fr); }
    .change-value { margin-left:22px; }
  }
  .port-input { width:100%; min-width:0; padding:6px 8px; border:1px solid var(--color-edge); border-radius:6px; background:var(--color-canvas); color:var(--color-ink); font-size:13px; }
  .port-input:disabled { color:var(--color-faint); opacity:.7; }
</style>
