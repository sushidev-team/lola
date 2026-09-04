import { describe, it, expect, vi, afterEach } from "vitest";
import {
  isOpenable,
  openExternal,
  resetBrowserCache,
  type BrowserLoader,
} from "./openurl";

describe("the scheme guard", () => {
  it("allows http and https", () => {
    expect(isOpenable("http://127.0.0.1:8000")).toBe(true);
    expect(isOpenable("https://github.com/x/y/pull/1")).toBe(true);
  });

  it("refuses every scheme a log line can also print", () => {
    // The reason the guard exists: terminal text is untrusted, and an agent can
    // be induced to print any of these.
    expect(isOpenable("file:///etc/passwd")).toBe(false);
    expect(isOpenable("javascript:alert(1)")).toBe(false);
    expect(isOpenable("data:text/html,<script>1</script>")).toBe(false);
    expect(isOpenable("lola://pane/lola-fe-42")).toBe(false);
  });

  it("refuses a string that is not a URL at all", () => {
    expect(isOpenable("")).toBe(false);
    expect(isOpenable("see http://x for details")).toBe(false);
    expect(isOpenable(undefined as unknown as string)).toBe(false);
  });
});

describe("openExternal", () => {
  // The chain is driven with an INJECTED loader rather than through the real
  // one. `@capacitor/browser` is a genuine dependency, so a dynamic import of it
  // succeeds under vitest and hands back the plugin's own web implementation —
  // which would make "there is no plugin here" untestable, and would make the
  // result depend on whether node_modules happened to be installed.
  const noPlugin: BrowserLoader = async () => undefined;
  const plugin =
    (open: (o: { url: string }) => Promise<unknown>): BrowserLoader =>
    async () => ({
      open,
    });

  afterEach(() => {
    delete (globalThis as { Capacitor?: unknown }).Capacitor;
    resetBrowserCache();
    vi.restoreAllMocks();
  });

  it("does nothing at all for a refused URL", async () => {
    const open = vi.fn();
    vi.stubGlobal("open", open);
    const load = vi.fn(noPlugin);
    await openExternal("javascript:alert(1)", load);
    expect(open).not.toHaveBeenCalled();
    expect(load).not.toHaveBeenCalled(); // the guard runs before anything is loaded
  });

  it("prefers the browser plugin when one is available", async () => {
    const plug = vi.fn().mockResolvedValue(undefined);
    const open = vi.fn();
    vi.stubGlobal("open", open);
    await openExternal("https://example.com", plugin(plug));
    expect(plug).toHaveBeenCalledWith({ url: "https://example.com" });
    expect(open).not.toHaveBeenCalled();
  });

  it("falls back to the system target when there is no plugin", async () => {
    // "_system" is what Capacitor's WebView maps onto the real browser. A plain
    // "_blank" in a WKWebView opens the page INSIDE the app, with no way back —
    // which is why it is the LAST resort and not the first.
    const open = vi.fn();
    vi.stubGlobal("open", open);
    await openExternal("http://127.0.0.1:8000", noPlugin);
    expect(open).toHaveBeenCalledWith(
      "http://127.0.0.1:8000",
      "_system",
      "noopener",
    );
    expect(open).toHaveBeenCalledTimes(1);
  });

  it("tries _blank when the shell did not recognise _system", async () => {
    // A desktop browser, which is what `npm run dev` runs in, knows nothing
    // about "_system" and returns null for it. The header promised this step for
    // a while before the code had it.
    const open = vi.fn((_u: string, target?: string) =>
      target === "_system" ? null : {},
    );
    vi.stubGlobal("open", open);
    await openExternal("https://example.com", noPlugin);
    expect(open.mock.calls.map((c) => c[1])).toEqual(["_system", "_blank"]);
  });

  it("swallows a failing plugin rather than rejecting into xterm's link handler", async () => {
    // It falls through to the window opener, which HERE succeeds — so the
    // answer is true. What is asserted is that nothing rejected.
    const open = vi.fn();
    vi.stubGlobal("open", open);
    await expect(
      openExternal(
        "https://example.com",
        plugin(vi.fn().mockRejectedValue(new Error("no"))),
      ),
    ).resolves.toBe(true);
    expect(open).toHaveBeenCalled();
  });

  it("swallows a loader that throws", async () => {
    const open = vi.fn();
    vi.stubGlobal("open", open);
    await expect(
      openExternal("https://example.com", async () => {
        throw new Error("no bundler");
      }),
    ).resolves.toBe(true);
    expect(open).toHaveBeenCalled();
  });

  it("does not throw when the shell has no window opener either", async () => {
    // AND SAYS SO. A button whose only job is to open something has to be able
    // to report that it could not — silence is what "it shows, but nothing
    // happens on click" is made of.
    vi.stubGlobal("open", undefined);
    await expect(openExternal("https://example.com", noPlugin)).resolves.toBe(
      false,
    );
  });

  it("answers false for a URL it refuses to open", async () => {
    await expect(openExternal("javascript:alert(1)", noPlugin)).resolves.toBe(
      false,
    );
    await expect(openExternal("", noPlugin)).resolves.toBe(false);
  });
});
