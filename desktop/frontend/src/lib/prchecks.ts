import type { PRCheck } from '@bindings/internal/protocol';

// The per-check list of a pull request (prReadiness `checkList`), grouped for display.
// Shared by the desktop and the phone's checks sheet.
//
// Invariants:
//  - Display only. Nothing here feeds a merge decision; the daemon's CI verdict stays the truth.
//  - Review gates (lola/* statuses) are never counted as CI: they get their own section.
//  - A bucket this code does not know is shown as running, never as passed.
//  - Check names, workflows and descriptions are untrusted text from GitHub or a third party.
//    `fixRequest` names failing checks only, quoted, and never includes a description.

export type CheckBucket = 'failing' | 'running' | 'passed' | 'skipped';

export interface CheckGroups {
  failing: PRCheck[];
  running: PRCheck[];
  passed: PRCheck[];
  skipped: PRCheck[];
  /** lola/* review statuses: a review verdict, never CI. */
  gates: PRCheck[];
}

export interface CheckSummary {
  failed: number;
  running: number;
  passed: number;
  skipped: number;
  /** CI entries only, gates excluded. */
  total: number;
}

export function bucketOf(c: PRCheck): CheckBucket {
  switch (c.bucket) {
    case 'fail': return 'failing';
    case 'pass': return 'passed';
    case 'skipped': return 'skipped';
    default: return 'running';
  }
}

/** Groups the list; within a group, required checks come first and GitHub's order is kept. */
export function groupChecks(list: readonly PRCheck[] | null | undefined): CheckGroups {
  const out: CheckGroups = { failing: [], running: [], passed: [], skipped: [], gates: [] };
  for (const c of list ?? []) {
    if (c.gate) out.gates.push(c);
    else out[bucketOf(c)].push(c);
  }
  const requiredFirst = (a: PRCheck, b: PRCheck) => Number(!!b.required) - Number(!!a.required);
  for (const k of ['failing', 'running', 'passed', 'skipped', 'gates'] as const) out[k].sort(requiredFirst);
  return out;
}

export function checkSummary(list: readonly PRCheck[] | null | undefined): CheckSummary {
  const g = groupChecks(list);
  const s = { failed: g.failing.length, running: g.running.length, passed: g.passed.length, skipped: g.skipped.length };
  return { ...s, total: s.failed + s.running + s.passed + s.skipped };
}

/** "45s", "3m 12s", "1h 4m"; "" when the check has not started. A running check counts to `now`. */
export function checkDuration(c: PRCheck, now: number = Date.now()): string {
  const start = c.startedAt ? Date.parse(c.startedAt) : NaN;
  if (!Number.isFinite(start)) return '';
  const end = c.completedAt ? Date.parse(c.completedAt) : now;
  if (!Number.isFinite(end) || end < start) return '';
  const secs = Math.round((end - start) / 1000);
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ${secs % 60}s`;
  return `${Math.floor(mins / 60)}h ${mins % 60}m`;
}

/** The check's own page when it is an absolute https URL with a host, else "". The daemon checks this too. */
export function checkURL(c: PRCheck): string {
  if (!c.url) return '';
  try {
    const u = new URL(c.url);
    return u.protocol === 'https:' && u.hostname !== '' && !u.username && !u.password ? u.href : '';
  } catch { return ''; }
}

/** At most this many failing checks are named in a fix request. */
export const FIX_REQUEST_NAMES = 10;

/**
 * The sentence lola sends when a person asks the agent to fix failing CI. It names the
 * failing checks (quoted, so a name cannot read as an instruction) and nothing else.
 */
export function fixRequest(failing: readonly PRCheck[]): string {
  const names = [...new Set(failing.filter(c => !c.gate && bucketOf(c) === 'failing').map(c => c.name.replace(/\s+/g, ' ').trim()).filter(Boolean))];
  if (!names.length) return '';
  const shown = names.slice(0, FIX_REQUEST_NAMES).map(n => JSON.stringify(n));
  const more = names.length > shown.length ? `, and ${names.length - shown.length} more` : '';
  if (names.length === 1) return `The CI check ${shown[0]} is failing on the pull request. Find the cause of the failure, fix it, and push.`;
  return `These CI checks are failing on the pull request: ${shown.join(', ')}${more}. Find the cause of each failure, fix it, and push.`;
}
