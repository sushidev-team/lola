// The phone's route stack and the one path grammar every entry point shares
// (dev links, preview fixtures, tests). Spec: docs/mobile/04-client.md §5.1.
//
// Invariants:
//  - `parseRoute` is pure and fails closed: anything outside the grammar is
//    null and the caller keeps its current route.
//  - Losing the connection never routes. `back()` never lands on Connect; only
//    `not_paired` and an explicit unpair put Connect on the stack.
//  - Any navigation closes the open sheet.

export type Seg = "conversation" | "changes" | "review" | "terminal";
export type Tab = "inbox" | "projects" | "activity" | "settings";
export type InboxFilter = "" | "needs" | "working" | "review" | "done";
export type ConnectState =
  | "scan"
  | "code"
  | "refused"
  | "expired"
  | "failed"
  | "dropped"
  | "not-paired"
  | "no-camera";

export type Route =
  | { name: "connect"; state: ConnectState }
  | { name: "tab"; tab: Tab; project?: string; pick?: "prs" | "tickets"; filter?: InboxFilter }
  | { name: "session"; id: string; seg: Seg; pane?: string }
  | { name: "dev"; page: "bridge-guard" };

export const SHEETS = [
  "sessionMenu",
  "newSession",
  "attach",
  "model",
  "merge",
  "feedback",
  "checkpoints",
  "bestOf",
  "devLinks",
  "rename",
  "broadcast",
  "tasks",
  "switchAgent",
  "paneMenu",
  "viewSettings",
  "filter",
  "connection",
  "reviewPackage",
  "checks",
  "unpair",
] as const;
export type SheetName = (typeof SHEETS)[number] | "";

export const SEGS: readonly Seg[] = ["conversation", "changes", "review", "terminal"];
export const TABS: readonly Tab[] = ["inbox", "projects", "activity", "settings"];
export const CONNECT_STATES: readonly ConnectState[] = [
  "scan",
  "code",
  "refused",
  "expired",
  "failed",
  "dropped",
  "not-paired",
  "no-camera",
];
const FILTERS: readonly Exclude<InboxFilter, "">[] = ["needs", "working", "review", "done"];
const PICKS = ["prs", "tickets"] as const;

/** Debug-only routes exist everywhere but a production build. */
const DEBUG_BUILD = import.meta.env.MODE !== "production";

export interface ParseContext {
  /** A daemon is already paired: `/connect/*` collapses to the inbox. */
  paired?: boolean;
  /** Overrides the build mode; `/dev/*` parses only when true. */
  debug?: boolean;
}

/** A route plus the sheet its path asked for. */
export interface Location {
  route: Route;
  sheet: SheetName;
  /** The tab a sheet opens on (`&tab=visuals`); only with a sheet. The sheet ignores a tab it does not have. */
  tab?: string;
}

const NAME = /^[^\s/?#\u0000-\u001f]{1,160}$/;
const SHEET_TAB = /^[a-z]{1,16}$/;

export function isSheetName(v: string): v is Exclude<SheetName, ""> {
  return (SHEETS as readonly string[]).includes(v);
}

function decode(seg: string): string | null {
  try {
    const v = decodeURIComponent(seg);
    return NAME.test(v) ? v : null;
  } catch {
    return null;
  }
}

function oneOf<T extends string>(list: readonly T[], v: string): T | null {
  return (list as readonly string[]).includes(v) ? (v as T) : null;
}

function parseRoutePart(segs: string[], q: URLSearchParams, ctx: ParseContext): Route | null {
  const [head, a, b, c] = segs;
  const inbox: Route = { name: "tab", tab: "inbox" };
  switch (head) {
    case "connect": {
      if (segs.length > 2) return null;
      const state = a === undefined ? "scan" : a === "scan" ? null : oneOf(CONNECT_STATES, a);
      if (!state) return null;
      return ctx.paired ? inbox : { name: "connect", state };
    }
    case "inbox": {
      if (segs.length > 1) return null;
      const raw = q.get("filter");
      if (raw === null || raw === "") return inbox;
      const filter = oneOf(FILTERS, raw);
      return filter ? { name: "tab", tab: "inbox", filter } : null;
    }
    case "activity":
    case "settings":
      return segs.length === 1 ? { name: "tab", tab: head } : null;
    case "projects": {
      if (segs.length === 1) return { name: "tab", tab: "projects" };
      const project = decode(a);
      if (!project || segs.length > 3) return null;
      if (b === undefined) return { name: "tab", tab: "projects", project };
      const pick = oneOf(PICKS, b);
      return pick ? { name: "tab", tab: "projects", project, pick } : null;
    }
    case "session": {
      const id = a === undefined ? null : decode(a);
      const seg = b === undefined ? null : oneOf(SEGS, b);
      if (!id || !seg || segs.length > 4) return null;
      if (c === undefined) return { name: "session", id, seg };
      const pane = decode(c);
      return pane ? { name: "session", id, seg, pane } : null;
    }
    case "dev":
      return ctx.debug && segs.length === 2 && a === "bridge-guard"
        ? { name: "dev", page: "bridge-guard" }
        : null;
    default:
      return null;
  }
}

/** Route and sheet of a path, or null when any part is outside the grammar. */
export function parseLocation(path: string, ctx: ParseContext = {}): Location | null {
  if (typeof path !== "string" || !path.startsWith("/")) return null;
  const cx: ParseContext = { paired: ctx.paired ?? false, debug: ctx.debug ?? DEBUG_BUILD };
  const [pathname, query = ""] = path.split("?", 2);
  const q = new URLSearchParams(query);
  const sheet = q.get("sheet") ?? "";
  if (sheet !== "" && !isSheetName(sheet)) return null;
  const tab = q.get("tab");
  if (tab !== null && (sheet === "" || !SHEET_TAB.test(tab))) return null;
  const route = parseRoutePart(pathname.slice(1).split("/"), q, cx);
  if (!route) return null;
  return tab ? { route, sheet: sheet as SheetName, tab } : { route, sheet: sheet as SheetName };
}

export function parseRoute(path: string, ctx: ParseContext = {}): Route | null {
  return parseLocation(path, ctx)?.route ?? null;
}

const enc = encodeURIComponent;

export function formatRoute(r: Route): string {
  switch (r.name) {
    case "connect":
      return r.state === "scan" ? "/connect" : `/connect/${r.state}`;
    case "dev":
      return `/dev/${r.page}`;
    case "session":
      return `/session/${enc(r.id)}/${r.seg}${r.pane ? `/${enc(r.pane)}` : ""}`;
    case "tab":
      if (r.tab === "inbox") return r.filter ? `/inbox?filter=${r.filter}` : "/inbox";
      if (r.tab === "projects") {
        if (!r.project) return "/projects";
        return `/projects/${enc(r.project)}${r.pick ? `/${r.pick}` : ""}`;
      }
      return `/${r.tab}`;
  }
}

export function formatLocation(l: Location): string {
  const path = formatRoute(l.route);
  if (l.sheet === "") return path;
  return `${path}${path.includes("?") ? "&" : "?"}sheet=${l.sheet}${l.tab ? `&tab=${l.tab}` : ""}`;
}

export class Router {
  stack = $state<Route[]>([{ name: "tab", tab: "inbox" }]);
  sheet = $state<SheetName>("");
  /** The tab the open sheet was asked to start on, or "". */
  sheetTab = $state("");

  get top(): Route {
    return this.stack[this.stack.length - 1];
  }

  /** A session over a tab. */
  push(r: Route): void {
    this.stack = [...this.stack, r];
    this.sheet = "";
  }

  /** Segment change, tab change, connect transitions. */
  replace(r: Route): void {
    this.stack = [...this.stack.slice(0, -1), r];
    this.sheet = "";
  }

  /** Pops, or climbs a tab (pick, then project, then list). Never reaches Connect. */
  back(): void {
    this.sheet = "";
    if (this.stack.length > 1) {
      if (this.stack[this.stack.length - 2].name === "connect") return;
      this.stack = this.stack.slice(0, -1);
      return;
    }
    const t = this.top;
    if (t.name !== "tab") return;
    if (t.pick) this.replace({ name: "tab", tab: t.tab, project: t.project });
    else if (t.project) this.replace({ name: "tab", tab: t.tab });
  }

  open(s: SheetName, tab = ""): void {
    this.sheet = s;
    this.sheetTab = tab;
  }

  close(): void {
    this.sheet = "";
    this.sheetTab = "";
  }
}

export const router = new Router();
