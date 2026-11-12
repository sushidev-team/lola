<script lang="ts">
  import { onMount, onDestroy, untrack } from 'svelte';
  import { ProjectGitService } from '@bindings/desktop';
  import type { ProjectGitState } from '$lib/projectgit.svelte';
  import type { GitRef } from '$lib/projectgit';
  import type { PROptions, PRPreview, PRSelection, PRResult } from '$lib/gitpullrequest';
  import { store } from '$lib/store.svelte';
  import Modal from './Modal.svelte';
  import Button from './Button.svelte';
  import Select from './Select.svelte';
  import Checkbox from './Checkbox.svelte';
  import Icon from './Icon.svelte';
  import Skeleton from './Skeleton.svelte';
  import GitProviderIcon from './GitProviderIcon.svelte';
  import ProjectGitPRChoices from './ProjectGitPRChoices.svelte';

  let {git,branch,target,historyRef='',limit=200,onClose}:{git:ProjectGitState;branch:GitRef;target?:GitRef;historyRef?:string;limit?:number;onClose:()=>void}=$props();
  const uid=$props.id();
  const saved=untrack(()=>!target&&git.pullRequestDraft?.sourceRef===branch.fullName?git.pullRequestDraft:null);
  const remotes=$derived((git.snapshot?.remotes??[]).filter(r=>r.host==='github.com'&&r.repo));
  function branchRemote(ref:GitRef){const name=ref.remote?ref.fullName:ref.upstream|| (ref.current?git.snapshot?.upstream:'') || '';return remotes.filter(r=>name.startsWith(`refs/remotes/${r.name}/`)||name.startsWith(r.name+'/')).sort((a,b)=>b.name.length-a.name.length)[0];}
  let remote=$state(untrack(()=>saved?.remote||branchRemote(branch)?.name||remotes.find(r=>r.name==='origin')?.name||remotes[0]?.name||''));
  let sourceRef=$state(untrack(()=>saved?.sourceRef||branch.fullName));
  let baseRepo=$state(untrack(()=>saved?.baseRepo||(target?branchRemote(target)?.repo:'')||''));
  let base=$state(untrack(()=>saved?.base||(target?(target.remote?target.name.slice((branchRemote(target)?.name.length??0)+1):target.name):'')));
  let title=$state(untrack(()=>saved?.title||git.snapshot?.commits.find(c=>c.sha===branch.sha)?.subject||''));
  let body=$state(saved?.body||'');let draft=$state(saved?.draft??false);let push=$state(false);
  let reviewers=$state<string[]>(saved?.reviewers??[]),assignees=$state<string[]>(saved?.assignees??[]),labels=$state<string[]>(saved?.labels??[]);
  let options=$state<PROptions|null>(null),preview=$state<PRPreview|null>(null),result=$state<PRResult|null>(null);
  let error=$state(''),draftError=$state('');let alive=true,optionsSeq=0,previewSeq=0;let previewKey='';
  const sourceBranches=$derived((git.snapshot?.refs??[]).filter(r=>!r.remote||(r.fullName.startsWith(`refs/remotes/${remote}/`)&&!r.fullName.endsWith('/HEAD'))));
  const source=$derived(sourceBranches.find(r=>r.fullName===sourceRef));
  const busy=$derived(!!git.busy);
  const selection=$derived<PRSelection>({remote,sourceRef,sourceSHA:source?.sha??'',baseRepo,base});
  const ready=$derived(!busy&&!git.prLoading&&!git.prPreviewLoading&&!git.prDrafting&&!!preview&&preview.commits>0&&!!title.trim()&&(!preview.needsPush||push));
  $effect(()=>{if(!result)git.pullRequestDraft={sourceRef,remote,baseRepo,base,title,body,draft,reviewers,assignees,labels};});
  onMount(()=>{void loadOptions();});
  onDestroy(()=>{alive=false;optionsSeq++;previewSeq++;git.prLoading=false;git.prPreviewLoading=false;git.prDrafting=false;});
  function close(){if(!busy)onClose();}
  function key(e:KeyboardEvent){e.stopPropagation();if(e.key==='Escape'){e.preventDefault();close();}}
  async function loadOptions(reset=false){
    const seq=++optionsSeq;++previewSeq;preview=null;error='';git.prLoading=true;git.prPreviewLoading=false;
    if(reset){baseRepo='';base='';reviewers=[];assignees=[];labels=[];}
    if(!remote){git.prLoading=false;error='Choose a GitHub remote to create a pull request.';return;}
    try{
      const data=await ProjectGitService.PullRequestOptions(git.project,git.worktree,remote,baseRepo);
      if(!alive||seq!==optionsSeq)return;
      options={...data,branches:data.branches??[],repositories:data.repositories??[],reviewers:data.reviewers??[],assignees:data.assignees??[],labels:data.labels??[],notices:data.notices??[]};
      baseRepo=data.baseRepo;if(!options.branches.includes(base))base=data.defaultBranch;
      reviewers=reviewers.filter(x=>options!.reviewers.some(o=>o.value===x));assignees=assignees.filter(x=>options!.assignees.some(o=>o.value===x));labels=labels.filter(x=>options!.labels.some(o=>o.value===x));
      if(!sourceBranches.some(r=>r.fullName===sourceRef))sourceRef=sourceBranches[0]?.fullName??'';
      void compare();
    }catch(e){if(alive&&seq===optionsSeq)error=String(e);}
    finally{if(alive&&seq===optionsSeq)git.prLoading=false;}
  }
  async function compare(){
    const seq=++previewSeq;preview=null;push=false;error='';git.prPreviewLoading=true;
    const picked={...selection};previewKey=JSON.stringify(picked);
    if(!picked.sourceSHA||!picked.base||!picked.baseRepo){git.prPreviewLoading=false;return;}
    try{const data=await ProjectGitService.PreviewPullRequest(git.project,git.worktree,picked);if(alive&&seq===previewSeq){preview=data;if(!title.trim())title=data.subject;}}
    catch(e){if(alive&&seq===previewSeq)error=String(e);}
    finally{if(alive&&seq===previewSeq)git.prPreviewLoading=false;}
  }
  async function generate(){
    if(!preview||busy||git.prDrafting)return;
    const picked={...selection},beforeTitle=title,beforeBody=body;git.prDrafting=true;draftError='';
    try{const text=await ProjectGitService.DraftPullRequest(git.project,git.worktree,picked,title,body);
      if(!alive)return;
      if(title!==beforeTitle||body!==beforeBody||JSON.stringify(selection)!==JSON.stringify(picked)){draftError='Your draft or branches changed. Generate again to use the latest selection.';return;}
      const [subject,...rest]=text.split('\n');title=subject;body=rest.join('\n').trim();
    }catch(e){if(alive)draftError=String(e);}finally{if(alive)git.prDrafting=false;}
  }
  async function create(e:SubmitEvent){
    e.preventDefault();if(!ready||previewKey!==JSON.stringify(selection))return;error='';
    const response=await git.createPullRequest({selection:{...selection},title:title.trim(),body,draft,push,reviewers:draft?[]:[...reviewers],assignees:[...assignees],labels:[...labels]},historyRef,limit);
    if(!alive)return;if(response)result=response;else error=git.error||'Could not create the pull request.';
  }
</script>

<Modal title={result?'Pull request ready':'Create pull request'} width="660px" portal onClose={close} onkey={key}>
 {#if result}
  <div class="pr-success"><Icon name="git-pull-request" size={24}/><div><strong>{result.existing?'An open pull request already exists':`Pull request #${result.number} created`}</strong><p>{baseRepo} · {preview?.head} → {base}</p></div></div>
  {#each result.warnings as warning}<p class="pr-warning" role="status">{warning}</p>{/each}
  <div class="dialog-actions"><Button onclick={close}>Done</Button><Button variant="primary" onclick={()=>void store.openURL(result!.url)}><Icon name="external-link" size={15}/>Open pull request</Button></div>
 {:else}
  <form class="pr-form" aria-label="Create pull request" aria-busy={busy} onsubmit={create}>
   <div class="pr-provider"><GitProviderIcon host="github.com"/><span>GitHub</span><span class="pr-summary">{git.prLoading?'Loading repository…':git.prPreviewLoading?'Comparing branches…':preview?`${preview.commits} commits · ${preview.files} files`:''}</span></div>
   <div class="pr-directions">
    <div class="pr-side"><label for={`${uid}-remote`}>From repository</label><Select id={`${uid}-remote`} bind:value={remote} disabled={busy||!remotes.length} onchange={()=>void loadOptions(true)}>{#each remotes as r}<option value={r.name}>{r.repo} · {r.name}</option>{/each}</Select>
     <label class="sr-only" for={`${uid}-source`}>Source branch</label><Select id={`${uid}-source`} bind:value={sourceRef} disabled={busy||git.prLoading} onchange={()=>void compare()}>{#each sourceBranches as r}<option value={r.fullName} data-description={r.remote?'Remote':'Local'}>{r.remote?r.name.slice(remote.length+1):r.name}</option>{/each}</Select>
    </div>
    <Icon name="arrow-right" size={17} class="pr-direction"/>
    <div class="pr-side"><label for={`${uid}-repository`}>Into repository</label><Select id={`${uid}-repository`} bind:value={baseRepo} disabled={busy||git.prLoading||!options} onchange={()=>{reviewers=[];assignees=[];labels=[];void loadOptions();}}>{#each options?.repositories??[] as repo}<option value={repo}>{repo}</option>{/each}</Select>
     <label class="sr-only" for={`${uid}-base`}>Base branch</label><Select id={`${uid}-base`} bind:value={base} disabled={busy||git.prLoading||!options} onchange={()=>void compare()}>{#each options?.branches??[] as branch}<option value={branch}>{branch}</option>{/each}</Select>
    </div>
   </div>
   {#if git.prLoading}<Skeleton height="16px" width="65%"/>{/if}
   {#if preview?.needsPush}<label class="pr-push"><Checkbox bind:checked={push} disabled={busy}/><span>Push <strong>{preview.head}</strong> to {preview.sourceRepo} before creating</span></label>{/if}
   {#if preview&&preview.commits===0}<p class="pr-warning">No commits to propose. Choose a different source or base branch.</p>{/if}
   <div class="pr-title-heading"><label for={`${uid}-title`}>Title</label><Button size="xs" variant="accent" loading={git.prDrafting} disabled={busy||!preview||git.prPreviewLoading||!preview.commits} onclick={()=>void generate()} title="Draft a title and description using Claude Haiku"><Icon name="sparkles" size={13}/>AI draft</Button></div>
   <input id={`${uid}-title`} bind:value={title} disabled={busy} maxlength="256" required placeholder="What does this change?"/>
   <label for={`${uid}-body`}>Description</label><textarea id={`${uid}-body`} bind:value={body} disabled={busy} maxlength="65000" rows="6" placeholder="Describe the change, its purpose, and how to verify it…"></textarea>
   {#if draftError}<p class="pr-error" role="alert">{draftError}</p>{/if}
   <div class="pr-metadata"><ProjectGitPRChoices label="Reviewers" options={options?.reviewers??[]} bind:selected={reviewers} disabled={busy||draft||git.prLoading}/><ProjectGitPRChoices label="Assignees" options={options?.assignees??[]} bind:selected={assignees} disabled={busy||git.prLoading}/><ProjectGitPRChoices label="Labels" options={options?.labels??[]} bind:selected={labels} disabled={busy||git.prLoading}/></div>
   <label class="pr-draft"><Checkbox bind:checked={draft} disabled={busy}/><span>Create as draft</span>{#if draft}<small>Request reviewers when ready.</small>{/if}</label>
   {#if options?.notices.length}<details class="pr-notices"><summary><Icon name="chevron-right" size={12}/>Some metadata is unavailable</summary>{#each options.notices as notice}<p>{notice}</p>{/each}</details>{/if}
   {#if error}<div class="pr-error" role="alert"><span>{error}</span>{#if !busy}<Button size="xs" onclick={()=>void loadOptions()}>Retry</Button>{/if}</div>{/if}
   <div class="dialog-actions"><Button disabled={busy} onclick={close}>Cancel</Button><Button type="submit" variant="primary" loading={git.busy==='create-pr'} disabled={!ready}><Icon name="git-pull-request" size={15}/>{preview?.needsPush?'Push & create PR':draft?'Create draft PR':'Create PR'}</Button></div>
  </form>
 {/if}
</Modal>

<style>
 .pr-form { display:grid; gap:8px; min-width:0; }
 .pr-provider { display:flex; align-items:center; gap:7px; color:var(--color-ink-2); font-size:13px; margin-bottom:3px; }
 .pr-summary { margin-left:auto; color:var(--color-faint); font-size:12px; }
 .pr-directions { display:grid; grid-template-columns:minmax(0,1fr) 17px minmax(0,1fr); gap:10px; align-items:center; margin-bottom:8px; }
 .pr-side { display:grid; gap:7px; min-width:0; }
 .pr-directions :global(.pr-direction) { color:var(--color-faint); margin-top:20px; }
 label { color:var(--color-ink-2); font-size:13px; }
 input,textarea { width:100%; min-width:0; padding:8px 10px; border:1px solid var(--color-edge); border-radius:6px; background:var(--color-canvas); color:var(--color-ink); font-size:13px; }
 textarea { resize:vertical; min-height:110px; max-height:280px; }
 input:focus,textarea:focus { outline:2px solid var(--color-accent); outline-offset:1px; }
 .pr-title-heading { display:flex; align-items:center; justify-content:space-between; margin-top:3px; }
 .pr-metadata { display:grid; grid-template-columns:repeat(3,minmax(0,1fr)); gap:8px; margin-top:5px; align-items:start; }
 .pr-draft,.pr-push { display:flex; align-items:center; gap:8px; cursor:pointer; }
 .pr-draft { margin-top:5px; }
 .pr-draft small { margin-left:auto; color:var(--color-faint); font-size:12px; }
 .pr-push { padding:8px 10px; border-radius:6px; background:var(--color-accent-fill); color:var(--color-accent-ink); overflow-wrap:anywhere; }
 .pr-push strong { font-weight:500; }
 .pr-error { display:flex; gap:8px; align-items:flex-start; color:var(--color-bad); font-size:13px; overflow-wrap:anywhere; }
 .pr-error span { flex:1; }
 .pr-warning { color:var(--color-warn); font-size:13px; margin-block:8px; overflow-wrap:anywhere; }
 .pr-notices { color:var(--color-faint); font-size:12px; }.pr-notices summary { display:flex; gap:5px; cursor:pointer; list-style:none; }.pr-notices summary::-webkit-details-marker{display:none;}.pr-notices p{margin-top:4px;}
 .dialog-actions { margin-top:12px; }
 .pr-success { display:flex; align-items:center; gap:12px; color:var(--color-accent-ink); padding-block:8px; }.pr-success p { color:var(--color-ink-2); margin-top:5px; font-size:13px; overflow-wrap:anywhere; }
 @media(max-width:540px){.pr-directions{grid-template-columns:minmax(0,1fr);}.pr-directions :global(.pr-direction){display:none;}.pr-metadata{grid-template-columns:minmax(0,1fr);}.pr-draft{flex-wrap:wrap;}.pr-draft small{margin-left:22px;}.pr-summary{max-width:65%;text-align:right;}}
</style>
