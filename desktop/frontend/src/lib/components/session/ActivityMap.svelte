<script lang="ts">
  import { feed } from '$lib/feed.svelte';
  import { ssh, sshClock } from '$lib/ssh.svelte';
  import { store } from '$lib/store.svelte';
  import { sessionActivity, activitySegments, activityDuration, activityStatus } from '$lib/sessionactivity';
  import { tooltip } from '$lib/tooltip';
  let { sessionId }: { sessionId: string } = $props();
  let focusId = $state('');
  const f = $derived(feed.view(sessionId));
  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const events = $derived(sessionActivity({ feed: f.items, items: view.items, scope: view.scope, alive: ['working', 'starting', 'waiting_input'].includes(session?.agentState ?? '') }));
  const map = $derived(activitySegments(events, sshClock.now));
  const tabId = $derived(map.find(e => e.event.id === focusId)?.event.id ?? map.at(-1)?.event.id);
  function jump(id: string) {
    view.filter = 'everything';
    view.cursor = id;
    view.jump = { block: `item:${id}`, n: (view.jump?.n ?? 0) + 1 };
  }
  function navigate(event: KeyboardEvent, index: number) {
    const next = event.key === 'ArrowLeft' ? index - 1 : event.key === 'ArrowRight' ? index + 1 : event.key === 'Home' ? 0 : event.key === 'End' ? map.length - 1 : null;
    if (next == null) return;
    event.preventDefault();
    const buttons = (event.currentTarget as HTMLElement).parentElement?.querySelectorAll<HTMLButtonElement>('button');
    buttons?.[Math.max(0, Math.min(map.length - 1, next))]?.focus();
  }
  function label(e: typeof map[number]) {
    return `${e.event.label} · ${new Date(e.event.at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}${e.elapsed ? ` · ${activityDuration(e.elapsed)}${e.event.status === 'running' ? ' so far' : ''}` : ' · duration not recorded'}`;
  }
</script>

{#if map.length}
  <nav class="cv-activity-map" aria-label={f.more ? 'Activity map of loaded recent history' : 'Conversation activity map'}>
    <div class="cv-activity-track" style={`--activity-count:${map.length}`} aria-label="Entries sized by duration with pauses compressed; arrow keys move between entries">
      {#each map as entry, i (entry.event.id)}
        {@const e = entry.event}
        <button type="button" class="cv-activity-band" data-activity-kind={e.kind} data-failed={e.status === 'error'} data-running={e.status === 'running'} aria-current={e.id === view.cursor ? 'location' : undefined}
          style={`flex-grow:${entry.elapsed};--activity-pause:${entry.gapMs > 0 ? 5 : 0}px`}
          tabindex={tabId === e.id ? 0 : -1} onfocus={() => focusId = e.id} onkeydown={event => navigate(event, i)}
          aria-label={label(entry)} use:tooltip={{ instant: true, text: e.label, heading: e.kind === 'tests' ? 'Tests' : e.kind === 'files' ? 'Files' : e.kind === 'commands' ? 'Command' : 'Agent update', meta: [e.status ? activityStatus(e.status) : '', entry.elapsed ? activityDuration(entry.elapsed) : 'Duration not recorded', entry.gapMs ? `${activityDuration(entry.gapMs)} pause before` : ''].filter(Boolean).join(' · '), tone: e.kind }} onclick={() => jump(e.id)}></button>
      {/each}
    </div>
  </nav>
{/if}
