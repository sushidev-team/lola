import { simplifyStroke } from "./reviewmaterial";

export type PreviewTool = "comment" | "area" | "arrow" | "pen" | "text";
export type Point = { x: number; y: number };
export type Rect = Point & { w: number; h: number };
// While drawing, x/y are frame coordinates. Saved marks use the same CSS
// pixels, relative to the DOM anchor. Stroke points are local to the mark.
export type PreviewMark = Rect & { kind: "area" | "arrow" | "pen" | "text"; points: number[]; label?: string; fontSize?: number; page?: boolean };

export function makeMark(tool: PreviewTool, path: number[]): PreviewMark {
  const points = simplifyStroke(path, 96);
  const xs = points.filter((_, i) => i % 2 === 0);
  const ys = points.filter((_, i) => i % 2 === 1);
  const x = Math.min(...xs), y = Math.min(...ys);
  return {
    kind: tool === "comment" ? "area" : tool,
    x, y, w: Math.max(1, Math.max(...xs) - x), h: Math.max(1, Math.max(...ys) - y),
    points: tool === "arrow" || tool === "pen" ? points.map((n, i) => n - (i % 2 === 0 ? x : y)) : [],
  };
}

export type Grip = "move" | "nw" | "ne" | "sw" | "se" | "start" | "end";
export const labelFontSize = 16;

// Work in frame CSS pixels for both responsive and scaled device previews.
export function transformMark(m: PreviewMark, grip: Grip, dx: number, dy: number): PreviewMark {
  if (grip === "move") return { ...m, x: m.x + dx, y: m.y + dy };
  if (grip === "start" || grip === "end") {
    const path = m.points.map((n, i) => n + (i % 2 ? m.y : m.x));
    const i = grip === "start" ? 0 : 2;
    path[i] += dx;
    path[i + 1] += dy;
    return { ...m, ...makeMark("arrow", path) };
  }
  if (m.kind === "text") {
    const size = m.fontSize ?? labelFontSize;
    const ratio = 1 + (dx * m.w + dy * m.h) / Math.max(1, m.w ** 2 + m.h ** 2);
    const fontSize = Math.max(10, Math.min(64, Math.round(size * ratio)));
    return { ...m, fontSize, w: Math.round(m.w * fontSize / size), h: Math.round(m.h * fontSize / size) };
  }
  const west = grip.endsWith("w"), north = grip.startsWith("n");
  const w = Math.max(1, m.w + (west ? -dx : dx)), h = Math.max(1, m.h + (north ? -dy : dy));
  return { ...m, x: m.x + (west ? m.w - w : 0), y: m.y + (north ? m.h - h : 0), w, h,
    points: m.points.map((n, i) => Math.round(n * (i % 2 ? h / m.h : w / m.w))) };
}

export function describeMark(mark: PreviewMark, selector: string): string {
  const name = { area: "Rectangle", arrow: "Arrow", pen: "Freehand stroke", text: "Text label" }[mark.kind];
  let text = `${name} at (${mark.x}, ${mark.y}) relative to the ${selector === ":root" ? "page" : "anchor"}’s top left, ${mark.w} × ${mark.h} CSS pixels.`;
  if (mark.points.length) {
    const points = [];
    for (let i = 0; i < mark.points.length; i += 2) points.push(`(${mark.x + mark.points[i]},${mark.y + mark.points[i + 1]})`);
    text += " Path relative to anchor: " + points.join(" ");
  }
  return text;
}
