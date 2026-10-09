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
};

const HUNK_RE = /^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/;

/** Number every row of one file's patch. Tolerant: an unplaceable row is kept. */
export function parsePatch(patch: string | undefined): Row[] {
  const rows: Row[] = [];
  if (!patch) return rows;
  let oldN = 0;
  let newN = 0;
  const lines = patch.endsWith("\n") ? patch.slice(0, -1).split("\n") : patch.split("\n");
  for (const raw of lines) {
    if (raw === "") continue;
    switch (raw[0]) {
      case "@": {
        const m = HUNK_RE.exec(raw);
        if (m) {
          oldN = Number(m[1]);
          newN = Number(m[2]);
        }
        rows.push({ kind: "hunk", oldLine: 0, newLine: 0, text: raw });
        break;
      }
      case "+":
        rows.push({ kind: "add", oldLine: 0, newLine: newN++, text: raw.slice(1) });
        break;
      case "-":
        rows.push({ kind: "del", oldLine: oldN++, newLine: 0, text: raw.slice(1) });
        break;
      case "\\":
        rows.push({ kind: "meta", oldLine: 0, newLine: 0, text: raw });
        break;
      default:
        rows.push({ kind: "ctx", oldLine: oldN++, newLine: newN++, text: raw.slice(1) });
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
  /** Unique identity for rendering: the full path of the file or directory
   * ("dir:" prefixed, so a directory can never collide with a file). Two
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
      out.push({ name: f.slice(f.lastIndexOf("/") + 1), path: f, key: f, depth, children: [] });
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
