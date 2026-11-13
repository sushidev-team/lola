<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte';
  import type { ProjectGitState } from '$lib/projectgit.svelte';
  import type { GitRef } from '$lib/projectgit';
  import Modal from './Modal.svelte';
  import Button from './Button.svelte';
  import Checkbox from './Checkbox.svelte';
  import Select from './Select.svelte';
  import Icon from './Icon.svelte';

  let { git, start, refs, historyRef = '', limit = 200, onClose, onCreated }: {
    git: ProjectGitState; start: { sha: string; label: string }; refs: GitRef[];
    historyRef?: string; limit?: number; onClose: () => void;
    onCreated: (path: string, checkout: boolean) => void;
  } = $props();
  const uid = $props.id();
  let name = $state('');
  let base = $state('initial');
  let checkout = $state(true);
  let error = $state('');
  let nameInput: HTMLInputElement;
  let alive = true;
  const sha = $derived(base === 'initial' ? start.sha : refs.find(branch => branch.fullName === base)?.sha ?? '');
  const busy = $derived(!!git.busy);
  onMount(() => { void tick().then(() => { if (alive) nameInput?.focus(); }); });
  onDestroy(() => { alive = false; });
  function close() { if (!busy) onClose(); }
  function key(event: KeyboardEvent) {
    event.stopPropagation();
    if (event.key === 'Escape') { event.preventDefault(); close(); }
  }
  async function create(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !name.trim() || !sha) return;
    error = '';
    const result = await git.createBranch(name.trim(), sha, checkout, historyRef, limit);
    if (!alive) return;
    if (result === null) error = git.error || 'Could not create the branch. Try again.';
    else onCreated(result, checkout);
  }
</script>

<Modal title="New branch" width="440px" portal onClose={close} onkey={key}>
  <form class="new-branch-form" onsubmit={create} aria-busy={busy}>
    <label for={`${uid}-name`}>Branch name</label>
    <input id={`${uid}-name`} bind:this={nameInput} bind:value={name} disabled={busy} placeholder="feature/my-change" autocomplete="off" spellcheck="false" required />
    <label class="base-label" for={`${uid}-base`}>Starting point</label>
    <div class="starting-point">
    <Select id={`${uid}-base`} bind:value={base} disabled={busy}>
      <option value="initial" data-description={start.sha.slice(0, 8)}>{start.label}</option>
      {#each [false, true] as remote}
        {#if refs.some(branch => branch.remote === remote)}<optgroup label={remote ? 'Remote branches' : 'Local branches'}>
          {#each refs.filter(branch => branch.remote === remote) as branch (branch.fullName)}<option value={branch.fullName} data-description={branch.sha.slice(0, 8)}>{branch.name}</option>{/each}
        </optgroup>{/if}
      {/each}
    </Select>
    <code class="branch-start">{sha.slice(0, 8)}</code>
    </div>
    <label class="checkout-option"><Checkbox bind:checked={checkout} disabled={busy} />Switch to new branch</label>
    {#if error}<p class="branch-error" role="alert"><Icon name="circle-alert" size={15} /><span>{error}</span></p>{/if}
    <div class="dialog-actions">
      <Button class="cancel" disabled={busy} onclick={close}>Cancel</Button>
      <Button type="submit" variant="primary" disabled={busy || !name.trim() || !sha} loading={git.busy === 'create-branch'}><Icon name="git-branch" size={15} />Create branch</Button>
    </div>
  </form>
</Modal>

<style>
  .new-branch-form { display:grid; grid-template-columns:minmax(0,1fr); gap:7px; }
  .new-branch-form > label { color:var(--color-ink-2); font-size:13px; }
  input { width:100%; padding:8px 10px; border:1px solid var(--color-edge); border-radius:6px; background:var(--color-canvas); color:var(--color-ink); font:13px var(--font-mono); }
  input:focus { outline:2px solid var(--color-accent); outline-offset:1px; }
  .base-label { margin-top:10px; }
  .starting-point { position:relative; }
  .starting-point :global(.select-value) { right:112px; }
  .branch-start { position:absolute; right:36px; top:50%; transform:translateY(-50%); pointer-events:none; color:var(--color-ink-2); font:11px var(--font-mono); }
  .checkout-option { display:flex; align-items:center; gap:8px; margin-top:10px; cursor:pointer; }
  .branch-error { display:flex; align-items:flex-start; gap:7px; margin-top:8px; color:var(--color-bad); font-size:13px; overflow-wrap:anywhere; }
  .dialog-actions { margin-top:16px; }
</style>
