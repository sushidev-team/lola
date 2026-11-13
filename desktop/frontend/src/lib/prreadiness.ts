import { DaemonService } from '@bindings/desktop';
import type { PRReadinessData } from '@bindings/internal/protocol';

// The review score and PR actions need the same facts. Share concurrent reads,
// but never cache completed results: Refresh must still ask GitHub again.
const pending = new Map<string, Promise<PRReadinessData>>();
export function readPRReadiness(session:string, number:number): Promise<PRReadinessData> {
  const key = JSON.stringify([session,number]);
  const existing = pending.get(key);
  if (existing) return existing;
  const request = Promise.resolve(DaemonService.PRReadiness(session,number)).finally(() => pending.delete(key));
  pending.set(key,request);
  return request;
}
