// An agent's answer as styled elements rather than as text lines. Pure and display-only: nothing here
// reaches a state, a gate or an agent, and it builds no HTML, so a line of it can only render as text.
//
// It fails open. What it does not understand stays a paragraph, and the only characters it ever drops
// are markers it consumed. The rules follow what real answers contain, counted by shape over real
// transcripts: lists, bold-only lines used as headings and paragraphs opening on a bold label do nearly
// all the work, and tables and `##` headings are rare.
import { inlineSpans, parseRich, type CalloutTone, type InlineSpan, type LedgerRow, type RichBlock } from "./ssh";

/**
 * Whether a beat is an answer or a line of narration, from its text alone, so a beat keeps its shape
 * when a later word arrives and nothing reflows when a turn ends.
 */
export function answerShaped(text: string): boolean {
  return text.includes("\n") || text.length > 280 || /^(#{1,6} |[-*] |\d+\. |\||```)/m.test(text);
}

const LEAD_SENTENCE = 140;
const LEAD_PARAGRAPH = 160;
const QUESTION_MAX = 280;
const LABEL_STACKS_OVER = 28;
const PLAIN_LABEL_MAX = 24;
/** A fence longer than this folds, and names its first line. */
export const FENCE_LINES = 8;

const ABBREVIATIONS = /(?:^|[\s(])(?:e\.g|i\.e|z\.B|d\.h|u\.a|bzw|vs|etc|ca|approx|cf)$/i;

const count = (text: string, marker: string) => text.split(marker).length - 1;

/** A cut that leaves a code span or a bold open is not a sentence's end. */
function balanced(text: string): boolean {
  return count(text, "`") % 2 === 0 && count(text, "**") % 2 === 0;
}

/** Where a paragraph's first sentence ends, or -1. A line break ends it too. */
export function firstSentenceEnd(text: string): number {
  let code = false;
  let bold = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (c === "`") code = !code;
    else if (c === "*" && text[i + 1] === "*") {
      bold = !bold;
      i++;
    } else if (c === "\n" && !code && !bold) {
      return i;
    } else if (!code && !bold && (c === "." || c === "!" || c === "?")) {
      const after = /^(\*\*|["“”»)]*)\s+(\S)/.exec(text.slice(i + 1));
      if (!after) continue;
      const before = text.slice(0, i);
      if (c === ".") {
        if (ABBREVIATIONS.test(before)) continue;
        // "z. B." and "d. h.": a lone letter and a full stop are not a sentence.
        if (/(?:^|[\s(])\p{L}$/u.test(before)) continue;
        // Nor is a word that goes on in lower case.
        if (/\p{Ll}/u.test(after[2])) continue;
      }
      return i + 1 + after[1].length;
    }
  }
  return -1;
}

/** Splits an answer's first paragraph into its lead and the rest. No lead when no short one exists. */
export function splitLead(text: string): { lead: string; rest: string } | null {
  const end = firstSentenceEnd(text);
  if (end > 0 && end <= LEAD_SENTENCE) {
    const lead = text.slice(0, end).trim();
    if (lead && balanced(lead)) return { lead, rest: text.slice(end).trim() };
  }
  if (end < 0 && text.length <= LEAD_SENTENCE) return { lead: text.trim(), rest: "" };
  if (text.length <= LEAD_PARAGRAPH && !text.includes("\n")) return { lead: text.trim(), rest: "" };
  return null;
}

// --- labels ------------------------------------------------------------------

const unwrap = (label: string) => {
  const t = label
    .trim()
    .replace(/\s*[:：]$/, "")
    .trim();
  const code = /^`([^`]+)`$/.exec(t);
  return code ? code[1] : t;
};

const BOLD_BODY = "((?:`[^`\\n]+`|[^*`\\n]|\\*(?!\\*))+?)";
const BOLD_COLON_IN = new RegExp(`^\\*\\*${BOLD_BODY}[:：]\\*\\*\\s+([\\s\\S]+)$`);
const BOLD_COLON_OUT = new RegExp(`^\\*\\*${BOLD_BODY}\\*\\*[:：]\\s+([\\s\\S]+)$`);
const BOLD_DASH = new RegExp(`^\\*\\*${BOLD_BODY}\\*\\*\\s+[–—-]\\s+([\\s\\S]+)$`);
const CODE_COLON = /^`([^`\n]+)`[:：]\s+([\s\S]+)$/;
const PLAIN_COLON = /^(\p{Lu}[^\n:.!?`*]*):\s+([\s\S]+)$/u;

export type Label = { label: string; text: string; bold: boolean };

/** `**X:** t` and its cousins. Needs a text behind it: a label alone is a heading, not a row. */
export function parseLabel(item: string): Label | null {
  for (const re of [BOLD_COLON_IN, BOLD_COLON_OUT, BOLD_DASH]) {
    const m = re.exec(item);
    if (m && m[2].trim()) return { label: unwrap(m[1]), text: m[2], bold: true };
  }
  const code = CODE_COLON.exec(item);
  if (code && code[2].trim()) return { label: code[1], text: code[2], bold: false };
  const plain = PLAIN_COLON.exec(item);
  if (plain && plain[1].length <= PLAIN_LABEL_MAX && plain[2].trim()) return { label: plain[1].trim(), text: plain[2], bold: false };
  return null;
}

// A claim about words: display-only, and anything else stays ink. Never green.
const WARN_WORDS = ["still open", "noch offen", "not verified", "nicht verifiziert", "open", "offen", "offene", "risk", "risks", "risiko", "risiken", "todo", "caveat", "warning", "warnung", "achtung", "unverified", "ungeklärt"];
const BAD_WORDS = ["failed", "failing", "blocked", "blocker", "broken", "fehler", "fehlgeschlagen"];

const startsWithWord = (text: string, words: readonly string[]) => words.some((w) => text === w || (text.startsWith(w) && !/[\p{L}\p{N}]/u.test(text[w.length])));

/** The hue a label earns from what it says. Whole label or its start, whole words, any case. */
export function labelTone(label: string): "" | "warn" | "bad" {
  const t = label.replace(/[`*]/g, "").trim().toLowerCase();
  if (startsWithWord(t, BAD_WORDS)) return "bad";
  return startsWithWord(t, WARN_WORDS) ? "warn" : "";
}

const CELL_WARN = new Set(["open", "offen", "pending", "warning", "warnung", "todo", "unverified", "not verified"]);
const CELL_BAD = new Set(["failed", "failing", "blocked", "broken", "fehler", "fehlgeschlagen"]);

/** A table cell that is exactly a status word. Never good: a table from an agent is a claim. */
export function cellTone(cell: string): "" | "warn" | "bad" {
  const t = cell.replace(/[`*]/g, "").trim().toLowerCase();
  return CELL_BAD.has(t) ? "bad" : CELL_WARN.has(t) ? "warn" : "";
}

// --- status marks ------------------------------------------------------------

const MARKS: Record<string, "ok" | "warn" | "bad"> = { "✓": "ok", "✅": "ok", "✔": "ok", "⚠": "warn", "⚠️": "warn", "✗": "bad", "❌": "bad", "✘": "bad" };

/** A mark opening a list item. The mark is consumed and the words stay. */
export function answerStatus(item: string): { tone?: "ok" | "warn" | "bad"; text: string } {
  const tag = /^\[(ok|warn|bad)\]\s*([\s\S]*)$/i.exec(item);
  if (tag) return { tone: tag[1].toLowerCase() as "ok" | "warn" | "bad", text: tag[2] };
  const mark = /^(⚠️|✓|✅|✔|⚠|✗|❌|✘)\s+([\s\S]*)$/u.exec(item);
  return mark ? { tone: MARKS[mark[1]], text: mark[2] } : { text: item };
}

// --- blocks ------------------------------------------------------------------

const BOLD_ONLY = new RegExp(`^\\*\\*${BOLD_BODY}\\*\\*[:：]?\\s*$`);
const CALLOUT_WORD = /^(?:\*\*)?(Note|Warning|Caution|Important|Hinweis|Warnung|Achtung|Wichtig)(?:[:：]\*\*|\*\*[:：]|[:：])\s+([\s\S]+)$/;
const CALLOUT_TONE: Record<string, CalloutTone> = { Note: "note", Hinweis: "note", Important: "note", Wichtig: "note", Warning: "warn", Caution: "warn", Warnung: "warn", Achtung: "warn" };

/** A paragraph's own opening: a bold-only line is a heading, whatever follows it. */
function openParagraph(text: string): RichBlock[] {
  const out: RichBlock[] = [];
  let lines = text.split("\n");
  while (lines.length) {
    const m = BOLD_ONLY.exec(lines[0]);
    if (!m) break;
    out.push({ kind: "heading", text: unwrap(m[1]) });
    lines = lines.slice(1);
  }
  if (!lines.length) return out;
  // An indented line after a list is a paragraph here, and its indent would otherwise print.
  const rest = lines.map((l) => l.trimStart()).join("\n");
  const call = CALLOUT_WORD.exec(rest);
  out.push(call ? { kind: "callout", tone: CALLOUT_TONE[call[1]], word: call[1], text: call[2] } : { kind: "p", text: rest });
  return out;
}

function labelledRow(l: Label): LedgerRow {
  return { label: l.label, text: l.text, tone: labelTone(l.label), stacked: l.label.length > LABEL_STACKS_OVER };
}

function ledgerRow(item: string, subs?: string[]): LedgerRow {
  const st = answerStatus(item);
  const l = parseLabel(st.text);
  const row: LedgerRow = l ? labelledRow(l) : { label: "", text: st.text, tone: "", stacked: false };
  if (st.tone) row.status = st.tone;
  if (subs?.length) row.subs = subs;
  return row;
}

/** A list becomes a ledger when at least two of three items carry a label, and there are at least two. */
function asLedger(b: Extract<RichBlock, { kind: "ul" | "ol" }>): RichBlock {
  if (b.kind !== "ul" || b.items.length < 2) return b;
  const labelled = b.items.filter((i) => parseLabel(answerStatus(i).text)).length;
  if (labelled * 3 < b.items.length * 2) return b;
  return { kind: "ledger", rows: b.items.map((item, i) => ledgerRow(item, b.subs?.[i])) };
}

/** Two or more paragraphs in a row opening on a bold label read as one ledger. */
function mergeLabelled(blocks: RichBlock[]): RichBlock[] {
  const out: RichBlock[] = [];
  for (let i = 0; i < blocks.length; i++) {
    const rows: LedgerRow[] = [];
    for (let j = i; j < blocks.length; j++) {
      const n = blocks[j];
      const l = n.kind === "p" ? parseLabel(n.text) : null;
      if (!l?.bold) break;
      rows.push(labelledRow(l));
    }
    if (rows.length >= 2) {
      out.push({ kind: "ledger", rows });
      i += rows.length - 1;
    } else {
      out.push(blocks[i]);
    }
  }
  return out;
}

function shape(blocks: RichBlock[]): RichBlock[] {
  const opened = blocks.flatMap((b): RichBlock[] => {
    if (b.kind === "p") return openParagraph(b.text);
    if (b.kind === "ul" || b.kind === "ol") return [asLedger(b)];
    if (b.kind === "fold") return [{ ...b, blocks: shape(b.blocks) }];
    return [b];
  });
  return mergeLabelled(opened);
}

function build(text: string): RichBlock[] {
  if (!answerShaped(text)) return [{ kind: "p", text }];
  const blocks = shape(parseRich(text.split("\n")));
  if (!blocks.length) return [{ kind: "p", text }];
  const first = blocks[0];
  if (first.kind === "p") {
    const split = splitLead(first.text);
    if (split) blocks.splice(0, 1, { kind: "lead", text: split.lead }, ...(split.rest ? [{ kind: "p" as const, text: split.rest }] : []));
  }
  const last = blocks[blocks.length - 1];
  if (blocks.length > 1 && last.kind === "p" && last.text.length <= QUESTION_MAX && /\?(?:\*\*)?\s*$/.test(last.text)) {
    blocks[blocks.length - 1] = { kind: "question", text: last.text };
  }
  return blocks;
}

/** An answer as elements. Whatever goes wrong in here, the text still reads as text. */
export function answerBlocks(text: string): RichBlock[] {
  try {
    return build(text);
  } catch {
    return [{ kind: "p", text }];
  }
}

const flat = (spans: InlineSpan[]): string => spans.map((s) => ("spans" in s && s.spans ? flat(s.spans) : s.text)).join("");

/** A line of an answer with its markers gone, for a place that draws one plain line: a folded turn. */
export function plainInline(line: string): string {
  return flat(inlineSpans(line.replace(/^\s*(?:#{1,6}\s+|[-*•]\s+)/, "")));
}

/** A long fence folds and names its first line. A short one stays open. */
export function fenceFold(text: string): { folds: boolean; label: string } {
  const lines = text.split("\n");
  return { folds: lines.length > FENCE_LINES, label: lines.find((l) => l.trim())?.trim() ?? "Code" };
}

/** Where `#n` goes, when the session's own PR says which repository it is in. Otherwise it is text. */
export function prHref(prUrl: string | undefined, ref: string): string {
  const m = /^(https:\/\/[^/\s]+\/[^/\s]+\/[^/\s]+)\/pull\/\d+/.exec(prUrl ?? "");
  const n = /^#(\d{1,6})$/.exec(ref);
  return m && n ? `${m[1]}/pull/${n[1]}` : "";
}
