import { langOf } from './diff';

/** Infer source format only from explicit filenames, never from words in a log. */
export function outputSourcePath(command: string): string {
  const paths = command.match(/[\w./-]+\.[a-zA-Z0-9]+\b/g) ?? [];
  const known = paths.filter(path => langOf(path));
  const extensions = new Set(known.map(path => path.split('.').pop()));
  return extensions.size === 1 ? known[0] : '';
}

export function outputLines(text: string, path: string, sourceFile = false) {
  return text.split('\n').map(line => {
    const match = path && /^(\d+)([:\t-])(.*)$/.exec(line);
    return match
      ? { number: match[1], separator: match[2], text: match[3], path }
      : { number: '', separator: '', text: line, path: sourceFile ? path : '' };
  });
}
