// What a person attaches to a message: a long paste or an image. The field holds a short numbered
// reference, `(1)`, and a tile above the field shows what it stands for, so a 30 KB log still reads
// as a sentence and can be edited around. The real text or path takes the reference's place when
// the message goes. Pasting the same text again straight away shows it as plain text instead. No
// DOM, so it can be tested.
//
// A reference is wrapped in word joiners, which draw nothing, so a person typing "(1)" in a sentence
// is never read as one. An agent's composer takes text from lola, so an image goes as the path of a
// copy in the session's folder.
//
// Only for what is said to an agent. A command stays visible, because a person must be able to read
// what is about to run on a host.

export type Attachment = {
  kind: "text" | "image";
  /** The text, or the path of the image's copy. */
  value: string;
  /** A small data URL. An image with none draws a plain tile. */
  thumb?: string;
  /** The token exists immediately, before the native image copy finishes. */
  loading?: boolean;
  error?: string;
};
export type Pastes = Map<number, Attachment>;

export const PASTE_MIN_BYTES = 800;
export const PASTE_MIN_LINES = 5;

const JOIN = "⁠";
// Accept older drafts too. Padding belongs inside the atomic reference, not in the user's prose.
const TOKEN_RE = /⁠\u00a0?\((\d+)\)\u00a0?⁠/g;
const IMAGE_PATH_RE = /^(?:~?\/)[^\n]+\.(?:png|jpe?g|gif|webp)$/i;

export function foldsPaste(text: string): boolean {
  return text.length >= PASTE_MIN_BYTES || text.split("\n").length >= PASTE_MIN_LINES;
}

/** A paste that is nothing but the path of an image, which is what a screenshot tool copies. */
export function isImagePath(text: string): boolean {
  return IMAGE_PATH_RE.test(text.trim());
}

export function attachmentToken(n: number): string {
  return `${JOIN}\u00a0(${n})\u00a0${JOIN}`;
}

/**
 * The colours that tell attachments apart, so a tile and its number in the text read as one thing.
 * Literal classes, for Tailwind. Orange and red are left out: those mean you are needed, or failure.
 */
const TONES = [
  { badge: "bg-accent", tint: "bg-accent/20", text: "text-accent-ink" },
  { badge: "bg-magenta", tint: "bg-magenta/20", text: "text-magenta" },
  { badge: "bg-good", tint: "bg-good/20", text: "text-good" },
  { badge: "bg-info", tint: "bg-info/20", text: "text-info" },
] as const;

export function attachmentTone(n: number): (typeof TONES)[number] {
  return TONES[(n - 1) % TONES.length];
}

/** The field's text cut at its references, for the layer drawn behind it. */
export function fieldSegments(text: string, pastes: Pastes): ({ text: string; n?: undefined } | { text: string; n: number })[] {
  const out: ({ text: string; n?: undefined } | { text: string; n: number })[] = [];
  let at = 0;
  for (const t of tokensIn(text, pastes)) {
    if (t.from > at) out.push({ text: text.slice(at, t.from) });
    out.push({ text: text.slice(t.from, t.to), n: t.n });
    at = t.to;
  }
  if (at < text.length) out.push({ text: text.slice(at) });
  return out;
}

/** What a text tile says about its size. */
export function textSize(text: string): string {
  const lines = text.split("\n").length;
  if (lines > 1) return `${lines} lines`;
  return text.length >= 1024 ? `${Math.round(text.length / 1024)} KB` : `${text.length} chars`;
}

/** The message as it goes. A reference nothing is kept for keeps its visible words. */
export function expandPastes(text: string, pastes: Pastes): string {
  if (!text.includes(JOIN)) return text;
  let out = '', at = 0, previousAttachment = false;
  const append = (part: string, separate: boolean) => {
    if (separate && out && part && !/\s$/.test(out) && !/^\s/.test(part)) out += ' ';
    out += part;
  };
  for (const match of text.matchAll(TOKEN_RE)) {
    append(text.slice(at, match.index), previousAttachment);
    const item = pastes.get(Number(match[1]));
    append(item?.value ?? `(${match[1]})`, !!item || previousAttachment);
    previousAttachment = !!item;
    at = match.index + match[0].length;
  }
  append(text.slice(at), previousAttachment);
  return out;
}

/** The references in this text that stand for something, in order. */
export function tokensIn(text: string, pastes: Pastes): { n: number; from: number; to: number }[] {
  const out: { n: number; from: number; to: number }[] = [];
  for (const m of text.matchAll(TOKEN_RE)) {
    const n = Number(m[1]);
    if (pastes.has(n)) out.push({ n, from: m.index, to: m.index + m[0].length });
  }
  return out;
}

/** The range one press of Backspace or Delete removes, when the caret sits against a reference. */
export function tokenAtCaret(text: string, caret: number, forward: boolean, pastes: Pastes): { n: number; from: number; to: number } | null {
  return tokensIn(text, pastes).find((t) => (forward ? t.from === caret : t.to === caret)) ?? null;
}

/** A reference is one editing unit, including its invisible delimiters. */
export function tokenStep(text: string, caret: number, forward: boolean, pastes: Pastes): number | null {
  const token = tokensIn(text, pastes).find(t => forward ? t.from <= caret && caret < t.to : t.from < caret && caret <= t.to);
  return token ? forward ? token.to : token.from : null;
}

/** Mouse, vertical navigation and native selection must not leave a half-reference selected. */
export function atomicRange(text: string, start: number, end: number, pastes: Pastes): { start: number; end: number } {
  for (const token of tokensIn(text, pastes)) {
    if (start === end && start > token.from && start < token.to) {
      const edge = start - token.from < token.to - start ? token.from : token.to;
      return { start: edge, end: edge };
    }
    if (start > token.from && start < token.to) start = token.from;
    if (end > token.from && end < token.to) end = token.to;
  }
  return { start, end };
}

/** Give existing drafts the same chip padding without inserting spaces into their surrounding text. */
export function padAttachments(text: string, start: number, end: number, pastes: Pastes): { text: string; start: number; end: number } {
  ({ start, end } = atomicRange(text, start, end, pastes));
  for (const token of tokensIn(text, pastes).reverse()) {
    const padded = attachmentToken(token.n);
    const delta = padded.length - (token.to - token.from);
    if (!delta) continue;
    text = text.slice(0, token.from) + padded + text.slice(token.to);
    if (start >= token.to) start += delta;
    if (end >= token.to) end += delta;
  }
  return { text, start, end };
}

/** One piece of a sent message as the story draws it: words, or the number of an image it shows. */
export type YouPart = { text: string; n?: undefined } | { text?: undefined; n: number };

// Only where desktop/talkimage.go writes a copy, so a path someone typed about is never rewritten.
const SENT_IMAGE_RE = /(?<![^\s(\[])\/\S*?\/\.lola\/images\/[^\s/]+?\.(?:png|jpe?g|gif|webp)(?![^\s),.;:!?\]])/gi;

/**
 * A sent message with its image copies' paths read back as numbers, `(1)`, in reading order. Display
 * only: the record keeps the real text and a find still reads it.
 */
export function youAttachments(text: string): { text: string; images: { n: number; path: string }[]; parts: YouPart[] } {
  const images: { n: number; path: string }[] = [];
  const parts: YouPart[] = [];
  let at = 0;
  for (const m of text.matchAll(SENT_IMAGE_RE)) {
    if (m.index > at) parts.push({ text: text.slice(at, m.index) });
    let image = images.find((i) => i.path === m[0]);
    if (!image) {
      image = { n: images.length + 1, path: m[0] };
      images.push(image);
    }
    parts.push({ n: image.n });
    at = m.index + m[0].length;
  }
  if (!images.length) return { text, images, parts: [{ text }] };
  if (at < text.length) parts.push({ text: text.slice(at) });
  return { text: parts.map((p) => (p.n ? `(${p.n})` : p.text)).join(""), images, parts };
}
