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
  expect(devPageURL("http://127.0.0.1:60001/a/b?c=1", "http://localhost:5173/")).toBe("http://localhost:5173/a/b?c=1");
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
  // An address chip re-points the active tab and keeps the set.
  preview.open("s", "http://localhost:4000/");
  expect(preview.tabs).toHaveLength(MAX_PREVIEW_TABS);
  expect(preview.tabs[2].url).toBe("http://localhost:4000/");
  // A single page is not a tab set; close clears everything.
  preview.openTabs("s", [pages[0]]);
  expect(preview.tabs).toHaveLength(0);
  preview.close();
  expect(preview.request).toBeNull();
});
