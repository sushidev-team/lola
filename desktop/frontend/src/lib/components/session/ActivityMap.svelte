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
  const selectedId = $derived(view.jump?.expand === false ? view.jump.block.replace(/^item:/, '') : view.cursor);
  let drag = $state<{ pointer: number; last: string; bounds: { id: string; left: number; right: number; button: HTMLButtonElement }[] } | null>(null);
  function jump(id: string) {
    focusId = id;
    view.filter = 'everything';
    view.jump = { block: `item:${id}`, n: (view.jump?.n ?? 0) + 1, expand: false };
  }
  function nearest(bounds: { id: string; left: number; right: number; button: HTMLButtonElement }[], x: number) {
    const distance = (b: { left: number; right: number }) => Math.max(b.left - x, x - b.right, 0);
    return bounds.reduce((best, b) => distance(b) < distance(best) ? b : best);
  }
  function startDrag(event: PointerEvent) {
    if (event.button !== 0 || event.isPrimary === false) return;
    const track = event.currentTarget as HTMLElement;
    const buttons = Array.from(track.querySelectorAll<HTMLButtonElement>('button'));
    if (!buttons.length) return;
    const bounds = buttons.map(button => {
      const rect = button.getBoundingClientRect();
      return { id: button.dataset.activityId!, left: rect.left, right: rect.right, button };
    });
    const target = nearest(bounds, event.clientX);
    drag = { pointer: event.pointerId, last: target.id, bounds };
    event.preventDefault();
    target.button.focus({ preventScroll: true });
    track.setPointerCapture(event.pointerId);
    jump(target.id);
  }
  function scrub(event: PointerEvent) {
    if (!drag || event.pointerId !== drag.pointer) return;
    const target = nearest(drag.bounds, event.clientX);
    if (target.id === drag.last) return;
    drag.last = target.id;
    jump(target.id);
  }
  function finishDrag(event: PointerEvent) {
    if (!drag || event.pointerId !== drag.pointer) return;
    const last = drag.bounds.find(b => b.id === drag?.last)?.button;
    drag = null;
    if (event.type === 'pointerup') last?.focus({ preventScroll: true });
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
    <div role="group" onpointerdown={startDrag} onpointermove={scrub} onpointerup={finishDrag} onpointercancel={finishDrag} onlostpointercapture={finishDrag} class="cv-activity-track" class:cv-activity-scrubbing={!!drag} style={`--activity-count:${map.length}`} aria-label="Entries sized by duration with pauses compressed; arrow keys move between entries">
      {#each map as entry, i (entry.event.id)}
        {@const e = entry.event}
        <button type="button" class="cv-activity-band" data-activity-id={e.id} data-activity-kind={e.kind} data-failed={e.status === 'error'} data-running={e.status === 'running'} aria-current={e.id === selectedId ? 'location' : undefined}
          style={`flex-grow:${entry.elapsed};--activity-pause:${entry.gapMs > 0 ? 5 : 0}px`}
          tabindex={tabId === e.id ? 0 : -1} onfocus={() => focusId = e.id} onkeydown={event => navigate(event, i)}
          aria-label={label(entry)} use:tooltip={drag ? '' : { instant: true, text: e.label, heading: e.kind === 'tests' ? 'Tests' : e.kind === 'files' ? 'Files' : e.kind === 'commands' ? 'Command' : 'Agent update', meta: [e.status ? activityStatus(e.status) : '', entry.elapsed ? activityDuration(entry.elapsed) : 'Duration not recorded', entry.gapMs ? `${activityDuration(entry.gapMs)} pause before` : ''].filter(Boolean).join(' · '), tone: e.kind }} onclick={event => { if (event.detail === 0) jump(e.id); }}></button>
      {/each}
    </div>
  </nav>
{/if}
