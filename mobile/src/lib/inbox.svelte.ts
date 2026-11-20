// The Inbox's model: sessions bucketed for a person who wants to know what needs
// them (spec §5.2 Inbox, §8.1 keyed deltas, §8.2 keyed state).
//
// Invariants:
//  - Rows are keyed. `ingest` replaces a session only when its object differs, so
//    a delta that changes one session computes one `rowModel` and leaves every
//    other row object `===`.
//  - Buckets come from the (agentState, delivery) pair through theme.ts, never
//    from the rolled-up status word.
//  - A card has exactly one action, chosen by the first matching row of the
//    table in `cardAction`. `approveChoice` is the only rule that makes Approve
//    available, and it accepts nothing but a lone choice labelled exactly "Yes".
//  - Grouping is a preference, not a route: `inboxView` holds it, and a grouped
//    list is built from the same models and the same keys as the status list.
//  - Everything on a card is display. The daemon re-reads the pane against the
//    hash before any key goes (`agentChoose`).

import { SvelteMap } from "svelte/reactivity";
import { untrack } from "svelte";
import { Events } from "@wailsio/runtime";
import type { AgentChoice, AgentPrompt } from "@bindings/internal/protocol";
import { store, type SessionInfo } from "$lib/store.svelte";
import { deliveryText, displayText, kanbanKey, sortRank } from "$lib/theme";
import { fluidAgo } from "$lib/time";
import { minuteClock } from "./clock.svelte";
import { bootMs, readStatus } from "./settings.svelte";
import { copy } from "./copy";
import type { InboxFilter } from "./router.svelte";
import { statusWord } from "./statustone";
import { loadInboxGroup, saveInboxGroup, type InboxGroup } from "./prefs";
import { buildRows, stackMembers, type LayoutGroup, type LayoutProject } from "$lib/sidebarlayout";
import type { RowKind } from "./components/VirtualList.svelte";

/**
 * Needs cards whose `talk:<id>` topic the Inbox watches; the rest show Answer. One below the
 * daemon's probes per device (`MaxProbedSessionsPerDevice` = 4), so an opened session always
 * has a slot.
 */
export const MAX_INBOX_TALK_WATCH = 3;
/** Approve stays disabled this long after the prompt the human is reading changed. */
export const APPROVE_SETTLE_MS = 600;
/** Rows the Done section shows before "Show all". */
export const DONE_VISIBLE = 20;

export type Bucket = Exclude<InboxFilter, "">;
export type CardAction = "approve" | "answer" | "resolve" | "checks" | "address" | "merge";
export type ChipTone = "urgent" | "broken" | "grey";

/** Section order, top to bottom. */
export const BUCKETS: readonly Bucket[] = ["needs", "working", "review", "done"];

/** Everything a row or card draws that is derived from one session. */
export interface RowModel {
  readonly id: string;
  readonly session: SessionInfo;
  readonly bucket: Bucket;
  readonly rank: number;
  readonly title: string;
  readonly issue: string;
  readonly project: string;
  readonly branch: string;
  /**
   * One Sentence-case state word. In the needs section it is the kind only
   * ("Permission", "CI failed"); elsewhere a full word ("Needs you", "In review").
   */
  readonly state: string;
  /** A literal text-colour class for `state`. */
  readonly stateClass: string;
  readonly chip: ChipTone;
  /** The agent's own sentence, or "". */
  readonly activity: string;
  /** The agent is blocked on a person. */
  readonly waiting: boolean;
  /** Set exactly when the session is a card. */
  readonly action: CardAction | null;
  /** The session's own timestamp for its age, falling back to the daemon's string. */
  readonly since: string;
  readonly ageFallback: string;
}

/**
 * The one Approve rule: the prompt is a permission request and exactly one of
 * its choices is labelled `Yes` (trimmed, exact). A label that merely contains
 * "yes" ("Yes, and don't ask again") is never it.
 */
export function approveChoice(prompt: AgentPrompt | null | undefined): AgentChoice | null {
  if (!prompt || prompt.kind !== "permission") return null;
  const yes = (prompt.choices ?? []).filter((c) => /^Yes$/.test(c.label.trim()));
  return yes.length === 1 ? yes[0] : null;
}

const REGRESSED = new Set(["ci_failed", "changes_requested", "merge_conflict"]);

export function bucketOf(s: SessionInfo): Bucket {
  const key = kanbanKey(s.agentState, s.delivery);
  if (key === "needs" || key === "fixing" || s.delivery === "approved") return "needs";
  if (key === "review") return "review";
  if (key === "done") return "done";
  return "working";
}

/** The card's action by the first matching condition; the Approve upgrade is the card's. */
function cardAction(s: SessionInfo): CardAction | null {
  if (s.agentState === "waiting_input") return "answer";
  switch (s.delivery) {
    case "merge_conflict":
      return "resolve";
    case "ci_failed":
      return "checks";
    case "changes_requested":
      return "address";
    case "approved":
      return "merge";
    default:
      return null;
  }
}

/** The needs-section kind word for an input reason: permission, question, dialog, else needs. */
function kindWord(reason: string | undefined): string {
  switch (reason) {
    case "permission_prompt":
      return copy.inbox.kinds.permission;
    case "question":
    case "AskUserQuestion":
      return copy.inbox.kinds.question;
    case "dialog":
    case "modal":
      return copy.inbox.kinds.dialog;
    default:
      return copy.inbox.kinds.needs;
  }
}

function stateOf(s: SessionInfo, bucket: Bucket): { text: string; cls: string; chip: ChipTone } {
  if (s.agentState === "waiting_input") {
    const text = bucket === "needs" ? kindWord(s.inputReason) : statusWord("needs_input");
    return { text, cls: displayText("needs_you"), chip: "urgent" };
  }
  const delivery = s.delivery ?? "";
  if (REGRESSED.has(delivery)) {
    return { text: statusWord(delivery), cls: deliveryText(delivery), chip: "broken" };
  }
  if (bucket === "needs") return { text: statusWord(delivery), cls: deliveryText(delivery), chip: "grey" };
  if (bucket === "review") return { text: statusWord("review_pending"), cls: deliveryText("review_pending"), chip: "grey" };
  if (bucket === "done") {
    const word = delivery === "merged" || delivery === "closed" ? statusWord(delivery) : "Ended";
    return { text: word, cls: "text-faint", chip: "grey" };
  }
  const ci = delivery === "ci_pending" || delivery === "draft" ? statusWord(delivery) : "";
  return { text: ci || statusWord("working"), cls: displayText("working"), chip: "grey" };
}

/** Per-session display data. Pure: called once per changed session. */
export function rowModel(s: SessionInfo): RowModel {
  const bucket = bucketOf(s);
  const st = stateOf(s, bucket);
  return {
    id: s.id,
    session: s,
    bucket,
    rank: sortRank(s.agentState, s.delivery),
    title: s.title || s.issue || s.id.slice(0, 12),
    issue: s.title ? s.issue : "",
    project: s.project,
    branch: s.branch,
    state: st.text,
    stateClass: st.cls,
    chip: st.chip,
    activity: s.headline || s.lastNotification || "",
    waiting: s.agentState === "waiting_input",
    action: bucket === "needs" ? cardAction(s) : null,
    since: s.agentStateSince || s.firstSeen || "",
    ageFallback: s.age,
  };
}

function compare(a: RowModel, b: RowModel): number {
  return (
    a.rank - b.rank ||
    a.project.localeCompare(b.project) ||
    a.issue.localeCompare(b.issue) ||
    a.id.localeCompare(b.id)
  );
}

/** A phone-rendered age; re-reads the shared minute clock. */
export function ageLabel(at: string | undefined, fallback = ""): string {
  const { text } = fluidAgo(at, minuteClock.now);
  return text || fallback;
}

/** "22m" as "22 minutes ago"; a date or an unknown shape is returned as given. */
export function spokenAge(short: string): string {
  const t = short.trim();
  if (t === "now") return "just now";
  const m = /^(\d+)\s*([mhd])$/.exec(t);
  if (!m) return t;
  const n = Number(m[1]);
  const unit = { m: "minute", h: "hour", d: "day" }[m[2] as "m" | "h" | "d"];
  return `${n} ${unit}${n === 1 ? "" : "s"} ago`;
}

/** The age as VoiceOver should say it ("22 minutes ago"). */
export function ageSpoken(at: string | undefined, fallback = ""): string {
  const { text } = fluidAgo(at, minuteClock.now);
  return spokenAge(text || fallback);
}

/** The one separator of a meta line: space, U+00B7, space. */
export const META_SEP = " \u00B7 ";

/**
 * The parts of a meta line, empty ones dropped: project label, the issue id (else the
 * branch) and, when given, the age. One helper so cards, rows, Activity and Project
 * detail never glue the string by hand.
 */
export function metaParts(model: RowModel, projectLabel: string, age = ""): string[] {
  const ref = model.session.issue || model.branch;
  return [projectLabel, ref, age].filter((p) => p !== "");
}

/** "Project · NOR-411 · 22m": the parts joined by a spaced middle dot. */
export function metaLine(model: RowModel, projectLabel: string, age = ""): string {
  return metaParts(model, projectLabel, age).join(META_SEP);
}

/** The composite label of a card or row: "Question, Which period…, Nori App NOR-411, 22 minutes ago". */
export function rowAriaLabel(model: RowModel, projectLabel: string): string {
  const meta = metaParts(model, projectLabel).join(" ");
  return [model.state, model.title, meta, ageSpoken(model.since, model.ageFallback)]
    .filter((p) => p !== "")
    .join(", ");
}

export class InboxModel {
  /** The sessions as last pushed, keyed by id; values keep the pushed object's identity. */
  readonly rows = new SvelteMap<string, SessionInfo>();
  readonly models = new SvelteMap<string, RowModel>();
  /** True once a list has been received or seeded. */
  loaded = $state(false);

  private readonly modelOf: (s: SessionInfo) => RowModel;

  constructor(modelOf: (s: SessionInfo) => RowModel = rowModel) {
    this.modelOf = modelOf;
  }

  /** Brings the map to `list`: new or changed sessions are upserted, absent ones removed. */
  ingest(list: readonly SessionInfo[]): void {
    const seen = new Set<string>();
    for (const s of list) {
      seen.add(s.id);
      if (this.rows.get(s.id) === s) continue;
      this.rows.set(s.id, s);
      this.models.set(s.id, this.modelOf(s));
    }
    for (const id of [...this.rows.keys()]) {
      if (seen.has(id)) continue;
      this.rows.delete(id);
      this.models.delete(id);
    }
    this.loaded = true;
  }

  /** Models per bucket, each in attention order. Unchanged models keep their identity. */
  readonly buckets = $derived.by(() => {
    const out: Record<Bucket, RowModel[]> = { needs: [], working: [], review: [], done: [] };
    for (const m of this.models.values()) out[m.bucket].push(m);
    for (const b of BUCKETS) out[b].sort(compare);
    return out;
  });

  readonly counts = $derived({
    needs: this.buckets.needs.length,
    working: this.buckets.working.length,
    review: this.buckets.review.length,
    done: this.buckets.done.length,
  });

  /** Ids of the needs cards worth a live `talk` watch: blocked on a person, first few only. */
  readonly watched = $derived(
    new Set(
      this.buckets.needs
        .filter((m) => m.waiting)
        .slice(0, MAX_INBOX_TALK_WATCH)
        .map((m) => m.id),
    ),
  );

  /** Needs-you count, for the tab badge and the header. */
  get needsCount(): number {
    return this.counts.needs;
  }
}

export const inbox = new InboxModel();

/**
 * Feeds `inbox` from the pushed `daemon:sessions` event, whose rows keep their
 * identity between pushes, and seeds it from what the store already holds. The
 * map is never cleared while the daemon is away, so the last snapshot stays.
 * Returns the teardown.
 */
export function attachInbox(model: InboxModel = inbox): () => void {
  untrack(() => {
    if (model.rows.size === 0 && store.sessions.length > 0) {
      model.ingest($state.snapshot(store.sessions) as SessionInfo[]);
    }
  });
  return Events.On("daemon:sessions", (e) => {
    model.ingest((e.data?.sessions ?? []) as SessionInfo[]);
  });
}

/** One entry of the Inbox's flat, virtualized list. */
export type InboxItem =
  | { key: string; kind: "section"; bucket: Bucket; count: number }
  /** A project header in a list grouped by project. Drawn like a section header. */
  | { key: string; kind: "group"; project: string; label: string; count: number }
  | { key: string; kind: "card"; model: RowModel }
  | { key: string; kind: "row"; model: RowModel }
  | { key: string; kind: "more"; hidden: number }
  /** The all-clear block that leads an unfiltered list when nothing needs the person. */
  | { key: string; kind: "clear" };

/** The row kind VirtualList estimates heights with. */
export function itemKind(it: InboxItem): RowKind {
  return it.kind === "section" || it.kind === "group" ? "section" : it.kind === "card" || it.kind === "clear" ? "card" : "row";
}

/**
 * The item objects of the last list, so an item whose content is unchanged keeps
 * its identity from one list to the next. The list is rebuilt on every push; a
 * new object for an unchanged row would make the keyed list hand every mounted
 * row a new item, and each of them would re-run its derived labels (spec §8.1:
 * a delta that changes one session re-renders one row).
 */
export class ItemCache {
  /** Card and row items by model: a model's bucket, and so its kind, never changes. */
  private byModel = new WeakMap<RowModel, InboxItem>();
  /** Section and "more" items by key, kept while their count is the same. */
  private byKey = new Map<string, InboxItem>();
  private static readonly CLEAR: InboxItem = { key: "clear", kind: "clear" };

  model(m: RowModel): InboxItem {
    const had = this.byModel.get(m);
    if (had) return had;
    const kind = m.bucket === "needs" ? "card" : "row";
    const it: InboxItem = { key: `${kind}:${m.id}`, kind, model: m };
    this.byModel.set(m, it);
    return it;
  }

  section(bucket: Bucket, count: number): InboxItem {
    const key = `section:${bucket}`;
    const had = this.byKey.get(key);
    if (had?.kind === "section" && had.count === count) return had;
    const it: InboxItem = { key, kind: "section", bucket, count };
    this.byKey.set(key, it);
    return it;
  }

  group(project: string, label: string, count: number): InboxItem {
    const key = `group:${project}`;
    const had = this.byKey.get(key);
    if (had?.kind === "group" && had.label === label && had.count === count) return had;
    const it: InboxItem = { key, kind: "group", project, label, count };
    this.byKey.set(key, it);
    return it;
  }

  more(hidden: number): InboxItem {
    const had = this.byKey.get("more:done");
    if (had?.kind === "more" && had.hidden === hidden) return had;
    const it: InboxItem = { key: "more:done", kind: "more", hidden };
    this.byKey.set("more:done", it);
    return it;
  }

  clear(): InboxItem {
    return ItemCache.CLEAR;
  }
}

/**
 * The list for a filter: sections in order, empty ones dropped, Done cut to
 * `DONE_VISIBLE` unless `showAllDone`. An unfiltered list with an empty Needs
 * section starts with the `clear` block. Items come from `cache`, so a list
 * built from the same models twice holds the same objects. With `grouping`,
 * the list is one section per project instead (see `groupedItems`).
 */
export function inboxItems(
  buckets: Record<Bucket, readonly RowModel[]>,
  filter: InboxFilter,
  showAllDone: boolean,
  cache: ItemCache = new ItemCache(),
  grouping?: GroupOptions,
): InboxItem[] {
  if (grouping) return groupedItems(buckets, filter, showAllDone, cache, grouping);
  const out: InboxItem[] = [];
  // Nothing needs you but something else is listed: lead with the all-clear block.
  if (filter === "" && buckets.needs.length === 0 && BUCKETS.some((b) => buckets[b].length > 0)) {
    out.push(cache.clear());
  }
  for (const b of BUCKETS) {
    if (filter !== "" && filter !== b) continue;
    const list = buckets[b];
    if (list.length === 0) continue;
    // A filtered list is one bucket: the chip carries its name and count.
    if (filter === "") out.push(cache.section(b, list.length));
    const shown = b === "done" && !showAllDone ? list.slice(0, DONE_VISIBLE) : list;
    for (const m of shown) out.push(cache.model(m));
    if (shown.length < list.length) out.push(cache.more(list.length - shown.length));
  }
  return out;
}

/** The Inbox's grouping choice, remembered across launches. */
export class InboxView {
  group = $state<InboxGroup>(loadInboxGroup());

  set(group: InboxGroup): void {
    this.group = group;
    saveInboxGroup(group);
  }
}

export const inboxView = new InboxView();

/**
 * Project ids in the desktop's arrangement: loose projects in config order with
 * each folder spliced in at its position, a folder's members in place, and a
 * stack's members right after the stack (the sidebar hides them under it).
 */
export function projectOrder(
  projects: readonly LayoutProject[],
  groups: readonly LayoutGroup[],
): string[] {
  const list = [...projects];
  const members = stackMembers(list);
  const out: string[] = [];
  const seen = new Set<string>();
  const add = (p: LayoutProject): void => {
    if (seen.has(p.name)) return;
    seen.add(p.name);
    out.push(p.name);
    for (const m of members.get(p.name) ?? []) add(m);
  };
  for (const row of buildRows(list, [...groups])) {
    if (row.kind === "project") add(row.project);
    else row.projects.forEach(add);
  }
  return out;
}

/** What `inboxItems` needs to list by project. */
export type GroupOptions = {
  /** Project ids in display order, from `projectOrder`. */
  order: readonly string[];
  /** A project id as a person reads it; "" is a session with no project. */
  label: (project: string) => string;
};

/**
 * The list grouped by project. Groups follow `order`; a project it does not name
 * comes after, by label, and sessions with no project come last. Inside a group
 * the status order holds, so needs-you cards lead. The status filter narrows the
 * models, and Done is cut globally to `DONE_VISIBLE` (a hidden row has no way to
 * be attributed to a project). A header counts the rows listed under it.
 */
function groupedItems(
  buckets: Record<Bucket, readonly RowModel[]>,
  filter: InboxFilter,
  showAllDone: boolean,
  cache: ItemCache,
  { order, label }: GroupOptions,
): InboxItem[] {
  const out: InboxItem[] = [];
  if (filter === "" && buckets.needs.length === 0 && BUCKETS.some((b) => buckets[b].length > 0)) {
    out.push(cache.clear());
  }
  const byProject = new Map<string, RowModel[]>();
  let hidden = 0;
  for (const b of BUCKETS) {
    if (filter !== "" && filter !== b) continue;
    const list = buckets[b];
    const shown = b === "done" && !showAllDone ? list.slice(0, DONE_VISIBLE) : list;
    hidden += list.length - shown.length;
    for (const m of shown) {
      const group = byProject.get(m.project);
      if (group) group.push(m);
      else byProject.set(m.project, [m]);
    }
  }
  const rank = new Map(order.map((p, i) => [p, i]));
  const projects = [...byProject.keys()].sort((a, b) => {
    if ((a === "") !== (b === "")) return a === "" ? 1 : -1;
    const ra = rank.get(a);
    const rb = rank.get(b);
    if (ra !== undefined && rb !== undefined) return ra - rb;
    if (ra !== undefined || rb !== undefined) return ra !== undefined ? -1 : 1;
    return label(a).localeCompare(label(b)) || a.localeCompare(b);
  });
  for (const project of projects) {
    // Insertion followed BUCKETS order and each bucket is already sorted: keep both.
    const models = byProject.get(project)!;
    out.push(cache.group(project, label(project), models.length));
    for (const m of models) out.push(cache.model(m));
  }
  if (hidden > 0) out.push(cache.more(hidden));
  return out;
}

/** The performance mark the boot budget ends at (spec §8.1). */
export const INTERACTIVE_MARK = "inbox-interactive";

/**
 * Marks the first painted session list, once per launch. Debug builds also log
 * `perf boot=<ms> path=<kind>`; `bootMs` keeps the same number for Settings.
 */
export function markInteractive(): void {
  if (typeof performance === "undefined" || performance.getEntriesByName(INTERACTIVE_MARK).length > 0) return;
  performance.mark(INTERACTIVE_MARK);
  if (import.meta.env.MODE === "production") return;
  void readStatus().then((s) => {
    const ms = bootMs(s?.launchedAt);
    if (ms !== null) console.info(`perf boot=${ms} path=${s?.path?.kind ?? "none"}`);
  });
}

/** Root font size past which a card title gets a third line (Dynamic Type has raised it). */
export const LARGE_TYPE_PX = 19;

function readLargeType(): boolean {
  if (typeof document === "undefined") return false;
  const px = parseFloat(getComputedStyle(document.documentElement).fontSize);
  return Number.isFinite(px) && px > LARGE_TYPE_PX;
}

/** True while the root font size is past `LARGE_TYPE_PX`; re-read on resize and foreground. */
class LargeType {
  value = $state(readLargeType());
  private attached = false;

  /** Starts tracking once per page; the listeners live as long as the app does. Call from an effect. */
  watch(): void {
    if (this.attached || typeof window === "undefined") return;
    this.attached = true;
    const apply = (): void => {
      this.value = readLargeType();
    };
    apply();
    window.addEventListener("resize", apply);
    document.addEventListener("visibilitychange", apply);
  }
}

export const largeType = new LargeType();
