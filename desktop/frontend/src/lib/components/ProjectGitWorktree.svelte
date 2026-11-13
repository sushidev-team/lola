<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import type { ProjectGitState } from '$lib/projectgit.svelte';
  import type { GitRef } from '$lib/projectgit';
  import Modal from './Modal.svelte';
  import Button from './Button.svelte';
  import Checkbox from './Checkbox.svelte';
  import Icon from './Icon.svelte';
  let {git, branch, historyRef = '', limit = 200, onClose, onCreated}: {
    git: ProjectGitState; branch: GitRef; historyRef?: string; limit?: number; onClose: () => void; onCreated: (path:string) => void;
  } = $props();
  const uid = $props.id();
  const needsBranch = untrack(() => branch.remote || !!git.snapshot?.worktrees.some(t => t.branch === branch.name));
  let createBranch = $state(needsBranch);
  let name = $state('');
  let path = $state(untrack(() => {
    const root = git.snapshot?.worktrees[0]?.path || git.snapshot?.path || '';
    return `${root}-${branch.name.replace(/[^a-zA-Z0-9._-]/g,'-')}`;
  }));
  let open = $state(true);
  let error = $state('');
  let pathInput: HTMLInputElement;
  const busy = $derived(!!git.busy);
  function close() {if (!busy) onClose();}
  onMount(() => { void tick().then(() => pathInput?.focus()); });
  async function create(e:SubmitEvent) {
    e.preventDefault(); if (busy || !path.trim() || (createBranch && !name.trim())) return;
    error = '';
    const result = await git.createWorktree(branch.fullName,branch.sha,path.trim(),createBranch ? name.trim() : '',historyRef,limit);
    if (result === null) error = git.error; else if (open) onCreated(result); else onClose();
  }
</script>
<Modal title="Create worktree" width="480px" portal onClose={close} onkey={(e) => {e.stopPropagation();if(e.key==='Escape'){e.preventDefault();close();}}}>
  <form onsubmit={create} aria-busy={busy}>
    <div class="source"><Icon name="git-branch" size={15}/><span>{branch.name}</span><code>{branch.sha.slice(0,8)}</code></div>
    <label for={`${uid}-path`}>New folder</label><input id={`${uid}-path`} bind:this={pathInput} bind:value={path} disabled={busy} required spellcheck="false" autocomplete="off" />
    <p>Use a new folder alongside your repository. Your current checkout stays in place.</p>
    <label class="choice"><Checkbox bind:checked={createBranch} disabled={busy || needsBranch}/>Create a new branch</label>
    {#if needsBranch}<p>{branch.remote ? 'Give the new local branch a name.' : 'This branch already has a checkout. Use a new branch for parallel work.'}</p>{/if}
    {#if createBranch}<label for={`${uid}-branch`}>Branch name</label><input id={`${uid}-branch`} bind:value={name} disabled={busy} required placeholder="feature/parallel-work" spellcheck="false" autocomplete="off" />{/if}
    <label class="choice"><Checkbox bind:checked={open} disabled={busy}/>Open worktree when created</label>
    {#if error}<p class="error" role="alert">{error}</p>{/if}
    <div class="dialog-actions"><Button disabled={busy} onclick={close}>Cancel</Button><Button type="submit" variant="primary" loading={git.busy==='create-worktree'} disabled={busy || !path.trim() || (createBranch && !name.trim())}><Icon name="folder" size={15}/>Create worktree</Button></div>
  </form>
</Modal>
<style>
  form { display:grid; gap:7px; }
  label { color:var(--color-ink-2); font-size:13px; }
  input { min-width:0; width:100%; padding:8px 10px; border:1px solid var(--color-edge); border-radius:6px; background:var(--color-canvas); color:var(--color-ink); font:13px var(--font-mono); }
  input:focus { outline:2px solid var(--color-accent); outline-offset:1px; }
  p { color:var(--color-ink-2); font-size:12px; }
  .source { display:flex; align-items:center; gap:7px; margin-bottom:12px; font-size:13px; color:var(--color-accent); }
  .source span { min-width:0; overflow-wrap:anywhere; }
  .source code { margin-left:auto; color:var(--color-ink-2); font:11px var(--font-mono); }
  .choice { display:flex; align-items:center; gap:8px; margin-top:10px; }
  .error { color:var(--color-bad); white-space:pre-wrap; overflow-wrap:anywhere; }
  .dialog-actions { display:flex; justify-content:space-between; gap:12px; margin-top:16px; }
</style>
