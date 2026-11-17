// The scripted daemon the performance tests share: a FakeChannel that
// acknowledges every watch (recording the stream id per topic, so a test can
// push on it) and answers every other request with an empty success. The
// transport is the app's real ChannelTransport; nothing above it is faked.

import { ChannelTransport } from "../wailsshim/channeltransport";
import { FRAME_RESP, FakeChannel, type Frame } from "../wire";

export interface Daemon {
  ch: FakeChannel;
  transport: ChannelTransport;
  /** Topic to the stream id of its latest `watch`. */
  ids: Map<string, string>;
  /** Every `req` frame sent so far. */
  reqs(): Frame[];
  /** Pushes one event on `topic`'s current stream. */
  push(topic: string, seq: number, payload: unknown): void;
}

export async function daemon(answer: (f: Frame) => unknown = () => ({})): Promise<Daemon> {
  const ch = new FakeChannel();
  const ids = new Map<string, string>();
  ch.onSend = (f: Frame) => {
    if (f.type !== "req") return;
    if (f.cmd === "watch") {
      const topics = (f.payload as { args: { topics: string[] } }).args.topics;
      for (const t of topics) ids.set(t, f.id!);
      ch.deliver({
        v: 2,
        type: FRAME_RESP,
        id: f.id,
        payload: { ok: true, data: { topics: topics.map((topic) => ({ topic, ok: true })) } },
      });
      return;
    }
    ch.deliver({ v: 2, type: FRAME_RESP, id: f.id, payload: { ok: true, data: answer(f) } });
  };
  const transport = new ChannelTransport({ open: async () => ch });
  await transport.connect({ daemonId: "d" });
  return {
    ch,
    transport,
    ids,
    reqs: () => ch.sent.filter((f) => f.type === "req"),
    push: (topic, seq, payload) => ch.deliverEvent(ids.get(topic)!, topic, seq, payload),
  };
}

/**
 * An IntersectionObserver that reports an element near the viewport only while
 * `near(el)` says so, so a test can model a phone screen showing the last few
 * turns of a long story. `flush()` re-reports every observed element.
 */
export class ScriptedIntersectionObserver {
  static instances: ScriptedIntersectionObserver[] = [];
  static near: (el: Element) => boolean = () => true;
  private readonly els = new Set<Element>();
  constructor(private readonly cb: IntersectionObserverCallback) {
    ScriptedIntersectionObserver.instances.push(this);
  }
  observe(el: Element): void {
    this.els.add(el);
    this.report([el]);
  }
  unobserve(el: Element): void {
    this.els.delete(el);
  }
  disconnect(): void {
    this.els.clear();
  }
  takeRecords(): IntersectionObserverEntry[] {
    return [];
  }
  flush(): void {
    this.report([...this.els]);
  }
  private report(els: Element[]): void {
    const entries = els.map((target) => ({ target, isIntersecting: ScriptedIntersectionObserver.near(target) }) as IntersectionObserverEntry);
    queueMicrotask(() => this.cb(entries, this as unknown as IntersectionObserver));
  }
  static flushAll(): void {
    for (const o of ScriptedIntersectionObserver.instances) o.flush();
  }
}

/** Element-level mutation records under `root` while `fn` runs, after one settle. */
export async function mutationsDuring(root: Node, fn: () => Promise<void>): Promise<MutationRecord[]> {
  const records: MutationRecord[] = [];
  const mo = new MutationObserver((rs) => records.push(...rs));
  mo.observe(root, { subtree: true, childList: true, attributes: true, characterData: true });
  await fn();
  records.push(...mo.takeRecords());
  mo.disconnect();
  return records;
}

/** The closest ancestor (or self) matching `sel` of a mutation's target, or null. */
export function ownerOf(r: MutationRecord, sel: string): Element | null {
  const n = r.target;
  const el = n.nodeType === Node.ELEMENT_NODE ? (n as Element) : n.parentElement;
  return el?.closest(sel) ?? null;
}

/** Prints a measurement when LOLA_PERF_LOG=1 (`LOLA_PERF_LOG=1 npx vitest run src/perf`); silent otherwise. */
export function report(line: string): void {
  if (process.env.LOLA_PERF_LOG === "1") process.stdout.write(`\nPERF ${line}\n`);
}
