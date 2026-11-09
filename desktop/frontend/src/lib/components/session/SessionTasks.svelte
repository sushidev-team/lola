<script lang="ts">
  import { tick } from 'svelte';
  import { slide } from 'svelte/transition';
  import { cubicOut } from 'svelte/easing';
  import { DaemonService } from '@bindings/desktop';
  import { store } from '$lib/store.svelte';
  import { ssh } from '$lib/ssh.svelte';
  import { isAgentless } from '$lib/sessionview';
  import type { SessionTask } from '@bindings/internal/protocol';
  import Button from '../Button.svelte';
  import Icon from '../Icon.svelte';
  import TaskStateIcon from './TaskStateIcon.svelte';

  function disclose(node: Element) {
    const reduce = !node.animate || window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
    return slide(node, { duration: reduce ? 0 : 160, easing: cubicOut });
  }

  let { sessionId }: { sessionId: string } = $props();
  const session = $derived(store.sessionById(sessionId));
  const all = $derived(session?.tasks ?? []);
  const tasks = $derived(all.filter(t => t.kind === 'task'));
  const requirements = $derived(all.filter(t => t.kind === 'requirement'));
  const unattached = $derived(requirements.filter(t => !t.parentId));
  const unattachedChecks = $derived(unattached.filter(t => t.state === 'checking'));
  // Older boards sometimes repeat the session's issue as their only task.
  // Flatten that presentation only; IDs, scope and completion gates stay intact.
  const issueTask = $derived(tasks.length === 1 && repeatsIssue(tasks[0].text) && requirements.some(r => r.parentId === tasks[0].id) ? tasks[0] : undefined);
  const visibleItems = $derived(issueTask ? requirements.filter(r => r.parentId === issueTask.id) : tasks);
  const done = $derived(visibleItems.filter(t => t.state === 'done').length);
  const complete = $derived(visibleItems.length > 0 && done === visibleItems.length && requirements.every(t => t.state === 'done'));
  const active = $derived(visibleItems.filter(t => t.state !== 'done').sort((a, b) => Number(b.state === 'doing') - Number(a.state === 'doing')));
  const finished = $derived(visibleItems.filter(t => t.state === 'done'));
  function repeatsIssue(text: string) {
    if (!session?.issue || !session.title) return false;
    const normalize = (value: string) => value.trim().replace(/\s+/g, ' ').toLowerCase();
    const { issue, title } = session;
    return [title, issue, `${title} (${issue})`, `${issue}: ${title}`, `${issue} — ${title}`, `${issue} - ${title}`].some(candidate => normalize(candidate) === normalize(text));
  }
  let showDone = $state(false);
  let expandedTasks = $state<Set<string>>(new Set());
  function toggleTask(id: string) { const next = new Set(expandedTasks); if (next.has(id)) next.delete(id); else next.add(id); expandedTasks = next; }
  function stateLabel(state: string) { return state === 'checking' ? 'Needs your check' : state === 'doing' ? 'In progress' : state === 'done' ? 'Done' : 'To do'; }
  function hasUnmetRequirements(item: SessionTask) {
    return item.kind === 'task' && requirements.some(r => r.parentId === item.id && r.state !== 'done');
  }
  function actionsFor(item?: SessionTask) {
    return [
      ...(item?.state === 'checking' && hasUnmetRequirements(item) ? [{id: 'feedback', label: 'Give feedback'}] : []),
      {id: 'edit', label: `Edit ${item?.kind ?? 'task'}`},
      {id: 'remove', label: `Remove ${item?.kind ?? 'task'}`},
    ];
  }
  function feedback(item: SessionTask) {
    const view = ssh.view(sessionId);
    const context = `Feedback on ${item.kind} [${item.id}] (${item.text}):\n`;
    if (!view.agentDraft.startsWith(context)) view.agentDraft = context + view.agentDraft;
    view.runDefault = false;
    view.composerFocus++;
  }
  let tab = $state('tasks');
  let globalRequirement = $state(false);
  let expandedRequirements = $state<Set<string>>(new Set());
  function toggleRequirement(id: string) { const next = new Set(expandedRequirements); if (next.has(id)) next.delete(id); else next.add(id); expandedRequirements = next; }
  function switchTab(next: string) { tab = next; composing = false; editing = ''; }
  let composing = $state(false);
  let text = $state('');
  let kind = $state('task');
  let parent = $state('');


  let editing = $state('');
  let draftID = '';
  let removing = $state('');
  let pending = $state(false);
  let error = $state('');
  let input = $state<HTMLInputElement>();
  let observedSession = '';
  let wasComplete = false;
  $effect(() => {
    if (observedSession !== sessionId) {
      observedSession = sessionId;
      tab = 'tasks'; showDone = false; expandedTasks = new Set(); expandedRequirements = new Set(); menu = null;
      composing = false; text = ''; editing = ''; removing = ''; error = '';
      wasComplete = complete;
    } else if (complete !== wasComplete) {
      wasComplete = complete;
      if (complete) showDone = false;
    }
  });

  async function mutate(argv: string[]) {
    if (pending) return false;
    const id = sessionId;
    pending = true; error = '';
    try {
      const next = await DaemonService.SessionTask(id, argv);
      store.sessions = store.sessions.map(s => s.id === id ? { ...s, tasks: next ?? [] } : s);
      void store.refresh();
      return id === sessionId;
    } catch (e) {
      if (id === sessionId) error = String(e);
      return false;
    } finally { pending = false; }
  }
  async function add(taskID = '') {
    editing = ''; draftID = crypto.randomUUID(); text = ''; kind = taskID || tab === 'requirements' ? 'requirement' : 'task'; parent = taskID; globalRequirement = false; composing = true;
    await tick(); input?.focus();
  }
  async function edit(task: SessionTask) {
    editing = task.id; text = task.text; kind = task.kind; parent = task.parentId ?? ''; globalRequirement = task.scope === 'global'; composing = true;
    await tick(); input?.focus();
  }
  async function submit() {
    const value = text.trim();
    if (!value) return;
    const argv = editing ? ['edit', editing, value] : ['add', draftID, kind, ...(kind === 'requirement' ? parent ? ['--task', parent] : globalRequirement ? ['--global'] : [] : []), value];
    if (await mutate(argv)) { if (parent) expandedTasks = new Set([...expandedTasks, parent]); else if (kind === 'requirement') tab = 'requirements'; text = ''; editing = ''; composing = false; }
  }

  let menu = $state<{ id: string; kind: 'state' | 'parent' | 'actions'; anchor: HTMLButtonElement; left: number; top: number; width: number; height: number } | null>(null);
  let menuElement = $state<HTMLDivElement>();
  const statuses = [{id: 'todo', label: 'To do'}, {id: 'doing', label: 'In progress'}, {id: 'checking', label: 'Needs your check'}, {id: 'done', label: 'Done'}];
  const menuItem = $derived(all.find(t => t.id === menu?.id));
  const menuOptions = $derived(menu?.kind === 'actions' ? actionsFor(menuItem) : menu?.kind === 'state' ? statuses : [{id: '--global', label: 'Global requirement'}, {id: 'none', label: 'Let the agent organize'}, ...tasks.map(t => ({id: t.id, label: t.text}))]);
  const menuValue = $derived(menu?.kind === 'state' ? menuItem?.state : menuItem ? menuItem.scope === 'global' ? '--global' : menuItem.parentId || 'none' : globalRequirement ? '--global' : parent || 'none');
  async function openMenu(event: MouseEvent, id: string, kind: 'state' | 'parent' | 'actions') {
    const anchor = event.currentTarget as HTMLButtonElement;
    if (menu?.anchor === anchor) { closeMenu(); return; }
    const r = anchor.getBoundingClientRect();
    const width = Math.min(kind === 'parent' ? 300 : 170, window.innerWidth - 16);
    const height = Math.min(260, (kind === 'actions' ? actionsFor(all.find(t => t.id === id)).length : kind === 'state' ? statuses.length : tasks.length + 2) * 34 + 10, window.innerHeight - 16);
    const below = window.innerHeight - r.bottom - 8;
    menu = { id, kind, anchor, width, height, left: Math.max(8, Math.min(r.left, window.innerWidth - width - 8)), top: Math.max(8, below >= height ? r.bottom + 4 : r.top - height - 4) };
    await tick(); menuElement?.querySelector<HTMLButtonElement>('[aria-checked="true"]')?.focus();
    if (!menuElement?.contains(document.activeElement)) menuElement?.querySelector<HTMLButtonElement>('button')?.focus();
  }
  function closeMenu(restore = true) { const anchor = menu?.anchor; menu = null; if (restore) anchor?.focus(); }
  function choose(option: string) {
    const m = menu; if (!m) return; const item = menuItem; closeMenu();
    if (m.kind === 'actions') { if (item) { if (option === 'edit') void edit(item); else if (option === 'feedback') feedback(item); else removing = item.id; } return; }
    if (m.kind === 'parent' && !m.id) { globalRequirement = option === '--global'; parent = option === '--global' || option === 'none' ? '' : option; return; }
    void mutate([m.kind === 'state' ? 'state' : 'attach', m.id, option]);
  }
  function menuKey(event: KeyboardEvent) {
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); closeMenu(); }
    else if (event.key === 'Tab') closeMenu();
    else if (['ArrowDown','ArrowUp','Home','End'].includes(event.key)) {
      event.preventDefault();
      const buttons = Array.from(menuElement?.querySelectorAll<HTMLButtonElement>('button') ?? []);
      const at = buttons.indexOf(document.activeElement as HTMLButtonElement);
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1 : (at + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length;
      buttons[next]?.focus();
    }
  }
  function portal(node: HTMLDivElement) { document.body.appendChild(node); return { destroy: () => node.remove() }; }
  $effect(() => {
    if (!menu) return;
    const outside = (event: PointerEvent) => { if (!menuElement?.contains(event.target as Node) && !menu?.anchor.contains(event.target as Node)) closeMenu(false); };
    const reposition = (event: Event) => { if (!menuElement?.contains(event.target as Node)) closeMenu(false); };
    document.addEventListener('pointerdown', outside, true); window.addEventListener('scroll', reposition, true); window.addEventListener('resize', reposition);
    return () => { document.removeEventListener('pointerdown', outside, true); window.removeEventListener('scroll', reposition, true); window.removeEventListener('resize', reposition); };
  });
</script>
{#snippet delivery(item: SessionTask)}
  {#if item.notification === 'failed' || item.notification === 'sending'}
    <div class="task-delivery"><span>{item.notification === 'failed' ? 'Saved · message not sent' : 'Delivery not confirmed'}</span><Button size="xs" disabled={pending || !store.alive} title={item.sendError || undefined} onclick={() => void mutate(['retry', item.id])}>Retry</Button></div>
  {/if}
{/snippet}

{#snippet removal(item: SessionTask)}
  {#if removing === item.id}<div class="task-remove"><span>{item.kind === 'task' ? 'Remove task? Requirements will be kept.' : 'Remove requirement?'}</span><Button size="xs" disabled={pending} onclick={async () => { if (await mutate(['remove', item.id])) removing = ''; }}>Remove</Button><Button size="xs" onclick={() => removing = ''}>Cancel</Button></div>{/if}
{/snippet}

{#snippet itemMenu(item: SessionTask)}
  <button type="button" class="item-menu" aria-label={`${item.kind === 'task' ? 'Task' : 'Requirement'} actions: ${item.text}`} aria-haspopup="menu" aria-expanded={menu?.id === item.id && menu?.kind === 'actions'} disabled={pending || !store.alive} onclick={(event) => openMenu(event, item.id, 'actions')}><Icon name="ellipsis" size={14} /></button>
{/snippet}

{#snippet statusButton(item: SessionTask)}
  <button type="button" class="status-trigger" title={stateLabel(item.state)} aria-label={`Status of ${item.text}: ${stateLabel(item.state)}`} aria-haspopup="menu" aria-expanded={menu?.id === item.id && menu?.kind === 'state'} disabled={pending || !store.alive} onclick={(event) => openMenu(event, item.id, 'state')}><TaskStateIcon state={item.state} /></button>
{/snippet}

{#snippet checkActions(item: SessionTask)}
  {#if item.state === 'checking' && !hasUnmetRequirements(item)}
    <div class="check-actions">
      <button type="button" class="check-confirm" aria-label={`Confirm checked: ${item.text}`} disabled={pending || !store.alive} onclick={() => void mutate(['state', item.id, 'done'])}><Icon name="check" size={14} />Confirm checked</button>
      <button type="button" aria-label={`Give feedback: ${item.text}`} title="Give feedback in the composer" onclick={() => feedback(item)}><Icon name="message-square-text" size={14} />Give feedback</button>
    </div>
  {/if}
{/snippet}

{#snippet itemFeedback(item: SessionTask)}
  {#if item.feedback}
    <details class="item-feedback">
      <summary aria-label={`Latest feedback: ${item.feedback}`}><Icon name="chevron-right" size={12} /><span>{item.feedback}</span></summary>
    </details>
  {/if}
{/snippet}

{#snippet criteria(items: SessionTask[], flat = false)}
  <ul class="task-requirements">
    {#each items as item (item.id)}
      {@const open = expandedRequirements.has(item.id)}
      <li class="requirement-item">
        <div class="requirement-row">
          {@render statusButton(item)}
          <button type="button" class="requirement-title" class:full-title={flat || open || item.state === 'checking'} class:done={item.state === 'done'} aria-expanded={open} onclick={() => toggleRequirement(item.id)}>{item.text}</button>
          {#if !item.parentId}<span class="requirement-scope">{item.scope === 'global' ? 'Global' : 'Unassigned'}</span>{/if}
          {@render itemMenu(item)}
        </div>
        {@render checkActions(item)}
        {#if open}
          {@render itemFeedback(item)}
          <div transition:disclose class="requirement-tools">
            <button type="button" class="text-action" aria-label={`Organize ${item.text}`} aria-haspopup="menu" aria-expanded={menu?.id === item.id && menu?.kind === 'parent'} disabled={pending || !store.alive} onclick={(event) => openMenu(event, item.id, 'parent')}>{item.parentId ? 'Move requirement' : item.scope === 'global' ? 'Change scope' : 'Attach to task'}<Icon name="chevron-down" size={11} /></button>
          </div>
        {/if}
        {@render delivery(item)}{@render removal(item)}
      </li>
    {/each}
  </ul>
{/snippet}

{#snippet taskRow(task: SessionTask)}
  {@const attached = requirements.filter(r => r.parentId === task.id)}
  {@const checks = attached.filter(r => r.state === 'checking')}
  {@const open = expandedTasks.has(task.id)}
  <li class="task-item" data-task-id={task.id}>
    <div class="task-row">
      {@render statusButton(task)}
      <button type="button" class="task-expand" aria-expanded={open} aria-controls={`task-${sessionId}-${task.id}`} onclick={() => toggleTask(task.id)}>
        <span class="task-title" class:full-title={open || task.state === 'checking' || checks.length > 0}>{task.text}</span>
        {#if attached.length}<span class="requirement-count num" title={`${attached.filter(r => r.state === 'done').length} of ${attached.length} requirements met`}>{attached.filter(r => r.state === 'done').length}/{attached.length}</span>{/if}
        <Icon name={open ? 'chevron-down' : 'chevron-right'} size={12} class="text-faint" />
      </button>
      {@render itemMenu(task)}
    </div>
    {@render checkActions(task)}
    {@render delivery(task)}
    {@render removal(task)}
    {#if open || checks.length}
      <div transition:disclose class="task-detail" id={`task-${sessionId}-${task.id}`}>
        {#if open}{@render itemFeedback(task)}{/if}
        {#if checks.length}<p class="pending-checks">{checks.length} {checks.length === 1 ? 'requirement needs' : 'requirements need'} your check</p>{/if}
        {@render criteria(open ? attached : checks)}
        {#if open}<div class="task-tools"><button type="button" class="add-criterion" disabled={pending || !store.alive} onclick={() => add(task.id)}><Icon name="plus" size={12} /> Add requirement</button></div>{/if}
      </div>
    {/if}
  </li>
{/snippet}

{#if session && !isAgentless(session)}
  <section class="session-tasks" aria-label="Session tasks" data-session-tasks>
    <div class="task-toolbar">
      <div class="task-tabs" role="tablist" aria-label="Tasks and requirements">
        {#each [{id:'tasks',label:'Tasks'}, {id:'requirements',label:'Requirements'}] as t, i}
          <button type="button" role="tab" id={`${sessionId}-${t.id}-tab`} aria-controls={`${sessionId}-task-panel`} aria-selected={tab === t.id} tabindex={tab === t.id ? 0 : -1} onclick={() => switchTab(t.id)} onkeydown={(event) => { if (['ArrowLeft','ArrowRight','Home','End'].includes(event.key)) { event.preventDefault(); const next = event.key === 'Home' ? 0 : event.key === 'End' ? 1 : 1-i; switchTab(next ? 'requirements' : 'tasks'); (event.currentTarget.parentElement?.children[next] as HTMLButtonElement)?.focus(); } }}>{t.label}<span class="num">{t.id === 'tasks' ? visibleItems.length ? `${done}/${visibleItems.length}` : '' : unattached.length || ''}</span></button>
        {/each}
      </div>
      <Button size="xs" disabled={pending || !store.alive} onclick={() => add()} aria-label={tab === 'tasks' ? 'Add task' : 'Add requirement'}><Icon name="plus" size={13} /> Add</Button>
    </div>
    <div class="task-body" role="tabpanel" id={`${sessionId}-task-panel`} aria-labelledby={`${sessionId}-${tab}-tab`}>
      {#if composing}
        <form class="task-form" onsubmit={(event) => { event.preventDefault(); void submit(); }}>
          <div class="task-input-row"><input bind:this={input} bind:value={text} maxlength="500" disabled={pending} aria-label="Task or requirement" placeholder={kind === 'task' ? 'What needs to be done?' : 'Add a requirement…'} class="min-w-0 rounded border border-edge bg-canvas text-ink" /><Button size="xs" icon aria-label="Cancel task" disabled={pending} onclick={() => { composing = false; editing = ''; }}><Icon name="x" size={13} /></Button></div>
          {#if kind === 'requirement' && !editing}<button type="button" class="text-action draft-parent" aria-label="Requirement scope" aria-haspopup="menu" onclick={(event) => openMenu(event, '', 'parent')}>{parent ? tasks.find(t => t.id === parent)?.text : globalRequirement ? 'Global requirement' : 'Let the agent organize'}<Icon name="chevron-down" size={11} /></button>{/if}
          <div class="task-form-footer"><span class="task-hint">Sent to your agent.</span><Button type="submit" size="xs" variant="secondary" loading={pending} disabled={!text.trim() || !store.alive}>{editing ? 'Save & send' : 'Add & send'}</Button></div>
        </form>
      {/if}
      {#if error}<p role="alert" class="task-error">{error}</p>{/if}
      {#if !store.alive}<p class="task-hint">Reconnect to change tasks.</p>{/if}
      {#if tab === 'tasks'}
        {#if unattachedChecks.length}
          <div class="unattached-checks">
            <p class="pending-checks">Requirements needing your check</p>
            {@render criteria(unattachedChecks)}
          </div>
        {/if}
        {#if active.length}{#if issueTask}{@render criteria(active, true)}{:else}<ul class="task-list" aria-label="Active tasks">{#each active as task (task.id)}{@render taskRow(task)}{/each}</ul>{/if}
        {:else if !tasks.length && !composing}<p class="task-hint">Add a task to get started.</p>{/if}
        {#if finished.length}<button type="button" class="completed-toggle" aria-expanded={showDone} onclick={() => showDone = !showDone}><Icon name={showDone ? 'chevron-down' : 'chevron-right'} size={12} />Completed <span class="num">{finished.length}</span></button>{#if showDone}<div transition:disclose>{#if issueTask}{@render criteria(finished, true)}{:else}<ul class="task-list" aria-label="Completed tasks">{#each finished as task (task.id)}{@render taskRow(task)}{/each}</ul>{/if}</div>{/if}{/if}
        {#if issueTask}
          {#if issueTask.state === 'checking' && !hasUnmetRequirements(issueTask)}
            <p class="pending-checks">Issue ready for your check</p>
            {@render checkActions(issueTask)}
          {/if}
          {@render delivery(issueTask)}
          <details class="issue-details">
            <summary><Icon name="chevron-right" size={12} />Issue task details</summary>
            <div class="task-row">
              {@render statusButton(issueTask)}
              <span class="issue-title">{issueTask.text}</span>
              {@render itemMenu(issueTask)}
            </div>
            {@render itemFeedback(issueTask)}
            {@render removal(issueTask)}
            <button type="button" class="add-criterion" disabled={pending || !store.alive} onclick={() => add(issueTask.id)}><Icon name="plus" size={12} /> Add requirement</button>
          </details>
        {/if}
      {:else}
        {#if unattached.length}{@render criteria(unattached)}{:else if !composing}<p class="task-hint">Global and unassigned requirements appear here.</p>{/if}
      {/if}
    </div>
  </section>
{/if}

{#if menu}
  <div use:portal bind:this={menuElement} role="menu" aria-label={menu.kind === 'actions' ? `${menuItem?.kind === 'task' ? 'Task' : 'Requirement'} actions` : menu.kind === 'state' ? 'Set status' : 'Organize requirement'} tabindex="-1" class="task-picker" style={`left:${menu.left}px;top:${menu.top}px;width:${menu.width}px;max-height:${menu.height}px`} onkeydown={menuKey}>
    {#each menuOptions as option (option.id)}
      <button type="button" role={menu.kind === 'actions' ? 'menuitem' : 'menuitemradio'} aria-checked={menu.kind === 'actions' ? undefined : menuValue === option.id} tabindex="-1" onclick={() => choose(option.id)}>{#if menu.kind === 'state'}<TaskStateIcon state={option.id} />{/if}<span>{option.label}</span>{#if menu.kind !== 'actions' && menuValue === option.id}<Icon name="check" size={12} />{/if}</button>
    {/each}
  </div>
{/if}

<style>
  .session-tasks { min-width: 0; }
  .issue-details { margin-top: 8px; }
  .issue-details summary { display: flex; align-items: center; gap: 6px; min-height: 28px; list-style: none; cursor: pointer; font-size: 12px; color: var(--color-faint); }
  .issue-details summary::-webkit-details-marker { display: none; }
  .issue-details[open] summary :global(svg) { transform: rotate(90deg); }
  .issue-details summary:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
  .issue-title { flex: 1; min-width: 0; padding-block: 5px; overflow-wrap: anywhere; font-size: 12px; line-height: 20px; }
  .check-actions { display: flex; align-items: center; flex-wrap: wrap; gap: 4px 8px; margin: 0 0 6px 27px; }
  .check-actions button { display: inline-flex; align-items: center; gap: 5px; min-height: 28px; padding: 3px 6px; border-radius: 4px; font-size: 12px; color: var(--color-ink-2); }
  .check-actions .check-confirm { color: var(--color-good); }
  .pending-checks { margin: 4px 0 2px; color: var(--color-warn); font-size: 12px; line-height: 18px; }
  .unattached-checks { margin-bottom: 8px; }
  .check-actions button:hover:not(:disabled) { background: var(--color-sel); }
  .item-feedback { margin: 0 0 2px 27px; min-width: 0; font-size: 12px; line-height: 18px; color: var(--color-ink-2); overflow-wrap: anywhere; }
  .task-detail > .item-feedback { margin-left: 0; }
  .item-feedback summary { display: flex; align-items: flex-start; gap: 5px; padding-block: 4px; cursor: pointer; list-style: none; }
  .item-feedback summary :global(svg) { flex-shrink: 0; margin-top: 3px; }
  .item-feedback summary span { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .item-feedback[open] summary span { white-space: pre-wrap; }
  .item-feedback summary::-webkit-details-marker { display: none; }
  .item-feedback[open] summary :global(svg) { transform: rotate(90deg); }
  .item-feedback summary:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
  .task-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
  .task-tabs { display: flex; min-width: 0; gap: 14px; }
  .task-tabs button { display: flex; align-items: center; gap: 5px; padding: 4px 0 8px; border-bottom: 2px solid transparent; color: var(--color-faint); font-size: 13px; }
  .task-tabs [aria-selected='true'] { border-color: var(--color-accent); color: var(--color-ink); font-weight: 500; }
  .task-tabs .num { font-size: 11px; color: var(--color-faint); font-weight: 400; }
  .task-body { margin-top: 5px; }
  .task-list, .task-requirements { display: grid; gap: 1px; }
  .task-row, .requirement-row { display: flex; align-items: flex-start; gap: 5px; min-height: 30px; }
  .status-trigger { display: grid; place-items: center; width: 22px; height: 30px; flex-shrink: 0; border-radius: 4px; }
  .status-trigger:hover { background: var(--color-sel); }
  .task-expand { display: flex; flex: 1; min-width: 0; align-items: flex-start; gap: 7px; padding: 5px 0; text-align: left; }
  .task-expand :global(svg) { flex-shrink: 0; margin-top: 4px; }
  .task-title, .requirement-title { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; line-height: 20px; text-align: left; }
  .requirement-title { padding: 5px 0; font-size: 12px; color: var(--color-ink-2); }
  .full-title { white-space: normal; overflow-wrap: anywhere; }
  .requirement-title.done { color: var(--color-faint); }
  .requirement-count, .requirement-scope { color: var(--color-faint); font-size: 11px; flex-shrink: 0; }
  .requirement-count { line-height: 20px; }
  .requirement-scope { line-height: 30px; }
  .task-detail { margin: 0 0 6px 10px; padding: 0 0 2px 13px; border-left: 1px solid var(--color-edge); }
  .task-tools, .requirement-tools { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
  .requirement-tools { margin-left: 27px; }
  .task-tools { margin-top: 2px; }
  .item-menu { display: grid; place-items: center; flex-shrink: 0; width: 24px; height: 30px; border-radius: 4px; color: var(--color-faint); }
  .item-menu:hover, .item-menu[aria-expanded='true'] { color: var(--color-ink); background: var(--color-sel); }
  .text-action, .add-criterion { display: inline-flex; min-width: 0; align-items: center; gap: 4px; font-size: 11px; line-height: 24px; color: var(--color-accent); text-align: left; }
  .draft-parent { max-width: 100%; overflow-wrap: anywhere; }
  .completed-toggle { display: flex; align-items: center; gap: 6px; min-height: 28px; margin-top: 5px; font-size: 12px; color: var(--color-faint); }
  .task-form { display: grid; gap: 4px; padding: 4px 0 8px; }
  .task-input-row, .task-form-footer { display: flex; align-items: center; gap: 6px; }
  .task-form input { width: 100%; font-size: 13px; }
  .task-form-footer { justify-content: space-between; }
  .task-hint { margin: 4px 0; font: 400 12px/18px var(--font-sans); letter-spacing: normal; color: var(--color-faint); }
  .task-error { margin: 6px 0; color: var(--color-bad); font-size: 12px; overflow-wrap: anywhere; }
  .task-remove, .task-delivery { display: flex; align-items: center; flex-wrap: wrap; gap: 5px; margin: 5px 0; font-size: 11px; }
  .task-delivery { color: var(--color-warn); }
  .task-picker { position: fixed; z-index: 10000; overflow-y: auto; overscroll-behavior: contain; padding: 4px; border: 1px solid var(--color-edge); border-radius: 7px; background: var(--color-panel); box-shadow: 0 8px 24px #0005; }
  .task-picker button { display: flex; align-items: center; gap: 8px; width: 100%; padding: 6px 8px; min-height: 30px; border-radius: 4px; color: var(--color-ink); font-size: 12px; line-height: 18px; text-align: left; }
  .task-picker button span { flex: 1; min-width: 0; overflow-wrap: anywhere; }
  .task-picker button:hover, .task-picker button:focus { background: var(--color-sel); outline: none; }
  button:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
  button:disabled { opacity: .45; cursor: not-allowed; }
  @container (max-width: 300px) { .task-tabs { gap: 8px; } .task-tabs button { font-size: 12px; } }
</style>
