<script lang="ts">
  import { untrack } from "svelte";
  import { feed } from "$lib/feed.svelte";
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { changes } from "$lib/changes.svelte";
  import { changesOffered } from "$lib/sessionview";
  import { liveVerb, pastVerb, type RunRowData } from "$lib/turns";
  import Button from "../Button.svelte";
  import Chevron from "../Chevron.svelte";
  import ToolPreview from "./ToolPreview.svelte";

  let { sessionId, row, active = false, readonly = false }: {
    sessionId: string; row: RunRowData; active?: boolean; readonly?: boolean;
  } = $props();
  const f = $derived(feed.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const paths = $derived([...new Set(row.items.map(item => item.target))]);
  let selected = $state("");
  const path = $derived(paths.includes(selected) ? selected : row.items.at(-1)?.target || "");
  const items = $derived(row.items.filter(item => item.target === path));
  const running = $derived(row.items.some(item => item.status === "running"));
  const open = $derived(!readonly && f.expanded.has(row.id));
  const shownItems = $derived(open ? items : row.items.slice(-1));
  const id = $props.id();

  function toggle() {
    if (open) f.expanded.delete(row.id);
    else f.expanded.add(row.id);
  }
  $effect(() => {
    const fetchItems = open ? items : active && !running ? row.items.slice(-1) : [];
    untrack(() => { for (const item of fetchItems) if (item.tool.hasOutput) void f.output(item.id); });
  });
</script>

<div class="cv-change-run cv-tool-card" data-run="edit" data-change-run>
  <button type="button" class="cv-change-head" disabled={readonly} aria-expanded={open} aria-controls={id} onclick={toggle}>
    <Chevron {open} />
    <span>{running ? 'Changing' : 'Changes'} · {paths.length} {paths.length === 1 ? 'file' : 'files'}</span>
    <span class="cv-change-path font-mono" title={paths.join('\n')}>{path}</span>
    {#if row.failed}<span class="text-bad">{row.failed} failed</span>{/if}
    {#if row.added || row.removed}<span class="num whitespace-nowrap text-faint" title="Lines added and removed across these edits">+{row.added} −{row.removed}</span>{/if}
  </button>
  {#if open || active}
    <div {id} class="cv-tool-content" class:cv-change-body={open}>
      {#if open}<div class="cv-change-files" aria-label="Changed files">
        {#each paths as file (file)}
          {@const edits = row.items.filter(item => item.target === file)}
          {@const last = edits.at(-1)!}
          <button type="button" aria-pressed={path === file} class="cv-change-file" onclick={() => selected = file} title={file}>
            <span class="cv-change-path font-mono">{file}</span>
            <span class:text-bad={edits.some(item => item.status === 'error')} class="text-faint">{last.status === 'running' ? liveVerb(last.verb) : last.status === 'error' ? 'Failed' : last.status === 'ok' ? pastVerb(last.verb) : last.status}</span>
          </button>
        {/each}
      </div>{/if}
      <div class:cv-change-preview={open} data-change-preview={open ? "" : undefined}>
        {#if open && session && changesOffered(session)}
          <Button size="xs" onclick={() => { changes.want(sessionId, path); if (session) ssh.openChanges(session); }}>View changes</Button>
        {/if}
        {#each shownItems as item (item.id)}
          {@const output = f.outputs.get(item.id)}
          {#if open && items.length > 1}<p class="text-sm text-faint">Edit {items.indexOf(item) + 1} of {items.length}</p>{/if}
          {#if output?.text || item.tool.preview}
            <ToolPreview text={output?.text || item.tool.preview || ""} sourcePath={item.target} running={item.status === "running"} expanded={open} />
          {:else if open && output?.loading}
            <p class="text-sm text-faint" role="status">Loading changes…</p>
          {:else if open}
            <p class="text-sm text-faint">{output?.failed ? 'Could not load this diff.' : item.status === 'running' ? 'Waiting for changes…' : 'No diff recorded for this action.'}</p>
          {/if}
          {#if open && output?.truncated}<p class="text-sm text-faint">Showing the recorded excerpt.</p>{/if}
        {/each}
      </div>
    </div>
  {/if}
</div>
