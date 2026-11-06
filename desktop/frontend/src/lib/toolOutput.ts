import { langOf } from './diff';

/** Explicit source filenames first; a numbered JSX excerpt is also unambiguous code. */
export function outputSourcePath(command: string, output = ''): string {
  const paths = command.match(/[\w./-]+\.[a-zA-Z0-9]+\b/g) ?? [];
  const known = paths.filter(path => langOf(path));
  const extensions = new Set(known.map(path => path.split('.').pop()));
  if (extensions.size === 1) return known[0];
  if (/^\s*\d+[:\t-]\s*<\/?[A-Za-z][\w.]*(?:\s|>|\/)/m.test(output)) return 'excerpt.tsx';
  return '';
}

/** Preserve gutters and diff markers byte-for-byte, but tokenize only the source. */
export function outputLines(text: string, path: string, sourceFile = false) {
  let diffPath = '';
  let inDiff = false;
  return text.split('\n').map(line => {
    // Compiler locations are labels, not source code. Keep their exact prefix for copy.
    const diagnostic = /^(\s*.+?(?:\(\d+(?:,\d+)?\)|:\d+(?::\d+)?):\s*(error|warning)\b[^:]*:\s*)(.*)$/i.exec(line);
    if (diagnostic) return { number: '', separator: '', text: line, path: '', kind: diagnostic[2].toLowerCase(), diagnosticPrefix: diagnostic[1], message: diagnostic[3] };
    const exit = /^\s*(?:[\w.-]+\s+)?exit(?: code)?[ :]+(-?\d+)\s*$/i.exec(line);
    if (exit) return { number: '', separator: '', text: line, path: '', kind: Number(exit[1]) ? 'error' : 'success' };
    const result = /^\s*(?:[ℹi✔✓✖×]\s*)?(pass|fail)\s+(\d+)\s*$/i.exec(line);
    if (result) return { number: '', separator: '', text: line, path: '', kind: result[1].toLowerCase() === 'fail' && Number(result[2]) ? 'error' : 'success' };

    const header = /^(?:\+\+\+|---) (?:[ab]\/)?([^\t]+?)(?:\t.*)?$/.exec(line);
    if (header) {
      if (langOf(header[1])) diffPath = header[1];
      inDiff = false;
      return { number: '', separator: '', text: line, path: '', kind: '' };
    }
    if (/^@@ /.test(line)) {
      inDiff = true;
      return { number: '', separator: '', text: line, path: '', kind: 'hunk' };
    }
    const named = /^(.+?\.[a-zA-Z0-9]+)[:\-](\d+)([:\-])(.*)$/.exec(line);
    if (named && langOf(named[1])) return { number: `${named[1]}${line[named[1].length]}${named[2]}`, separator: named[3], text: named[4], path: named[1], kind: '' };
    const match = path && /^(\s*\d+)([:\t-])(.*)$/.exec(line);
    if (match) return { number: match[1], separator: match[2], text: match[3], path, kind: '' };
    if (inDiff && /^[ +\-]/.test(line)) return { number: '', separator: line[0], text: line.slice(1), path: diffPath || path, kind: line[0] === '+' ? 'add' : line[0] === '-' ? 'del' : '' };
    return { number: '', separator: '', text: line, path: sourceFile ? path : '', kind: '' };
  });
}
