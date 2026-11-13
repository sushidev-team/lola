// Filtering and highlights share the same fuzzy match. Prefer consecutive
// text, then fuzzy text. Each query word must fit entirely within one field.
export function searchMatch(query: string, ...values: (string | undefined)[]): number[][] | null {
  const fields = values.map(value => Array.from(value || ""));
  const hits = fields.map(() => new Set<number>());
  for (const word of query.toLocaleLowerCase().trim().split(/\s+/).filter(Boolean)) {
    const needle = Array.from(word);
    let positions: [number, number][] | undefined;
    for (const consecutive of [true, false]) {
      for (let f = 0; f < fields.length; f++) {
        const indices = matchIndices(needle, fields[f].map(c => c.toLocaleLowerCase()), consecutive);
        if (indices) { positions = indices.map(i => [f, i]); break; }
      }
      if (positions) break;
    }
    if (!positions) return null;
    for (const [f, i] of positions) hits[f].add(i);
  }
  return hits.map(set => [...set].sort((a, b) => a - b));
}

function matchIndices(needle: string[], text: string[], consecutive: boolean): number[] | undefined {
  if (consecutive) {
    const start = text.findIndex((_, i) => needle.every((c, j) => text[i + j] === c));
    return start < 0 ? undefined : needle.map((_, i) => start + i);
  }
  const indices: number[] = [];
  let at = 0;
  for (const char of needle) {
    at = text.indexOf(char, at);
    if (at < 0) return undefined;
    indices.push(at++);
  }
  return indices;
}

export function matchesSearch(query: string, ...values: (string | undefined)[]): boolean {
  return searchMatch(query, ...values) !== null;
}
