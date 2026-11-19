// Pure helpers behind the diff tab (DiffView.svelte): number a file's patch,
// pair it into split rows, file-tree the changed paths, and tokenize a line for
// syntax colouring. No DOM, no store — everything here is unit-tested.
//
// The patch text comes from the daemon (cmd=diff → internal/gitdiff): each
// file's unified diff from its first "@@" on. The line numbering mirrors
// gitdiff.ParseLines on the Go side, which the TUI uses.

export type RowKind = "ctx" | "add" | "del" | "hunk" | "meta";

export type Row = {
  kind: RowKind;
  /** 1-based line in the base version; 0 when the row has none. */
  oldLine: number;
  /** 1-based line in the working tree; 0 when the row has none. */
  newLine: number;
  text: string;
  /** Index of this row's line in the patch text; what a line-level stage sends back. */
  at?: number;
};

const HUNK_RE = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/;

/** Number every row of one file's patch. Tolerant: an unplaceable row is kept. */
export function parsePatch(patch: string | undefined): Row[] {
  const rows: Row[] = [];
  if (!patch) return rows;
  let oldN = 0;
  let newN = 0;
  const lines = patch.endsWith("\n") ? patch.slice(0, -1).split("\n") : patch.split("\n");
  for (const [at, raw] of lines.entries()) {
    if (raw === "") continue;
    switch (raw[0]) {
      case "@": {
        const m = HUNK_RE.exec(raw);
        if (m) {
          oldN = Number(m[1]);
          newN = Number(m[2]);
        }
        rows.push({ kind: "hunk", oldLine: 0, newLine: 0, text: raw, at });
        break;
      }
      case "+":
        rows.push({ kind: "add", oldLine: 0, newLine: newN++, text: raw.slice(1), at });
        break;
      case "-":
        rows.push({ kind: "del", oldLine: oldN++, newLine: 0, text: raw.slice(1), at });
        break;
      case "\\":
        rows.push({ kind: "meta", oldLine: 0, newLine: 0, text: raw, at });
        break;
      default:
        rows.push({ kind: "ctx", oldLine: oldN++, newLine: newN++, text: raw.slice(1), at });
    }
  }
  return rows;
}

/** One split-view row: the base side on the left, the working tree on the right. */
export type SplitRow = { left: Row | null; right: Row | null; full?: Row };

/** Pair unified rows for the side-by-side view: a run of removals and the run
 * of additions after it are laid side by side; context sits on both sides; a
 * hunk header or meta row spans the whole width (`full`). */
export function splitRows(rows: Row[]): SplitRow[] {
  const out: SplitRow[] = [];
  let i = 0;
  while (i < rows.length) {
    const r = rows[i];
    if (r.kind === "hunk" || r.kind === "meta") {
      out.push({ left: null, right: null, full: r });
      i++;
    } else if (r.kind === "ctx") {
      out.push({ left: r, right: r });
      i++;
    } else {
      const dels: Row[] = [];
      const adds: Row[] = [];
      while (i < rows.length && rows[i].kind === "del") dels.push(rows[i++]);
      while (i < rows.length && rows[i].kind === "add") adds.push(rows[i++]);
      for (let k = 0; k < Math.max(dels.length, adds.length); k++) {
        out.push({ left: dels[k] ?? null, right: adds[k] ?? null });
      }
    }
  }
  return out;
}

/** A half-open [start, end) range of characters within a row's text. */
export type Span = [number, number];

const PIECE_RE = /[A-Za-z0-9_$]+|\s+|[^A-Za-z0-9_$\s]/gu;
const MAX_PIECES = 400;

/** The characters that differ between a removed line and the added line that
 * replaces it, as ranges in each. Compared by word, whitespace run and
 * punctuation mark (so a changed identifier lights up whole), via a longest
 * common subsequence. Null when a highlight would say nothing: no shared
 * piece, or lines too long to compare cheaply. */
export function changedSpans(oldText: string, newText: string): { old: Span[]; new: Span[] } | null {
  const a = oldText.match(PIECE_RE) ?? [];
  const b = newText.match(PIECE_RE) ?? [];
  if (a.length > MAX_PIECES || b.length > MAX_PIECES) return null;
  const w = b.length + 1;
  const lcs = new Uint16Array((a.length + 1) * w);
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lcs[i * w + j] = a[i] === b[j] ? lcs[(i + 1) * w + j + 1] + 1 : Math.max(lcs[(i + 1) * w + j], lcs[i * w + j + 1]);
    }
  }
  const keepA = new Array<boolean>(a.length).fill(false);
  const keepB = new Array<boolean>(b.length).fill(false);
  let i = 0;
  let j = 0;
  let shared = false;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      keepA[i] = keepB[j] = true;
      if (a[i].trim() !== "") shared = true;
      i++;
      j++;
    } else if (lcs[(i + 1) * w + j] >= lcs[i * w + j + 1]) i++;
    else j++;
  }
  if (!shared) return null;
  const spans = (pieces: string[], keep: boolean[]): Span[] => {
    const out: Span[] = [];
    let at = 0;
    pieces.forEach((p, k) => {
      if (!keep[k]) {
        const last = out[out.length - 1];
        if (last && last[1] === at) last[1] = at + p.length;
        else out.push([at, at + p.length]);
      }
      at += p.length;
    });
    return out;
  };
  return { old: spans(a, keepA), new: spans(b, keepB) };
}

/** Changed-character ranges for every removed/added row that has a partner,
 * paired the way splitRows pairs them (k-th removal with k-th addition). */
export function intralineSpans(rows: Row[]): Map<Row, Span[]> {
  const out = new Map<Row, Span[]>();
  let i = 0;
  while (i < rows.length) {
    if (rows[i].kind !== "del") {
      i++;
      continue;
    }
    const dels: Row[] = [];
    const adds: Row[] = [];
    while (i < rows.length && rows[i].kind === "del") dels.push(rows[i++]);
    while (i < rows.length && rows[i].kind === "meta") i++;
    while (i < rows.length && rows[i].kind === "add") adds.push(rows[i++]);
    for (let k = 0; k < Math.min(dels.length, adds.length); k++) {
      const c = changedSpans(dels[k].text, adds[k].text);
      if (!c) continue;
      if (c.old.length) out.set(dels[k], c.old);
      if (c.new.length) out.set(adds[k], c.new);
    }
  }
  return out;
}

/** Cut syntax tokens at the span edges; `hot` marks the pieces inside a span. */
export function applySpans(tokens: Token[], spans: Span[] | undefined): (Token & { hot: boolean })[] {
  if (!spans || spans.length === 0) return tokens.map((t) => ({ ...t, hot: false }));
  const out: (Token & { hot: boolean })[] = [];
  let at = 0;
  for (const t of tokens) {
    let pos = 0;
    while (pos < t.text.length) {
      const abs = at + pos;
      const inside = spans.find((s) => abs >= s[0] && abs < s[1]);
      const edge = inside ? inside[1] : Math.min(Infinity, ...spans.filter((s) => s[0] > abs).map((s) => s[0]));
      const end = Math.min(t.text.length, edge - at);
      out.push({ kind: t.kind, text: t.text.slice(pos, end), hot: !!inside });
      pos = end;
    }
    at += t.text.length;
  }
  return out;
}

/** Which side of the diff a line lives on. A comment on a removed line names
 * the BASE line number (side "old"); everything else names the working tree. */
export type Side = "new" | "old";

/** The line a row can be commented at, or null for headers/meta rows. */
export function anchorOf(r: Row): { side: Side; line: number } | null {
  if (r.kind === "del") return { side: "old", line: r.oldLine };
  if (r.kind === "add" || r.kind === "ctx") return { side: "new", line: r.newLine };
  return null;
}

/** A selected range of commentable rows within ONE file, on one side. */
export type Selection = { path: string; side: Side; start: number; end: number };

/** Extend a selection to a new row (shift-click). A row on the other side or in
 * another file starts a fresh selection, since a range has one numbering. */
export function extendSelection(sel: Selection | null, path: string, side: Side, line: number): Selection {
  if (!sel || sel.path !== path || sel.side !== side) return { path, side, start: line, end: line };
  return { path, side, start: Math.min(sel.start, line), end: Math.max(sel.end, line) };
}

/** Whether a row falls inside the selection. */
export function inSelection(sel: Selection | null, path: string, r: Row): boolean {
  const a = anchorOf(r);
  if (!sel || !a || sel.path !== path || a.side !== sel.side) return false;
  return a.line >= sel.start && a.line <= sel.end;
}

/** The text of the selected rows, for the comment's quote. */
export function quoteFor(rows: Row[], sel: Selection): string {
  return rows
    .filter((r) => {
      const a = anchorOf(r);
      return a && a.side === sel.side && a.line >= sel.start && a.line <= sel.end;
    })
    .map((r) => r.text)
    .join("\n");
}

// --- file tree -------------------------------------------------------------

export type TreeNode = {
  /** Display name: a directory chain collapsed GitHub-style ("internal/daemon"). */
  name: string;
  /** Full path for a file; "" for a directory. */
  path: string;
  /** Unique identity for rendering: the full path of the file or directory,
   * prefixed "file:" or "dir:" so neither can ever collide with the other — a
   * root file may itself be NAMED "dir:client". Two
   * directories may share a name and a depth ("client/components",
   * "server/components"), so neither may be keyed on those alone. */
  key: string;
  depth: number;
  children: TreeNode[];
};

/** Build a directory tree of the changed paths, directories first, each level
 * sorted by name. A directory with a single child directory and no files is
 * collapsed into it ("a/b/c"), so a deep change does not cost five rows. */
export function buildTree(paths: string[]): TreeNode[] {
  type Dir = { dirs: Map<string, Dir>; files: string[] };
  const root: Dir = { dirs: new Map(), files: [] };
  for (const p of paths) {
    const parts = p.split("/");
    let d = root;
    for (const seg of parts.slice(0, -1)) {
      let next = d.dirs.get(seg);
      if (!next) d.dirs.set(seg, (next = { dirs: new Map(), files: [] }));
      d = next;
    }
    d.files.push(p);
  }
  const walk = (d: Dir, depth: number, prefix: string): TreeNode[] => {
    const out: TreeNode[] = [];
    for (const name of [...d.dirs.keys()].sort()) {
      let label = name;
      let sub = d.dirs.get(name)!;
      while (sub.files.length === 0 && sub.dirs.size === 1) {
        const [only, next] = [...sub.dirs.entries()][0];
        label += "/" + only;
        sub = next;
      }
      const full = prefix + label;
      out.push({ name: label, path: "", key: `dir:${full}`, depth, children: walk(sub, depth + 1, full + "/") });
    }
    for (const f of [...d.files].sort()) {
      out.push({ name: f.slice(f.lastIndexOf("/") + 1), path: f, key: `file:${f}`, depth, children: [] });
    }
    return out;
  };
  return walk(root, 0, "");
}

/** Depth-first flattening of a tree into the rows the file list draws. */
export function flattenTree(nodes: TreeNode[]): TreeNode[] {
  const out: TreeNode[] = [];
  const visit = (n: TreeNode) => {
    out.push(n);
    n.children.forEach(visit);
  };
  nodes.forEach(visit);
  return out;
}

// --- syntax colouring ------------------------------------------------------
//
// A deliberately small tokenizer rather than a highlighting library: a diff is
// read line by line (a hunk can start inside a block comment or a template
// string, which no per-line highlighter gets right anyway), and the job is to
// make keywords, strings, comments and numbers stand apart, not to parse. It
// knows the comment and string syntax of the languages lola projects are
// written in and one shared keyword set.

export type TokKind = "plain" | "kw" | "str" | "com" | "num";
export type Token = { kind: TokKind; text: string };

type Lang = { line: string[]; quotes: string };

const C_LIKE: Lang = { line: ["//"], quotes: `"'\`` };
const HASH: Lang = { line: ["#"], quotes: `"'` };
const LANGS: Record<string, Lang> = {
  go: { line: ["//"], quotes: `"'\`` },
  json: { line: [], quotes: '"' },
  ts: C_LIKE,
  tsx: C_LIKE,
  js: C_LIKE,
  jsx: C_LIKE,
  mjs: C_LIKE,
  cjs: C_LIKE,
  svelte: C_LIKE,
  vue: C_LIKE,
  java: C_LIKE,
  kt: C_LIKE,
  swift: C_LIKE,
  rs: C_LIKE,
  c: C_LIKE,
  h: C_LIKE,
  cpp: C_LIKE,
  cs: C_LIKE,
  php: { line: ["//", "#"], quotes: `"'` },
  css: { line: [], quotes: `"'` },
  scss: C_LIKE,
  py: HASH,
  rb: HASH,
  sh: HASH,
  bash: HASH,
  zsh: HASH,
  toml: HASH,
  yaml: HASH,
  yml: HASH,
  sql: { line: ["--"], quotes: `'"` },
  lua: { line: ["--"], quotes: `'"` },
};

/** The language of a path, by extension; null when it should stay plain
 * (Markdown, text, unknown types — colouring prose is noise). */
export function langOf(path: string): Lang | null {
  const base = path.slice(path.lastIndexOf("/") + 1);
  if (base === "Makefile" || base === "Dockerfile") return HASH;
  const dot = base.lastIndexOf(".");
  if (dot < 0) return null;
  return LANGS[base.slice(dot + 1).toLowerCase()] ?? null;
}

const KEYWORDS = new Set(
  (
    "package import export from func function fn def class struct interface type enum const let var " +
    "return if else elif for while do switch case default break continue go defer select chan map range " +
    "new delete try catch finally throw throws async await yield in of is as public private protected " +
    "static readonly abstract extends implements namespace use mod pub impl trait match where mut self " +
    "this super null nil None true false True False undefined void and or not lambda with pass raise " +
    "echo fi then esac done local"
  ).split(" "),
);

const NUM_RE = /^(0x[0-9a-fA-F_]+|\d[\d_]*(\.\d+)?([eE][+-]?\d+)?)/;
const WORD_RE = /^[A-Za-z_$][\w$]*/;

/** Tokenize one line. Unknown languages come back as one plain token. */
export function tokenize(text: string, lang: Lang | null): Token[] {
  if (!lang || text === "") return [{ kind: "plain", text }];
  const out: Token[] = [];
  let plain = "";
  const push = (kind: TokKind, t: string) => {
    if (plain) out.push({ kind: "plain", text: plain });
    plain = "";
    out.push({ kind, text: t });
  };
  let i = 0;
  while (i < text.length) {
    const rest = text.slice(i);
    const com = lang.line.find((c) => rest.startsWith(c));
    if (com) {
      push("com", rest);
      break;
    }
    if (rest.startsWith("/*")) {
      const end = rest.indexOf("*/", 2);
      const t = end < 0 ? rest : rest.slice(0, end + 2);
      push("com", t);
      i += t.length;
      continue;
    }
    const ch = text[i];
    if (lang.quotes.includes(ch)) {
      let j = i + 1;
      while (j < text.length && text[j] !== ch) j += text[j] === "\\" ? 2 : 1;
      const t = text.slice(i, Math.min(j + 1, text.length));
      push("str", t);
      i += t.length;
      continue;
    }
    const prev = i > 0 ? text[i - 1] : "";
    if (/\d/.test(ch) && !/[\w$]/.test(prev)) {
      const m = NUM_RE.exec(rest);
      if (m) {
        // Hashes, identifiers and unit-bearing values are whole tokens, not a
        // number followed by an unrelated suffix (e.g. 120eced or 4.1s).
        if (/[A-Za-z_]/.test(rest[m[0].length] ?? "")) {
          const word = /^[\w.]+/.exec(rest)?.[0] ?? m[0];
          plain += word;
          i += word.length;
          continue;
        }
        push("num", m[0]);
        i += m[0].length;
        continue;
      }
    }
    const w = WORD_RE.exec(rest);
    if (w) {
      if (KEYWORDS.has(w[0])) push("kw", w[0]);
      else plain += w[0];
      i += w[0].length;
      continue;
    }
    plain += ch;
    i++;
  }
  if (plain) out.push({ kind: "plain", text: plain });
  return out;
}
