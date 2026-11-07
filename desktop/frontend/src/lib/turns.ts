// The story's shape, as pure data: the agent's log and lola's store merged into turns of rows.
//
// The merge happens here, on the client, so no private block can reach anything an agent reads.
// Nothing in this file touches a store, the DOM or a binding, because a transcript is hundreds of
// tool calls around a few short sentences, and whether the prose stays the figure is decided by
// the rules below, each of which should be readable and testable on its own.
//
// Every string that came from the log is a claim, including a tool's "ok". Only store items are
// facts, and only a store message ever gets the you fill.

import type { AgentFeedItem, AgentFeedTool } from "@bindings/internal/protocol";
import { clockTime, evidenceTargets, itemConn, itemMatches, matchesFilter, type Filter, type SSHTimelineItem } from "./ssh";
import { youAttachments } from "./composerpaste";
import { answerShaped } from "./answerblocks";
import { toMillis } from "./time";
import { activityTool } from "./toolActivity";

/** Legacy transcripts have no send receipt. Recognize only Lola's complete recipe
 * signature (including clipped starts), never generic review prose. Display only. */
export function isLolaReviewInstruction(text: string): boolean {
  return text.includes("List the open threads:\ngh api graphql -f query='") &&
    text.includes("Reply to one (databaseId from above):") &&
    text.includes("Resolve one (id from above):") &&
    text.includes("resolveReviewThread") && text.includes("<databaseId>/replies");
}

export type ToolStatus = "running" | "ok" | "error" | "interrupted" | "no_result" | string;

type RowBase = {
  id: string;
  /** Every source item this row stands for, so a find or a jump can land on it. */
  ids: string[];
  /** When it happened, in ms. NaN only when no source in the story knows. */
  at: number;
  /** Space above it, from the timeline's table in the design: 0, 6, 10, 12 or 14. */
  gap: number;
};

export type ToolRowData = RowBase & {
  kind: "tool";
  item: AgentFeedItem;
  tool: AgentFeedTool;
  /** The status as far as lola can honestly say it: see resolveStatus. */
  status: ToolStatus;
  verb: string;
  /** The whole value, for the tooltip and for a row of its own. */
  target: string;
  /** The failed row's preview is open with nothing expanded. */
  previewOpen: boolean;
  /** The agent spoke after this in the same turn, so a failure here is history. See settleWork. */
  settled?: boolean;
};

export type RunRowData = RowBase & {
  kind: "run";
  category: string;
  verb: string;
  /** Unique short names in the order they happened. A run line is never a bare count. */
  names: string[];
  items: ToolRowData[];
  added: number;
  removed: number;
  durationMs: number;
  /** Failures folded into this line, in a stretch too long for each to have a row. Named first. */
  failed: number;
  /** The agent spoke after this in the same turn, so a failure here is history. See settleWork. */
  settled?: boolean;
};

export type PlateFoldRow = RowBase & { kind: "platefold"; items: SSHTimelineItem[]; names: string[]; totalMs: number };

export type StoryRow =
  | ToolRowData
  | RunRowData
  | PlateFoldRow
  | (RowBase & { kind: "you"; item: SSHTimelineItem })
  | (RowBase & { kind: "terminal"; text: string; truncated: boolean })
  | (RowBase & { kind: "origin"; text: string; source?: "lola"; label?: string })
  | (RowBase & { kind: "prose"; text: string; truncated: boolean })
  | (RowBase & { kind: "notice"; text: string })
  | (RowBase & { kind: "plate"; item: SSHTimelineItem })
  | (RowBase & { kind: "element"; item: SSHTimelineItem })
  | (RowBase & { kind: "widget"; item: SSHTimelineItem })
  | (RowBase & { kind: "moment"; text: string; item: SSHTimelineItem })
  | (RowBase & { kind: "divider"; label: string; life: boolean })
  | (RowBase & { kind: "receipt"; words: string; seq: number; stopped: boolean; title: string; clock: string; changed: string });

export type Turn = {
  id: string;
  rows: StoryRow[];
  /** The first line of the turn's answer, its last prose: what a folded older turn says after its ask. */
  firstAnswer: string;
  receipt?: StoryRow;
  /** A folded turn's right side, from the same record as its receipt: `+84 −12 in 6 files · 14:02`. */
  meta: string;
  /** Space above it. */
  gap: number;
  /** This turn opens a new life: a divider that ended an earlier session or context. */
  life: boolean;
};

export type Story = {
  turns: Turn[];
  /** The newest store message's id, so sending can return the reader to the bottom. */
  lastYou: string;
  /** Rows across all turns, for counting what arrived while someone was scrolled up. */
  rowCount: number;
};

export type StoryInput = {
  feed: readonly AgentFeedItem[];
  items: readonly SSHTimelineItem[];
  filter?: Filter;
  /** The host in scope, "" under All. A host filters the story's store items, never the log. */
  scope?: string;
  /** Whether the agent can still answer an unresolved call. See resolveStatus. */
  alive?: boolean;
  /** Plate folds a human opened. */
  opened?: ReadonlySet<string>;
  /** Already-presented rows must not disappear into an automatic group. */
  standing?: ReadonlySet<string>;
};

/** Over this many tool rows between prose, a stretch collapses by category. */
export const MAX_STRETCH_ROWS = 8;
/** Older turns than this fold and mount no rows. */
export const OPEN_TURNS = 10;

const LOUD = new Set(["error", "running", "interrupted", "no_result"]);

/** A store item in the story's order: when the agent got it, else when it happened. */
export function storeTime(i: SSHTimelineItem): number {
  const delivered = i.human?.deliveredAt ? toMillis(i.human.deliveredAt as unknown as string) : NaN;
  return Number.isNaN(delivered) ? toMillis(i.at as unknown as string) : delivered;
}

/** What an unresolved call reads as: a parked agent will answer it, a resting or dead one never will. */
export function resolveStatus(status: string | undefined, alive: boolean): ToolStatus {
  const s = status || "ok";
  return s === "running" && !alive ? "no_result" : s;
}

/** `lola report`, `lola ssh` and `lola post`: the store already shows these as facts. A chain did real work. */
export function isLolaCall(t: Pick<AgentFeedTool, "category" | "summary">): boolean {
  if (t.category !== "run") return false;
  const s = (t.summary ?? "").trim();
  return /^lola\s+(report|ssh|post)\b/.test(s) && !/&&|\|\||;|\|/.test(s);
}

function lastSegment(name: string): string {
  const parts = name.split("__");
  return parts[parts.length - 1] || name;
}

export function toolVerb(t: Pick<AgentFeedTool, "name" | "category">): string {
  if (["Delete", "Create", "Touch", "Move", "Copy", "Commit", "Push", "Pull", "Merge", "Rebase", "Checkout", "Switch", "Stage"].includes(t.name)) return t.name;
  switch (t.category) {
    case "read":
      if (/grep|search/i.test(t.name)) return "Search";
      if (/glob|^ls$|list/i.test(t.name)) return "List";
      return "Read";
    case "edit":
      return /write/i.test(t.name) ? "Write" : "Edit";
    case "run":
      return "Ran";
    case "agent":
      return "Agent";
    default:
      return lastSegment(t.name).slice(0, 7) || "Tool";
  }
}

const RUN_VERB: Record<string, string> = { read: "Read", edit: "Edit", run: "Ran" };

/** A path's file name, for the short list on a run line. Anything else is already its own name. */
export function shortName(t: Pick<AgentFeedTool, "name" | "category" | "summary">): string {
  const s = (t.summary ?? "").trim();
  if (!s) return lastSegment(t.name);
  if ((t.category === "read" || t.category === "edit") && !/\s/.test(s) && s.includes("/")) return s.slice(s.lastIndexOf("/") + 1) || s;
  return s;
}

/** Clips out of the middle, where a path says the least. */
export function clipMiddle(text: string, max: number): string {
  if (text.length <= max || max < 5) return text;
  const left = Math.ceil((max - 1) / 2);
  return `${text.slice(0, left)}…${text.slice(text.length - (max - 1 - left))}`;
}

/** Clips off the end, where a command says the least. */
export function clipEnd(text: string, max: number): string {
  return text.length <= max || max < 2 ? text : `${text.slice(0, max - 1)}…`;
}

/**
 * As many names as fit in `chars`, then how many did not. At least one always shows, clipped if
 * it has to be, because a run line with no name in it is exactly the row this design forbids.
 */
export function fitNames(names: readonly string[], chars: number, clip: (text: string, max: number) => string = clipMiddle): { shown: string[]; rest: number } {
  if (!names.length) return { shown: [], rest: 0 };
  const shown: string[] = [];
  let used = 0;
  for (let i = 0; i < names.length; i++) {
    const cost = names[i].length + (shown.length ? 2 : 0);
    const reserve = i < names.length - 1 ? 5 : 0;
    if (used + cost + reserve > chars) break;
    shown.push(names[i]);
    used += cost;
  }
  if (!shown.length) return { shown: [clip(names[0], Math.max(8, chars - 5))], rest: names.length - 1 };
  return { shown, rest: names.length - shown.length };
}

function toolRow(item: AgentFeedItem, alive: boolean): ToolRowData {
  const tool = activityTool(item.tool as AgentFeedTool);
  const status = resolveStatus(tool.status, alive);
  return {
    kind: "tool",
    id: item.id,
    ids: [item.id],
    at: toMillis(item.at as unknown as string),
    gap: 0,
    item,
    tool,
    status,
    verb: toolVerb(tool),
    target: tool.summary || lastSegment(tool.name),
    previewOpen: status === "error" && !!tool.preview,
  };
}

function runRow(category: string, members: ToolRowData[]): RunRowData {
  const names: string[] = [];
  // What failed comes first, so it is what shows when not every name fits.
  const failedFirst = [...members.filter((m) => m.status === "error"), ...members.filter((m) => m.status !== "error")];
  for (const m of failedFirst) {
    const n = shortName(m.tool);
    if (!names.includes(n)) names.push(n);
  }
  return {
    kind: "run",
    id: `run-${members[0].id}`,
    ids: members.map((m) => m.id),
    at: members[0].at,
    gap: 0,
    category,
    verb: RUN_VERB[category] ?? members[0].verb,
    names,
    items: members,
    added: members.reduce((n, m) => n + (m.tool.added ?? 0), 0),
    removed: members.reduce((n, m) => n + (m.tool.removed ?? 0), 0),
    durationMs: members.reduce((n, m) => n + (m.tool.durationMs ?? 0), 0),
    failed: members.filter((m) => m.status === "error").length,
  };
}

/** Quiet and groupable: anything failed, unresolved or a subagent always keeps its own row. */
function isLoud(r: ToolRowData): boolean {
  return LOUD.has(r.status);
}

function groupQuiet(category: string, members: ToolRowData[]): (ToolRowData | RunRowData)[] {
  if (!members.length) return [];
  if (category === "read") return members.length > 1 ? [runRow(category, members)] : members;
  return members.length >= 3 ? [runRow(category === "other" ? "other" : "run", members)] : members;
}

/**
 * One stretch of tool calls between prose. Under the limit it reads in order, with a failed or
 * running call splitting it. Over the limit, one line per category, because that is what keeps
 * prose the figure in a real turn: hundreds of calls around one-sentence answers.
 *
 * A failure in a long stretch is folded into its category's line, named first with the count in
 * red, because at 5% of 480 calls one row each is what the screen ends up being about. `tail` is
 * the stretch the agent is still in, where a failure may be what happens next, so there the last
 * failure also keeps its row and its preview. Anything still unresolved always has a row.
 */
export function collapseWork(tools: readonly AgentFeedItem[], alive: boolean, tail = false, current = "", standing?: ReadonlySet<string>): (ToolRowData | RunRowData)[] {
  const rows = tools.filter((i) => i.tool && !isLolaCall(i.tool)).map((i) => toolRow(i, alive));
  const out: (ToolRowData | RunRowData)[] = [];
  let chunk: ToolRowData[] = [];
  const flush = () => {
    if (!chunk.length) return;
    out.push(...(chunk[0].tool.category === "edit" ? [runRow("edit", chunk)] : collapseOtherWork(chunk, tail, current)));
    chunk = [];
  };
  for (const row of rows) {
    if (standing?.has(row.id) || row.id === current || row.status === "running") {
      flush();
      out.push(row);
      continue;
    }
    const previous = chunk.at(-1);
    if (previous && ((previous.tool.category === "edit") !== (row.tool.category === "edit") ||
      row.tool.category === "edit" && row.at - previous.at > 30_000)) flush();
    chunk.push(row);
  }
  flush();
  return out;
}

function collapseOtherWork(rows: ToolRowData[], tail: boolean, current: string): (ToolRowData | RunRowData)[] {
  if (rows.length > MAX_STRETCH_ROWS) {
    const lastFailed = tail ? rows.findLastIndex((r) => r.status === "error") : -1;
    const byCategory = new Map<string, ToolRowData[]>();
    const own: ToolRowData[] = [];
    rows.forEach((r, i) => {
      const unresolved = isLoud(r) && r.status !== "error";
      if (r.id === current || unresolved || r.tool.category === "agent" || i === lastFailed) {
        own.push(r);
        return;
      }
      const c = categoryOf(r);
      const list = byCategory.get(c);
      if (list) list.push(r);
      else byCategory.set(c, [r]);
    });
    const lines: (ToolRowData | RunRowData)[] = [];
    for (const [c, list] of byCategory) {
      if (list.length > 1) {
        lines.push(runRow(c, list));
      } else {
        list[0].previewOpen = false;
        lines.push(list[0]);
      }
    }
    return [...lines, ...own];
  }
  const out: (ToolRowData | RunRowData)[] = [];
  let run: ToolRowData[] = [];
  let runCategory = "";
  const flush = () => {
    out.push(...groupQuiet(runCategory, run));
    run = [];
  };
  for (const r of rows) {
    if (r.id === current || isLoud(r) || r.tool.category === "agent") {
      flush();
      out.push(r);
      continue;
    }
    const c = categoryOf(r);
    if (c !== runCategory) flush();
    runCategory = c;
    run.push(r);
  }
  flush();
  return out;
}

function categoryOf(r: ToolRowData): string {
  const c = r.tool.category;
  return c === "read" || c === "edit" || c === "run" ? c : "other";
}

const QUIET_EVENTS = new Set(["go", "wheel_taken"]);

function quietEvent(i: SSHTimelineItem, filter: Filter): boolean {
  const s = i.system;
  if (!s || filter === "facts") return false;
  return QUIET_EVENTS.has(s.kind) || (s.kind === "handed_back" && !s.note && !s.hasTranscript);
}

function changedWords(added: number | undefined, removed: number | undefined, files: number): string {
  return `+${added ?? 0} −${removed ?? 0} in ${files} ${files === 1 ? "file" : "files"}`;
}

/** `Done · 2m 31s · +84 −12 in 6 files · #14`. With no checkpoint there is no number. */
export function receiptWords(s: NonNullable<SSHTimelineItem["system"]>, format: (ms: number) => string): string {
  if (s.kind === "stopped") return ["Stopped", s.durationMs ? format(s.durationMs) : ""].filter(Boolean).join(" · ");
  const parts = [s.text || "Done"];
  if (s.durationMs) parts.push(format(s.durationMs));
  if (s.files) parts.push(changedWords(s.added, s.removed, s.files));
  if (s.seq) parts.push(`#${s.seq}`);
  return parts.join(" · ");
}

function shortDuration(ms: number): string {
  const v = Math.max(0, ms);
  if (v < 60_000) return `${Math.round(v / 1000)}s`;
  const m = Math.floor(v / 60_000);
  const s = Math.round((v % 60_000) / 1000);
  return s ? `${m}m ${s}s` : `${m}m`;
}

const WORK = new Set(["tool", "run"]);
const PLATES = new Set(["plate", "platefold"]);

/**
 * Space above a row inside a turn, from the timeline's table. Between groups is at least twice
 * within. Prose takes its gap from its size, which comes from its text, so a beat that stops being
 * the last word moves nothing.
 */
function gapBetween(prev: StoryRow | undefined, row: StoryRow): number {
  if (!prev) return 0;
  if (row.kind === "receipt") return 10;
  if (row.kind === "prose") {
    const own = answerShaped(row.text) ? 28 : 6;
    return prev.kind === "you" || prev.kind === "terminal" ? Math.max(own, 12) : own;
  }
  if (row.kind === "tool" && row.status === "running") return 10;
  if (WORK.has(row.kind)) return WORK.has(prev.kind) ? 0 : 12;
  if (row.kind === "origin" || row.kind === "notice" || row.kind === "moment") return 6;
  return 12;
}

type Entry = { at: number; src: 0 | 1; idx: number; feed?: AgentFeedItem; item?: SSHTimelineItem };

function mergeEntries(feed: readonly AgentFeedItem[], items: readonly SSHTimelineItem[]): Entry[] {
  const entries: Entry[] = [];
  // Something with no time of its own stays beside what came before it in its own source.
  let last = 0;
  feed.forEach((f, idx) => {
    const t = toMillis(f.at as unknown as string);
    last = Number.isNaN(t) ? last : t;
    entries.push({ at: last, src: 0, idx, feed: f });
  });
  last = 0;
  items.forEach((item, idx) => {
    const t = storeTime(item);
    last = Number.isNaN(t) ? last : t;
    entries.push({ at: last, src: 1, idx, item });
  });
  return entries.sort((a, b) => a.at - b.at || a.src - b.src || a.idx - b.idx);
}

/** A human message the story shows. Undelivered ones are queue rows in the action zone. */
function humanShown(i: SSHTimelineItem): boolean {
  const h = i.human;
  if (!h) return false;
  if (!h.text?.trim() && !h.transcript) return false;
  return !!h.delivered || !!h.recalled || !!h.returned;
}

function isLifeLabel(label: string): boolean {
  return label === "New session started" || label === "Context cleared";
}

/** Everything the story needs from a store item in scope. */
export function inScope(items: readonly SSHTimelineItem[], scope: string): SSHTimelineItem[] {
  if (!scope) return items as SSHTimelineItem[];
  return items.filter((i) => {
    const c = itemConn(i);
    return !c || c === scope;
  });
}

function firstLine(text: string): string {
  return (text.split("\n").find((l) => l.trim()) ?? "").trim();
}

/** A first line as words: the marks that make it a heading or a bold lead mean nothing in one row. */
function plainLine(text: string): string {
  return firstLine(text).replace(/^#{1,6}\s+/, "").replace(/\*\*/g, "");
}

/**
 * The whole story. A turn starts at a human message or another trigger and ends at a stop or an
 * interrupt. The thinking blocks a transcript holds never reach here: the reader drops them.
 */
export function buildStory(input: StoryInput): Story {
  const filter = input.filter ?? "everything";
  const alive = input.alive ?? true;
  const opened = input.opened ?? new Set<string>();
  const items = inScope(input.items, input.scope ?? "").filter((i) => matchesFilter(i, filter) && !quietEvent(i, filter));
  const feed = filter === "everything" ? input.feed : [];
  const keep = evidenceTargets(input.items as SSHTimelineItem[]);

  const turns: Turn[] = [];
  let cur: StoryRow[] | null = null;
  let curId = "";
  let tools: AgentFeedItem[] = [];
  let plates: SSHTimelineItem[] = [];
  let lastYou = "";

  const pushRow = (row: StoryRow) => {
    if (!cur) {
      cur = [];
      curId = row.id;
    }
    row.gap = gapBetween(cur[cur.length - 1], row);
    cur.push(row);
  };

  const flushTools = (tail = false) => {
    if (!tools.length) return;
    for (const r of collapseWork(tools, alive, tail, alive && input.feed.at(-1)?.tool ? input.feed.at(-1)!.id : "", input.standing)) pushRow(r);
    tools = [];
  };

  const flushPlates = () => {
    if (!plates.length) return;
    const list = plates;
    plates = [];
    const foldable = list.length >= 3 && filter !== "facts" && !opened.has(`fold-${list[0].id}`);
    if (foldable) {
      const names = list.map((i) => (i.block?.command ?? "").trim()).filter(Boolean);
      pushRow({
        kind: "platefold",
        id: `fold-${list[0].id}`,
        ids: list.map((i) => i.id),
        at: storeTime(list[0]),
        gap: 0,
        items: list,
        names,
        totalMs: list.reduce((n, i) => n + (i.block?.durationMs ?? 0), 0),
      });
    } else {
      for (const item of list) pushRow({ kind: "plate", id: item.id, ids: [item.id], at: storeTime(item), gap: 0, item });
    }
  };

  const flush = () => {
    flushTools();
    flushPlates();
  };

  const endTurn = () => {
    // Nothing spoke after these, so this stretch is where the turn currently stands.
    flushTools(true);
    flushPlates();
    if (cur?.length) {
      const rows: StoryRow[] = cur;
      settleWork(rows);
      const answer = rows.findLast((r) => r.kind === "prose");
      const you = rows.find((r) => r.kind === "you" || r.kind === "terminal");
      const receipt = rows.find((r) => r.kind === "receipt");
      turns.push({
        id: curId,
        rows,
        firstAnswer: answer?.kind === "prose" ? plainLine(answer.text) : you?.kind === "you" ? firstLine(you.item.human?.text ?? "") : you?.kind === "terminal" ? firstLine(you.text) : "",
        receipt,
        meta: receipt?.kind === "receipt" ? [receipt.changed, receipt.clock].filter(Boolean).join(" · ") : "",
        gap: turns.length ? 32 : 0,
        life: rows[0].kind === "divider" && rows[0].life,
      });
    }
    cur = null;
    curId = "";
  };

  for (const e of mergeEntries(feed, items)) {
    if (e.feed) {
      const f = e.feed;
      const at = e.at;
      switch (f.kind) {
        case "tool":
          flushPlates();
          tools.push(f);
          break;
        case "text":
          flush();
          pushRow({ kind: "prose", id: f.id, ids: [f.id], at, gap: 0, text: f.text ?? "", truncated: !!f.truncated });
          break;
        case "user":
          // An echo is lola's own send, already shown as a store row. It also starts nothing.
          if (f.echo) break;
          endTurn();
          if (isLolaReviewInstruction(f.text ?? "")) pushRow({ kind: "origin", id: f.id, ids: [f.id], at, gap: 0, text: f.text ?? "", source: "lola", label: "Review follow-up" });
          else if (f.origin === "task") pushRow({ kind: "origin", id: f.id, ids: [f.id], at, gap: 0, text: f.text ? `Background task finished · ${firstLine(f.text)}` : "Background task finished" });
          else if (f.origin === "system" || f.origin === "teammate") pushRow({ kind: "origin", id: f.id, ids: [f.id], at, gap: 0, text: f.text ?? "" });
          else pushRow({ kind: "terminal", id: f.id, ids: [f.id], at, gap: 0, text: f.text ?? "", truncated: !!f.truncated });
          break;
        case "divider": {
          const label = f.text || "Context cleared";
          endTurn();
          pushRow({ kind: "divider", id: f.id, ids: [f.id], at, gap: 0, label, life: isLifeLabel(label) && turns.length > 0 });
          endTurn();
          break;
        }
        case "notice":
          flush();
          pushRow({ kind: "notice", id: f.id, ids: [f.id], at, gap: 0, text: f.text ?? "" });
          break;
      }
      continue;
    }
    const it = e.item as SSHTimelineItem;
    const at = e.at;
    if (it.human) {
      if (!humanShown(it)) continue;
      if (it.human.origin === "choice") {
        // A label read off the pane: a process could put words in it, so never the you fill.
        flush();
        pushRow({ kind: "moment", id: it.id, ids: [it.id], at, gap: 0, text: it.human.text, item: it });
        continue;
      }
      endTurn();
      lastYou = it.id;
      pushRow({ kind: "you", id: it.id, ids: [it.id], at, gap: 0, item: it });
    } else if (it.block) {
      const b = it.block;
      // Waiting on a person, it is a card in the tray. It joins the story once it is decided.
      if (b.status === "pending_approval") continue;
      flushTools();
      if (filter !== "facts" && b.status === "succeeded" && b.actor !== "human" && !keep.has(b.id)) {
        plates.push(it);
      } else {
        flushPlates();
        pushRow({ kind: "plate", id: it.id, ids: [it.id], at, gap: 0, item: it });
      }
    } else if (it.element) {
      flush();
      pushRow({ kind: it.element.kind === "widget" ? "widget" : "element", id: it.id, ids: [it.id], at, gap: 0, item: it });
    } else if (it.system) {
      const s = it.system;
      if (s.kind === "checkpoint" || s.kind === "stopped") {
        flush();
        // A stop reads as two rows (map E). The sentence says what a stop did not end, which a
        // person needs on screen rather than in a tooltip, and the receipt under it carries the time.
        if (s.kind === "stopped" && s.text) pushRow({ kind: "moment", id: `${it.id}:moment`, ids: [it.id], at, gap: 0, text: s.text, item: it });
        pushRow({ kind: "receipt", id: it.id, ids: [it.id], at, gap: 0, words: receiptWords(s, shortDuration), seq: s.seq ?? 0, stopped: s.kind === "stopped", title: s.kind === "stopped" ? s.text : "", clock: clockTime(it.at as unknown as string), changed: s.kind === "checkpoint" && s.files ? changedWords(s.added, s.removed, s.files) : "" });
        endTurn();
      } else if (s.kind === "session_started") {
        endTurn();
        // The story's own first row says nothing a person needs, and would top every session.
        if (turns.length) {
          pushRow({ kind: "divider", id: it.id, ids: [it.id], at, gap: 0, label: "New session started", life: true });
          endTurn();
        }
      } else if (s.kind === "handoff") {
        // lola typed something, which is what opens the turn that answers it.
        endTurn();
        pushRow({ kind: "moment", id: it.id, ids: [it.id], at, gap: 0, text: s.text, item: it });
      } else {
        flush();
        pushRow({ kind: "moment", id: it.id, ids: [it.id], at, gap: 0, text: s.text, item: it });
      }
    }
  }
  endTurn();

  return { turns, lastYou, rowCount: turns.reduce((n, t) => n + t.rows.length, 0) };
}

/** Past this a person's row folds, so one pasted wall cannot be most of the story. */
export const CLAMP_LINES = 12;
const CLAMP_CHARS = 1400;

/** Whether a user or terminal row is long enough to fold behind a disclosure. */
export function clampsRow(text: string, lines = CLAMP_LINES): boolean {
  return text.length > CLAMP_CHARS || text.split("\n").length > lines;
}

/**
 * What opened a turn, as one line: a folded turn's start and the running head's text. Usually what
 * the person asked. A background task or lola's own hand-off opens a turn too, and says what it was.
 */
export function turnAsk(turn: Pick<Turn, "rows">): string {
  for (const r of turn.rows) {
    if (r.kind === "you") return firstLine(youAttachments(r.item.human?.text ?? "").text);
    if (r.kind === "terminal") return firstLine(r.text);
  }
  const first = turn.rows[0];
  return first?.kind === "origin" || first?.kind === "moment" ? firstLine(first.text) : "";
}

/** What a turn's edits touched, for the receipt's file list. A name is never a bare count. */
export type TurnFile = { name: string; path: string; added: number; removed: number };
export const TURN_FILES = 5;

export type TurnFiles = { files: TurnFile[]; more: number };

export function turnFiles(rows: readonly StoryRow[], max = TURN_FILES): TurnFiles {
  const seen = new Map<string, TurnFile>();
  const add = (r: ToolRowData) => {
    if (r.tool.category !== "edit" || !r.target) return;
    const f = seen.get(r.target) ?? { name: lastName(r.target), path: r.target, added: 0, removed: 0 };
    f.added += r.tool.added ?? 0;
    f.removed += r.tool.removed ?? 0;
    seen.set(r.target, f);
  };
  for (const r of rows) {
    if (r.kind === "tool") add(r);
    else if (r.kind === "run") r.items.forEach(add);
  }
  const all = [...seen.values()];
  return { files: all.slice(0, max), more: Math.max(0, all.length - max) };
}

/** A folded turn's words. A turn with no prose or message names what it did, never a row's kind. */
export function foldLabel(turn: Pick<Turn, "firstAnswer" | "rows">): string {
  if (turn.firstAnswer) return turn.firstAnswer;
  for (const r of turn.rows) {
    switch (r.kind) {
      case "divider":
        return r.label;
      case "run":
        return `${r.verb} ${r.names.join(", ")}`;
      case "tool":
        return [r.verb, r.target].filter(Boolean).join(" ");
      case "origin":
      case "notice":
      case "moment":
        return firstLine(r.text);
      case "receipt":
        return r.words;
      case "plate":
        return r.item.block?.command ?? "";
      case "platefold":
        return r.names.join(", ");
      case "element":
      case "widget":
        return r.item.element?.title ?? "";
    }
  }
  return "";
}

/**
 * Work the agent has already spoken after. Its failures are history, so they keep their word but
 * lose the colour, and red on a row means nothing has answered it yet. A failed row still never
 * groups, and the word never goes.
 */
function settleWork(rows: StoryRow[]) {
  let last = -1;
  rows.forEach((r, i) => {
    if (r.kind === "prose") last = i;
  });
  for (let i = 0; i < last; i++) {
    const r = rows[i];
    if (r.kind === "tool") r.settled = true;
    else if (r.kind === "run") {
      r.settled = true;
      for (const m of r.items) m.settled = true;
    }
  }
}

/** One line of a turn: a row, or the work before the agent's last word, folded to what it touched. */
export type TurnPart = { kind: "row"; row: StoryRow } | { kind: "work"; id: string; rows: StoryRow[]; label: string; failed: number };

/** Narration beats left standing in a turn. Real turns reach 63, which would bury the answer. */
export const KEEP_BEATS = 6;

function lastName(path: string): string {
  const trimmed = path.replace(/\/+$/, "");
  return trimmed.slice(trimmed.lastIndexOf("/") + 1) || path;
}

/** Up to this many names per verb on a dig line, then a count of the rest. */
const SEGMENT_NAMES = 3;

const PAST_VERB: Record<string, string> = { Edit: "Edited", Write: "Wrote", Search: "Searched", List: "Listed", Delete: "Deleted", Create: "Created", Touch: "Touched", Move: "Moved", Copy: "Copied", Commit: "Committed", Push: "Pushed", Pull: "Pulled", Merge: "Merged", Rebase: "Rebased", Checkout: "Checked out", Switch: "Switched", Stage: "Staged" };

export function pastVerb(verb: string): string { return PAST_VERB[verb] ?? verb; }

/** One verb on a dig line and what it touched. A bare count is the row this design avoids. */
export type WorkSegment = { verb: string; names: string[]; rest: number; stat: string };

function byVerb(rows: readonly StoryRow[]) {
  const verbs = new Map<string, { category: string; names: string[]; added: number; removed: number }>();
  const add = (verb: string, category: string, names: readonly string[], added = 0, removed = 0) => {
    const e = verbs.get(verb) ?? { category, names: [], added: 0, removed: 0 };
    for (const n of names) if (n && !e.names.includes(n)) e.names.push(n);
    e.added += added;
    e.removed += removed;
    verbs.set(verb, e);
  };
  for (const r of rows) {
    if (r.kind === "tool") add(r.verb, r.tool.category, [r.target ?? ""], r.tool.added, r.tool.removed);
    else if (r.kind === "run") add(r.verb, r.category, r.names, r.added, r.removed);
  }
  // What changed comes first: it is what a person wants from this line.
  const rank = (c: string) => (c === "edit" ? 0 : c === "run" ? 1 : c === "read" ? 3 : 2);
  return [...verbs.entries()].sort((a, b) => rank(a[1].category) - rank(b[1].category));
}

const shownName = (category: string, name: string) => (category === "run" ? clipEnd(name, 36) : lastName(name));

/** What a stretch of work touched, by verb and by name: the title of a dig line, and what a find reads. */
export function workLabel(rows: readonly StoryRow[]): string {
  return byVerb(rows)
    .map(([verb, e]) => [verb, e.names.length ? shownName(e.category, e.names[0]) : "", e.names.length > 1 ? `+${e.names.length - 1}` : ""].filter(Boolean).join(" "))
    .join(" · ");
}

/** A factual activity label, plus the agent's own latest commentary as context. */
export function activitySummary(rows: readonly StoryRow[]): { label: string; context: string } {
  const tools = rows.flatMap(row => row.kind === "run" ? row.items : row.kind === "tool" ? [row] : []);
  const edits = new Set<string>();
  const reads = new Set<string>();
  let commands = 0;
  let other = 0;
  for (const tool of tools) {
    if (tool.tool.category === "edit") edits.add(tool.target || tool.id);
    else if (tool.tool.category === "read") reads.add(tool.target || tool.id);
    else if (tool.tool.category === "run") commands++;
    else other++;
  }
  const labels = [
    edits.size ? `Edited ${edits.size} ${edits.size === 1 ? "file" : "files"}` : "",
    commands ? `Ran ${commands} ${commands === 1 ? "command" : "commands"}` : "",
    reads.size ? `Read ${reads.size} ${reads.size === 1 ? "file" : "files"}` : "",
    other ? `${other} other ${other === 1 ? "call" : "calls"}` : "",
  ].filter(Boolean);
  const notes = rows.filter(row => row.kind === "prose");
  const context = notes.length ? firstLine(notes[notes.length - 1].text) : "";
  return { label: labels.join(" · ") || `${notes.length} progress ${notes.length === 1 ? "update" : "updates"}`, context };
}

/** The same stretch as a dig line draws it: past tense, in the same order, up to three names a verb. */
export function workSegments(rows: readonly StoryRow[]): WorkSegment[] {
  return byVerb(rows).map(([verb, e]) => ({
    verb: PAST_VERB[verb] ?? verb,
    names: e.names.slice(0, SEGMENT_NAMES).map((n) => shownName(e.category, n)),
    rest: Math.max(0, e.names.length - SEGMENT_NAMES),
    stat: diffStat(e.added, e.removed),
  }));
}

/**
 * Splits a turn so its words stand. Tools before the agent's last word fold to one line naming what
 * they touched, and narration stays up and splits those lines: the smaller text between the prose
 * is the point. What is still happening, after the last word, stays open. A plate, a card, a widget,
 * an element, a message or a moment is never folded: it breaks the stretch instead, because those
 * are what a person acts on or what lola witnessed. A lone tool is already one line.
 *
 * Only the last `keepBeats` narration beats stay up. Older ones fold with their work, and this is
 * the only way prose folds. Prose with no work beside it stays, since a fold would name nothing.
 */
export function turnParts(rows: readonly StoryRow[], keepBeats = KEEP_BEATS, settled = false, standing?: ReadonlySet<string>): TurnPart[] {
  const beats: number[] = [];
  rows.forEach((r, i) => {
    if (r.kind === "prose") beats.push(i);
  });
  const last = beats.length ? beats[beats.length - 1] : -1;
  // The first narration beat still standing: everything before it may fold with its words.
  const narration = beats.slice(0, -1);
  const standsFrom = keepBeats === 0 ? last : narration.length > keepBeats ? narration[narration.length - keepBeats] : -1;
  const parts: TurnPart[] = [];
  let run: StoryRow[] = [];
  const flush = () => {
    const work = run.filter((r) => r.kind === "tool" || r.kind === "run");
    if (run.length >= 2 && (work.length > 0 || keepBeats === 0)) {
      let failed = 0;
      for (const r of work) {
        if (r.kind === "run") failed += r.failed;
        else if (r.kind === "tool" && r.status === "error") failed++;
      }
      parts.push({ kind: "work", id: `work-${run[0].id}`, rows: run, label: workLabel(run), failed });
    } else {
      for (const row of run) parts.push({ kind: "row", row });
    }
    run = [];
  };
  rows.forEach((row, i) => {
    // An active call or unanswered failure must remain visible even in a completed turn.
    const needsAttention = row.kind === "tool" && (row.status === "running" || (i >= last && !row.settled && LOUD.has(row.status))) ||
      row.kind === "run" && (row.items.some(item => item.status === "running") || row.failed > 0 && i >= last && !row.settled);
    const folds = !standing?.has(row.id) && !needsAttention && (((i < last || settled) && (row.kind === "tool" || row.kind === "run")) || (i < last && row.kind === "prose" && i < standsFrom));
    if (folds) {
      run.push(row);
      return;
    }
    flush();
    parts.push({ kind: "row", row });
  });
  flush();
  return parts;
}

/**
 * The prose a turn ends on, once it has ended: its answer. While the agent may still speak, every
 * beat is narration. Only colour and marker follow from this, so nothing moves when it changes.
 */
export function answerRowId(turn: Pick<Turn, "rows" | "receipt">, live: boolean): string {
  if (live && !turn.receipt) return "";
  return turn.rows.findLast((r) => r.kind === "prose")?.id ?? "";
}

/** The rail's mark for a row. Neutral, static and never a state: see S/RailMark. */
export type RailKind = "you" | "tick" | "tick-bad" | "narration" | "answer" | "plate" | "plate-why" | "finding" | "element" | "running" | "receipt" | "terminal" | "none";

export function railKind(row: StoryRow, answerId: string, filter: Filter = "everything"): RailKind {
  switch (row.kind) {
    case "you":
      return "you";
    case "terminal":
      return "terminal";
    case "prose":
      return row.id === answerId ? "answer" : "narration";
    case "tool":
      if (row.status === "running") return "running";
      return row.status === "error" && !row.settled ? "tick-bad" : "tick";
    case "run":
      return row.failed > 0 && !row.settled ? "tick-bad" : "tick";
    case "plate":
      return row.item.block?.why && filter !== "facts" ? "plate-why" : "plate";
    case "platefold":
    case "widget":
      return "plate";
    case "element":
      return row.item.element?.kind === "finding" ? "finding" : "element";
    case "receipt":
      return "receipt";
    default:
      return "none";
  }
}

/** What a turn reads as when it is older than the last few: first answer plus receipt. */
export function isFolded(index: number, total: number, openedTurns: ReadonlySet<string>, id: string): boolean {
  return index < total - OPEN_TURNS && !openedTurns.has(`turn-${id}`);
}

/** Where a collapsed earlier life ends: the index of the newest turn that began a new one. */
export function lastLifeIndex(turns: readonly Turn[]): number {
  for (let i = turns.length - 1; i > 0; i--) if (turns[i].life) return i;
  return -1;
}

/** The row holding a source item, and the turn it sits in. */
export function locate(story: Story, id: string): { turn: number; row: StoryRow } | null {
  for (let t = 0; t < story.turns.length; t++) {
    for (const row of story.turns[t].rows) if (row.id === id || row.ids.includes(id)) return { turn: t, row };
  }
  return null;
}

/** The rows a find reaches, in story order. Output has its own find in the viewer. */
export function findInStory(story: Story, query: string): StoryRow[] {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  const has = (s: string | undefined) => !!s && s.toLowerCase().includes(q);
  const out: StoryRow[] = [];
  for (const t of story.turns) {
    for (const r of t.rows) {
      let hit = false;
      switch (r.kind) {
        case "tool":
          hit = has(r.target) || has(r.tool.name) || has(r.tool.preview);
          break;
        case "run":
          hit = r.items.some((m) => has(m.target) || has(m.tool.name) || has(m.tool.preview));
          break;
        case "platefold":
          hit = r.items.some((i) => itemMatches(i, q));
          break;
        case "you":
        case "plate":
        case "element":
        case "widget":
          hit = itemMatches(r.item, q);
          break;
        case "moment":
          hit = has(r.text);
          break;
        case "terminal":
        case "origin":
        case "prose":
        case "notice":
          hit = has(r.text);
          break;
        case "divider":
          hit = has(r.label);
          break;
        case "receipt":
          hit = has(r.words);
          break;
      }
      if (hit) out.push(r);
    }
  }
  return out;
}

export type CatchUp = { rowId: string; turnId: string; turns: number; files: number; ci: string; words: string };

/**
 * "Since you looked", counted locally with no model anywhere near it. Keyed on a timestamp rather
 * than a feed sequence, because a sequence resets when the daemon restarts. Null when nothing is
 * new, and on a first open, where everything would be new and the line would say nothing.
 */
export function catchUp(story: Story, stamp: number): CatchUp | null {
  if (!stamp || Number.isNaN(stamp)) return null;
  let first: { rowId: string; turnId: string } | null = null;
  let turns = 0;
  const files = new Set<string>();
  let counted = 0;
  let ci = "";
  for (const t of story.turns) {
    let touched = false;
    for (const r of t.rows) {
      if (!(r.at > stamp)) continue;
      if (!first) first = { rowId: r.id, turnId: t.id };
      touched = true;
      if (r.kind === "tool" && r.tool.category === "edit") files.add(r.target);
      if (r.kind === "run" && r.category === "edit") for (const m of r.items) files.add(m.target);
      if (r.kind === "receipt") {
        const n = /in (\d+) file/.exec(r.words);
        if (n) counted += Number(n[1]);
      }
      if (r.kind === "moment" && r.item.system?.kind === "ci") ci = /fail/i.test(r.text) ? "CI failed" : /pass/i.test(r.text) ? "CI passed" : ci;
    }
    if (touched) turns++;
  }
  if (!first) return null;
  const n = Math.max(files.size, counted);
  const parts = [`${turns} ${turns === 1 ? "turn" : "turns"}`];
  if (n) parts.push(`${n} ${n === 1 ? "file" : "files"}`);
  if (ci) parts.push(ci);
  return { ...first, turns, files: n, ci, words: `Since you looked: ${parts.join(" · ")}` };
}

/** Every row in display order, for the Glance's tail and the keyboard. */
export function flatRows(story: Story): StoryRow[] {
  return story.turns.flatMap((t) => t.rows);
}

/** A diff's size with only the sides that moved: `+18`, `−23`, `+12 −3`, or nothing. */
export function diffStat(added: number | undefined, removed: number | undefined): string {
  return [added ? `+${added}` : "", removed ? `−${removed}` : ""].filter(Boolean).join(" ");
}

const LIVE_VERB: Record<string, string> = { Ran: "Running", Read: "Reading", Edit: "Editing", Write: "Writing", Delete: "Deleting", Create: "Creating", Touch: "Touching", Move: "Moving", Copy: "Copying", Commit: "Committing", Push: "Pushing", Pull: "Pulling", Merge: "Merging", Rebase: "Rebasing", Stage: "Staging" };

/** A call in progress says so in its verb. One too long for the 56px column keeps its word. */
export function liveVerb(verb: string): string {
  return LIVE_VERB[verb] ?? verb;
}

/**
 * What the right side of a tool row says. Never the word "exit" and never a tick: this came from
 * the agent's own log. A failure is the only word in colour. `verbSaid` means a running row's verb
 * already reads as running, so only its timer is left to say.
 */
export function toolStatusWords(
  tool: Pick<AgentFeedTool, "category" | "durationMs" | "added" | "removed">,
  status: ToolStatus,
  elapsed: string,
  duration: string,
  verbSaid = false,
): { word: string; bad: boolean; tail: string } {
  switch (status) {
    case "error":
      return { word: "failed", bad: true, tail: duration };
    case "running":
      return { word: verbSaid ? "" : "running", bad: false, tail: elapsed };
    case "interrupted":
      return { word: "stopped", bad: false, tail: "" };
    case "no_result":
      return { word: "no result", bad: false, tail: "" };
    default:
      if (tool.category === "edit") return { word: "", bad: false, tail: diffStat(tool.added, tool.removed) };
      if (tool.category === "read") return { word: "", bad: false, tail: "" };
      return { word: "ok", bad: false, tail: duration };
  }
}
