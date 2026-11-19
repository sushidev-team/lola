<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { ProjectGitService } from '@bindings/desktop';
  import type { ProjectGitState } from '$lib/projectgit.svelte';
  import { conflictBlocks, mergeLockfile, parseConflictChoices } from '$lib/conflictmerge';
  import {atRow, fileComparison, fileResult, visibleSource} from '$lib/conflictdiff';
  import GitFilePath from './GitFilePath.svelte';
  import ConflictSource from './ConflictSource.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';

  let {git,path,autosuggest = false,onrequesthandled,onclose,onstaged,onstageexternal}: {
    git: ProjectGitState; path: string; autosuggest?: boolean; onrequesthandled?: () => void; onclose: () => void; onstaged: (automatic?: boolean) => Promise<void>; onstageexternal: () => void;
  } = $props();
  let original = $state(''), token = $state(''), error = $state(''), saving = $state(false);
  let choices = $state<(string | null)[]>([]), index = $state(0), full = $state(false);
  let suggestions = $state<Record<number, 'current' | 'incoming' | 'merged'>>({});
  const suggestionCount = $derived(Object.keys(suggestions).length);
  function reviewNext(kind: 'suggestion' | 'unresolved') {
    for (let offset = 1; offset <= ranges.length; offset++) {
      const next = (index + offset) % ranges.length;
      const change = ranges[next];
      if (kind === 'suggestion' ? suggestions[change.id] : !change.optional && choices[change.id] == null) {
        full = false;
        navigate(next);
        return;
      }
    }
  }
  let custom = $state<string | null>(null), showBase = $state(false);
  let wholeCurrent = $state<string | undefined>(), wholeIncoming = $state<string | undefined>();
  let finding = $state(false), query = $state(''), found = $state(true);
  let searchInput = $state<HTMLInputElement>(), fullField = $state<HTMLTextAreaElement>();
  let panel = $state<HTMLElement>();
  const blocks = $derived(conflictBlocks(original));
  let exact=$state(false), top=$state(0);
  const comparison=$derived(fileComparison(original,blocks));
  const ranges=$derived(comparison.changes.map((change,id)=>({...change,id})).filter(change=>exact || !change.optional));
  const range=$derived(ranges[index]);
  const block=$derived(range ? blocks[range.owner] : undefined);
  const remaining=$derived(comparison.changes.filter((change,id)=>!change.optional && choices[id]==null).length);
  const output=$derived(custom ?? fileResult(comparison,choices));
  function jump(next:number) {
    index=Math.max(0,Math.min(next,ranges.length-1));
    top=Math.max(0,(ranges[index]?.row ?? 0)-2)*21;
    showBase=false;
  }
  function scrollSources(offset:number) {
    top=offset;
    const next=atRow(ranges,Math.floor(offset/21)+2);
    if(next!==index) showBase=false;
    index=next;
  }
  function appendLine(line:string,id:number) {
    const next=ranges.findIndex(change=>change.id===id);
    if(next<0) return;
    if(next!==index) jump(next);
    choose((choices[id] ?? '')+line);
  }
  function toggleWhitespace(enabled:boolean) {
    const row=range?.row ?? 0;
    exact=enabled;
    jump(atRow(ranges,row));
    remember();
  }
  const hasMarkers = $derived(/^(?:<{7,}|>{7,})(?: |\r?$)/m.test(output));
  const complete = $derived(!!token && !hasMarkers && (custom !== null || remaining === 0));
  const jsonIssue = $derived.by(() => {
    if (complete && path.endsWith('.json')) {
      try { JSON.parse(output); }
      catch (err) { return err instanceof Error ? err.message : 'Invalid JSON'; }
    }
    return '';
  });
  const ready = $derived(complete && !jsonIssue);
  const key = () => JSON.stringify([git.project,git.worktree,path]);

  let resolving = $state(false), suggesting = $state(false), suggestionNote = $state('');
  async function suggestChoices(attempted = new Set<number>()) {
    if (!token || suggesting || saving || custom !== null) return;
    suggesting = true;
    suggestionNote = '';
    const requestedToken = token;
    try {
      const excerpts = [];
      const offered = new Map<string, boolean>();
      let bytes = 0;
      for (const [id, change] of comparison.changes.entries()) {
        if (change.optional || choices[id] != null || attempted.has(id)) continue;
        const before = Math.max(0, change.row - 3);
        const context = (row:number, count:number) => visibleSource(comparison,row,count,false).map(line => line.text).join('').slice(0,1500);
        const block = blocks[change.owner];
        // A generated lockfile can contain one very large insertion. Keep the
        // original text locally so selecting a side always uses every byte.
        const sampled = new TextEncoder().encode(change.current + change.incoming).length > 96000;
        const alternative = (text:string) => sampled && text.length > 3000
          ? {sampled:true, lines:text.split('\n').length, start:text.slice(0,1500), end:text.slice(-1500)}
          : text;
        const excerpt = {id, currentLabel:block.currentName, incomingLabel:block.incomingName, before:context(before,change.row-before),
          current:alternative(change.current), incoming:alternative(change.incoming), sampled, after:context(change.row+Math.max(change.removed,change.added),3)};
        const size = new TextEncoder().encode(JSON.stringify(excerpt)).length + 1;
        if (bytes + size > 120000 || excerpts.length >= 128) continue;
        bytes += size;
        excerpts.push(excerpt);
        offered.set(String(id), sampled);
      }
      if (!excerpts.length) {
        suggestionNote = remaining ? 'These changes need manual review.' : 'All changes already have a resolution.';
        return;
      }
      for (const id of offered.keys()) attempted.add(Number(id));
      const input = JSON.stringify({path, changes:excerpts});
      let picks:Record<string, unknown> | null = null;
      for (let attempt = 0; attempt < 2 && !picks; attempt++) {
        const raw = await ProjectGitService.SuggestConflictChoices(git.project, input);
        if (!mounted || token !== requestedToken || custom !== null) return;
        picks = parseConflictChoices(raw);
      }
      if (!picks) {
        suggestionNote = 'AI returned an unreadable reply twice. Click Suggest to try again or choose a resolution below.';
        return;
      }
      let count = 0;
      for (const [id, pick] of Object.entries(picks)) {
        const changeId = Number(id);
        if (!offered.has(id) || choices[changeId] != null) continue;
        const change = comparison.changes[changeId];
        if (pick === 'current' || pick === 'incoming') {
          choices[changeId] = change[pick];
          suggestions[changeId] = pick;
        } else if (!offered.get(id) && pick && typeof pick === 'object' && 'merged' in pick && typeof pick.merged === 'string') {
          const text = edited(pick.merged, change.current || change.incoming);
          if (/^(?:<{7,}|={7,}|>{7,})(?: |\r?$)/m.test(text)) continue;
          choices[changeId] = text;
          suggestions[changeId] = 'merged';
        } else continue;
        count++;
      }
      remember();
      suggestionNote = count ? '' : 'AI found no clear choice. Review the changes manually.';
      return true;
    } catch(e) {
      if (mounted) suggestionNote = 'Could not suggest a resolution. ' + String(e).replace(/^RuntimeError: /,'');
    } finally { suggesting = false; }
  }

  let mounted = false;
  onMount(() => {
    mounted = true;
    void load();
    return () => { mounted = false; };
  });
  async function load() {
    error = '';
    const requested = autosuggest;
    if (requested) onrequesthandled?.();
    try {
      const doc = await ProjectGitService.ConflictDocument(git.project,git.worktree,path);
      if (!mounted) return;
      original = doc.text;
      token = doc.token;
      wholeCurrent = doc.current ?? undefined;
      wholeIncoming = doc.incoming ?? undefined;
      suggestionNote = '';
      const saved = drafts.get(key());
      if (saved?.token === token) {
        suggestions = {...saved.suggestions};
        choices = saved.choices.slice();
        exact = saved.exact; index = saved.index;
        custom = saved.custom;
        full = saved.full;
      } else {
        suggestions = {};
        choices = comparison.changes.map(() => null);
        index = 0;
        custom = null;
        full = !ranges.length;
      }
      jump(index);
      if (requested && ranges.length && custom === null) {
        resolving = true;
        if (path.split('/').at(-1) === 'package-lock.json' && doc.base && doc.current && doc.incoming &&
            choices.every((choice,id) => choice === null || suggestions[id] === 'current' && choice === comparison.changes[id].current || suggestions[id] === 'incoming' && choice === comparison.changes[id].incoming)) {
          const result = mergeLockfile(original,blocks,doc.base,doc.current,doc.incoming);
          if (result !== null) {
            custom = result; full = true; remember();
            await save(true);
            return;
          }
        }
        const attempted = new Set<number>();
        while (mounted && comparison.changes.some((c,id) => !c.optional && choices[id] == null && !attempted.has(id))) {
          if (!await suggestChoices(attempted)) break;
        }
        if (!mounted) return;
        if (ready) {
          await save(true);
        } else if (jsonIssue) {
          full = true;
        } else if (remaining) {
          reviewNext('unresolved');
          suggestionNote ||= 'AI left this change unresolved. Review the result before staging.';
        }
      }
    } catch(e) {
      if (mounted) error = String(e).replace(/^RuntimeError: /,'');
    } finally { resolving = false; }
  }
  function remember() {
    if (!drafts.has(key()) && drafts.size >= 12) drafts.delete(drafts.keys().next().value!);
    drafts.set(key(), {token,suggestions:{...suggestions},choices:choices.slice(),index,custom,full,exact});
  }
  function choose(value: string) { if (!range) return; if (suggestions[range.id] === 'merged') delete suggestions[range.id]; choices[range.id] = value; remember(); }
  function edited(value: string, reference: string) {
    return reference.includes('\r\n') ? value.replace(/\r?\n/g,'\r\n') : value;
  }
  function navigate(next:number) {
    jump(next);
    remember();
  }
  async function openFind() {
    finding = true; await tick(); searchInput?.focus(); searchInput?.select();
  }
  async function findNext() {
    if (!query) return;
    full = true; await tick();
    if (!fullField) return;
    const text = fullField.value.toLowerCase(), target = query.toLowerCase();
    let offset = text.indexOf(target,fullField.selectionEnd);
    if (offset < 0) offset = text.indexOf(target);
    found = offset >= 0;
    if (!found) return;
    fullField.focus(); fullField.setSelectionRange(offset,offset+query.length);
    fullField.scrollTop = Math.max(0,(text.slice(0,offset).split('\n').length-3)*parseFloat(getComputedStyle(fullField).lineHeight));
  }
  function keys(e: KeyboardEvent) {
    if (!panel?.contains(e.target as Node)) return;
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'f') {
      e.preventDefault(); void openFind(); return;
    }
    if (!full && e.altKey && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
      e.preventDefault(); navigate(index + (e.key === 'ArrowDown' ? 1 : -1));
    }
  }
  async function save(automatic = false) {
    if (!ready || saving || git.busy) return;
    saving = true; error = '';
    try {
      await ProjectGitService.SaveConflict(git.project,git.worktree,path,token,output);
      drafts.delete(key()); await onstaged(automatic);
    } catch(e) { error = String(e).replace(/^RuntimeError: /,''); }
    finally { saving = false; }
  }
</script>
<script module lang="ts">
  // Navigating between files preserves an unfinished resolution for this app session.
  const drafts = new Map<string,{token:string; suggestions:Record<number,'current'|'incoming'|'merged'>; choices:(string|null)[]; index:number; custom:string|null; full:boolean; exact:boolean}>();
</script>
<svelte:window onkeydown={keys}/>
<section class="resolver" aria-label="Merge editor" bind:this={panel}>
  <header>
    <div class="file-info"><div class="identity" title={path}><Icon name="git-merge" size={16}/><GitFilePath {path}/></div>
    {#if token}<span class="state" class:ready>{ready ? 'Ready to stage' : jsonIssue ? 'Invalid JSON' : hasMarkers && full ? 'Conflict markers remain' : remaining + ' unresolved'}</span>{/if}
    </div>
      {#if ranges.length}
        <div inert={saving || resolving} class="view-switch" role="group" aria-label="Merge view">
        <Button size="xs" aria-pressed={!full} selected={!full} disabled={custom !== null} onclick={() => full = false}>Compare sources</Button>
        <Button size="xs" aria-pressed={full} selected={full} onclick={() => full = true}>Full result</Button>
        </div>
      {/if}
    <div class="file-actions">
    {#if token && remaining && custom === null}<Button size="xs" title="Suggest resolutions for this file" disabled={saving || suggesting} loading={suggesting} onclick={() => suggestChoices()}><Icon name="sparkles" size={13}/>Suggest</Button>{/if}
    {#if token}<Button size="xs" icon class="find" aria-label="Find in merged result" title="Find in merged result (⌘F)" onclick={openFind}><Icon name="search" size={14}/></Button>{/if}
    <Button class="save" size="sm" variant="primary" disabled={!ready || saving || resolving || !!git.busy} loading={saving} onclick={() => save()}>Save &amp; stage</Button>
    <Button size="xs" icon aria-label="Close diff" title="Close diff" onclick={onclose}><Icon name="x" size={14}/></Button>
    </div>
  </header>
  {#if resolving}<div class="suggestion-summary" role="status">Resolving conflicts with AI…</div>
  {:else if suggestionCount && custom === null}
    <div class="suggestion-summary">
      <Icon name="sparkles" size={14}/><span>AI suggested {suggestionCount} {suggestionCount === 1 ? 'change' : 'changes'}. Review before staging.</span>
      <Button size="xs" onclick={() => reviewNext('suggestion')}>Review {suggestionCount === 1 ? 'suggestion' : 'next suggestion'}</Button>
      {#if remaining}<Button size="xs" onclick={() => reviewNext('unresolved')}>Go to unresolved</Button>{/if}
    </div>
  {/if}
  {#if jsonIssue}<div class="issue" role="alert"><Icon name="circle-alert" size={15}/><span>Fix the JSON before staging. {jsonIssue}</span></div>{/if}
  {#if suggestionNote}<div class="issue" role="status"><Icon name="sparkles" size={14}/><span>{suggestionNote}</span></div>{/if}
  {#if error}<div class="issue" role="alert"><Icon name="circle-alert" size={15}/><span>{error}</span><Button size="xs" onclick={load}>{token ? 'Reload from disk' : 'Retry'}</Button>{#if !token}<Button size="xs" onclick={onstageexternal}>Stage external resolution…</Button>{/if}</div>{/if}
  {#if !token}
    {#if !error}<p class="loading">Reading conflict…</p>{/if}
  {:else}
    {#if finding}<form class="search" onsubmit={e => {e.preventDefault(); void findNext();}}>
      <Icon name="search" size={14}/><input bind:this={searchInput} bind:value={query} aria-label="Find text" placeholder="Find in merged result" oninput={() => found = true}/>
      {#if !found}<span>No matches</span>{/if}
      <Button type="submit" size="xs" disabled={!query}>Find next</Button><Button icon size="xs" aria-label="Close search" onclick={() => finding=false}><Icon name="x" size={14}/></Button>
    </form>{/if}
    {#if !full && range}
    <nav aria-label="Conflict navigation" inert={saving || resolving}>
      <div class="range-controls">
          <Button size="xs" icon aria-label="Previous changed range" disabled={index===0} onclick={() => navigate(index-1)}><Icon name="chevron-up" size={14}/></Button>
          <strong>Change {index+1} of {ranges.length} in file</strong>
          <Button size="xs" icon aria-label="Next changed range" disabled={index+1===ranges.length} onclick={() => navigate(index+1)}><Icon name="chevron-down" size={14}/></Button>
          <span class="removed" title="Lines only on Current">−{range.removed}</span><span class="added" title="Lines only on Incoming">+{range.added}</span>
      </div>
        {#if range.formatting}<span>Whitespace only</span>{/if}
        {#if comparison.changes.some(change=>change.optional)}<label title="Formatting-only changes alongside content edits keep Current formatting unless you choose otherwise."><input type="checkbox" checked={exact} onchange={e => toggleWhitespace(e.currentTarget.checked)}/> Include whitespace changes</label>{/if}
    </nav>
    {/if}
    {#if full}
      <div class="whole" inert={saving || resolving}><div class="pane-head"><strong>Result</strong><div class="tools">
        {#if wholeCurrent !== undefined}<Button size="xs" onclick={() => {custom = edited(wholeCurrent!,original); remember();}}>Use current file</Button>{/if}
        {#if wholeIncoming !== undefined}<Button size="xs" onclick={() => {custom = edited(wholeIncoming!,original); remember();}}>Use incoming file</Button>{/if}
        {#if custom !== null}<Button size="xs" onclick={() => {custom = null; full = !ranges.length; remember();}}>Reset file edits</Button>{/if}
      </div></div>
      <textarea bind:this={fullField} aria-label="Full merged result" spellcheck="false" wrap="off" value={output} oninput={e => {custom = edited(e.currentTarget.value,original); remember();}}></textarea></div>
    {:else if range && block}
      <div class="panes" inert={saving || resolving}>
        <ConflictSource name="Current" label={block.currentName} {comparison} {top} whitespace={exact} {path}
          suggested={suggestions[range.id] === 'current'} chosen={choices[range.id] === range.current} onuse={() => choose(range.current)} onappend={appendLine} onscroll={scrollSources}/>
        <ConflictSource name="Incoming" label={block.incomingName} {comparison} {top} whitespace={exact} {path}
          suggested={suggestions[range.id] === 'incoming'} chosen={choices[range.id] === range.incoming} onuse={() => choose(range.incoming)} onappend={appendLine} onscroll={scrollSources}/>
      </div>
      <section class="result" inert={saving || resolving}>
        <div class="pane-head"><strong>Result for change {index+1}</strong><span>Line {range.line}</span>
          {#if suggestions[range.id] === 'merged'}<span><Icon name="sparkles" size={12}/> AI merge draft</span>{/if}
          <div class="tools">{#if block.base !== undefined}<Button size="xs" selected={showBase} onclick={() => showBase = !showBase}>Ancestor context</Button>{/if}
          <Button size="xs" onclick={() => choose(range.current + range.incoming)}>Use both</Button>
          <Button size="xs" disabled={choices[range.id] === null} onclick={() => {choices[range.id] = null; delete suggestions[range.id]; remember();}}>Reset</Button></div>
        </div>
        {#if showBase}<pre aria-label="Common ancestor for the surrounding conflict">{block.base}</pre>{/if}
        <textarea aria-label="Conflict result" spellcheck="false" wrap="off" placeholder="Choose lines above or write the resolution here." value={choices[range.id] ?? (range.optional ? range.current : '')} oninput={e => choose(edited(e.currentTarget.value, original))}></textarea>
      </section>
    {/if}
  {/if}
</section>

<style>
  .suggestion-summary {display:flex; align-items:center; flex-wrap:wrap; gap:8px; padding:4px 12px; border-bottom:1px solid var(--color-edge); color:var(--color-ink-2); font-size:12px;}
  .resolver {display:flex; flex-direction:column; min-height:0; min-width:0; overflow:auto; background:var(--color-panel); color:var(--color-ink); container-type:inline-size;}
  .file-info,nav,.pane-head,.tools,.identity,.issue,.file-actions,.view-switch,.range-controls {display:flex; align-items:center; gap:8px;}
  header,nav,.pane-head {border-bottom:1px solid var(--color-edge);}
  header {display:grid; grid-template-columns:minmax(0,1fr) auto minmax(0,1fr); align-items:center; padding:4px 10px; min-height:36px; column-gap:10px;}
  .file-info {min-width:0; gap:10px;}
  .file-actions {grid-column:3; justify-self:end;}
  .view-switch {grid-column:2; justify-self:center;}
  .identity {min-width:0; font-weight:600;}
  .identity :global(svg) {flex:none; color:var(--color-warn);}
  .file-actions {margin-left:auto; flex:none; gap:3px;}
  .state,.pane-head span,.loading {font-size:12px; color:var(--color-ink-2);}
  .state {color:var(--color-warn); white-space:nowrap;} .state.ready {color:var(--color-good);}
  .tools {margin-left:auto;}
  nav {background:var(--color-canvas); flex-wrap:wrap; gap:4px 12px; padding:4px 10px; color:var(--color-ink-2); font-size:12px;}
  nav:empty {display:none;}
  .view-switch {gap:1px;}
  .range-controls {gap:4px;  font-variant-numeric:tabular-nums;}
  .range-controls strong {font-weight:500; color:var(--color-ink);}
  nav label {display:flex; align-items:center; gap:5px; margin-inline-start:auto; text-transform:none; cursor:pointer;}
  .pane-head {padding:6px 12px;}
  @container (max-width:590px) {
    nav {gap:3px 6px;}
    nav label {margin-inline-start:0;}

  }
  @container (max-width:610px) {
    header {grid-template-columns:minmax(0,1fr) auto; row-gap:3px;}
    .file-actions {grid-column:2; grid-row:1;}
    .view-switch {grid-row:2; grid-column:1/-1;}
  }
  .removed {color:var(--color-bad);} .added {color:var(--color-good);}

  .panes {display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); min-height:135px; flex:1;}
  .result,.whole {display:flex; flex-direction:column; min-width:0; min-height:0;}

  .pane-head {background:var(--color-canvas); font-size:12px; min-height:32px;}
  .pane-head span {overflow:hidden; white-space:nowrap; text-overflow:ellipsis;}
  textarea,pre {font:12px/1.65 var(--font-mono); tab-size:4; color:var(--color-ink); background:var(--color-panel); padding:10px 12px;}
  textarea {flex:1; min-height:48px; width:100%; resize:none; border:0; outline-offset:-2px;}
  textarea:focus-visible {outline:1px solid var(--color-accent);}
  .result,.whole {flex:1; min-height:120px;}
  .result pre {overflow:auto; max-height:30%; color:var(--color-ink-2);}
  .search {display:flex; align-items:center; gap:8px; padding:5px 12px; border-bottom:1px solid var(--color-edge); color:var(--color-ink-2);}
  .search input {flex:1; min-width:50px; font-size:13px; background:var(--color-canvas); padding:4px 8px;}
  .issue,.loading {padding:12px;} .issue {align-items:flex-start; flex-wrap:wrap; color:var(--color-warn); font-size:13px;}
  @container (max-width:440px) { .panes {grid-template-columns:1fr; min-height:250px;} }
</style>
