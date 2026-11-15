import { afterEach, describe, expect, it, vi } from "vitest";

import { isLoopbackUrl, isOpenable, openExternal, openOnPhone, OpenOnPhoneError } from "./openurl";

const held = vi.hoisted(() => ({ native: null as unknown, browser: null as unknown }));
vi.mock("../native", () => ({
  get native() {
    return held.native;
  },
  get browser() {
    return held.browser;
  },
}));

afterEach(() => {
  held.native = null;
  held.browser = null;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("the scheme guard", () => {
  it("allows http and https", () => {
    expect(isOpenable("http://127.0.0.1:8000")).toBe(true);
    expect(isOpenable("https://github.com/x/y/pull/1")).toBe(true);
  });

  it("refuses every scheme a log line can also print", () => {
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

describe("isLoopbackUrl", () => {
  it("names the Mac for localhost, 127/8 and ::1", () => {
    for (const u of [
      "http://localhost:3000",
      "http://app.localhost/",
      "http://127.0.0.1:8080",
      "http://127.4.5.6/",
      "http://[::1]:3000/",
    ]) {
      expect(isLoopbackUrl(u)).toBe(true);
    }
  });

  it("leaves LAN and public hosts alone", () => {
    for (const u of ["http://192.168.20.3:65497", "https://github.com", "http://127.example.com/", "not a url"]) {
      expect(isLoopbackUrl(u)).toBe(false);
    }
  });
});

describe("openExternal", () => {
  it("does nothing at all for a refused URL", async () => {
    const openURL = vi.fn();
    held.native = { openURL };
    const open = vi.fn();
    vi.stubGlobal("open", open);
    await expect(openExternal("javascript:alert(1)")).resolves.toBe(false);
    await expect(openExternal("")).resolves.toBe(false);
    expect(openURL).not.toHaveBeenCalled();
    expect(open).not.toHaveBeenCalled();
  });

  it("prefers the system browser over the in-app one", async () => {
    // The in-app browser resolved without presenting anything for a
    // private-network http address; UIApplication.open reports the system's answer.
    const openURL = vi.fn().mockResolvedValue(undefined);
    const inApp = vi.fn().mockResolvedValue(undefined);
    held.native = { openURL };
    held.browser = { open: inApp };
    await expect(openExternal("http://192.168.20.3:65497")).resolves.toBe(true);
    expect(openURL).toHaveBeenCalledWith({ url: "http://192.168.20.3:65497" });
    expect(inApp).not.toHaveBeenCalled();
  });

  it("falls through to the in-app browser when the system one rejects", async () => {
    held.native = { openURL: vi.fn().mockRejectedValue(new Error("declined")) };
    const inApp = vi.fn().mockResolvedValue(undefined);
    held.browser = { open: inApp };
    await expect(openExternal("https://example.com")).resolves.toBe(true);
    expect(inApp).toHaveBeenCalledWith({ url: "https://example.com" });
  });

  it("falls through to the window opener when both plugins fail", async () => {
    held.native = { openURL: vi.fn().mockRejectedValue(new Error("no")) };
    held.browser = { open: vi.fn().mockRejectedValue(new Error("no")) };
    const open = vi.fn();
    vi.stubGlobal("open", open);
    await expect(openExternal("https://example.com")).resolves.toBe(true);
    expect(open).toHaveBeenCalledWith("https://example.com", "_system", "noopener");
  });

  it("uses the window opener in a browser session with no plugins", async () => {
    const open = vi.fn();
    vi.stubGlobal("open", open);
    await expect(openExternal("http://127.0.0.1:8000")).resolves.toBe(true);
    expect(open).toHaveBeenCalledTimes(1);
  });

  it("tries _blank when the shell did not recognise _system", async () => {
    const open = vi.fn((_u: string, target?: string) => (target === "_system" ? null : {}));
    vi.stubGlobal("open", open);
    await openExternal("https://example.com");
    expect(open.mock.calls.map((c) => c[1])).toEqual(["_system", "_blank"]);
  });

  it("answers false when nothing can open it", async () => {
    vi.stubGlobal("open", undefined);
    await expect(openExternal("https://example.com")).resolves.toBe(false);
  });
});

describe("openOnPhone", () => {
  it("opens an http link natively and resolves", async () => {
    const openURL = vi.fn().mockResolvedValue(undefined);
    held.native = { openURL };
    await expect(openOnPhone("https://github.com/x/y/pull/1")).resolves.toBeUndefined();
    expect(openURL).toHaveBeenCalledTimes(1);
  });

  it("rejects a loopback link before opening anything", async () => {
    const openURL = vi.fn();
    held.native = { openURL };
    await expect(openOnPhone("http://localhost:3000")).rejects.toMatchObject({ reason: "loopback" });
    expect(openURL).not.toHaveBeenCalled();
  });

  it("rejects a non-http link", async () => {
    await expect(openOnPhone("file:///etc/passwd")).rejects.toMatchObject({ reason: "not_http" });
  });

  it("rejects when no opener worked", async () => {
    vi.stubGlobal("open", undefined);
    const err = await openOnPhone("https://example.com").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(OpenOnPhoneError);
    expect(err).toMatchObject({ reason: "failed" });
  });
});
