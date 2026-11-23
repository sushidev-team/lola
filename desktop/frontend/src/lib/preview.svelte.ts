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
    return `${d.origin}${p.pathname}${p.search}${p.hash}`;
  } catch {
    return devUrl;
  }
}

export interface PreviewMark {
  selector?: string;
  text?: string;
  label?: string;
}

export interface PreviewStep {
  do: string;
  selector?: string;
  text?: string;
  value?: string;
  key?: string;
  path?: string;
  ms?: number;
}

/**
 * How the agent prepared one page for the reviewer (the review manifest's
 * page/screen fields). Declarative data the injected prepare script carries out;
 * nothing here is code.
 */
export interface PreviewPrep {
  device?: string;
  waitFor?: string;
  scrollY?: number;
  marks?: PreviewMark[] | null;
  /** Clicks and typing before the marks. The prepare script runs them only on a local address. */
  steps?: PreviewStep[] | null;
}

export interface PreviewRequest {
  sessionId: string;
  /** One of the session's own dev URLs. */
  url: string;
  prep?: PreviewPrep;
  /** What to look at here, in the agent's words. */
  caption?: string;
}

/** Most pages one preview opens as tabs; more is a list, not a review. */
export const MAX_PREVIEW_TABS = 6;

export interface PreviewTab {
  url: string;
  title: string;
  caption?: string;
  prep?: PreviewPrep;
}

/** Whether a prep says anything: an empty one is not "prepared". */
export function hasPrep(p?: PreviewPrep): p is PreviewPrep {
  return !!p && !!(p.device || p.waitFor || p.scrollY || p.marks?.length || p.steps?.length);
}

function request(sessionId: string, t: { url: string; caption?: string; prep?: PreviewPrep }): PreviewRequest {
  const r: PreviewRequest = { sessionId, url: t.url };
  if (hasPrep(t.prep)) r.prep = t.prep;
  if (t.caption) r.caption = t.caption;
  return r;
}

export interface OpenPreviewTab {
  id: number;
  request: PreviewRequest;
  title: string;
  documentTitle: string;
  currentURL: string;
  visited: boolean;
}

class PreviewStore {
  private nextId = 0;
  sidebarOpen = $state<boolean | null>(null);
  tabs = $state<OpenPreviewTab[]>([]);
  active = $state(0);
  visible = $state(false);
  sessionId = $derived(this.tabs[0]?.request.sessionId);
  request = $derived(this.visible ? this.tabs[this.active]?.request ?? null : null);

  private tab(sessionId: string, page: PreviewTab): OpenPreviewTab {
    return {
      id: ++this.nextId, request: request(sessionId, page), title: page.title,
      documentTitle: "", currentURL: page.url, visited: false,
    };
  }

  open(sessionId: string, url: string, extra: { prep?: PreviewPrep; caption?: string; title?: string } = {}) {
    if (this.sessionId !== sessionId) this.close();
    this.tabs.push(this.tab(sessionId, { url, title: "", ...extra }));
    this.select(this.tabs.length - 1);
    this.visible = true;
  }
  duplicate(device?: string) {
    const original = this.tabs[this.active];
    if (!original) return;
    // Load the current route in a fresh document. Replaying the original steps
    // could submit a form again or take the duplicate away from that route.
    const copy = this.tab(original.request.sessionId, {
      url: original.currentURL, title: original.title, prep: device ? { device } : undefined,
    });
    copy.documentTitle = original.documentTitle;
    const next = this.active + 1;
    this.tabs.splice(next, 0, copy);
    this.select(next);
  }

  /**
   * Pages of one app as separate tabs, at most MAX_PREVIEW_TABS, first one shown.
   * The same address twice is fine when it is prepared differently: that is two
   * areas of one app.
   */
  openTabs(sessionId: string, pages: PreviewTab[]) {
    const seen = new Set<string>();
    const tabs = pages
      .filter((p) => {
        const key = p.url + "\n" + JSON.stringify(hasPrep(p.prep) ? p.prep : null);
        return !seen.has(key) && seen.add(key);
      })
      .slice(0, MAX_PREVIEW_TABS);
    if (!tabs.length) return;
    this.tabs = tabs.map(page => this.tab(sessionId, page));
    this.select(0);
    this.visible = true;
  }

  select(index: number) {
    const tab = this.tabs[index];
    if (!tab) return;
    this.active = index;
    tab.visited = true;
  }

  closeTab(index: number) {
    if (!this.tabs[index]) return;
    this.tabs.splice(index, 1);
    if (!this.tabs.length) this.close();
    else this.select(index < this.active ? this.active - 1 : Math.min(this.active, this.tabs.length - 1));
  }

  close() {
    this.visible = false;
    this.sidebarOpen = null;
    this.tabs = [];
    this.active = 0;
  }
}

export const preview = new PreviewStore();

/** The preparation fields of a review page or screen, as a PreviewPrep. */
export function prepOf(o: { device?: string; waitFor?: string; scrollY?: number; marks?: PreviewMark[] | null; steps?: PreviewStep[] | null }): PreviewPrep | undefined {
  const p: PreviewPrep = { device: o.device, waitFor: o.waitFor, scrollY: o.scrollY, marks: o.marks, steps: o.steps };
  return hasPrep(p) ? p : undefined;
}
