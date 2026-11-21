import { beforeEach, expect, it, vi } from "vitest";
// The script the embedded preview and the screenshot browser both run.
// Read from disk: it lives outside the frontend root, and the package carries no Node types.
const nodeFs = "node:" + "fs";
const { readFileSync } = await import(/* @vite-ignore */ nodeFs);
const root = (globalThis as unknown as { process: { cwd(): string } }).process.cwd();
const source: string = readFileSync(root + "/../../internal/preview/prepare.js", "utf8");
type Spec = { waitFor?: string; scrollY?: number; marks?: { selector?: string; text?: string; label?: string }[] };
const run = (spec: Spec, timeout?: number) =>
  (window as unknown as { __lolaPrepare: (s: Spec, t?: number) => Promise<{ found: boolean[]; waited: boolean }> }).__lolaPrepare(spec, timeout);

beforeEach(() => {
  delete (window as unknown as Record<string, unknown>).__lolaPrepare;
  new Function(source)();
  document.body.innerHTML = `<main><h1>Title</h1><section id="s"><p>Intro</p><button class="b">Save changes</button></section><script>var secret = "Save changes"</script></main>`;
  Element.prototype.scrollIntoView = vi.fn();
  window.scrollTo = vi.fn() as never;
});

it("scrolls the first found mark to the middle and outlines every found one with its label", async () => {
  const r = await run({ marks: [{ selector: "#nope" }, { selector: "#s", label: "Section" }, { text: "save changes", label: "Save" }] }, 300);
  expect(r.found).toEqual([false, true, true]);
  expect(r.waited).toBe(true);
  expect((Element.prototype.scrollIntoView as ReturnType<typeof vi.fn>).mock.contexts[0]).toBe(document.getElementById("s"));
  expect(Element.prototype.scrollIntoView).toHaveBeenCalledWith(expect.objectContaining({ block: "center" }));
  const layer = document.getElementById("lola-prepare-layer")!;
  expect([...layer.children].map((c) => c.textContent).filter(Boolean)).toEqual(["Section", "Save"]);
  expect(layer.style.pointerEvents).toBe("none");
});

it("finds an element by its visible text, the deepest one, never script text", async () => {
  const r = await run({ marks: [{ text: "Save changes" }] });
  expect(r.found).toEqual([true]);
  expect((Element.prototype.scrollIntoView as ReturnType<typeof vi.fn>).mock.contexts[0]).toBe(document.querySelector(".b"));
});

it("falls back to scrollY, tolerates a bad selector, and draws a label as text, not HTML", async () => {
  const bad = await run({ scrollY: 250, marks: [{ selector: "[[[" }] }, 100);
  expect(bad.found).toEqual([false]);
  expect(window.scrollTo).toHaveBeenCalledWith(0, 250);
  await run({ marks: [{ selector: "#s", label: "<img src=x onerror=alert(1)>" }] });
  const layer = document.getElementById("lola-prepare-layer")!;
  expect(layer.querySelector("img")).toBeNull();
  expect(layer.textContent).toContain("<img");
});

it("waits for the selector the agent named, and reports when it never came", async () => {
  setTimeout(() => document.body.insertAdjacentHTML("beforeend", `<div id="late"></div>`), 300);
  const r = await run({ waitFor: "#late", marks: [{ selector: "#late", label: "Late" }] }, 3000);
  expect(r).toEqual({ found: [true], waited: false });
  const never = await run({ waitFor: "#never" }, 300);
  expect(never.waited).toBe(true);
});
