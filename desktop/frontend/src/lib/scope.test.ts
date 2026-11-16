import { describe, expect, it } from 'vitest';
import { makeScope } from './scope';

const projects = [{ name: 'nori', stack: '' }, { name: 'nori-web', stack: 'nori' }, { name: 'okane', stack: 'nori' }, { name: 'lola', stack: '' }];

describe('makeScope', () => {
  it('matches everything when nothing is selected', () => expect(makeScope([], projects)).toBeNull());
  it('finds a member by its own sessions and by the stack sessions it is part of', () => {
    const m = makeScope(['nori-web'], projects)!;
    expect(m({ project: 'nori-web' })).toBe(true);
    expect(m({ project: 'nori', apps: [{ name: 'nori-web' }, { name: 'okane' }] })).toBe(true);
    expect(m({ project: 'nori', apps: [{ name: 'okane' }] })).toBe(false);
    expect(m({ project: 'lola' })).toBe(false);
  });
  it('lets a stack reach its members', () => {
    const m = makeScope(['nori'], projects)!;
    expect(m({ project: 'nori' })).toBe(true);
    expect(m({ project: 'okane' })).toBe(true);
    expect(m({ project: 'lola' })).toBe(false);
  });
  it('matches ANY of several projects', () => {
    const m = makeScope(['lola', 'okane'], projects)!;
    expect(m({ project: 'lola' })).toBe(true);
    expect(m({ project: 'okane' })).toBe(true);
    expect(m({ project: 'nori-web' })).toBe(false);
    expect(m({ project: undefined, apps: null })).toBe(false);
  });
});
