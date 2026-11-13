import { toMillis } from './time';
import { commands } from './toolActivity';
import { buildStory, flatRows, type StoryInput, type StoryRow } from './turns';

export const ACTIVITY_KINDS = ['agent', 'commands', 'files', 'tests'] as const;
export type ActivityKind = typeof ACTIVITY_KINDS[number];
export type ActivityEvent = { id: string; kind: ActivityKind; label: string; at: number; status: string; durationMs?: number };

// Display classification only. Match executable positions, never a quoted mention of a test.
export function isTestCommand(command: string): boolean {
  if (/<<|`|\$\(/.test(command)) return false; // Do not classify script bodies or substitutions.
  return commands(command).some(words => {
    let rest = words.slice();
    if (rest[0] === 'env') rest.shift();
    while (/^[A-Za-z_]\w*=/.test(rest[0] ?? '')) rest.shift();
    if (rest.includes('--help') || rest.includes('--version')) return false;
    if (rest[0] === 'npx') rest.shift();
    const [cmd, arg, next] = rest;
    if (['npm', 'pnpm', 'yarn', 'bun'].includes(cmd)) return /^test(?::|$)/.test(arg === 'run' ? next ?? '' : arg ?? '');
    return ['vitest', 'jest', 'pytest'].includes(cmd) ||
      (['go', 'cargo', 'playwright'].includes(cmd) && arg === 'test') ||
      (/^python[\d.]*$/.test(cmd) && arg === '-m' && ['pytest', 'unittest'].includes(next)) ||
      (cmd === 'make' && ['test', 'check'].includes(arg));
  });
}

export function sessionActivity(input: StoryInput): ActivityEvent[] {
  const result: ActivityEvent[] = [];
  const sourceTimes = new Map(input.feed.map(e => [e.id, toMillis(e.at)]));
  function take(row: StoryRow) {
    if (row.kind === 'run') { row.items.forEach(take); return; }
    if (row.kind === 'tool') {
      const t = row.tool;
      const kind: ActivityKind = t.category === 'run' && isTestCommand(t.detail || t.summary) ? 'tests'
        : t.category === 'read' || t.category === 'edit' ? 'files' : t.category === 'run' ? 'commands' : 'agent';
      result.push({ id: row.id, kind, label: `${row.verb} ${row.target}`, at: row.at, status: row.status, durationMs: t.durationMs });
    } else if (row.kind === 'prose' || row.kind === 'moment') {
      result.push({ id: row.id, kind: 'agent', label: row.text.replace(/[#*`]/g, '').trim().split('\n')[0] || 'Agent update', at: sourceTimes.get(row.id) ?? row.at, status: '' });
    }
  }
  flatRows(buildStory(input)).forEach(take);
  return result;
}

export type ActivitySegment = { event: ActivityEvent; elapsed: number; gapMs: number };

/** One chronological row weighted by execution duration. Idle gaps are metadata for a capped
 * separator, never part of the weight. Concurrent calls remain individually navigable. */
export function activitySegments(events: ActivityEvent[], now: number): ActivitySegment[] {
  const timed = events.filter(e => Number.isFinite(e.at)).slice().sort((a, b) => a.at - b.at);
  let coveredUntil = timed[0]?.at ?? 0;
  return timed.map(event => {
    const elapsed = event.status === 'running' ? Math.max(0, now - event.at)
      : Number.isFinite(event.durationMs) ? Math.max(0, event.durationMs ?? 0) : 0;
    const gapMs = Math.max(0, event.at - coveredUntil);
    coveredUntil = Math.max(coveredUntil, event.at + elapsed);
    return { event, elapsed, gapMs };
  });
}
export function activityDuration(ms?: number): string {
  if (!ms || ms < 0) return '';
  return ms < 1000 ? '<1s' : ms < 60000 ? `${Math.round(ms / 1000)}s` : `${Math.round(ms / 60000)}m`;
}
export function activityStatus(status: string): string {
  return ({ ok: 'Completed', error: 'Failed', running: 'Running', interrupted: 'Interrupted', no_result: 'No result' } as Record<string, string>)[status] || 'Unknown';
}
