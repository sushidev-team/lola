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
    expect(lines[0]).toEqual({ number: '433', separator: ':', text: 'pub fn rule() {', path: 'at_page.rs', kind: '' });
    expect(lines[2].path).toBe('');
    expect(lines.map(l => l.number + l.separator + l.text).join('\n')).toBe(text);
  });
  it('highlights unnumbered file reads but leaves unknown logs alone', () => {
    expect(outputLines('const x = 1;', 'file.ts', true)[0].path).toBe('file.ts');
    expect(outputLines('200: request complete', '')[0].number).toBe('');
  });
});

describe('numbered and diff source output', () => {
  it('highlights indented JSX excerpts even when a long command hid the filename', () => {
    const text = '  665-          <Button\n  666-            size="sm"\n  667:            onClick={() => restoreVersion(1)}';
    const path = outputSourcePath('cd /a/very/long/checkout; SP=/…', text);
    expect(path).toBe('excerpt.tsx');
    const lines = outputLines(text, path);
    expect(lines.every(line => line.path === path)).toBe(true);
    expect(lines.map(l => l.number + l.separator + l.text).join('\n')).toBe(text);
  });
  it('uses each file in a multi-file diff and leaves the headers intact', () => {
    const text = '--- a/a.ts\n+++ b/a.ts\n@@ -1 +1 @@\n-const x = 1;\n+const x = 2;\n--- a/b.py\n+++ b/b.py\n@@ -1 +1 @@\n+print("hi")';
    const lines = outputLines(text, '');
    expect(lines[3]).toMatchObject({ kind: 'del', path: 'a.ts', text: 'const x = 1;' });
    expect(lines[8]).toMatchObject({ kind: 'add', path: 'b.py' });
    expect(lines.map(l => l.number + l.separator + l.text).join('\n')).toBe(text);
  });
  it('recognizes filename-prefixed search results without coloring ordinary logs', () => {
    expect(outputLines('src/a.ts:12:const x = true;', '')[0]).toMatchObject({ path: 'src/a.ts', text: 'const x = true;' });
    expect(outputSourcePath('npm test', '200: request complete')).toBe('');
  });
});

describe('mixed command output', () => {
  it('separates compiler locations from messages without treating diagnostics as code', () => {
    const text = "vendor/sushidev/okane/resources/js/headless/admin.ts(250,72): error TS2339: Property 'localeCompare' does not exist on type 'never'.";
    const line = outputLines(text, 'admin.ts')[0];
    expect(line).toMatchObject({ kind: 'error', path: '', diagnosticPrefix: 'vendor/sushidev/okane/resources/js/headless/admin.ts(250,72): error TS2339: ', message: "Property 'localeCompare' does not exist on type 'never'.", text });
  });
  it('distinguishes a failing compiler exit from passing tests in the same command', () => {
    const lines = outputLines('tsc exit 1\ni pass 11\ni fail 0\ni fail 2\nUNKNOWN', '');
    expect(lines.map(line => line.kind)).toEqual(['error', 'success', 'success', 'error', '']);
    expect(lines.map(line => line.text).join('\n')).toBe('tsc exit 1\ni pass 11\ni fail 0\ni fail 2\nUNKNOWN');
  });
});
