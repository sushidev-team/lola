<script lang="ts">
  import type { SessionInfo } from "$lib/store.svelte";
  import { boardStale } from "$lib/board";
  import { activityStatus, type ActivityEvent } from "$lib/sessionactivity";
  import { tooltip } from "$lib/tooltip";
  import Icon from "../Icon.svelte";
  import LocalChecks from "../LocalChecks.svelte";
  import TimeAgo from "../TimeAgo.svelte";

  let { session, tests, onJump }: { session?: SessionInfo; tests: ActivityEvent[]; onJump: (id: string) => void } = $props();
  let expandedSession = $state<string | null>(null);
  const showOlder = $derived(expandedSession === (session?.id ?? 'local'));
  const olderCount = $derived(Math.max(0, tests.length - 3));
  const checks = $derived(session?.board?.checks ?? []);
  const board = $derived(session?.board);
  const stale = $derived(boardStale(board));
  const live = $derived(!stale && ['working', 'starting'].includes(session?.agentState ?? ''));
  const passing = $derived(checks.filter(c => c.state === 'pass').length);
  const failing = $derived(checks.filter(c => c.state === 'fail').length);
</script>

<section data-side-checks>
  <h3 class="cv-overview-heading mb-2">Local checks</h3>
  <div role="region" aria-label="Local checks">
      {#if board?.mismatches?.length}
        <div class="mb-3 grid gap-1 text-warn" role="status">
          <span class="font-medium">Claim not backed by evidence</span>
          {#each board.mismatches as mismatch}<p class="selectable break-words">{mismatch}</p>{/each}
        </div>
      {/if}
      {#if checks.length}
        <div class="checks-summary">
          <span class="num {failing ? 'text-bad' : passing === checks.length ? 'text-good' : 'text-faint'}">{failing ? `${failing} failed` : `${passing}/${checks.length} passed`}</span>
          <TimeAgo at={board?.updatedAt} class="checks-age" hint="When the agent last reported these checks" />
        </div>
        <LocalChecks {checks} {live} />
        <p class="checks-footnote">Agent-reported results{stale ? ' · older report' : ''}</p>
      {/if}
      {#if tests.length}
        <ul class="cv-overview-events">
          {#each (showOlder ? tests.slice() : tests.slice(-3)).reverse() as t (t.id)}
            <li><button type="button" onclick={() => onJump(t.id)} use:tooltip={{ text: t.label, heading: 'Test command', meta: activityStatus(t.status), tone: 'tests' }}>
              <span class="check-command">{t.label.replace(/^Ran /, '')}</span>
              {#if t.status === 'ok'}<span class="check-completed text-good"><Icon name="check" size={16} /><span class="sr-only">Completed</span></span>
              {:else}<span class="text-xs {t.status === 'error' ? 'text-bad' : 'text-ink-2'}">{activityStatus(t.status)}</span>{/if}
            </button></li>
          {/each}
        </ul>
        {#if olderCount > 0}
          <button type="button" class="older-checks" aria-expanded={showOlder} onclick={() => expandedSession = showOlder ? null : session?.id ?? 'local'}>{showOlder ? 'Show fewer' : `Show ${olderCount} older ${olderCount === 1 ? 'command' : 'commands'}`}<Icon name={showOlder ? 'chevron-up' : 'chevron-down'} size={12} /></button>
        {/if}
      {:else if !checks.length}<p class="text-faint">No local checks reported or test commands in loaded history.</p>{/if}

  </div>
</section>


<style>
  section, [role="region"], .cv-overview-events, .cv-overview-events li { min-width:0; }
  .cv-overview-events { grid-template-columns:minmax(0,1fr); }
  .cv-overview-events button { align-items:flex-start; }
  .check-command { flex:1; min-width:0; white-space:normal; overflow-wrap:anywhere; line-height:18px; }

  .older-checks { display: inline-flex; align-items: center; gap: 5px; margin-top: 6px; padding: 3px 0; font-size: 12px; color: var(--color-faint); }
  .older-checks:hover { color: var(--color-accent); }
  .older-checks:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 3px; border-radius: 2px; }
  .check-completed { display: inline-flex; align-items: center; justify-content: center; flex-shrink: 0; width: 20px; }
  .checks-summary { display: flex; align-items: baseline; justify-content: space-between; gap: 8px; margin: 2px 0 4px; font-size: 12px; }
  :global(.checks-age) { color: var(--color-faint); font-size: 11px; line-height: 16px; font-weight: 400; }
  .checks-footnote { margin-top: 8px; color: var(--color-faint); font-size: 11px; line-height: 16px; }
</style>
