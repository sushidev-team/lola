<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { ProjectGitService } from '@bindings/desktop';
  import type { ProjectGitState } from '$lib/projectgit.svelte';
  import { conflictBlocks, mergedText } from '$lib/conflictmerge';
  import ConflictSource from './ConflictSource.svelte';
  import Button from './Button.svelte';
  import Icon from './Icon.svelte';

  let {git,path,onstaged,onstageexternal}: {
    git: ProjectGitState; path: string; onstaged: () => Promise<void>; onstageexternal: () => void;
  } = $props();
  let original = $state(''), token = $state(''), error = $state(''), saving = $state(false);
  let choices = $state<(string | null)[]>([]), index = $state(0), full = $state(false);
  let custom = $state<string | null>(null), showBase = $state(false);
  let wholeCurrent = $state<string | undefined>(), wholeIncoming = $state<string | undefined>();
  let finding = $state(false), query = $state(''), found = $state(true);
  let searchInput = $state<HTMLInputElement>(), fullField = $state<HTMLTextAreaElement>();
  let panel = $state<HTMLElement>();
  const blocks = $derived(conflictBlocks(original)), block = $derived(blocks[index]);
  const before = $derived(block ? original.slice(Math.max(0,block.start-1000),block.start).trimEnd().split(/\r?\n/).slice(-2).join('\n') : '');
  const after = $derived(block ? original.slice(block.end,block.end+1000).split(/\r?\n/).slice(0,2).join('\n') : '');
  const remaining = $derived(choices.filter(value => value === null).length);
  const output = $derived(custom ?? mergedText(original, blocks, choices));
  const hasMarkers = $derived(/^(?:<{7,}|>{7,})(?: |\r?$)/m.test(output));
  const ready = $derived(!!token && !hasMarkers && (custom !== null || remaining === 0));
  const key = () => JSON.stringify([git.project,git.worktree,path]);

  let mounted = false;
  onMount(() => {
    mounted = true;
    void load();
    return () => { mounted = false; };
  });
  async function load() {
    error = '';
    try {
      const doc = await ProjectGitService.ConflictDocument(git.project,git.worktree,path);
      if (!mounted) return;
      original = doc.text;
      token = doc.token;
      wholeCurrent = doc.current ?? undefined;
      wholeIncoming = doc.incoming ?? undefined;
      const saved = drafts.get(key());
      if (saved?.token === token) {
        choices = saved.choices.slice();
        index = saved.index;
        custom = saved.custom;
        full = saved.full;
      } else {
        choices = conflictBlocks(original).map(() => null);
        index = 0;
        custom = null;
        full = !choices.length;
      }
    } catch(e) {
      if (mounted) error = String(e).replace(/^RuntimeError: /,'');
    }
  }
  function remember() {
    if (!drafts.has(key()) && drafts.size >= 12) drafts.delete(drafts.keys().next().value!);
    drafts.set(key(), {token,choices:choices.slice(),index,custom,full});
  }
  function choose(value: string) { choices[index] = value; remember(); }
  function edited(value: string, reference: string) {
    return reference.includes('\r\n') ? value.replace(/\r?\n/g,'\r\n') : value;
  }
  function navigate(next: number) { index = Math.max(0, Math.min(blocks.length-1,next)); showBase = false; remember(); }
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
  async function save() {
    if (!ready || saving || git.busy) return;
    saving = true; error = '';
    try {
      await ProjectGitService.SaveConflict(git.project,git.worktree,path,token,output);
      drafts.delete(key()); await onstaged();
    } catch(e) { error = String(e).replace(/^RuntimeError: /,''); }
    finally { saving = false; }
  }
</script>
<script module lang="ts">
  // Navigating between files preserves an unfinished resolution for this app session.
  const drafts = new Map<string,{token:string; choices:(string|null)[]; index:number; custom:string|null; full:boolean}>();
</script>
<svelte:window onkeydown={keys}/>
<section class="resolver" aria-label="Merge editor" bind:this={panel}>
  <header>
    <div class="identity"><Icon name="git-merge" size={15}/><strong>Merge editor</strong></div>
    {#if token}<span class="state" class:ready>{ready ? 'Ready to stage' : hasMarkers && full ? 'Conflict markers remain' : remaining + ' unresolved'}</span>{/if}
    {#if token}<Button size="xs" icon class="find" aria-label="Find in merged result" title="Find in merged result (⌘F)" onclick={openFind}><Icon name="search" size={14}/></Button>{/if}
    <Button class="save" size="sm" variant="primary" disabled={!ready || saving || !!git.busy} loading={saving} onclick={save}>Save &amp; stage</Button>
  </header>
  {#if error}<div class="issue" role="alert"><Icon name="circle-alert" size={15}/><span>{error}</span><Button size="xs" onclick={load}>{token ? 'Reload from disk' : 'Retry'}</Button>{#if !token}<Button size="xs" onclick={onstageexternal}>Stage external resolution…</Button>{/if}</div>{/if}
  {#if !token}
    {#if !error}<p class="loading">Reading conflict…</p>{/if}
  {:else}
    {#if finding}<form class="search" onsubmit={e => {e.preventDefault(); void findNext();}}>
      <Icon name="search" size={14}/><input bind:this={searchInput} bind:value={query} aria-label="Find text" placeholder="Find in merged result" oninput={() => found = true}/>
      {#if !found}<span>No matches</span>{/if}
      <Button type="submit" size="xs" disabled={!query}>Find next</Button><Button icon size="xs" aria-label="Close search" onclick={() => finding=false}><Icon name="x" size={14}/></Button>
    </form>{/if}
    <nav aria-label="Conflict navigation" inert={saving}>
      {#if blocks.length}
        <Button size="xs" selected={!full} disabled={custom !== null} onclick={() => full = false}>Conflicts</Button>
        <Button size="xs" selected={full} onclick={() => full = true}>Full result</Button>
      {/if}
      {#if !full && block}
        <div class="pager"><span>Conflict {index+1} of {blocks.length}</span>
        <Button size="xs" icon aria-label="Previous conflict" title="Previous conflict (⌥↑)" disabled={index === 0} onclick={() => navigate(index-1)}><Icon name="chevron-up" size={14}/></Button>
        <Button size="xs" icon aria-label="Next conflict" title="Next conflict (⌥↓)" disabled={index+1 === blocks.length} onclick={() => navigate(index+1)}><Icon name="chevron-down" size={14}/></Button></div>
      {/if}
    </nav>
    {#if full}
      <div class="whole" inert={saving}><div class="pane-head"><strong>Result</strong><span>{path}</span><div class="tools">
        {#if wholeCurrent !== undefined}<Button size="xs" onclick={() => {custom = edited(wholeCurrent!,original); remember();}}>Use current file</Button>{/if}
        {#if wholeIncoming !== undefined}<Button size="xs" onclick={() => {custom = edited(wholeIncoming!,original); remember();}}>Use incoming file</Button>{/if}
        {#if custom !== null}<Button size="xs" onclick={() => {custom = null; full = !blocks.length; remember();}}>Reset file edits</Button>{/if}
      </div></div>
      <textarea bind:this={fullField} aria-label="Full merged result" spellcheck="false" wrap="off" value={output} oninput={e => {custom = edited(e.currentTarget.value,original); remember();}}></textarea></div>
    {:else if block}
      <div class="panes" inert={saving}>
        <ConflictSource name="Current" label={block.currentName} value={block.current} {path} firstLine={block.line+1}
          chosen={choices[index] === block.current} onuse={() => choose(block.current)} onappend={lines => choose((choices[index] ?? '') + lines)}/>
        <ConflictSource name="Incoming" label={block.incomingName} value={block.incoming} {path} firstLine={block.incomingLine}
          chosen={choices[index] === block.incoming} onuse={() => choose(block.incoming)} onappend={lines => choose((choices[index] ?? '') + lines)}/>
      </div>
      <section class="result" inert={saving}>
        <div class="pane-head"><strong>Result</strong><span>Line {block.line}</span>
          <div class="tools">{#if block.base !== undefined}<Button size="xs" selected={showBase} onclick={() => showBase = !showBase}>Base</Button>{/if}
          <Button size="xs" onclick={() => choose(block.current + block.incoming)}>Use both</Button>
          <Button size="xs" disabled={choices[index] === null} onclick={() => {choices[index] = null; remember();}}>Reset</Button></div>
        </div>
        {#if showBase}<pre aria-label="Common ancestor">{block.base}</pre>{/if}
        {#if before}<pre class="context" aria-label="Preceding context">{before}</pre>{/if}
        <textarea aria-label="Conflict result" spellcheck="false" wrap="off" placeholder="Choose lines above or write the resolution here." value={choices[index] ?? ''} oninput={e => choose(edited(e.currentTarget.value, original))}></textarea>
        {#if after}<pre class="context" aria-label="Following context">{after}</pre>{/if}
      </section>
    {/if}
  {/if}
</section>

<style>
  .resolver {display:flex; flex-direction:column; min-height:0; min-width:0; overflow:auto; background:var(--color-panel); color:var(--color-ink); container-type:inline-size;}
  header,nav,.pane-head,.tools,.pager,.identity,.issue {display:flex; align-items:center; gap:8px;}
  header,nav,.pane-head {padding:6px 12px; border-bottom:1px solid var(--color-edge);}
  header {min-height:42px; flex-wrap:wrap;}
  .identity {font-size:14px;} .identity :global(svg){color:var(--color-warn);}
  header :global(.find),.pager,.tools {margin-left:auto;}
  .state,.pager,.pane-head span,.loading {font-size:12px; color:var(--color-ink-2);}
  .state {color:var(--color-warn);} .state.ready {color:var(--color-good);}
  nav {background:var(--color-canvas); flex-wrap:wrap; gap:3px;} nav:empty {display:none;}
  .panes {display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); min-height:135px; flex:1;}
  .result,.whole {display:flex; flex-direction:column; min-width:0; min-height:0;}

  .pane-head {background:var(--color-canvas); font-size:12px; min-height:32px;}
  .pane-head span {overflow:hidden; white-space:nowrap; text-overflow:ellipsis;}
  textarea,pre {font:12px/1.65 var(--font-mono); tab-size:4; color:var(--color-ink); background:var(--color-panel); padding:10px 12px;}
  textarea {flex:1; min-height:48px; width:100%; resize:none; border:0; outline-offset:-2px;}
  textarea:focus-visible {outline:1px solid var(--color-accent);}
  .result,.whole {flex:1; min-height:120px;}
  .result pre {overflow:auto; max-height:30%; color:var(--color-ink-2);}
  .result .context {flex:none; padding:4px 12px; max-height:48px; background:var(--color-canvas); color:var(--color-faint);}
  .search {display:flex; align-items:center; gap:8px; padding:5px 12px; border-bottom:1px solid var(--color-edge); color:var(--color-ink-2);}
  .search input {flex:1; min-width:50px; font-size:13px; background:var(--color-canvas); padding:4px 8px;}
  .issue,.loading {padding:12px;} .issue {align-items:flex-start; flex-wrap:wrap; color:var(--color-warn); font-size:13px;}
  @container (max-width:440px) { .panes {grid-template-columns:1fr; min-height:250px;} }
</style>
