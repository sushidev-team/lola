import { expect, it } from "vitest";
import { MAX_PREVIEW_TABS, VIEWPORTS, devPageURL, frameStyle, parsePick, preview } from "./preview.svelte";

it("takes only a pick message from the picker", () => {
  const ok = { source: "lola-preview", type: "pick", selector: "main > a", url: "http://127.0.0.1:5/x", html: "<a>", text: "go", tag: "a" };
  expect(parsePick(ok)).toMatchObject({ selector: "main > a", tag: "a" });
  expect(parsePick({ ...ok, source: "other" })).toBeNull();
  expect(parsePick({ ...ok, type: "ready" })).toBeNull();
  expect(parsePick({ ...ok, selector: "  " })).toBeNull();
  expect(parsePick("pick")).toBeNull();
  expect(parsePick(null)).toBeNull();
});

it("clips what a page claims", () => {
  const p = parsePick({ source: "lola-preview", type: "pick", selector: "a", html: "x".repeat(10000), text: "y".repeat(1000) });
  expect(p?.html.length).toBe(6000);
  expect(p?.text.length).toBe(400);
});

it("sizes the frame by preset", () => {
  expect(frameStyle(VIEWPORTS.find((v) => v.id === "mobile")!)).toBe("width:390px;height:844px");
  expect(frameStyle(VIEWPORTS.find((v) => v.id === "desktop")!)).toBe("width:100%;height:100%");
});

it("names the page on the dev server's origin, not the proxy's", () => {
  expect(devPageURL("http://127.0.0.1:60001/a/b?c=1#/settings", "http://localhost:5173/")).toBe("http://localhost:5173/a/b?c=1#/settings");
  expect(devPageURL("garbage", "http://localhost:5173/")).toBe("http://localhost:5173/");
});

it("opens pages as tabs, deduped and capped, and switches between them", () => {
  const pages = Array.from({ length: MAX_PREVIEW_TABS + 3 }, (_, i) => ({ url: `http://localhost:3000/p${i}`, title: `P${i}` }));
  preview.openTabs("s", [pages[0], ...pages]);
  expect(preview.tabs).toHaveLength(MAX_PREVIEW_TABS);
  expect(preview.request).toEqual({ sessionId: "s", url: pages[0].url });
  preview.select(2);
  expect(preview.request?.url).toBe(pages[2].url);
  preview.select(99);
  expect(preview.active).toBe(2);
  // Manual tabs may share a URL and are independent of the bulk-open limit.
  preview.open("s", pages[2].url);
  expect(preview.tabs).toHaveLength(MAX_PREVIEW_TABS + 1);
  expect(preview.tabs.at(-1)!.id).not.toBe(preview.tabs[2].id);
  preview.closeTab(0);
  expect(preview.active).toBe(MAX_PREVIEW_TABS - 1);
  preview.closeTab(preview.active);
  expect(preview.request?.url).toBe(pages[MAX_PREVIEW_TABS - 1].url);
  preview.openTabs("s", [pages[0]]);
  expect(preview.tabs).toHaveLength(1);
  preview.closeTab(0);
  expect(preview.request).toBeNull();
});

it("keeps one address twice when it is prepared for two different areas, and carries the preparation", () => {
  const home = { url: "http://localhost:3000/", title: "Top", prep: { marks: [{ selector: "header" }] } };
  const footer = { url: "http://localhost:3000/", title: "Bottom", prep: { marks: [{ selector: "footer" }] }, caption: "Check the footer" };
  preview.openTabs("s", [home, footer, { ...footer }]);
  expect(preview.tabs.map((t) => t.title)).toEqual(["Top", "Bottom"]);
  expect(preview.request).toEqual({ sessionId: "s", url: home.url, prep: home.prep });
  preview.select(1);
  expect(preview.request).toEqual({ sessionId: "s", url: footer.url, prep: footer.prep, caption: "Check the footer" });
  preview.close();
  preview.open("s", "http://localhost:3000/x", { prep: { device: "mobile" } });
  expect(preview.request?.prep?.device).toBe("mobile");
  preview.open("s", "http://localhost:3000/y", { prep: {} });
  expect(preview.request?.prep).toBeUndefined();
  preview.close();
});
