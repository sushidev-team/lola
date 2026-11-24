// Only the human's submitted console input travels to the inspected page. All
// results are untrusted display data, rendered as text in the desktop sidebar.
export type DebugTarget = { frame: Window; origin: string; documentId: string };
export type DebugCommand = { op: string; scoped?: boolean; documentKey?: string; after?: number; revision?: number; treeVersion?: number; offset?: number; computed?: boolean; styles?: boolean; elements?: boolean; index?: number; on?: boolean; text?: string; name?: string; originalName?: string; ruleId?: number; value?: string };
export type ConsoleLine = { id: number; level: string; text: string };
export type TreeRow = { id: number; depth: number; name: string; attrs: string[][]; text: string; count: number; expanded: boolean; foreign?: boolean; limited?: boolean; selected: boolean; offset?: number };
export type MatchedRule = { id?: number; selector: string; source: string; context: string; declarations: string[][] };
export type ElementInfo = { tag: string; width: number; height: number; css: string[][]; attrs: string[][]; inline: string[][]; rules: MatchedRule[]; unreadable: number; truncated: boolean };
export type DebugSnapshot = { documentKey: string; width: number; height: number; picking: boolean; revision: number; lines: ConsoleLine[]; editError?: string; element?: ElementInfo; tree?: TreeRow[]; treeScoped?: boolean; treeVersion?: number };
export type DebugConnection = { ready: Promise<unknown>; run(cmd: DebugCommand): Promise<DebugSnapshot | null>; close(): void };

// External pages and iframe scripts can replace their debugger. Validate both
// transports before untrusted values reach keyed rows or DOM attributes.
function snapshot(value: unknown): DebugSnapshot | null {
  if (!value || typeof value !== "object") return null;
  const s = value as DebugSnapshot;
  if (typeof s.documentKey !== "string" || !Number.isSafeInteger(s.revision) ||
      !Number.isFinite(s.width) || !Number.isFinite(s.height) || !Array.isArray(s.lines)) return null;
  const lines = s.lines.slice(-60).filter(l => Number.isSafeInteger(l?.id) && typeof l.level === "string" && typeof l.text === "string")
    .map(l => ({ id: l.id, level: l.level.slice(0, 12), text: l.text.slice(0, 1800) }));
  const pairs = (list: string[][]) => Array.isArray(list) ? list.filter(p =>
    Array.isArray(p) && typeof p[0] === "string" && typeof p[1] === "string").map(p => [p[0], p[1].slice(0, 2048)]) : [];
  // CSSOM property names are unique within a block; the rows use them as keys.
  const declarations = (list: string[][]) => {
    const rows = new Map<string, string[]>();
    if (Array.isArray(list)) for (const row of list) {
      if (!Array.isArray(row) || typeof row[0] !== "string" || typeof row[1] !== "string") continue;
      const item = [row[0], row[1].slice(0, 2060)];
      if (row[2] === "disabled") item.push("disabled");
      rows.set(row[0], item);
    }
    return [...rows.values()];
  };
  const seenRules = new Set<number>();
  function ruleId(id?: number) {
    if (typeof id !== "number" || !Number.isSafeInteger(id) || id <= 0 || seenRules.has(id)) return undefined;
    seenRules.add(id);
    return id;
  }
  let element: ElementInfo | undefined;
  const el = s.element;
  if (el && typeof el.tag === "string" && [el.width, el.height].every(Number.isFinite)) {
    element = { tag: el.tag.slice(0, 150), width: el.width, height: el.height, attrs: pairs(el.attrs), css: pairs(el.css), inline: declarations(el.inline),
      rules: Array.isArray(el.rules) ? el.rules.slice(0, 120).filter(rule => typeof rule?.selector === "string").map(rule => ({
        id: ruleId(rule.id), selector: rule.selector.slice(0, 1024), source: String(rule.source ?? "").slice(0, 1024),
        context: String(rule.context ?? "").slice(0, 1024), declarations: declarations(rule.declarations),
      })) : [], unreadable: Math.max(0, Number(el.unreadable) || 0), truncated: el.truncated === true };
  }
  const tree = Array.isArray(s.tree) ? s.tree.slice(0, 500).filter(row => Number.isSafeInteger(row?.id) &&
    Number.isSafeInteger(row.depth) && typeof row.name === "string").map(row => ({
      id: row.id, name: row.name.slice(0, 150), depth: Math.max(0, Math.min(400, row.depth)), attrs: pairs(row.attrs).slice(0, 2),
      text: typeof row.text === "string" ? row.text.slice(0, 80) : "", count: Number(row.count) || 0, expanded: row.expanded === true, foreign: row.foreign === true, limited: row.limited === true,
      selected: row.selected === true, offset: Number.isSafeInteger(row.offset) ? row.offset : undefined,
    })) : undefined;
  return { documentKey: s.documentKey.slice(0, 128), revision: s.revision, width: s.width, height: s.height,
    picking: s.picking === true, lines, element, tree, treeScoped: s.treeScoped === true, editError: typeof s.editError === "string" ? s.editError.slice(0, 180) : undefined, treeVersion: s.treeVersion };

}

export function connectPreviewTools(id: number, target: DebugTarget | null, dirty: () => void): DebugConnection {
  if (!target) {
    const bridge = (window as Window & { webkit?: { messageHandlers?: { lolaPreviewTools?: {
      postMessage(value: unknown): Promise<string | null>;
    } } } }).webkit?.messageHandlers?.lolaPreviewTools;
    if (!bridge) throw new Error("External page tools require the Lola desktop app.");
    const token = crypto.randomUUID();
    const send = (cmd: DebugCommand) => bridge.postMessage({ ...cmd, id, token });
    return { ready: send({ op: "open" }), async run(cmd) {
      const raw = await send(cmd);
      return raw ? snapshot(JSON.parse(raw)) : null;
    }, close() { void send({ op: "close" }).catch(() => {}); } };
  }

  const { port1, port2 } = new MessageChannel();
  let seq = 0;
  const pending = new Map<number, { resolve(value: DebugSnapshot | null): void; reject(reason: Error): void }>();
  let ready!: () => void, fail!: (reason: Error) => void;
  const connected = new Promise<void>((resolve, reject) => { ready = resolve; fail = reject; });
  const timeout = setTimeout(() => fail(new Error("The page did not connect. Reload the preview to retry.")), 4000);
  port1.onmessage = ({ data }) => {
    if (data?.ready) { clearTimeout(timeout); ready(); return; }
    if (data?.dirty) { dirty(); return; }
    const call = pending.get(data?.id);
    if (!call) return;
    pending.delete(data.id);
    if (data.error) call.reject(new Error(String(data.error)));
    else call.resolve(snapshot(data.result));
  };
  target.frame.postMessage({ target: "lola-preview", type: "debug-connect", documentId: target.documentId }, target.origin, [port2]);
  return { ready: connected, run(cmd) {
    const id = ++seq;
    return new Promise((resolve, reject) => { pending.set(id, { resolve, reject }); port1.postMessage({ ...cmd, id }); });
  }, close() {
    clearTimeout(timeout);
    port1.postMessage({ op: "close" });
    port1.close();
    const closed = new Error("Preview tools closed.");
    fail(closed);
    pending.forEach(call => call.reject(closed));
    pending.clear();
  } };
}
