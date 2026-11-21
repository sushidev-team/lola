import { beforeEach, expect, it, vi } from "vitest";
// The script the embedded preview and the screenshot browser both run.
// Read from disk: it lives outside the frontend root, and the package carries no Node types.
const nodeFs = "node:" + "fs";
const { readFileSync } = await import(/* @vite-ignore */ nodeFs);
const root = (globalThis as unknown as { process: { cwd(): string } }).process.cwd();
const source: string = readFileSync(root + "/../../internal/preview/prepare.js", "utf8");
type Step = { do: string; selector?: string; text?: string; value?: string; key?: string; path?: string; ms?: number };
type Spec = { waitFor?: string; scrollY?: number; marks?: { selector?: string; text?: string; label?: string }[]; steps?: Step[]; from?: number; stepTimeoutMs?: number };
type Result = { superseded?: boolean; found: boolean[]; waited: boolean; failed: null | { index: number; step: string; why: string } };
const run = (spec: Spec, timeout?: number, onStep?: (i: number) => void) =>
  (window as unknown as { __lolaPrepare: (s: Spec, t?: number, o?: (i: number) => void) => Promise<Result> }).__lolaPrepare(spec, timeout, onStep);

beforeEach(() => {
  document.getElementById("lola-prepare-layer")?.remove();
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
  expect(r).toEqual({ found: [true], waited: false, failed: null });
  const never = await run({ waitFor: "#never" }, 300);
  expect(never.waited).toBe(true);
});

// Mirrors localHostCases in internal/reviewpack/store_test.go: the Go rule and
// this script's rule must agree.
const localHostCases: Record<string, boolean> = {
  "localhost": true, "app.localhost": true, "shop.test": true, "SHOP.TEST": true, "127.0.0.1": true, "[::1]": true,
  "10.1.2.3": true, "172.16.0.9": true, "172.31.255.1": true, "192.168.1.20": true, "169.254.1.1": true, "fd12::1": true,
  "example.com": false, "shop.test.evil.com": false, "evil-test": false, "8.8.8.8": false, "172.32.0.1": false,
  "192.169.1.1": false, "11.0.0.1": false, "localhost.evil.com": false, "": false, "intranet": false, "app.local": false,
};

it("agrees with the Go rule on which hosts steps may run against", () => {
  const f = (window as unknown as { __lolaLocalHost: (h: string) => boolean }).__lolaLocalHost;
  for (const [host, want] of Object.entries(localHostCases)) expect(f(host), host).toBe(want);
});

const LOGIN = `
  <form id="login"><label for="e">Email</label><input id="e" name="email"><input type="password" placeholder="Your password">
  <select id="role"><option value="a">Admin</option><option value="m">Member</option></select>
  <button type="submit">Sign in</button></form><div id="app"></div>`;

function wireLogin() {
  document.body.innerHTML = LOGIN;
  const submitted: Record<string, string>[] = [];
  document.getElementById("login")!.addEventListener("submit", (e) => {
    e.preventDefault();
    const els = (e.target as HTMLFormElement).elements;
    submitted.push({ email: (els[0] as HTMLInputElement).value, pw: (els[1] as HTMLInputElement).value, role: (els[2] as HTMLSelectElement).value });
    setTimeout(() => (document.getElementById("app")!.innerHTML = `<h2 class="dashboard">Dashboard</h2>`), 50);
  });
  return submitted;
}

it("logs in: fills by selector and by label or placeholder, picks an option, clicks, waits, then marks", async () => {
  const submitted = wireLogin();
  const seen: number[] = [];
  const r = await run({
    steps: [
      { do: "fill", selector: "#e", value: "dev@shop.test" },
      { do: "fill", text: "your password", value: "dev-only" },
      { do: "fill", text: "role", selector: "#role", value: "member" },
      { do: "click", text: "Sign in" },
      { do: "wait", selector: ".dashboard" },
    ],
    marks: [{ selector: ".dashboard", label: "Landed" }],
  }, 2000, (i) => seen.push(i));
  expect(r.failed).toBeNull();
  expect(r.found).toEqual([true]);
  expect(submitted).toEqual([{ email: "dev@shop.test", pw: "dev-only", role: "m" }]);
  expect(seen).toEqual([0, 1, 2, 3, 4]);
});

it("finds a field by its label text and presses Enter to submit", async () => {
  const submitted = wireLogin();
  const r = await run({ steps: [{ do: "fill", text: "Email", value: "a@b.test" }, { do: "press", selector: "#e", key: "Enter" }] });
  expect(r.failed).toBeNull();
  expect(submitted).toHaveLength(1);
  expect(submitted[0].email).toBe("a@b.test");
});

it("clicks something that opens a modal, resuming from spec.from", async () => {
  document.body.innerHTML = `<button id="a">Skip me</button><button id="open">Open settings</button>`;
  let skipped = 0;
  document.getElementById("a")!.addEventListener("click", () => skipped++);
  document.getElementById("open")!.addEventListener("click", () => document.body.insertAdjacentHTML("beforeend", `<div role="dialog" id="modal">Settings</div>`));
  const r = await run({ steps: [{ do: "click", selector: "#a" }, { do: "click", text: "open settings" }], from: 1, marks: [{ selector: "#modal" }] }, 1000);
  expect(r.failed).toBeNull();
  expect(skipped).toBe(0);
  expect(r.found).toEqual([true]);
});

it("reports the step that did not work and draws nothing after it", async () => {
  wireLogin();
  const r = await run({ steps: [{ do: "fill", selector: "#e", value: "x" }, { do: "click", text: "No such button" }, { do: "fill", selector: "#e", value: "never" }], marks: [{ selector: "#login" }], stepTimeoutMs: 200 });
  expect(r.failed).toMatchObject({ index: 1, why: "nothing to click was found" });
  expect(r.found).toEqual([false]);
  expect((document.getElementById("e") as HTMLInputElement).value).toBe("x");
  expect(document.getElementById("lola-prepare-layer")).toBeNull();
});

it("refuses unsafe goto paths and unknown steps instead of acting", async () => {
  document.body.innerHTML = "<p>hi</p>";
  for (const path of ["//evil.com/x", "https://evil.com/", "/a\\b", "relative"]) {
    const r = await run({ steps: [{ do: "goto", path }] });
    expect(r.failed?.why, path).toBe("not a path on this origin");
  }
  expect((await run({ steps: [{ do: "eval", text: "alert(1)" }] })).failed?.why).toBe("unknown step");
});

it("lets only the newest run act: a second call supersedes the first", async () => {
  document.body.innerHTML = `<input id="e">`;
  const first = run({ steps: [{ do: "wait", ms: 400 }, { do: "fill", selector: "#e", value: "old" }] });
  const second = run({ steps: [{ do: "fill", selector: "#e", value: "new" }] });
  expect((await second).failed).toBeNull();
  expect((await first).superseded).toBe(true);
  expect((await first).failed).toBeNull();
  expect((document.getElementById("e") as HTMLInputElement).value).toBe("new");
});

it("turns a throwing or impossible step into that step's failure instead of hanging", async () => {
  document.body.innerHTML = `<input id="f" type="file"><div id="wrap"></div>`;
  const file = await run({ steps: [{ do: "fill", selector: "#f", value: "x" }] });
  expect(file.failed).toMatchObject({ index: 0, why: "that field does not take this value" });
  const div = await run({ steps: [{ do: "fill", selector: "#wrap", value: "x" }] });
  expect(div.failed).toMatchObject({ index: 0 });
});

it("reports a step once it has had its effect, so a step still looking is run again", async () => {
  document.body.innerHTML = `<button id="b">Go</button>`;
  const seen: number[] = [];
  const r = await run({ steps: [{ do: "click", selector: "#b" }, { do: "fill", selector: "#late", value: "x" }], stepTimeoutMs: 200 }, 300, (i) => seen.push(i));
  expect(r.failed).toMatchObject({ index: 1 });
  expect(seen).toEqual([0]);
});

it("checks the marks again on demand", async () => {
  document.body.innerHTML = `<p id="a">x</p>`;
  const check = (window as unknown as { __lolaCheck: (s: Spec) => boolean[] }).__lolaCheck;
  expect(check({ marks: [{ selector: "#a" }, { selector: "#gone" }] })).toEqual([true, false]);
});
