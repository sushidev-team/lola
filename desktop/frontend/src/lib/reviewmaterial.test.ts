import { expect, it } from 'vitest';
import { arrowHead, markKind, markLocation, maxPenPoints, simplifyStroke, validMark } from './reviewmaterial';

const base = { x: .1, y: .2, w: .3, h: .1, side: 'After' as const, text: '' };

it('reads older drafts without a kind as a point or an area', () => {
  expect(markKind({ ...base, w: 0, h: 0 })).toBe('pin');
  expect(markKind(base)).toBe('area');
  expect(markKind({ ...base, kind: 'arrow', x2: .5, y2: .5 })).toBe('arrow');
});

it('describes each shape by where it sits on the screenshot', () => {
  expect(markLocation({ ...base, kind: 'ellipse' })).toBe('After screenshot, ellipse from top-left: x=10%, y=20%, width=30%, height=10%');
  expect(markLocation({ ...base, kind: 'line', x2: .5, y2: .25 })).toBe('After screenshot, line from top-left: x=10%, y=20% to x=50%, y=25%');
  expect(markLocation({ ...base, kind: 'pen', pts: [.1, .2, .4, .3] })).toBe('After screenshot, freehand stroke, bounding box from top-left: x=10%, y=20%, width=30%, height=10%');
});

it('accepts a stored draft only when every coordinate is a fraction of the image', () => {
  expect(validMark(base)).toBe(true);
  expect(validMark({ ...base, kind: 'arrow', x2: .4, y2: .4 })).toBe(true);
  expect(validMark({ ...base, kind: 'arrow' })).toBe(false);
  expect(validMark({ ...base, kind: 'arrow', x2: 4, y2: .4 })).toBe(false);
  expect(validMark({ ...base, kind: 'pen', pts: [.1, .1] })).toBe(false);
  expect(validMark({ ...base, kind: 'pen', pts: [.1, .1, .2, .2, .3] })).toBe(false);
  expect(validMark({ ...base, kind: 'pen', pts: [.1, .1, .2, 2] })).toBe(false);
  expect(validMark({ ...base, kind: 'spiral' })).toBe(false);
  expect(validMark({ ...base, side: 'Middle' })).toBe(false);
});

it('thins a long stroke but keeps both ends', () => {
  const long = Array.from({ length: 1000 }, (_, i) => [i / 1000, 0]).flat();
  const thin = simplifyStroke(long);
  expect(thin.length).toBe(maxPenPoints * 2);
  expect(thin.slice(0, 2)).toEqual([0, 0]);
  expect(thin.slice(-2)).toEqual([.999, 0]);
  expect(simplifyStroke([0, 0, 1, 1])).toEqual([0, 0, 1, 1]);
});

it('puts the arrow tip on the end point', () => {
  const [tip] = arrowHead(0, 0, 100, 0, 10).split(' ');
  expect(tip).toBe('100,0');
});
