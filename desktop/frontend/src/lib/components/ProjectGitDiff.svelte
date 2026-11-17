<script lang="ts">
  import { tick } from 'svelte';
  import { parsePatch, splitRows, langOf, tokenize, type Row } from '$lib/diff';
  import type { GitDiffFile } from '$lib/projectgit';
  import type { GitMenuItem } from '$lib/gitactions.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';
  let { file, lineAction = null, busy = false, onlines, oncontext }: {
    file: GitDiffFile;
    /** 'stage' for unstaged changes, 'unstage' for staged ones; null when single lines cannot be changed here. */
    lineAction?: 'stage' | 'unstage' | null;
    busy?: boolean;
    /** Patch line indexes (into file.patch) of the changed lines to move. */
    onlines?: (lines: number[]) => void;
    oncontext?: (title: string, items: GitMenuItem[], event: MouseEvent) => void;
  } = $props();
  let split = $state(false);
  let wrap = $state(false);
  const rows = $derived(parsePatch(file.patch));
  const paired = $derived(splitRows(rows));
  const language = $derived(langOf(file.path));
  const selectable = $derived(!!lineAction && !!onlines && !split && !file.binary && !file.tooLarge);
  const verb = $derived(lineAction === 'unstage' ? 'Unstage' : 'Stage');

  // A selection is a contiguous run of rows (anchor..head); only its changed rows count.
  let anchor = $state(-1);
  let head = $state(-1);
  let hover = $state(-1);
  let dragging = $state(false);
  let rowEls = $state<Record<number, HTMLElement | undefined>>({});
  let viewport = $state(0);
  let box = $state<{ top: number; height: number } | null>(null);
  const isChange = (row: Row | undefined) => row?.kind === 'add' || row?.kind === 'del';
  const low = $derived(Math.min(anchor, head));
  const high = $derived(Math.max(anchor, head));
  const chosen = $derived(anchor < 0 ? [] : rows.slice(low, high + 1).filter(isChange));
  const target = $derived.by(() => {
    if (chosen.length) return { from: low, to: high, lines: chosen.map(row => row.at!) };
    if (isChange(rows[hover])) return { from: hover, to: hover, lines: [rows[hover].at!] };
    return null;
  });

  $effect(() => { file.patch; anchor = head = hover = -1; dragging = false; });
  $effect(() => { if (!selectable) anchor = head = hover = -1; });
  $effect(() => {
    const goal = target, wrapped = wrap, patch = file.patch;
    void wrapped; void patch;
    if (!goal) { box = null; return; }
    void tick().then(() => {
      const first = rowEls[goal.from], last = rowEls[goal.to];
      if (!first || !last) { box = null; return; }
      box = { top: first.offsetTop, height: last.offsetTop + last.offsetHeight - first.offsetTop };
    });
  });

  function press(event: PointerEvent, index: number) {
    if (!selectable || event.button > 0) return;
    if (event.shiftKey && anchor >= 0) head = index; else { anchor = head = index; }
    dragging = true;
    event.preventDefault();
    (event.currentTarget as HTMLElement).closest<HTMLElement>('.diff-code')?.focus();
  }
  // Clicking the code selects its row (shift extends) without blocking text selection;
  // only the gutter starts a drag, since a drag over code selects text.
  function pick(event: PointerEvent, index: number) {
    if (!selectable || event.button > 0 || (event.target as HTMLElement).closest('.gutter')) return;
    if (event.shiftKey && anchor >= 0) { head = index; event.preventDefault(); } else if (!window.getSelection()?.toString()) anchor = head = index;
  }
  function enter(index: number) {
    if (dragging) head = index;
    hover = index;
  }
  function apply(lines: number[]) { if (!busy && lines.length) onlines?.(lines); }
  function hunkLines(index: number): number[] {
    const out: number[] = [];
    for (let i = index + 1; i < rows.length && rows[i].kind !== 'hunk'; i++) if (isChange(rows[i])) out.push(rows[i].at!);
    return out;
  }
  function hunkOf(index: number) {
    let i = index;
    while (i >= 0 && rows[i].kind !== 'hunk') i--;
    return i;
  }
  function noun(n: number) { return n === 1 ? 'line' : `${n} lines`; }
  function context(event: MouseEvent, index: number) {
    if (!selectable || !oncontext || !isChange(rows[index])) return;
    event.preventDefault();
    if (!(chosen.length && index >= low && index <= high)) { anchor = head = index; }
    const lines = chosen.length && index >= low && index <= high ? chosen.map(row => row.at!) : [rows[index].at!];
    const hunk = hunkOf(index);
    const items: GitMenuItem[] = [
      { label: `${verb} ${noun(lines.length)}`, icon: lineAction === 'unstage' ? 'minus' : 'plus', disabled: busy, run: () => apply(lines) },
    ];
    if (hunk >= 0) items.push({ label: `${verb} hunk`, icon: lineAction === 'unstage' ? 'minus' : 'plus', disabled: busy, run: () => apply(hunkLines(hunk)) });
    items.push({ label: 'Clear selection', icon: 'x', separator: true, run: () => { anchor = head = -1; } });
    oncontext('', items, event);
  }
  function key(event: KeyboardEvent) {
    if (event.key === 'Escape' && anchor >= 0) { event.stopPropagation(); anchor = head = -1; }
    // Enter (or s / u) moves the selected lines, or the hovered one, without reaching for the button.
    else if (selectable && target && (event.key === 'Enter' || event.key === (lineAction === 'unstage' ? 'u' : 's')) && !event.metaKey && !event.ctrlKey && !event.altKey) { event.preventDefault(); event.stopPropagation(); apply(target.lines); }
  }
</script>

<svelte:window onpointerup={() => dragging = false} />

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
  {#if lineAction && split}<div class="diff-note">Switch to Unified to {verb.toLowerCase()} single lines.</div>{/if}
  {#if file.oldPath && file.oldPath !== file.path}<div class="diff-note">Renamed from <code>{file.oldPath}</code></div>{/if}
  {#if file.binary}<div class="diff-empty"><Icon name="file" size={20} /><p>Binary file changed</p><span>This file cannot be displayed as a text diff.</span></div>
  {:else if file.tooLarge}<div class="diff-empty"><Icon name="file" size={20} /><p>Diff exceeds the preview limit</p><span>Use your editor to inspect this file.</span></div>
  {:else if !rows.length}<div class="diff-empty"><p>No text changes</p><span>This may be a rename, a file mode change, or an empty file.</span></div>
  {:else}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
    <div class="diff-code" tabindex="0" role="region" aria-label="Code changes" bind:clientWidth={viewport} onkeydown={key}>
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
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <div class="unified-lines" onpointerleave={() => hover = -1}>
          {#if selectable && target && box}
            <div class="action-rail" style:width="{viewport}px">
              <button type="button" class="line-action" class:tall={box.height > 30} style:top="{box.top}px" style:height="{box.height}px" disabled={busy} aria-label={`${verb} ${noun(target.lines.length)}`} title={`${verb} ${noun(target.lines.length)}`} onclick={() => apply(target.lines)}><span class="line-action-icon"><Icon name={lineAction === 'unstage' ? 'minus' : 'plus'} size={14} /></span></button>
            </div>
          {/if}
          {#each rows as row, index}
            {#if row.kind === 'hunk' || row.kind === 'meta'}<div class="diff-meta" bind:this={rowEls[index]}>{row.text}</div>
            {:else}<!-- svelte-ignore a11y_no_static_element_interactions -->
              <div class="unified-row row-{row.kind}" class:selectable class:picked={selectable && anchor >= 0 && index >= low && index <= high} bind:this={rowEls[index]} onpointerdown={(event) => pick(event, index)} onpointerenter={() => enter(index)} oncontextmenu={(event) => context(event, index)}>
                <span class="line-no gutter" onpointerdown={(event) => press(event, index)}>{row.oldLine || ''}</span><span class="line-no gutter" onpointerdown={(event) => press(event, index)}>{row.newLine || ''}</span><span class="change-mark" aria-hidden="true">{row.kind === 'add' ? '+' : row.kind === 'del' ? '−' : ' '}</span>{@render code(row)}
              </div>{/if}
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
  .unified-lines { position:relative; }
  .split-lines { width:100%; }
  .unified-row { display:grid; grid-template-columns:44px 44px 20px minmax(0,1fr); min-width:max-content; }
  .split-row { display:grid; grid-template-columns: minmax(0,1fr) minmax(0,1fr); }
  .half { display:grid; grid-template-columns:44px minmax(0,1fr); overflow:auto; }
  .half + .half { border-left:1px solid var(--color-edge); }
  code { white-space:pre; padding-right:16px; }
  .line-no { text-align:right; padding-right:10px; user-select:none; color:var(--color-faint); font-size:12px; }
  .change-mark { color:var(--color-faint); user-select:none; }
  .diff-meta { color:var(--color-info); background:color-mix(in srgb,var(--color-info) 6%,var(--color-panel)); padding:5px 14px; white-space:pre; font-size:12px; }
  .gutter { position:sticky; left:0; z-index:1; background:var(--color-panel); cursor:default; }
  .gutter + .gutter { left:44px; }
  .row-add .gutter { background:color-mix(in srgb,var(--color-good) 10%,var(--color-panel)); }
  .row-del .gutter { background:color-mix(in srgb,var(--color-bad) 10%,var(--color-panel)); }
  .selectable .gutter { cursor:pointer; }
  .picked { box-shadow:inset 3px 0 0 var(--color-accent); background-image:linear-gradient(var(--color-accent-fill),var(--color-accent-fill)); }
  .action-rail { position:sticky; left:0; height:0; z-index:2; pointer-events:none; }
  .line-action { position:absolute; right:14px; width:26px; display:block; padding:0; border:1px solid var(--color-accent); border-radius:5px; background:var(--color-panel); color:var(--color-accent-ink,var(--color-accent)); cursor:pointer; pointer-events:auto; box-shadow:0 1px 4px #0003; }
  .line-action:hover:not(:disabled) { background:var(--color-accent-fill); }
  .line-action:disabled { opacity:.5; cursor:default; }
  .line-action-icon { position:sticky; top:4px; display:grid; place-content:center; width:100%; height:19px; }
  .line-action:not(.tall) .line-action-icon { position:static; height:100%; }
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
