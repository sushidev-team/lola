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
import type { FeedbackComment, FeedbackElement } from "@bindings/internal/protocol";
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

export type ElementDraft = FeedbackElement & { reviewKey?: string; images?: string[] };

// Conversation and the agent both recognize the session-local image paths in
// the message. Keep them separate from editable prose until the note is sent.
export function elementForAgent({ reviewKey, images, ...element }: ElementDraft): FeedbackElement {
  if (!images?.length) return element;
  const note = [element.note, ...images].filter(Boolean).join("\n");
  // renderFeedbackElement clips at feedbackMaxBodyRunes. Never truncate a path.
  if (Array.from(note).length > 4000) throw new Error("Send these images in smaller batches so each attachment reaches the agent.");
  return { ...element, note };
}

type Saved = Record<string, { drafts: Draft[]; note: string; elements?: ElementDraft[] }>;

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
  private elements = new SvelteMap<string, ElementDraft[]>();
  /** Session ids with a send in flight — the button shows it and refuses a second click. */
  private sending = new SvelteMap<string, boolean>();

  constructor() {
    for (const [id, v] of Object.entries(load())) {
      if (Array.isArray(v?.drafts) && v.drafts.length) this.drafts.set(id, v.drafts);
      if (typeof v?.note === "string" && v.note) this.notes.set(id, v.note);
      if (Array.isArray(v?.elements) && v.elements.length) this.elements.set(id, v.elements);
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

  elementsFor(id: string): ElementDraft[] {
    return this.elements.get(id) ?? [];
  }

  addElement(id: string, element: ElementDraft, previous?: ElementDraft) {
    const elements = this.elementsFor(id);
    if (previous && elements.includes(previous)) {
      this.elements.set(id, elements.map(e => e === previous ? element : e));
      this.save();
      return;
    }
    if (elements.length >= 10) throw new Error("Send or remove a live comment before adding another (10 per batch).");
    this.elements.set(id, [...elements, element]);
    this.save();
  }

  removeElement(id: string, element: FeedbackElement) {
    this.elements.set(id, this.elementsFor(id).filter(e => e !== element));
    this.save();
  }

  /** How many things a send would deliver (comments + a non-blank note). */
  count(id: string): number {
    return this.draftsFor(id).length + this.elementsFor(id).length + (this.noteFor(id).trim() ? 1 : 0);
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
    this.elements.delete(id);
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
    // The composer stays usable while the request travels, so remember exactly
    // what went out: only that is cleared on success. A comment added, or a
    // note edited, during the request was never sent and must survive it.
    const sentKeys = new Set(this.draftsFor(id).map((d) => d.key));
    const sentNote = this.noteFor(id);
    const elements = this.elementsFor(id);
    this.sending.set(id, true);
    try {
      const r = await DaemonService.SendFeedback({ session: id, comments, note: sentNote, ...(elements.length ? { elements: elements.map(elementForAgent) } : {}) });
      const remaining = this.elementsFor(id).filter(e => !elements.includes(e));
      if (remaining.length) this.elements.set(id, remaining);
      else this.elements.delete(id);
      const rest = this.draftsFor(id).filter((d) => !sentKeys.has(d.key));
      if (rest.length) this.drafts.set(id, rest);
      else this.drafts.delete(id);
      if (this.noteFor(id) === sentNote) this.notes.delete(id);
      this.save();
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
      const ids = new Set([...this.drafts.keys(), ...this.notes.keys(), ...this.elements.keys()]);
      for (const id of ids) all[id] = { drafts: this.draftsFor(id), note: this.noteFor(id), elements: this.elementsFor(id) };
      localStorage.setItem(KEY, JSON.stringify(all));
    } catch {
      /* storage unavailable — the queue still works for this run */
    }
  }
}

export const feedback = new Feedback();
