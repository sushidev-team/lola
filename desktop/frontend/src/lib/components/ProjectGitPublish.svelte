<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import type { ProjectGitState } from '$lib/projectgit.svelte';
  import type { GitDestination } from '$lib/gitactions.svelte';
  import type { GitRef } from '$lib/projectgit';
  import Button from './Button.svelte';
  import Select from './Select.svelte';
  import Icon from './Icon.svelte';
  let {git, branch, destination, historyRef = '', limit = 200, onClose}: {
    git: ProjectGitState; branch: GitRef; destination?: GitDestination; historyRef?: string; limit?: number; onClose: () => void;
  } = $props();
  const uid = $props.id();
  const remotes = $derived(git.snapshot?.remotes ?? []);
  const trackingOnly = $derived(!!destination?.trackingOnly);
  const title = $derived(trackingOnly ? 'Set upstream' : 'Publish branch');
  let remote = $state(untrack(() => destination?.remote || git.snapshot?.remotes?.find(r => r.name === 'origin')?.name || git.snapshot?.remotes?.[0]?.name || ''));
  let name = $state(untrack(() => destination?.name || branch.name));
  let error = $state('');
  let input = $state<HTMLInputElement>();
  const remoteBranches = $derived(git.snapshot?.refs.filter(r => r.remote && r.fullName.startsWith(`refs/remotes/${remote}/`) && !r.fullName.endsWith('/HEAD')) ?? []);
  const target = $derived(remoteBranches.find(r => r.fullName === `refs/remotes/${remote}/${name}`));
  const ready = $derived(remotes.some(r => r.name === remote) && !!name.trim() && (!trackingOnly || !!target));
  let section: HTMLFormElement;
  onMount(() => { void tick().then(() => input?.focus()); });
  async function publish(event: SubmitEvent) {
    event.preventDefault();
    if (git.busy || !ready) return;
    error = '';
    const result = await git.branchAction({action:trackingOnly?'set-upstream':'publish',source:branch.fullName,sourceSHA:branch.sha,target:trackingOnly?target!.fullName:'',targetSHA:trackingOnly?target!.sha:'',remote,remoteBranch:name.trim()},historyRef,limit);
    if (result !== null) onClose(); else { error = git.error; git.error = ''; }
  }
</script>

<svelte:window onkeydown={(e) => { if (!e.defaultPrevented && e.key === 'Escape' && !git.busy) { e.preventDefault(); onClose(); } }}/>
<form class="publish-form" bind:this={section} aria-label={title} aria-busy={!!git.busy} onsubmit={publish}>
  <div class="publish-source"><Icon name="git-branch" size={16}/><div><strong>{title}</strong><span>{branch.name}</span></div></div>
  <div class="publish-destination">
    <label for={`${uid}-remote`}>Remote</label><label for={`${uid}-branch`}>Remote branch</label>
    <Select id={`${uid}-remote`} bind:value={remote} disabled={!!git.busy || !remotes.length}>{#each remotes as item}<option value={item.name}>{item.name}</option>{/each}</Select>
    {#if trackingOnly}<Select id={`${uid}-branch`} bind:value={name} disabled={!!git.busy || !remoteBranches.length}><option value="" disabled>Choose a branch</option>{#each remoteBranches as item}<option value={item.fullName.slice(`refs/remotes/${remote}/`.length)}>{item.fullName.slice(`refs/remotes/${remote}/`.length)}</option>{/each}</Select>
    {:else}<input id={`${uid}-branch`} bind:this={input} bind:value={name} disabled={!!git.busy} autocomplete="off" spellcheck="false" required />{/if}
    <p>{!remotes.length ? 'Add a Git remote to this repository, then refresh.' : trackingOnly && !remoteBranches.length ? 'Fetch this remote to load its branches.' : trackingOnly ? 'Future pulls and pushes will track this branch. No commits will be pushed.' : 'Future pulls and pushes will use this destination.'}</p>
  </div>
  <div class="publish-actions"><Button type="submit" size="sm" variant="primary" loading={git.busy === 'publish' || git.busy === 'set-upstream'} disabled={!!git.busy || !ready}><Icon name={trackingOnly?'cloud':'arrow-up'} size={15}/>{title}</Button><Button size="sm" disabled={!!git.busy} onclick={onClose}>Cancel</Button></div>
  {#if error}<p class="publish-error" role="alert">{error}</p>{/if}
</form>

<style>
  .publish-form { display:flex; align-items:center; flex-wrap:wrap; gap:16px 24px; padding:12px 16px; background:var(--color-panel); border-bottom:1px solid var(--color-edge); }
  .publish-source { display:flex; gap:8px; align-items:center; min-width:0; max-width:300px; color:var(--color-accent); }
  .publish-source div { display:grid; gap:3px; min-width:0; }
  .publish-source strong { font-size:13px; color:var(--color-ink); }
  .publish-source span { font-size:12px; color:var(--color-ink-2); overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
  .publish-destination { display:grid; grid-template-columns:minmax(100px,140px) minmax(150px,1fr); gap:5px 8px; flex:1; max-width:540px; min-width:260px; }
  label, .publish-destination p { font-size:12px; color:var(--color-ink-2); }
  .publish-destination p { grid-column:1 / -1; }
  input { min-width:0; width:100%; padding:6px 10px; border:1px solid var(--color-edge); border-radius:6px; background:var(--color-canvas); color:var(--color-ink); font:12px var(--font-mono); }
  input:focus { outline:2px solid var(--color-accent); outline-offset:1px; }
  .publish-actions { display:flex; gap:6px; }
  .publish-error { flex-basis:100%; color:var(--color-bad); font-size:13px; white-space:pre-wrap; overflow-wrap:anywhere; }
</style>
