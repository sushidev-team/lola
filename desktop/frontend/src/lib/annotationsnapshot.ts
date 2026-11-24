import type { Rect } from "./previewmark";

// Capture only the visible part of the dev page around both endpoints. Leave
// room for a row's number and name even when the arrow starts in its padding.
export function annotationCrop(frame: DOMRect, mark: Rect, scale: number, width: number, height: number): Rect {
  const x = Math.max(0, frame.left, frame.left + (mark.x - 300) * scale);
  const y = Math.max(0, frame.top, frame.top + (mark.y - 64) * scale);
  const right = Math.min(width, frame.right, frame.left + (mark.x + mark.w + 300) * scale);
  const bottom = Math.min(height, frame.bottom, frame.top + (mark.y + mark.h + 64) * scale);
  return { x, y, w: right - x, h: bottom - y };
}

export async function annotationSnapshot(frame: HTMLIFrameElement, mark: Rect, scale: number): Promise<string> {
  const handler = (window as Window & { webkit?: { messageHandlers?: { lolaAnnotationSnapshot?: {
    postMessage(request: Rect & { viewport: number }): Promise<string>;
  } } } }).webkit?.messageHandlers?.lolaAnnotationSnapshot;
  const crop = annotationCrop(frame.getBoundingClientRect(), mark, scale, innerWidth, innerHeight);
  if (!handler || crop.w <= 0 || crop.h <= 0) throw new Error("Preview capture is unavailable.");
  return handler.postMessage({ ...crop, viewport: innerWidth });
}
