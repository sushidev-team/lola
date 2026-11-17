import { DaemonService } from '@bindings/desktop';
import type { PRReadinessData } from '@bindings/internal/protocol';

// The review score and PR actions need the same facts. Share concurrent reads.
// `readPRReadiness` never answers from a completed result: Refresh must still ask
// GitHub again. It does remember the last answer, so a second view of the same PR
// opened right after (the phone's checks sheet beside its Review segment) can reuse
// it through `readPRReadinessRecent` instead of spending another GitHub read; a
// phone's external-API budget is small.
const pending = new Map<string, Promise<PRReadinessData>>();
const recent = new Map<string, { at: number; data: PRReadinessData }>();
const RECENT_MAX = 32;

export function readPRReadiness(session:string, number:number): Promise<PRReadinessData> {
  const key = JSON.stringify([session,number]);
  const existing = pending.get(key);
  if (existing) return existing;
  const request = Promise.resolve(DaemonService.PRReadiness(session,number)).then(data => {
    recent.delete(key);
    recent.set(key,{at:Date.now(),data});
    if (recent.size > RECENT_MAX) recent.delete(recent.keys().next().value!);
    return data;
  }).finally(() => pending.delete(key));
  pending.set(key,request);
  return request;
}

/** The last answer when it is at most `maxAgeMs` old, else a fresh read. */
export function readPRReadinessRecent(session:string, number:number, maxAgeMs:number): Promise<PRReadinessData> {
  const hit = recent.get(JSON.stringify([session,number]));
  if (hit && Date.now() - hit.at <= maxAgeMs) return Promise.resolve(hit.data);
  return readPRReadiness(session,number);
}
