import { describe, expect, it } from 'vitest';
import { sessionActivity, activitySegments, isTestCommand } from './sessionactivity';
import type { AgentFeedItem } from '@bindings/internal/protocol';
const tool = (id: string, command: string, status = 'ok'): AgentFeedItem => ({ id, kind: 'tool', order: Number(id), seq: Number(id), tool: { name: 'Bash', category: 'run', summary: command, status } });

describe('activity derived from the conversation', () => {
  it('recognizes test executables but not quoted mentions, filenames, or help', () => {
    for (const command of ['npm test', 'pnpm run test:unit', 'cd app && npm test', 'GOCACHE=/tmp go test ./...', 'npx playwright test', 'python3 -m pytest', 'make check']) expect(isTestCommand(command), command).toBe(true);
    for (const command of ['cat test.ts', 'echo "&& npm test"', 'git commit -m "run pytest"', 'npm test --help', 'node test-helper.js', 'cat <<EOF\nnpm test\nEOF']) expect(isTestCommand(command), command).toBe(false);
  });
  it('keeps individual tool anchors inside folded runs and preserves missing results', () => {
    const feed = Array.from({ length: 10 }, (_, i) => tool(String(i), 'npm test', i === 9 ? 'running' : 'ok'));
    const events = sessionActivity({ feed, items: [], alive: false });
    expect(events.map(e => e.id)).toEqual(feed.map(e => e.id));
    expect(events.every(e => e.kind === 'tests')).toBe(true);
    expect(events.at(-1)?.status).toBe('no_result');
  });
  it('preserves duration weights, compressible gaps, and concurrent calls in one sequence', () => {
    const events = [
      { id: 'a', kind: 'commands' as const, label: 'A', at: 1000, status: 'ok', durationMs: 2000 },
      { id: 'b', kind: 'tests' as const, label: 'B', at: 2000, status: 'ok', durationMs: 1000 },
      { id: 'c', kind: 'agent' as const, label: 'C', at: 10000, status: '' },
      { id: 'missing', kind: 'agent' as const, label: '?', at: NaN, status: '' },
    ];
    const map = activitySegments(events, 11000);
    expect(map.map(e => e.event.id)).toEqual(['a', 'b', 'c']);
    expect(map[0].elapsed).toBe(map[1].elapsed * 2);
    expect(map.map(e => e.gapMs)).toEqual([0, 0, 7000]);
    expect(map[2].elapsed).toBe(0);
    expect(activitySegments([], 0)).toEqual([]);
  });
  it('keeps simultaneous untimed entries individually reachable', () => {
    const map = activitySegments(['a', 'b', 'c'].map(id => ({ id, kind: 'agent', label: id, at: 1000, status: '' })), 2000);
    expect(map.map(e => e.event.id)).toEqual(['a', 'b', 'c']);
    expect(map.map(e => e.gapMs)).toEqual([0, 0, 0]);
  });
  it('extends a running command to now without extending completed commands', () => {
    const map = activitySegments([{ id: 'a', kind: 'tests', label: 'test', at: 1000, status: 'running' }], 6000);
    expect(map[0].elapsed).toBe(5000);
  });
});
