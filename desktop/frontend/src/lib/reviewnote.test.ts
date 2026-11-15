import { describe, expect, it } from 'vitest';
import type { ReviewPackage } from '@bindings/internal/protocol';
import { parseReviewNote, removeReviewComment, reviewCommentContext, withFreeText } from './reviewnote';

const pkg = {
  version: 1, id: 'pkg-1', session: 's5', repo: 'acme/app', pr: 4, title: 't', body: '', base: 'aaa', head: 'bbb', createdAt: '',
  artifacts: [], screens: [{ title: 'Light', after: 'after.png', before: 'before.png', viewport: '390x844' }],
} as ReviewPackage;

describe('reviewCommentContext', () => {
  it('writes the header the workspace has always written', () => {
    expect(reviewCommentContext(pkg, 'Write-up')).toBe('Review package pkg-1\nRepository: acme/app PR #4\nBase: aaa\nHead: bbb\nSection: Write-up\n');
    expect(reviewCommentContext(pkg, 'Screen 1: Light', 0)).toBe('Review package pkg-1\nRepository: acme/app PR #4\nBase: aaa\nHead: bbb\nSection: Screen 1: Light\nPage: unspecified; viewport: 390x844\nScreenshots: before before.png, after after.png\n');
    expect(reviewCommentContext(undefined, 'x')).toBe('');
  });
});

describe('parseReviewNote', () => {
  const a = reviewCommentContext(pkg, 'Screen 1: Light', 0) + 'Header is clipped';
  const b = reviewCommentContext(pkg, 'Section 2: Risks') + 'Explain the risk.\n\nWith two paragraphs.';
  const note = ['Please also rename the file', a, b].join('\n\n');

  it('reads the person’s text and each comment in order', () => {
    const p = parseReviewNote(note);
    expect(p.free).toBe('Please also rename the file');
    expect(p.comments.map(c => [c.packageId, c.section, c.text])).toEqual([
      ['pkg-1', 'Screen 1: Light', 'Header is clipped'],
      ['pkg-1', 'Section 2: Risks', 'Explain the risk.\n\nWith two paragraphs.'],
    ]);
    expect(p.comments[0].raw).toBe(a);
  });

  it('leaves a note without comments alone', () => {
    expect(parseReviewNote('just a note')).toEqual({ free: 'just a note', comments: [] });
  });

  it('removes one comment and keeps the rest', () => {
    expect(removeReviewComment(note, a)).toBe(['Please also rename the file', b].join('\n\n'));
  });

  it('removes only the comment and one separator, leaving every other byte', () => {
    const free = '  Para one\n\n\nPara two after two blank lines\n';
    const c = reviewCommentContext(pkg, 'Screen 3: Dark') + 'Body\n\n\nwith a double blank line.';
    const n = [free, a, c].join('\n\n');
    expect(removeReviewComment(n, a)).toBe([free, c].join('\n\n'));
    expect(removeReviewComment(n, c)).toBe([free, a].join('\n\n'));
    expect(removeReviewComment([a, c].join('\n\n'), a)).toBe(c);
  });

  it('replaces only the person’s text', () => {
    expect(withFreeText(note, 'New text')).toBe(['New text', a, b].join('\n\n'));
    expect(withFreeText(note, '')).toBe([a, b].join('\n\n'));
    expect(parseReviewNote(withFreeText(note, 'line\n')).free).toBe('line\n');
  });
});
