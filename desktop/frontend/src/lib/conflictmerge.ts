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
