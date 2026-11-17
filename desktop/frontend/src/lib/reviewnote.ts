import type { ReviewPackage } from '@bindings/internal/protocol';

// Review-package comments travel inside the session's feedback note (`feedback.setNote`):
// each one is a context header written by `reviewCommentContext` followed by the comment
// text, and comments are separated by a blank line. The desktop workspace writes them;
// the phone's feedback sheet reads them back as rows. One module owns the format, so
// the writer and the reader cannot drift apart.

/** The header in front of a review comment: which package, revision, section and screen it is about. */
export function reviewCommentContext(pkg: ReviewPackage | undefined, section: string, screenIndex?: number): string {
  if (!pkg) return '';
  const screen = screenIndex === undefined ? undefined : pkg.screens?.[screenIndex];
  const images = screen ? `Page: ${screen.url || 'unspecified'}; viewport: ${screen.viewport || 'unspecified'}${screen.selector ? `; element: ${screen.selector}` : ''}\nScreenshots: before ${screen.before || 'unavailable'}, after ${screen.after}\n` : '';
  return `Review package ${pkg.id}\nRepository: ${pkg.repo || 'unspecified'}${pkg.pr ? ` PR #${pkg.pr}` : ''}\nBase: ${pkg.base}\nHead: ${pkg.head}\nSection: ${section}\n${images}`;
}

export interface ReviewNoteComment {
  /** The exact text of this comment in the note, header included. */
  raw: string;
  packageId: string;
  section: string;
  text: string;
}

const HEADER = /^Review package (\S+)\nRepository: [^\n]*\nBase: [^\n]*\nHead: [^\n]*\nSection: ([^\n]*)\n(?:Page: [^\n]*\nScreenshots: [^\n]*\n)?/gm;

/** Splits a note into the person's own text (before the first comment) and the review comments, in order. */
export function parseReviewNote(note: string): { free: string; comments: ReviewNoteComment[] } {
  const found = [...note.matchAll(HEADER)];
  if (!found.length) return { free: note, comments: [] };
  const comments = found.map((m, i) => {
    const start = m.index!, end = i + 1 < found.length ? found[i + 1].index! : note.length;
    const raw = note.slice(start, end).replace(/\s+$/, '');
    return { raw, packageId: m[1], section: m[2], text: raw.slice(m[0].length).trim() };
  });
  return { free: note.slice(0, found[0].index).replace(/\n\n$/, ''), comments };
}

/** The note without one comment; other comments and the person's text are kept as written. */
export function removeReviewComment(note: string, raw: string): string {
  const at = note.indexOf(raw);
  if (at < 0) return note;
  const end = at + raw.length;
  // Take one adjacent separator with the comment (after it, else before it) and touch nothing else.
  if (note.startsWith('\n\n', end)) return note.slice(0, at) + note.slice(end + 2);
  if (at >= 2 && note.startsWith('\n\n', at - 2)) return note.slice(0, at - 2) + note.slice(end);
  return note.slice(0, at) + note.slice(end);
}

/** The note with the person's own text replaced and every review comment kept. */
export function withFreeText(note: string, free: string): string {
  const { comments } = parseReviewNote(note);
  return [free, ...comments.map(c => c.raw)].filter(s => s.trim() !== '').join('\n\n');
}
