<script lang="ts">
  import { IssueBrief } from '@bindings/desktop/linearservice';
  import { store } from '$lib/store.svelte';
  import { ssh } from '$lib/ssh.svelte';
  import { plainInline } from '$lib/answerblocks';
  import Icon from '../Icon.svelte';
  import Button from '../Button.svelte';

  let { sessionId }: { sessionId: string } = $props();
  const session = $derived(store.sessionById(sessionId));
  const issue = $derived(session?.issue ?? '');
  let loaded = $state<{ issue: string; title: string; description: string } | null>(null);
  let loading = $state(false);
  let failed = $state(false);
  let expanded = $state(false);
  let retry = $state(0);

  $effect(() => {
    const identifier = issue;
    sessionId; retry;
    let cancelled = false;
    loaded = null;
    expanded = false;
    failed = false;
    loading = !!identifier;
    if (identifier) {
      void IssueBrief(identifier).then(brief => {
        if (!cancelled) loaded = { issue: identifier, ...brief };
      }).catch(() => {
        if (!cancelled) failed = true;
      }).finally(() => {
        if (!cancelled) loading = false;
      });
    }
    return () => { cancelled = true; };
  });

  const current = $derived(loaded?.issue === issue ? loaded : null);
  const title = $derived(current?.title || session?.title || 'Session task');
  const opening = $derived(ssh.view(sessionId).items.find(item => item.human?.origin === 'initial' && !item.human.recalled)?.human?.text);
  const taskOutline = $derived((session?.tasks ?? []).filter(task => task.kind === 'task' && task.author === 'user').map(task => task.text).join('\n'));
  // Task intent stays separate from progress summaries and the agent's latest response.
  const source = $derived(issue ? current?.description || '' : opening || taskOutline);
  const description = $derived(source.split('\n').map(plainInline).join('\n').trim());
  const condensed = $derived(description.replace(/\s+/g, ' '));
  const long = $derived(condensed.length > 280);
  const preview = $derived(long ? condensed.slice(0, 280).replace(/\s+\S*$/, '') + '…' : condensed);
</script>

<section aria-label="Session task summary" data-side-brief>
  <div class="brief-heading">
    <h3 class="cv-overview-heading">{title}</h3>
    {#if issue}
      <Button variant="accent" size="xs" aria-label="Open {issue} in Linear" title="Open in Linear"
        onclick={() => store.openURL(`https://linear.app/issue/${encodeURIComponent(issue)}`)}>{issue}<Icon name="external-link" size={12} /></Button>
    {/if}
  </div>
  {#if description}
    <p class="brief-description selectable" class:expanded>{expanded ? description : preview}</p>
    {#if long}<Button size="xs" aria-expanded={expanded} onclick={() => expanded = !expanded}>{expanded ? 'Show less' : 'Show more'}<Icon name={expanded ? 'chevron-up' : 'chevron-down'} size={12} /></Button>{/if}
  {:else if loading}
    <p class="brief-status" role="status">Loading task description…</p>
  {:else if failed}
    <div class="brief-status" role="status">Could not load the Linear description. <Button size="xs" onclick={() => retry++}>Retry</Button></div>
  {/if}
</section>

<style>
  .brief-heading { display:flex;align-items:baseline;justify-content:space-between;gap:12px; }
  .brief-heading h3 { min-width:0; }
  .brief-description { margin-top:8px;font-size:12px;line-height:19px;color:var(--color-ink-2);overflow-wrap:anywhere; }
  .brief-description.expanded { white-space:pre-line; }
  .brief-status { margin-top:6px;font-size:11px;line-height:18px;color:var(--color-faint); }
</style>
