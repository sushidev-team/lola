import { describe, it, expect } from 'vitest';
import { outputLines, outputSourcePath } from './toolOutput';
describe('source excerpts in tool output', () => {
  it('uses an explicit source filename from a command', () => {
    expect(outputSourcePath('wc -l crates/paged/src/at_page.rs && grep -n rule crates/paged/src/at_page.rs')).toBe('crates/paged/src/at_page.rs');
    expect(outputSourcePath('npm test')).toBe('');
    expect(outputSourcePath('cat a.rs b.ts')).toBe('');
  });
  it('separates grep gutters and preserves source and shell notices verbatim', () => {
    const text = '433:pub fn rule() {\n704-    let slot = 1;\nShell cwd was reset to /tmp';
    const lines = outputLines(text, 'at_page.rs');
    expect(lines[0]).toEqual({ number: '433', separator: ':', text: 'pub fn rule() {', path: 'at_page.rs' });
    expect(lines[2].path).toBe('');
    expect(lines.map(l => l.number + l.separator + l.text).join('\n')).toBe(text);
  });
  it('highlights unnumbered file reads but leaves unknown logs alone', () => {
    expect(outputLines('const x = 1;', 'file.ts', true)[0].path).toBe('file.ts');
    expect(outputLines('200: request complete', '')[0].number).toBe('');
  });
});
