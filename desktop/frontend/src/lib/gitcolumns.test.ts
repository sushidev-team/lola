import { beforeEach, describe, expect, it } from 'vitest';
import { defaultGitColumns, readGitColumns, saveGitColumns } from './gitcolumns';
beforeEach(() => localStorage.removeItem('lola.git.columns'));
describe('Git column preferences', () => {
  it('restores hidden columns and widths while keeping the commit subject available', () => {
    saveGitColumns({ hidden: ['author', 'subject'], widths: { refs: 260, age: 90 } });
    expect(readGitColumns()).toEqual({ hidden: ['author'], widths: { refs: 260, age: 90 } });
  });
  it('bounds persisted dimensions and discards corrupt settings', () => {
    localStorage.setItem('lola.git.columns', JSON.stringify({ hidden: ['unknown'], widths: { refs: -10, subject: 9000, author: 'wide' } }));
    expect(readGitColumns()).toEqual({ hidden: [], widths: { refs: 90, subject: 1000 } });
    localStorage.setItem('lola.git.columns', 'invalid');
    expect(readGitColumns()).toEqual(defaultGitColumns());
  });
});
