/** Split Markdown at top-level section headings, leaving fenced examples intact. */
export function reviewSections(body: string): { title: string; body: string }[] {
  const sections: { title: string; body: string }[] = [];
  let title = 'Summary', lines: string[] = [], fence = '';
  const flush = () => { if (lines.join('\n').trim()) sections.push({ title, body: lines.join('\n').trim() }); };
  for (const line of body.split('\n')) {
    const marker = line.match(/^\s{0,3}(`{3,}|~{3,})/);
    if (marker) {
      if (!fence) fence = marker[1];
      else if (marker[1][0] === fence[0] && marker[1].length >= fence.length) fence = '';
      lines.push(line); continue;
    }
    const heading = !fence && line.match(/^#{1,2}\s+(.+?)\s*#*$/);
    if (heading) { flush(); title = heading[1]; lines = []; }
    else lines.push(line);
  }
  flush(); return sections;
}

export type MarkKind = 'pin' | 'area' | 'ellipse' | 'arrow' | 'line' | 'pen';
export const markKinds: MarkKind[] = ['pin', 'area', 'ellipse', 'arrow', 'line', 'pen'];
/** Every coordinate is a fraction (0–1) of the screenshot. x/y/w/h is always the mark's bounding box;
 *  an arrow or line starts at x/y and ends at x2/y2, a pen stroke is the flat list `pts`. */
export type ReviewMark = { x: number; y: number; w: number; h: number; side: 'Before' | 'After'; text: string; kind?: MarkKind; x2?: number; y2?: number; pts?: number[]; id?: string; savedText?: string; savedFeedback?: string };
export const maxPenPoints = 400;
/** Older drafts carry no kind: a box was an area, anything else a point. */
export function markKind(mark: ReviewMark): MarkKind {
  if (mark.kind && markKinds.includes(mark.kind)) return mark.kind;
  return mark.w || mark.h ? 'area' : 'pin';
}
/** Accept a stored draft only if every number is a fraction of the image. */
export function validMark(m: unknown): m is ReviewMark {
  const v = m as ReviewMark;
  if (!v || typeof v !== 'object' || !['Before', 'After'].includes(v.side) || typeof v.text !== 'string') return false;
  const fraction = (n: unknown) => typeof n === 'number' && Number.isFinite(n) && n >= 0 && n <= 1;
  if (!(['x', 'y', 'w', 'h'] as const).every(k => fraction(v[k]))) return false;
  if (v.kind !== undefined && !markKinds.includes(v.kind)) return false;
  if (v.x2 !== undefined && !fraction(v.x2)) return false;
  if (v.y2 !== undefined && !fraction(v.y2)) return false;
  if (v.pts !== undefined && (!Array.isArray(v.pts) || v.pts.length > maxPenPoints * 2 || v.pts.length % 2 || !v.pts.every(fraction))) return false;
  if ((v.kind === 'arrow' || v.kind === 'line') && (v.x2 === undefined || v.y2 === undefined)) return false;
  if (v.kind === 'pen' && (!v.pts || v.pts.length < 4)) return false;
  return true;
}
/** Triangle for an arrow tip at (x2,y2), in whatever unit the caller draws in. */
export function arrowHead(x1: number, y1: number, x2: number, y2: number, size: number): string {
  const angle = Math.atan2(y2 - y1, x2 - x1), spread = Math.PI / 7;
  const wing = (a: number) => `${x2 - size * Math.cos(a)},${y2 - size * Math.sin(a)}`;
  return `${x2},${y2} ${wing(angle - spread)} ${wing(angle + spread)}`;
}
/** Thin a pen stroke to at most `max` points, keeping the first and the last. */
export function simplifyStroke(pts: number[], max = maxPenPoints): number[] {
  const count = pts.length / 2;
  if (count <= max) return pts;
  const out: number[] = [];
  for (let i = 0; i < max; i++) { const j = Math.round(i * (count - 1) / (max - 1)); out.push(pts[j * 2], pts[j * 2 + 1]); }
  return out;
}
export function markLocation(mark: ReviewMark): string {
  const pct = (n: number) => `${Math.round(n * 1000) / 10}%`;
  const kind = markKind(mark), at = `x=${pct(mark.x)}, y=${pct(mark.y)}`, box = `, width=${pct(mark.w)}, height=${pct(mark.h)}`;
  const lead = `${mark.side} screenshot, `;
  if (kind === 'pin') return `${lead}point from top-left: ${at}`;
  if (kind === 'area') return `${lead}area from top-left: ${at}${box}`;
  if (kind === 'ellipse') return `${lead}ellipse from top-left: ${at}${box}`;
  if (kind === 'pen') {
    const path: string[] = [];
    for (let i = 0; i + 1 < (mark.pts ?? []).length; i += 2) path.push(`(${pct(mark.pts![i])}, ${pct(mark.pts![i + 1])})`);
    return `${lead}freehand stroke, bounding box from top-left: ${at}${box}${path.length ? `, path (x, y): ${path.join(' ')}` : ''}`;
  }
  return `${lead}${kind} from top-left: ${at} to x=${pct(mark.x2 ?? mark.x)}, y=${pct(mark.y2 ?? mark.y)}`;
}

/** Whether a point (in stage pixels) is on a saved mark: near a line, stroke or pin, or inside an area or ellipse. */
export function markHit(mark: ReviewMark, px: number, py: number, w: number, h: number, tolerance = 8): boolean {
  const kind = markKind(mark);
  const near = (ax: number, ay: number, bx: number, by: number) => {
    const dx = bx - ax, dy = by - ay, len = dx * dx + dy * dy;
    const t = len ? Math.max(0, Math.min(1, ((px - ax) * dx + (py - ay) * dy) / len)) : 0;
    return Math.hypot(px - (ax + t * dx), py - (ay + t * dy)) <= tolerance;
  };
  if (kind === 'pin') return Math.hypot(px - mark.x * w, py - mark.y * h) <= tolerance * 2;
  if (kind === 'area') return px >= mark.x * w && px <= (mark.x + mark.w) * w && py >= mark.y * h && py <= (mark.y + mark.h) * h;
  if (kind === 'ellipse') {
    const rx = (mark.w * w) / 2, ry = (mark.h * h) / 2;
    if (!rx || !ry) return false;
    return ((px - (mark.x * w + rx)) / rx) ** 2 + ((py - (mark.y * h + ry)) / ry) ** 2 <= 1;
  }
  if (kind === 'pen') {
    const pts = mark.pts ?? [];
    for (let i = 0; i + 3 < pts.length; i += 2) if (near(pts[i] * w, pts[i + 1] * h, pts[i + 2] * w, pts[i + 3] * h)) return true;
    return false;
  }
  return near(mark.x * w, mark.y * h, (mark.x2 ?? mark.x) * w, (mark.y2 ?? mark.y) * h);
}
