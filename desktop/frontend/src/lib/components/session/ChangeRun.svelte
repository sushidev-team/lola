<script lang="ts">
  import { feed } from "$lib/feed.svelte";
  import { disclosureSlide } from "$lib/disclosureMotion";
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { changes } from "$lib/changes.svelte";
  import { changesOffered } from "$lib/sessionview";
  import { liveVerb, pastVerb, type RunRowData } from "$lib/turns";
  import Button from "../Button.svelte";
  import Chevron from "../Chevron.svelte";
  import ToolOutput from "./ToolOutput.svelte";
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
  const id = $props.id();

  function toggle() {
    if (open) f.expanded.delete(row.id);
    else f.expanded.add(row.id);
  }
  $effect(() => {
    if (open) for (const item of items) if (item.tool.hasOutput) void f.output(item.id);
  });
</script>

<div class="cv-change-run" data-run="edit" data-change-run>
  <button type="button" class="cv-change-head" disabled={readonly} aria-expanded={open} aria-controls={id} onclick={toggle}>
    <Chevron {open} />
    <span>{running ? 'Changing' : 'Changes'} · {paths.length} {paths.length === 1 ? 'file' : 'files'}</span>
    <span class="cv-change-path font-mono" title={paths.join('\n')}>{path}</span>
    {#if row.failed}<span class="text-bad">{row.failed} failed</span>{/if}
    {#if row.added || row.removed}<span class="num whitespace-nowrap text-faint" title="Lines added and removed across these edits">+{row.added} −{row.removed}</span>{/if}
  </button>
  {#if !open && active}<ToolPreview text={row.items.at(-1)?.tool.preview || path} {running} />{/if}
  {#if open}
    <div {id} class="cv-change-body" transition:disclosureSlide>
      <div class="cv-change-files" aria-label="Changed files">
        {#each paths as file (file)}
          {@const edits = row.items.filter(item => item.target === file)}
          {@const last = edits.at(-1)!}
          <button type="button" aria-pressed={path === file} class="cv-change-file" onclick={() => selected = file} title={file}>
            <span class="cv-change-path font-mono">{file}</span>
            <span class:text-bad={edits.some(item => item.status === 'error')} class="text-faint">{last.status === 'running' ? liveVerb(last.verb) : last.status === 'error' ? 'Failed' : last.status === 'ok' ? pastVerb(last.verb) : last.status}</span>
          </button>
        {/each}
      </div>
      <div class="cv-change-preview" data-change-preview>
        {#if session && changesOffered(session)}
          <Button size="xs" onclick={() => { changes.want(sessionId, path); if (session) ssh.openChanges(session); }}>View changes</Button>
        {/if}
        {#each items as item (item.id)}
          {@const output = f.outputs.get(item.id)}
          {#if items.length > 1}<p class="text-sm text-faint">Edit {items.indexOf(item) + 1} of {items.length}</p>{/if}
          {#if output?.text || item.tool.preview}
            <ToolOutput text={output?.text || item.tool.preview} sourcePath={path} />
          {:else if output?.loading}
            <p class="text-sm text-faint" role="status">Loading changes…</p>
          {:else}
            <p class="text-sm text-faint">{output?.failed ? 'Could not load this diff.' : item.status === 'running' ? 'Waiting for changes…' : 'No diff recorded for this action.'}</p>
          {/if}
          {#if output?.truncated}<p class="text-sm text-faint">Showing the recorded excerpt.</p>{/if}
        {/each}
      </div>
    </div>
  {/if}
</div>
