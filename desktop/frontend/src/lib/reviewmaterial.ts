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

export type ReviewMark = { x: number; y: number; w: number; h: number; side: 'Before' | 'After'; text: string; id?: string; savedText?: string; savedFeedback?: string };
export function markLocation(mark: ReviewMark): string {
  const pct = (n: number) => `${Math.round(n * 1000) / 10}%`;
  return `${mark.side} screenshot, ${mark.w || mark.h ? 'area' : 'point'} from top-left: x=${pct(mark.x)}, y=${pct(mark.y)}${mark.w || mark.h ? `, width=${pct(mark.w)}, height=${pct(mark.h)}` : ''}`;
}
