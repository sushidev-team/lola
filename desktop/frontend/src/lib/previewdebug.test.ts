import { expect, it } from "vitest";
import type { DebugCommand, DebugSnapshot } from "./previewtools";

const nodeFs = "node:" + "fs";
const { readFileSync } = await import(/* @vite-ignore */ nodeFs);
const root = (globalThis as unknown as { process: { cwd(): string } }).process.cwd();
const source = readFileSync(root + "/../../internal/preview/debug.js", "utf8");

it("opens body once and excludes preview overlays from the root rows and count", () => {
  const frame = document.createElement("iframe");
  document.body.appendChild(frame);
  const win = frame.contentWindow as Window & typeof globalThis;
  const doc = win.document;
  // The native preview can install the debugger before the parser reaches body.
  doc.body.remove();
  const run = win.Function(source + "\nreturn lolaPreviewDebugger();")() as (cmd: DebugCommand) => DebugSnapshot;
  try {
    run({ op: "read", elements: true });
    const body = doc.createElement("body");
    body.innerHTML = '<div id="app"></div>';
    doc.documentElement.appendChild(body);
    for (const attr of ["data-lola-preview", "data-lola-prepare"]) {
      const overlay = doc.createElement("div");
      overlay.setAttribute(attr, "");
      doc.documentElement.appendChild(overlay);
    }
    doc.documentElement.appendChild(doc.createElement("div"));

    const { tree, documentKey } = run({ op: "read", elements: true });
    expect(tree?.map(row => [row.name, row.depth])).toEqual([
      ["html", 0], ["head", 1], ["body", 1], ["div", 2], ["div", 1],
    ]);
    expect(tree![0].count).toBe(3);
    expect(tree![2].expanded).toBe(true);
    expect(tree![3].attrs).toEqual([["id", "app"]]);

    run({ op: "branch", documentKey, elements: true, index: tree![2].id, on: false });
    expect(run({ op: "read", elements: true }).tree?.find(row => row.name === "body")?.expanded).toBe(false);
  } finally {
    run({ op: "close" });
    frame.remove();
  }
});

it("renames an inline property in one edit and preserves the original when the edit fails", () => {
  const frame = document.createElement("iframe");
  document.body.appendChild(frame);
  const win = frame.contentWindow as Window & typeof globalThis;
  const style = win.document.documentElement.style;
  style.cssText = "visibility: hidden; opacity: 1";
  Object.defineProperty(win, "CSS", { configurable: true, value: {
    supports: (name: string, value: string) => name === "overflow" && value === "hidden",
  } });
  const run = win.Function(source + "\nreturn lolaPreviewDebugger();")() as (cmd: DebugCommand) => DebugSnapshot;
  try {
    const { documentKey, tree } = run({ op: "read", elements: true });
    const edit = { op: "style", index: tree!.find(row => row.selected)!.id, documentKey, originalName: "visibility" };
    expect(run({ ...edit, name: "opacity", value: "1" }).editError).toMatch(/already exists/);
    expect(run({ ...edit, name: "overflow", value: "bogus" }).editError).toMatch(/valid CSS/);
    expect(style.visibility).toBe("hidden");
    expect(run({ ...edit, name: "overflow", value: "hidden !important" }).editError).toBeUndefined();
    expect(style.visibility).toBe("");
    expect(style.overflow).toBe("hidden");
    expect(style.getPropertyPriority("overflow")).toBe("important");
    expect(style.opacity).toBe("1");
  } finally {
    run({ op: "close" });
    frame.remove();
  }
});

it("writes to the chosen live stylesheet rule and rejects a replacement with the same selector", () => {
  const frame = document.createElement("iframe");
  document.body.appendChild(frame);
  const win = frame.contentWindow as Window & typeof globalThis;
  const sheet = win.document.createElement("style");
  sheet.textContent = "html { opacity: .2 } html { opacity: .6 }";
  win.document.head.appendChild(sheet);
  Object.defineProperty(win, "CSS", { configurable: true, value: { supports: () => true } });
  const run = win.Function(source + "\nreturn lolaPreviewDebugger();")() as (cmd: DebugCommand) => DebugSnapshot;
  try {
    const { documentKey, tree, element } = run({ op: "read", elements: true, styles: true });
    const [last, first] = element!.rules;
    expect(last.id).not.toBe(first.id);
    const edit = { op: "ruleStyle", documentKey, index: tree!.find(row => row.selected)!.id,
      elements: true, styles: true, ruleId: first.id, name: "opacity", value: "1 !important" };
    const current = run(edit);
    expect(current.editError).toBeUndefined();
    expect(current.element!.rules.find(rule => rule.id === first.id)!.declarations).toEqual([["opacity", "1 !important"]]);
    expect(current.element!.rules[0]).toEqual(last);
    expect(win.document.documentElement.style.length).toBe(0);
    const css = sheet.sheet!;
    css.deleteRule(0);
    css.insertRule("html { opacity: .8 }", 0);
    expect(run(edit).editError).toMatch(/rule changed/);
    expect((css.cssRules[0] as CSSStyleRule).style.getPropertyValue("opacity")).toBe(".8");
    expect(win.document.documentElement.style.length).toBe(0);
  } finally {
    run({ op: "close" });
    frame.remove();
  }
});

it("reveals hydrated app content alongside its body siblings and preserves a manual collapse", async () => {
  const frame = document.createElement("iframe");
  document.body.appendChild(frame);
  const win = frame.contentWindow as Window & typeof globalThis;
  const doc = win.document;
  doc.body.innerHTML = '<div id="app"></div><script></script><span id="measurement"></span><iframe></iframe>';
  const run = win.Function(source + "\nreturn lolaPreviewDebugger();")() as (cmd: DebugCommand) => DebugSnapshot;
  try {
    const { documentKey, treeVersion } = run({ op: "read", elements: true });
    doc.getElementById("app")!.innerHTML = "<div><main><section><h1>Order</h1></section></main></div>";
    await Promise.resolve();
    const cmd = { op: "read", elements: true, documentKey };
    const rows = run({ ...cmd, treeVersion }).tree!;
    expect(rows.map(row => row.name)).toEqual(["html", "head", "body", "div", "div", "main", "section", "h1", "script", "span", "iframe"]);
    const app = rows.find(row => row.attrs.some(([key, value]) => key === "id" && value === "app"))!;
    run({ ...cmd, op: "branch", index: app.id, on: false });
    expect(run(cmd).tree!.some(row => row.name === "main")).toBe(false);
  } finally { run({ op: "close" }); frame.remove(); }
});

it("keeps later siblings reachable when expanded branches fill the snapshot", () => {
  const frame = document.createElement("iframe");
  document.body.appendChild(frame);
  const win = frame.contentWindow as Window & typeof globalThis;
  const doc = win.document;
  doc.body.innerHTML = "<main>" + ("<section>" + "<i></i>".repeat(25) + "</section>").repeat(22) + "</main><footer></footer>";
  const run = win.Function(source + "\nreturn lolaPreviewDebugger();")() as (cmd: DebugCommand) => DebugSnapshot;
  try {
    const initial = run({ op: "read", elements: true });
    const cmd = { elements: true, documentKey: initial.documentKey, op: "branch", on: true };
    const groups = initial.tree!.filter(row => row.name === "section");
    expect(groups).toHaveLength(22);
    let current = initial;
    for (const group of groups) {
      const row = current.tree!.find(node => node.id === group.id && node.offset === undefined)!;
      if (!row.limited) current = run({ ...cmd, index: row.id });
    }
    expect(current.tree!.length).toBeLessThanOrEqual(400);
    expect(current.tree!.at(-1)?.name).toBe("footer");
    const last = current.tree!.find(row => row.id === groups.at(-1)!.id)!;
    expect(last.limited).toBe(true);
    const scoped = run({ ...cmd, index: last.id, scoped: true });
    expect(scoped.treeScoped).toBe(true);
    expect(scoped.tree!.filter(row => row.name === "i")).toHaveLength(24);
    const next = scoped.tree!.at(-1)!;
    const paged = run({ ...cmd, index: next.id, offset: next.offset });
    expect(paged.tree!.filter(row => row.name === "i")).toHaveLength(1);
    const back = run({ ...cmd, op: "treeBack" });
    expect(back.treeScoped).toBe(false);
    expect(back.tree!.find(row => row.selected)?.id).toBe(last.id);
  } finally { run({ op: "close" }); frame.remove(); }
});

it("restores disabled inline declarations in order and allows deleting a disabled declaration", () => {
  const frame = document.createElement("iframe");
  document.body.appendChild(frame);
  const win = frame.contentWindow as Window & typeof globalThis;
  const style = win.document.documentElement.style;
  style.cssText = "display: block; color: red !important; opacity: .5";
  const open = win.Function(source + "\nreturn lolaPreviewDebugger();") as () => (cmd: DebugCommand) => DebugSnapshot;
  let run = open();
  function target() {
    const { documentKey, tree } = run({ op: "read", elements: true });
    return { op: "toggleStyle", index: tree!.find(row => row.selected)!.id, documentKey, elements: true, value: "", on: false };
  }
  try {
    let cmd = target();
    run({ ...cmd, name: "color" });
    run({ ...cmd, name: "display" });
    expect(style.color).toBe("");
    run({ op: "close" });
    run = open();
    cmd = target();
    expect(run({ ...cmd, op: "read" }).element!.inline.slice(0, 2)).toEqual([
      ["display", "block", "disabled"], ["color", "red !important", "disabled"],
    ]);
    run({ ...cmd, name: "color", on: true });
    run({ ...cmd, name: "display", on: true });
    expect(Array.from(style)).toEqual(["display", "color", "opacity"]);
    expect(style.getPropertyPriority("color")).toBe("important");
    run({ ...cmd, name: "color" });
    const removed = run({ ...cmd, op: "style", name: "color", originalName: "color" });
    expect(removed.element!.inline.map(([name]) => name)).toEqual(["display", "opacity"]);
  } finally { run({ op: "close" }); frame.remove(); }
});
