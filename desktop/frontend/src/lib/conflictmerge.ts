// Offsets refer to the original working file. Untouched text, including line
// endings and a missing final newline, passes through verbatim.
export interface ConflictBlock {
  start: number; end: number; line: number;
  current: string; incoming: string; base?: string;
  currentName: string; incomingName: string; incomingLine: number;
}

export function conflictBlocks(text: string): ConflictBlock[] {
  const blocks: ConflictBlock[] = [];
  const lines = /^([^\r\n]*)(\r?\n|$)/gm;
  let start = -1, line = 0, startLine = 0, width = 0, content = 0, divided = false;
  let incomingLine = 0;
  let current = '', base: string | undefined, currentName = '';
  for (const match of text.matchAll(lines)) {
    const row = match[1], offset = match.index!;
    line++;
    const opening = /^(<{7,})(?: (.*))?$/.exec(row);
    if (opening) {
      if (start >= 0) return []; // ambiguous markers: use the full-file editor
      start = offset; startLine = line; width = opening[1].length;
      currentName = opening[2] || ''; base = undefined; divided = false;
      content = offset + match[0].length;
    } else if (start >= 0) {
      const next = offset + match[0].length;
      const baseMarker = '|'.repeat(width), endMarker = '>'.repeat(width);
      if (!divided && base === undefined && (row === baseMarker || row.startsWith(baseMarker + ' '))) {
        current = text.slice(content, offset); base = ''; content = next;
      } else if (!divided && row === '='.repeat(width)) {
        if (base === undefined) current = text.slice(content, offset);
        else base = text.slice(content, offset);
        divided = true; content = next; incomingLine = line + 1;
      } else if (divided && (row === endMarker || row.startsWith(endMarker + ' '))) {
        blocks.push({start,end:next,line:startLine,current,base,incoming:text.slice(content,offset),
          currentName,incomingLine,incomingName:row.slice(width).trim()});
        start = -1;
      }
    }
  }
  return start < 0 ? blocks : [];
}

export function mergedText(text: string, blocks: ConflictBlock[], choices: (string | null)[]): string {
  let offset = 0, output = '';
  blocks.forEach((block, i) => {
    output += text.slice(offset, block.start) + (choices[i] ?? text.slice(block.start, block.end));
    offset = block.end;
  });
  return output + text.slice(offset);
}

export function selectedLines(value: string, start: number, end: number): string {
  const first = start === 0 ? 0 : value.lastIndexOf('\n', start - 1) + 1;
  const last = value.indexOf('\n', Math.max(start, end - 1));
  return value.slice(first, last < 0 ? value.length : last + 1);
}

// Accept one complete decision object. Providers sometimes wrap it in Markdown
// or introduce it with a sentence. Never salvage entries from a partial reply.
export function parseConflictChoices(reply: string): Record<string, unknown> | null {
  const text = reply.trim();
  const start = text.indexOf('{'), end = text.lastIndexOf('}');
  if (start < 0 || end < start) return null;
  const prefix = text.slice(0, start), suffix = text.slice(end + 1);
  if (/[[\]]/.test(prefix + suffix)) return null;
  try {
    return JSON.parse(text.slice(start, end + 1));
  } catch {
    return null;
  }
}

// Resolve independent npm lockfile edits by key. Arrays are indivisible, and
// an absent key is distinct from null. A competing edit needs the usual editor.
export function mergeLockfile(text:string, blocks:ConflictBlock[], base:string, current:string, incoming:string): string | null {
  if (!blocks.length) return null;
  // Preserve local working-file edits. One projection must match its complete
  // index blob, and the opposite conflict excerpts must occur there in order
  // in their own index version (Git can align repeated lockfile context).
  function matches(side:'current'|'incoming', source:string) {
    let offset = 0;
    for (const block of blocks) {
      const pos = source.indexOf(block[side],offset);
      if (pos < 0) return false;
      offset = pos + block[side].length;
    }
    return true;
  }
  if (!matches('current',current) || !matches('incoming',incoming)) return null;
  if (mergedText(text,blocks,blocks.map(b=>b.current)) !== current &&
      mergedText(text,blocks,blocks.map(b=>b.incoming)) !== incoming) return null;

  function object(value:unknown): value is Record<string,unknown> {
    return value !== null && typeof value === 'object' && !Array.isArray(value);
  }
  function merge(ancestor:unknown, left:unknown, right:unknown):unknown {
    const a = JSON.stringify(ancestor), l = JSON.stringify(left), r = JSON.stringify(right);
    if (l === r || r === a) return left;
    if (l === a) return right;
    if (!object(ancestor) || !object(left) || !object(right)) throw new Error('Competing lockfile edits');
    const result:Record<string,unknown> = Object.create(null);
    const own = (obj:Record<string,unknown>, key:string) => Object.hasOwn(obj,key) ? obj[key] : undefined;
    for (const key of new Set([...Object.keys(left), ...Object.keys(right)])) {
      const value = merge(own(ancestor,key), own(left,key), own(right,key));
      if (value !== undefined) result[key] = value;
    }
    return result;
  }
  try {
    const versions = [base,current,incoming].map(s=>JSON.parse(s));
    if (!versions.every(v => object(v) && object(v.packages) && v.lockfileVersion === versions[0].lockfileVersion)) return null;
    const result = merge(versions[0],versions[1],versions[2]);
    const indent = current.match(/\n([ \t]+)"/)?.[1] ?? 2;
    const eol = current.includes('\r\n') ? '\r\n' : '\n';
    return JSON.stringify(result,null,indent).replace(/\n/g,eol) + (current.endsWith('\n') ? eol : '');
  } catch { return null; }
}
