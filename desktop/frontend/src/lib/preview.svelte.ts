// State and pure helpers for the session preview pane: the session's dev server
// framed through internal/preview's loopback proxy, with an element picker.
// The picker script lives in the framed page and talks to the pane by
// postMessage (internal/preview/picker.js); everything it says is untrusted
// page text, so it is only ever shown, clipped, or sent as quoted evidence.

export type ViewportId = "mobile" | "tablet" | "desktop";

export interface Viewport {
  id: ViewportId;
  label: string;
  /** CSS pixels. `null` width = fill the pane. */
  width: number | null;
  height: number | null;
}

export const VIEWPORTS: Viewport[] = [
  { id: "mobile", label: "Mobile", width: 390, height: 844 },
  { id: "tablet", label: "Tablet", width: 820, height: 1180 },
  { id: "desktop", label: "Desktop", width: null, height: null },
];

export const DEFAULT_VIEWPORT: ViewportId = "desktop";

/** The marker every message from the picker script carries. */
export const PICKER_SOURCE = "lola-preview";

export interface PickedElement {
  url: string;
  selector: string;
  tag: string;
  html: string;
  text: string;
}

const clip = (v: unknown, n: number) => (typeof v === "string" ? v.slice(0, n) : "");

/**
 * Reads a pick message from the framed page. Anything that is not one — wrong
 * source, wrong type, no selector — is null, so a page cannot make the pane act
 * on a message it was not built to take.
 */
export function parsePick(data: unknown): PickedElement | null {
  if (!data || typeof data !== "object") return null;
  const d = data as Record<string, unknown>;
  if (d.source !== PICKER_SOURCE || d.type !== "pick") return null;
  const selector = clip(d.selector, 300).trim();
  if (!selector) return null;
  return { url: clip(d.url, 600), selector, tag: clip(d.tag, 40), html: clip(d.html, 6000), text: clip(d.text, 400) };
}

/** The frame size for a preset, as an inline style. */
export function frameStyle(v: Viewport): string {
  return v.width && v.height ? `width:${v.width}px;height:${v.height}px` : "width:100%;height:100%";
}

/**
 * The page the picked element sits on, as the dev server (not the proxy)
 * knows it: the proxy's origin is swapped for the dev URL's, so the agent is
 * told an address it can recognise from its own dev tab.
 */
export function devPageURL(picked: string, devUrl: string): string {
  try {
    const p = new URL(picked);
    const d = new URL(devUrl);
    return `${d.origin}${p.pathname}${p.search}`;
  } catch {
    return devUrl;
  }
}

export interface PreviewRequest {
  sessionId: string;
  /** One of the session's own dev URLs. */
  url: string;
}

/** Most pages one preview opens as tabs; more is a list, not a review. */
export const MAX_PREVIEW_TABS = 6;

export interface PreviewTab {
  url: string;
  title: string;
}

class PreviewStore {
  request = $state<PreviewRequest | null>(null);
  /** Several pages of one app, each its own tab; empty for a single page. */
  tabs = $state<PreviewTab[]>([]);
  active = $state(0);
  /** One address. Inside a tab set it re-points the active tab (the address chips). */
  open(sessionId: string, url: string) {
    if (this.tabs.length > 1 && this.request?.sessionId === sessionId) {
      this.tabs[this.active] = { url, title: "" };
    } else {
      this.tabs = [];
      this.active = 0;
    }
    this.request = { sessionId, url };
  }
  /** Pages of one app as separate tabs, at most MAX_PREVIEW_TABS, first one shown. */
  openTabs(sessionId: string, pages: PreviewTab[]) {
    const seen = new Set<string>();
    const tabs = pages.filter((p) => !seen.has(p.url) && seen.add(p.url)).slice(0, MAX_PREVIEW_TABS);
    if (!tabs.length) return;
    this.tabs = tabs.length > 1 ? tabs : [];
    this.active = 0;
    this.request = { sessionId, url: tabs[0].url };
  }
  select(index: number) {
    const tab = this.tabs[index];
    if (!tab || !this.request) return;
    this.active = index;
    this.request = { sessionId: this.request.sessionId, url: tab.url };
  }
  close() {
    this.request = null;
    this.tabs = [];
    this.active = 0;
  }
}

export const preview = new PreviewStore();
