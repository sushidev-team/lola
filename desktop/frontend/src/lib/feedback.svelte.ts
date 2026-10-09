// The diff tab's comment queue: line comments a human has written against a
// session's diff but not yet sent. "Send to agent" ships the whole batch to the
// daemon (cmd=feedback), which renders it into ONE message and types it only
// into a pane verifiably resting at its prompt — queueing it there otherwise.
//
// The queue is per session and mirrored to localStorage (best-effort, like the
// terminal tab names): a review is minutes of typing, and losing it to an app
// restart or a session switch would be the worst thing this view could do.
import { SvelteMap } from "svelte/reactivity";
import { DaemonService } from "@bindings/desktop";
import type { FeedbackComment } from "@bindings/internal/protocol";
import { store } from "./store.svelte";
import type { Side } from "./diff";

export type Draft = {
  /** Local key, stable across re-renders. */
  key: string;
  path: string;
  side: Side;
  line: number;
  endLine: number;
  quote: string;
  body: string;
};

const KEY = "lola.diff.drafts";

type Saved = Record<string, { drafts: Draft[]; note: string }>;

function load(): Saved {
  try {
    const raw = localStorage.getItem(KEY);
    const v: unknown = raw ? JSON.parse(raw) : null;
    return v && typeof v === "object" ? (v as Saved) : {};
  } catch {
    return {};
  }
}

let seq = 0;

class Feedback {
  private drafts = new SvelteMap<string, Draft[]>();
  private notes = new SvelteMap<string, string>();
  /** Session ids with a send in flight — the button shows it and refuses a second click. */
  private sending = new SvelteMap<string, boolean>();

  constructor() {
    for (const [id, v] of Object.entries(load())) {
      if (Array.isArray(v?.drafts) && v.drafts.length) this.drafts.set(id, v.drafts);
      if (typeof v?.note === "string" && v.note) this.notes.set(id, v.note);
    }
  }

  draftsFor(id: string): Draft[] {
    return this.drafts.get(id) ?? [];
  }

  /** Drafts anchored on one file side + line (the last line of their range). */
  draftsAt(id: string, path: string, side: Side, line: number): Draft[] {
    return this.draftsFor(id).filter((d) => d.path === path && d.side === side && d.endLine === line);
  }

  noteFor(id: string): string {
    return this.notes.get(id) ?? "";
  }

  setNote(id: string, note: string) {
    if (note) this.notes.set(id, note);
    else this.notes.delete(id);
    this.save();
  }

  isSending(id: string): boolean {
    return this.sending.get(id) ?? false;
  }

  /** How many things a send would deliver (comments + a non-blank note). */
  count(id: string): number {
    return this.draftsFor(id).length + (this.noteFor(id).trim() ? 1 : 0);
  }

  add(id: string, d: Omit<Draft, "key">) {
    if (!d.body.trim()) return;
    this.drafts.set(id, [...this.draftsFor(id), { ...d, key: `${Date.now()}-${seq++}` }]);
    this.save();
  }

  remove(id: string, key: string) {
    const next = this.draftsFor(id).filter((d) => d.key !== key);
    if (next.length) this.drafts.set(id, next);
    else this.drafts.delete(id);
    this.save();
  }

  clear(id: string) {
    this.drafts.delete(id);
    this.notes.delete(id);
    this.save();
  }

  /** Ship the batch. Cleared only once the daemon has ACCEPTED it (delivered or
   * queued on its side) — a refused or failed send keeps every draft. */
  async send(id: string): Promise<boolean> {
    if (this.isSending(id) || this.count(id) === 0) return false;
    const comments: FeedbackComment[] = this.draftsFor(id).map((d) => ({
      path: d.path,
      line: d.line,
      endLine: d.endLine > d.line ? d.endLine : 0,
      side: d.side,
      quote: d.quote,
      body: d.body,
    }));
    this.sending.set(id, true);
    try {
      const r = await DaemonService.SendFeedback({ session: id, comments, note: this.noteFor(id) });
      this.clear(id);
      store.setFlash(
        r?.delivered ? "feedback sent to the agent" : "feedback queued — it is sent when the agent is at its prompt",
        r?.delivered ? "good" : "warn",
      );
      void store.refresh();
      return true;
    } catch (err) {
      store.setFlash(String(err), "bad");
      return false;
    } finally {
      this.sending.delete(id);
    }
  }

  private save() {
    try {
      const all: Saved = {};
      const ids = new Set([...this.drafts.keys(), ...this.notes.keys()]);
      for (const id of ids) all[id] = { drafts: this.draftsFor(id), note: this.noteFor(id) };
      localStorage.setItem(KEY, JSON.stringify(all));
    } catch {
      /* storage unavailable — the queue still works for this run */
    }
  }
}

export const feedback = new Feedback();
