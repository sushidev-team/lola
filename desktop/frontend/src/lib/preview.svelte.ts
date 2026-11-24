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

/** Size the browsing viewport first, then scale its painted contents into the stage. */
export function previewLayout(v: Viewport, width: number, height: number) {
  const logicalWidth = v.width ?? 0;
  const scale = width > 0 && height > 0 && logicalWidth > 0
    ? Math.min(1, width / logicalWidth, v.height ? height / v.height : 1)
    : 1;
  const logicalHeight = v.height ?? 0;
  const style = `width:${logicalWidth ? logicalWidth + "px" : "100%"};height:${logicalHeight ? logicalHeight + "px" : "100%"}`;
  return { scale, style: scale < 1 ? `${style};transform:scale(${scale})` : style };
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

/** Resolve a site's icon on the same origin that serves its preview. */
export function previewIcon(declared: unknown, page: string, upstream = page): string {
  const origin = new URL(page).origin;
  const fallback = origin + "/favicon.ico";
  if (typeof declared !== "string" || !declared || declared.length > 32768) return fallback;
  if (/^data:image\/(?:png|svg\+xml|gif|webp|x-icon|vnd\.microsoft\.icon)[;,]/i.test(declared)) return declared;
  try {
    const icon = new URL(declared, page);
    if (["http:", "https:"].includes(icon.protocol) && !icon.username && !icon.password &&
        (icon.origin === origin || icon.origin === new URL(upstream).origin)) {
      return origin + icon.pathname + icon.search + icon.hash;
    }
  } catch { /* Try the conventional icon when the declared one is malformed. */ }
  return fallback;
}

/** Paths use the displayed page; bare websites default to HTTPS, and known
 * servers keep the scheme they advertised. */
export function previewAddress(input: string, current: string, servers: string[]): string | undefined {
  const raw = input.trim();
  if (!raw) return;
  const origins = new Set<string>();
  for (const server of servers) {
    try { origins.add(new URL(server).origin); } catch { /* Ignore an unusable dev URL. */ }
  }
  try {
    const host = raw.split(/[/?#]/, 1)[0].toLowerCase();
    const base = new URL(current);
    const bare = [base.origin, ...origins].find(origin => new URL(origin).host === host);
    let url: URL;
    if (bare) {
      url = new URL(new URL(bare).protocol + "//" + raw);
    } else if (/^(?:localhost|\[[\da-f:.]+\]|[\p{L}\p{N}-]+(?:\.[\p{L}\p{N}-]+)+\.?)(?::\d+)?$/iu.test(host)) {
      const loopback = /^(?:localhost|127\.[\d.]+|\[::1\])(?::|$)/.test(host);
      url = new URL((loopback ? "http://" : "https://") + raw);
    } else {
      url = new URL(raw, base);
    }
    if (["http:", "https:"].includes(url.protocol) && !url.username && !url.password) return url.href;
  } catch { /* Malformed address. */ }
}

/** The session's dev servers use the instrumented preview. Other web
 * addresses open in the native browser view in the same workspace. */
export function isPreviewDevServer(address: string, servers: string[]): boolean {
  const origin = new URL(address).origin;
  return servers.some(server => {
    try { return new URL(server).origin === origin; } catch { return false; }
  });
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
  /** A session dev URL unless this is a direct external browser tab. */
  url: string;
  external?: boolean;
  prep?: PreviewPrep;
  /** What to look at here, in the agent's words. */
  caption?: string;
}

/** Most pages one preview opens as tabs; more is a list, not a review. */
export const MAX_PREVIEW_TABS = 6;

export interface PreviewTab {
  url: string;
  external?: boolean;
  title: string;
  caption?: string;
  prep?: PreviewPrep;
}

/** Whether a prep says anything: an empty one is not "prepared". */
export function hasPrep(p?: PreviewPrep): p is PreviewPrep {
  return !!p && !!(p.device || p.waitFor || p.scrollY || p.marks?.length || p.steps?.length);
}

function request(sessionId: string, t: PreviewTab): PreviewRequest {
  const r: PreviewRequest = { sessionId, url: t.url };
  if (t.external) r.external = true;
  if (hasPrep(t.prep)) r.prep = t.prep;
  if (t.caption) r.caption = t.caption;
  return r;
}

export interface OpenPreviewTab {
  id: number;
  request: PreviewRequest;
  title: string;
  documentTitle: string;
  favicon?: string;
  currentURL: string;
  backStack: string[];
  canGoBackInFrame: boolean;
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
      documentTitle: "", currentURL: page.url, backStack: [], canGoBackInFrame: false, visited: false,
    };
  }

  visit(tab: OpenPreviewTab, url: string, kind: "push" | "replace" | "traverse" = "push") {
    const href = new URL(url).href;
    if (href !== new URL(tab.currentURL).href) {
      const previous = kind === "traverse" ? tab.backStack.findLastIndex(entry => new URL(entry).href === href) : -1;
      if (previous >= 0) tab.backStack.splice(previous);
      else if (kind !== "replace") tab.backStack.push(tab.currentURL);
    }
    tab.currentURL = url;
  }

  open(sessionId: string, url: string, extra: Partial<Omit<PreviewTab, "url">> = {}) {
    if (this.sessionId !== sessionId) this.close();
    this.tabs.push(this.tab(sessionId, { url, title: "", ...extra }));
    this.select(this.tabs.length - 1);
    this.visible = true;
  }
  openLink(sessionId: string, address: string, servers: string[], device?: string) {
    let url: URL;
    try { url = new URL(address); } catch { return; }
    if (!["http:", "https:"].includes(url.protocol) || url.username || url.password) return;
    const external = !isPreviewDevServer(url.href, servers);
    this.open(sessionId, url.href, { external, prep: !external && device ? { device } : undefined });
  }
  duplicate(device?: string) {
    const original = this.tabs[this.active];
    if (!original) return;
    // Load the current route in a fresh document. Replaying the original steps
    // could submit a form again or take the duplicate away from that route.
    const copy = this.tab(original.request.sessionId, {
      url: original.currentURL, title: original.title, external: original.request.external, prep: device ? { device } : undefined,
    });
    copy.documentTitle = original.documentTitle;
    copy.favicon = original.favicon;
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
