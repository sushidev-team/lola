<script lang="ts">
  import { parsePatch, splitRows, langOf, tokenize, type Row } from '$lib/diff';
  import type { GitDiffFile } from '$lib/projectgit';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  let { file }: { file: GitDiffFile } = $props();
  let split = $state(false);
  let wrap = $state(false);
  const rows = $derived(parsePatch(file.patch));
  const paired = $derived(splitRows(rows));
  const language = $derived(langOf(file.path));
</script>

{#snippet code(row: Row | null)}
  {#if row}<code>{#each tokenize(row.text, language) as token}<span class="syntax-{token.kind}">{token.text}</span>{/each}</code>{/if}
{/snippet}

<section class="git-diff" class:wrap aria-label={`Diff for ${file.path}`}>
  <header class="diff-header">
    <span class="diff-path" title={file.path}><Icon name="file-text" size={14} /><span>{file.path}</span></span>
    <div class="diff-options"><span class="add-count">+{file.additions}</span><span class="del-count">−{file.deletions}</span>
      <Button size="xs" selected={!split} onclick={() => split = false}>Unified</Button>
      <Button size="xs" selected={split} onclick={() => split = true}>Split</Button>
      <Button size="xs" selected={wrap} onclick={() => wrap = !wrap}>Wrap</Button>
    </div>
  </header>
  {#if file.oldPath && file.oldPath !== file.path}<div class="diff-note">Renamed from <code>{file.oldPath}</code></div>{/if}
  {#if file.binary}<div class="diff-empty"><Icon name="file" size={20} /><p>Binary file changed</p><span>This file cannot be displayed as a text diff.</span></div>
  {:else if file.tooLarge}<div class="diff-empty"><Icon name="file" size={20} /><p>Diff exceeds the preview limit</p><span>Use your editor to inspect this file.</span></div>
  {:else if !rows.length}<div class="diff-empty"><p>No text changes</p><span>This may be a rename, a file mode change, or an empty file.</span></div>
  {:else}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div class="diff-code" tabindex="0" role="region" aria-label="Code changes">
      {#if split}
        <div class="split-lines">
          {#each paired as pair}
            {#if pair.full}<div class="diff-meta">{pair.full.text}</div>
            {:else}
              <div class="split-row"><div class="half row-{pair.left?.kind ?? 'blank'}"><span class="line-no">{pair.left?.oldLine || ''}</span>{@render code(pair.left)}</div><div class="half row-{pair.right?.kind ?? 'blank'}"><span class="line-no">{pair.right?.newLine || ''}</span>{@render code(pair.right)}</div></div>
            {/if}
          {/each}
        </div>
      {:else}
        <div class="unified-lines">
          {#each rows as row}
            {#if row.kind === 'hunk' || row.kind === 'meta'}<div class="diff-meta">{row.text}</div>
            {:else}<div class="unified-row row-{row.kind}"><span class="line-no">{row.oldLine || ''}</span><span class="line-no">{row.newLine || ''}</span><span class="change-mark" aria-hidden="true">{row.kind === 'add' ? '+' : row.kind === 'del' ? '−' : ' '}</span>{@render code(row)}</div>{/if}
          {/each}
        </div>
      {/if}
    </div>
  {/if}
</section>

<style>
  .git-diff { display:grid; grid-template-rows:auto auto minmax(0,1fr); min-height:0; min-width:0; height:100%; background:var(--color-panel); }
  .diff-header { display:flex; flex-wrap:wrap; align-items:center; justify-content:space-between; gap:8px; padding:6px 12px; border-bottom:1px solid var(--color-edge); }
  .diff-path { display:flex; align-items:center; gap:8px; min-width:0; flex:1; }
  .diff-path > span { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-size:13px; }
  .diff-options { display:flex; align-items:center; gap:4px; font-size:12px; }
  .add-count,.del-count { font-family:var(--font-mono); }
  .add-count { color:var(--color-good); font-variant-numeric:tabular-nums; }
  .del-count { color:var(--color-bad); margin-right:8px; font-variant-numeric:tabular-nums; }
  .diff-note { padding:6px 14px; color:var(--color-faint); font-size:12px; border-bottom:1px solid var(--color-edge); }
  .diff-code { grid-row:3; overflow:auto; min-height:0; min-width:0; font-family:var(--font-mono); font-size:13px; line-height:21px; tab-size:4; }
  .unified-lines,.split-lines { min-width:100%; width:max-content; }
  .split-lines { width:100%; }
  .unified-row { display:grid; grid-template-columns:44px 44px 20px minmax(0,1fr); min-width:max-content; }
  .split-row { display:grid; grid-template-columns: minmax(0,1fr) minmax(0,1fr); }
  .half { display:grid; grid-template-columns:44px minmax(0,1fr); overflow:auto; }
  .half + .half { border-left:1px solid var(--color-edge); }
  code { white-space:pre; padding-right:16px; }
  .line-no { text-align:right; padding-right:10px; user-select:none; color:var(--color-faint); font-size:12px; }
  .change-mark { color:var(--color-faint); user-select:none; }
  .diff-meta { color:var(--color-info); background:color-mix(in srgb,var(--color-info) 6%,var(--color-panel)); padding:5px 14px; white-space:pre; font-size:12px; }
  .row-add { background:color-mix(in srgb,var(--color-good) 10%,transparent); }
  .row-del { background:color-mix(in srgb,var(--color-bad) 10%,transparent); }
  .row-add .change-mark { color:var(--color-good); }
  .row-del .change-mark { color:var(--color-bad); }
  .row-blank { background:var(--color-canvas); }
  .syntax-kw { color:var(--color-magenta); } .syntax-str { color:var(--color-good); } .syntax-com { color:var(--color-faint); } .syntax-num { color:var(--color-orange); }
  .wrap code { white-space:pre-wrap; overflow-wrap:anywhere; }
  .wrap .unified-lines,.wrap .unified-row { width:100%; min-width:0; }
  .diff-empty { display:grid; place-content:center; justify-items:center; gap:8px; color:var(--color-faint); padding:40px 20px; text-align:center; }
  .diff-empty p { color:var(--color-ink); } .diff-empty span { font-size:12px; }
</style>
