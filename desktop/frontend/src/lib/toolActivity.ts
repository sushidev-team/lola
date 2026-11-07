import type { AgentFeedTool } from '@bindings/internal/protocol';
import { langOf } from './diff';

// A display-only lexer: quotes keep shell operators inside arguments. Never evaluates text.
export function commands(text: string): string[][] {
  const out: string[][] = [];
  let words: string[] = [], word = '', quote = '', escaped = false;
  const flushWord = () => { if (word) words.push(word); word = ''; };
  const flush = () => { flushWord(); if (words.length) out.push(words); words = []; };
  for (const ch of text.slice(0, 8192)) {
    if (escaped) { word += ch; escaped = false; continue; }
    if (ch === '\\' && quote !== "'") { escaped = true; continue; }
    if (quote) { if (ch === quote) quote = ''; else word += ch; continue; }
    if (ch === '"' || ch === "'") { quote = ch; continue; }
    if (';&|\n'.includes(ch)) { flush(); continue; }
    if (/\s/.test(ch)) flushWord(); else word += ch;
  }
  flush();
  return out;
}

/** Conservative recognition of source-reading shell commands; never evaluate a command. */
export function sourceReadPaths(command: string): string[] {
  const steps = commands(command).filter(words => words[0] !== 'cd');
  const paths: string[] = [];
  if (!steps.length || /[<>`]|\$\(/.test(command)) return paths;
  for (const [cmd, ...args] of steps) {
    if (!['sed', 'cat', 'head', 'tail', 'nl', 'cut'].includes(cmd)) return [];
    if (cmd === 'sed') {
      // Only print-range expressions. sed can also write files or execute shell commands.
      const expressions = args.filter(arg => /^\d+(?:,\d+|,\$)?p$/.test(arg));
      if (expressions.length !== 1 || args.some(arg => arg.startsWith('-i'))) return [];
      const extras = args.filter(arg => !expressions.includes(arg) && arg !== '-n' && arg !== '-e');
      if (!extras.length || extras.some(arg => arg.startsWith('-') || !langOf(arg))) return [];
    }
    for (const arg of args) if (!arg.startsWith('-') && langOf(arg) && !paths.includes(arg)) paths.push(arg);
  }
  return paths;
}

/** Name only explicit operations. Reading a diff or searching code is not an edit. */
export function activityTool(tool: AgentFeedTool): AgentFeedTool {
  if (tool.category !== 'run') return tool;
  const source = tool.detail || tool.summary || '';
  const reads = sourceReadPaths(source);
  if (reads.length) return { ...tool, category: 'read', name: 'Read', summary: reads.join(', '), detail: source };
  const steps = commands(source).map(words => {
    while (words.length && /^[A-Za-z_]\w*=/.test(words[0])) words.shift();
    return words;
  }).filter(words => words.length && words[0] !== 'cd');
  const candidates: AgentFeedTool[] = [];
  for (const [index, words] of steps.entries()) {
    const [cmd, ...args] = words;
    const values = args.filter(arg => !arg.startsWith('-'));
    let name = '', category = tool.category, summary = '';
    if (['rm', 'unlink', 'touch', 'mv', 'cp'].includes(cmd) && values.length && !args.includes('--help')) {
      name = ({ rm: 'Delete', unlink: 'Delete', touch: 'Touch', mv: 'Move', cp: 'Copy' } as Record<string, string>)[cmd];
      category = 'edit';
      summary = values.join(cmd === 'mv' || cmd === 'cp' ? ' → ' : ', ');
    } else if (cmd === 'git') {
      let rest = args;
      while (rest[0] === '-C' || rest[0] === '-c') rest = rest.slice(2);
      const action = rest[0];
      const verbs: Record<string, string> = { commit: 'Commit', push: 'Push', pull: 'Pull', merge: 'Merge', rebase: 'Rebase', checkout: 'Checkout', switch: 'Switch', add: 'Stage' };
      if (verbs[action] && !rest.includes('--help') && !rest.includes('--dry-run') && !rest.includes('-n')) {
        name = verbs[action];
        const msg = rest.findIndex(arg => arg === '-m' || arg === '--message');
        summary = action === 'commit' && msg >= 0 ? rest[msg + 1] || 'git commit' : `git ${rest.join(' ')}`;
      }
    }
    if (name && index === steps.length - 1) candidates.push({ ...tool, name, category, summary, detail: source });
  }
  // A compound command can fail before a later operation. Only infer completed actions
  // for a single operation; a successful chain may name its final significant action.
  if (candidates.length && (tool.status === 'ok' || steps.length === 1)) return candidates.at(-1)!;
  // Keep the raw command in the tooltip; discard only setup at the front of the label.
  const stripped = source.replace(/^\s*cd\s+(?:"[^"\n]*"|'[^'\n]*'|[^\s;&|]+)\s*(?:&&|;)\s*/, '')
    .replace(/^(?:[A-Za-z_]\w*=(?:"[^"\n]*"|'[^'\n]*'|[^\s;&|]+)\s*(?:;|&&)?\s*)+/, '');
  return stripped && stripped !== source && (!tool.summary || tool.summary === source)
    ? { ...tool, summary: stripped, detail: source } : tool;
}
