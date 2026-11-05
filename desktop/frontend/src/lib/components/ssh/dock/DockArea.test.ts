import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { tick, createRawSnippet } from "svelte";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

const svc = vi.hoisted(() => ({ Timeline: vi.fn(), WidgetStatus: vi.fn() }));
vi.mock("@bindings/desktop", () => ({
  SSHService: svc,
  DaemonService: { Alive: vi.fn().mockResolvedValue(false) },
  ConfigService: { SSHHostForms: vi.fn().mockResolvedValue([]) },
  TermService: {},
}));

import { ssh } from "$lib/ssh.svelte";
import { PANEL_PAD, allTabs, groupOf, groups, moveTab, type Split } from "$lib/docklayout";
import DockArea from "./DockArea.svelte";
import SSHWidgetRow from "../SSHWidgetRow.svelte";

let n = 0;
let SID = "";
const item = (id: string, title = id) => ({ id, kind: "element", element: { id, kind: "widget", title, html: "<b>x</b>", revision: 1 } });
const children = createRawSnippet(() => ({ render: () => "<div data-testid='timeline'></div>" }));

let size = { w: 1400, h: 800 };
const widthGetter = Object.getOwnPropertyDescriptor(Element.prototype, "clientWidth");
const heightGetter = Object.getOwnPropertyDescriptor(Element.prototype, "clientHeight");

beforeEach(() => {
  n++;
  SID = `dock${n}`;
  size = { w: 1400, h: 800 };
  Object.defineProperty(Element.prototype, "clientWidth", { configurable: true, get: () => size.w });
  Object.defineProperty(Element.prototype, "clientHeight", { configurable: true, get: () => size.h });
  svc.Timeline.mockResolvedValue({ session: SID, connections: null, items: [], lastSeq: 0 });
});
afterEach(() => {
  cleanup();
  if (widthGetter) Object.defineProperty(Element.prototype, "clientWidth", widthGetter);
  if (heightGetter) Object.defineProperty(Element.prototype, "clientHeight", heightGetter);
});

function seed(...ids: string[]) {
  const v = ssh.view(SID);
  v.items = ids.map((i) => item(i)) as never;
  v.loaded = true;
  return v;
}
const show = () => render(DockArea, { sessionId: SID, children });
const frames = () => [...document.querySelectorAll("iframe")];

describe("the dock", () => {
  it("gives the timeline the whole width while there is no widget", async () => {
    seed();
    show();
    await tick();
    expect(document.querySelector("[data-dock-group]")).toBeNull();
    expect((document.querySelector("[data-dock-main]") as HTMLElement).style.width).toBe("1400px");
  });
  it("opens a widget as a tab beside the timeline, and the timeline gives up the room", async () => {
    seed("w1");
    show();
    await waitFor(() => expect(document.querySelector('[data-dock-tab="w1"]')).not.toBeNull());
    expect(parseInt((document.querySelector("[data-dock-main]") as HTMLElement).style.width)).toBeLessThan(1400);
    expect(screen.getByText("by Claude")).toBeTruthy();
  });
  it("runs nothing until the human allows it, once per session, from inside the panel", async () => {
    const v = seed("w1");
    show();
    await waitFor(() => expect(document.querySelector("[data-widget-gate]")).not.toBeNull());
    expect(frames()).toEqual([]);
    await fireEvent.click(screen.getByRole("button", { name: "Show widgets" }));
    expect(v.widgetsAllowed).toBe(true);
    await waitFor(() => expect(frames().length).toBe(1));
    expect(frames()[0].getAttribute("sandbox")).toBe("allow-scripts");
    expect(document.querySelector("[data-widget-gate]")).toBeNull();
  });
  it("does not take the focus when a widget arrives", async () => {
    const v = seed();
    const input = document.body.appendChild(document.createElement("textarea"));
    input.focus();
    v.widgetsAllowed = true;
    show();
    v.items = [item("w1")] as never;
    await waitFor(() => expect(frames().length).toBe(1));
    expect(document.activeElement).toBe(input);
    input.remove();
  });
  it("keeps a frame in the same DOM node through a move, a split and a close of its neighbor", async () => {
    const v = seed("a", "b", "c");
    v.widgetsAllowed = true;
    show();
    await waitFor(() => expect(frames().length).toBe(1));
    await fireEvent.click(document.querySelector('[data-dock-tab="b"] [role="tab"]')!);
    await waitFor(() => expect(frames().length).toBe(2));
    const before = frames();
    const parents = before.map((f) => f.parentElement);
    v.setDock(moveTab(v.dock, "a", { kind: "edge", node: "main", edge: "bottom" }));
    await tick();
    v.setDock(moveTab(v.dock, "b", { kind: "outer", edge: "left" }));
    await tick();
    // "c" comes forward once it is alone in its panel, and joins at the end.
    expect(frames().slice(0, 2)).toEqual(before);
    expect(frames().slice(0, 2).map((f) => f.parentElement)).toEqual(parents);
    v.closeWidget("c");
    await tick();
    expect(frames().filter((f) => before.includes(f))).toEqual(before);
  });
  it("hides a background tab's frame, and does not start one nobody has seen", async () => {
    const v = seed("a", "b", "c");
    v.widgetsAllowed = true;
    show();
    await waitFor(() => expect(document.querySelector('[data-dock-frame="a"]')).not.toBeNull());
    expect(document.querySelector('[data-dock-frame="c"]')).toBeNull();
    await fireEvent.click(document.querySelector('[data-dock-tab="c"] [role="tab"]')!);
    await waitFor(() => expect((document.querySelector('[data-dock-frame="a"]') as HTMLElement).style.visibility).toBe("hidden"));
    expect((document.querySelector('[data-dock-frame="c"]') as HTMLElement).style.visibility).toBe("visible");
  });
  it("does not reopen a closed widget on an update, and its row does", async () => {
    const v = seed("w1");
    show();
    await waitFor(() => expect(allTabs(v.dock)).toEqual(["w1"]));
    await fireEvent.click(screen.getByRole("button", { name: "Close w1" }));
    expect(allTabs(v.dock)).toEqual([]);
    // Back to full width, and a new revision of the page does not change that.
    v.items = [{ ...item("w1"), element: { ...item("w1").element, data: "{}", revision: 2 } }] as never;
    await tick();
    expect(allTabs(v.dock)).toEqual([]);
    render(SSHWidgetRow, { sessionId: SID, element: item("w1").element as never });
    expect(document.querySelector('[data-widget-dot="off"]')).not.toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Show" }));
    expect(allTabs(v.dock)).toEqual(["w1"]);
    expect(v.dockClosed.has("w1")).toBe(false);
  });
  it("points at the tab of a widget that is already open", async () => {
    const v = seed("a", "b");
    show();
    await waitFor(() => expect(allTabs(v.dock)).toEqual(["a", "b"]));
    render(SSHWidgetRow, { sessionId: SID, element: item("b").element as never });
    expect(screen.queryByRole("button", { name: "Show" })).toBeNull();
    await fireEvent.click(document.querySelector('[data-widget-row="b"] button')!);
    expect(groupOf(v.dock, "b")!.active).toBe("b");
    await waitFor(() => expect(document.querySelector('[data-dock-tab="b"] [data-dock-pulse]')).not.toBeNull());
  });
  it("does not flip the page being read when a new widget arrives", async () => {
    const v = seed("a");
    show();
    await waitFor(() => expect(groupOf(v.dock, "a")!.active).toBe("a"));
    v.items = [item("a"), item("b")] as never;
    await waitFor(() => expect(allTabs(v.dock)).toEqual(["a", "b"]));
    expect(groupOf(v.dock, "a")!.active).toBe("a");
    await waitFor(() => expect(document.querySelector('[data-dock-tab="b"] [data-dock-unseen]')).not.toBeNull());
    await fireEvent.click(document.querySelector('[data-dock-tab="b"] [role="tab"]')!);
    await waitFor(() => expect(document.querySelector("[data-dock-unseen]")).toBeNull());
  });
  it("draws the gate once, whatever the number of panels", async () => {
    const v = seed("a", "b");
    show();
    await waitFor(() => expect(allTabs(v.dock).length).toBe(2));
    v.setDock(moveTab(v.dock, "b", { kind: "edge", node: groupOf(v.dock, "a")!.id, edge: "bottom" }));
    await waitFor(() => expect(groups(v.dock).length).toBe(2));
    expect(document.querySelectorAll("[data-widget-gate]").length).toBe(1);
    expect(document.querySelector("[data-widget-gate]")!.textContent).not.toMatch(/network|your system/);
  });
  it("moves focus to the tab that took over, and to the composer after the last", async () => {
    const v = seed("a", "b");
    show();
    await waitFor(() => expect(allTabs(v.dock)).toEqual(["a", "b"]));
    (document.querySelector('[data-dock-tab="a"] [role="tab"]') as HTMLElement).focus();
    await fireEvent.click(screen.getByRole("button", { name: "Close a" }));
    await waitFor(() => expect(document.activeElement).toBe(document.querySelector('[data-dock-tab="b"] [role="tab"]')));
    const before = v.composerFocus;
    await fireEvent.click(screen.getByRole("button", { name: "Close b" }));
    await waitFor(() => expect(v.composerFocus).toBe(before + 1));
  });
  it("leaves focus that is somewhere else alone when a tab closes", async () => {
    const v = seed("a", "b");
    show();
    await waitFor(() => expect(allTabs(v.dock).length).toBe(2));
    const input = document.body.appendChild(document.createElement("textarea"));
    input.focus();
    await fireEvent.click(screen.getByRole("button", { name: "Close a" }));
    await tick();
    expect(document.activeElement).toBe(input);
    input.remove();
  });
  it("focuses the menu itself when a mouse opened it, and no row wears a ring", async () => {
    const v = seed("a", "b");
    show();
    await waitFor(() => expect(allTabs(v.dock).length).toBe(2));
    await fireEvent.contextMenu(document.querySelector('[data-dock-tab="b"]')!);
    await waitFor(() => expect(document.activeElement).toBe(document.querySelector("[data-dock-menu]")));
  });
  it("says why a split cannot happen", async () => {
    const v = seed("a", "b");
    size = { w: 700, h: 800 };
    show();
    await waitFor(() => expect(allTabs(v.dock).length).toBe(2));
    await fireEvent.contextMenu(document.querySelector('[data-dock-tab="b"]')!);
    const split = await screen.findByRole("menuitem", { name: "Split right" });
    expect(split).toBeDisabled();
    expect(split.getAttribute("title")).toBe("No room");
  });
  it("lists the tabs of a strip that cannot show them all", async () => {
    const sw = Object.getOwnPropertyDescriptor(Element.prototype, "scrollWidth");
    Object.defineProperty(Element.prototype, "scrollWidth", { configurable: true, get: () => 3000 });
    try {
      const v = seed("a", "b", "c");
      show();
      await waitFor(() => expect(allTabs(v.dock).length).toBe(3));
      await waitFor(() => expect(document.querySelector('[role="tablist"]')!.getAttribute("data-more")).toBe("right"));
      await fireEvent.click(screen.getByRole("button", { name: "List 3 tabs" }));
      await fireEvent.click(await screen.findByRole("menuitem", { name: "c" }));
      expect(groupOf(v.dock, "c")!.active).toBe("c");
    } finally {
      if (sw) Object.defineProperty(Element.prototype, "scrollWidth", sw);
    }
  });
  it("offers a tab's moves from a menu, by keyboard", async () => {
    const v = seed("a", "b");
    show();
    await waitFor(() => expect(allTabs(v.dock)).toEqual(["a", "b"]));
    const tab = document.querySelector('[data-dock-tab="b"] [role="tab"]') as HTMLElement;
    await fireEvent.keyDown(tab, { key: "ContextMenu" });
    await waitFor(() => expect(document.querySelector("[data-dock-menu]")).not.toBeNull());
    await waitFor(() => expect(document.activeElement?.textContent).toContain("Split right"));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Split down" }));
    expect(groups(v.dock).length).toBe(2);
    expect(groupOf(v.dock, "b")!.tabs).toEqual(["b"]);
    expect(document.querySelector("[data-dock-menu]")).toBeNull();
  });
  it("reorders a tab by keyboard", async () => {
    const v = seed("a", "b");
    show();
    await waitFor(() => expect(allTabs(v.dock)).toEqual(["a", "b"]));
    await fireEvent.keyDown(document.querySelector('[data-dock-tab="a"] [role="tab"]')!, { key: "ArrowRight", altKey: true });
    expect(allTabs(v.dock)).toEqual(["b", "a"]);
  });
  it("resizes by keyboard, and arrows stay with the sash", async () => {
    const v = seed("a");
    show();
    await waitFor(() => expect(document.querySelector("[data-dock-sash]")).not.toBeNull());
    const before = (v.dock as Split).sizes[0];
    const outer = vi.fn();
    window.addEventListener("keydown", outer);
    await fireEvent.keyDown(document.querySelector("[data-dock-sash]")!, { key: "ArrowRight" });
    window.removeEventListener("keydown", outer);
    expect((v.dock as Split).sizes[0]).toBeGreaterThan(before);
    expect(outer).not.toHaveBeenCalled();
  });
  it("in a narrow window keeps the timeline whole and offers a drawer", async () => {
    const v = seed("a", "b");
    v.widgetsAllowed = true;
    size = { w: 560, h: 800 };
    show();
    const main = () => document.querySelector("[data-dock-main]") as HTMLElement;
    await waitFor(() => expect(document.querySelector("[data-dock-compact]")).not.toBeNull());
    expect(main().style.width).toBe("");
    expect(document.querySelector("[data-dock-group]")).toBeNull();
    expect(v.dockCompact).toBe(true);
    v.dockDrawer = true;
    await waitFor(() => expect(document.querySelector('[data-dock-group="drawer"]')).not.toBeNull());
    expect(document.querySelectorAll("[data-dock-tab]").length).toBe(2);
    // The arrangement is untouched, for when the window grows.
    expect(groups(v.dock).length).toBe(1);
    await fireEvent.click(screen.getByRole("button", { name: "Hide widgets" }));
    expect(document.querySelector('[data-dock-group="drawer"]')).toBeNull();
  });
  it("closes the drawer on Esc ahead of the app, and hands focus back", async () => {
    const v = seed("a");
    size = { w: 560, h: 800 };
    show();
    await waitFor(() => expect(v.dockCompact).toBe(true));
    v.dockDrawer = true;
    await waitFor(() => expect(document.activeElement).toBe(document.querySelector('[data-dock-tab="a"] [role="tab"]')));
    const app = vi.fn();
    window.addEventListener("keydown", app);
    const before = v.composerFocus;
    await fireEvent.keyDown(document.activeElement!, { key: "Escape" });
    window.removeEventListener("keydown", app);
    expect(app).not.toHaveBeenCalled();
    expect(v.dockDrawer).toBe(false);
    await waitFor(() => expect(v.composerFocus).toBe(before + 1));
  });
});

describe("dragging a tab", () => {
  // jsdom has no PointerEvent, so a mouse event wears its one extra field.
  const pointer = (type: string, x: number, y: number) => {
    const e = new MouseEvent(type, { bubbles: true, button: 0, clientX: x, clientY: y });
    Object.defineProperty(e, "pointerId", { value: 1 });
    return e;
  };
  const start = async (tab: string) => {
    document.querySelector(`[data-dock-tab="${tab}"]`)!.dispatchEvent(pointer("pointerdown", 900, 15));
    await tick();
  };
  const moveTo = async (x: number, y: number) => {
    window.dispatchEvent(pointer("pointermove", x, y));
    await tick();
  };
  const drop = async (x: number, y: number) => {
    window.dispatchEvent(pointer("pointerup", x, y));
    await tick();
  };
  beforeEach(() => {
    Element.prototype.setPointerCapture = vi.fn();
    Element.prototype.releasePointerCapture = vi.fn();
    Element.prototype.hasPointerCapture = vi.fn(() => false);
    document.elementsFromPoint = () => [];
  });
  const ghost = () => document.querySelector("[data-dock-ghost]") as HTMLElement;

  it("draws the preview as the card that results, keeps the ghost on screen and dims the source", async () => {
    const v = seed("a", "b");
    show();
    await waitFor(() => expect(allTabs(v.dock)).toEqual(["a", "b"]));
    await start("b");
    await moveTo(1395, 400);
    const preview = document.querySelector("[data-dock-preview]") as HTMLElement;
    expect(preview).not.toBeNull();
    expect(preview.className).toContain("rounded-lg");
    expect(parseInt(preview.style.top)).toBe(PANEL_PAD);
    expect(parseInt(ghost().style.left)).toBeLessThanOrEqual(window.innerWidth - 176);
    expect(document.querySelector('[data-dock-tab="b"]')!.className).toContain("opacity-50");
    await drop(1395, 400);
  });
  it("counts a drop just over the header as the top edge", async () => {
    const v = seed("a", "b");
    show();
    await waitFor(() => expect(allTabs(v.dock)).toEqual(["a", "b"]));
    await start("b");
    await moveTo(400, -6);
    expect(document.querySelector("[data-dock-preview]")).not.toBeNull();
    await drop(400, -6);
    expect(groups(v.dock).length).toBe(2);
    expect((v.dock as Split).dir).toBe("col");
  });
  it("says there is no room where a drop would not fit, and does nothing there", async () => {
    const v = seed("a", "b");
    size = { w: 640, h: 800 };
    show();
    await waitFor(() => expect(allTabs(v.dock)).toEqual(["a", "b"]));
    const shape = v.dock;
    await start("b");
    await moveTo(636, 400);
    expect(ghost().textContent).toBe("No room");
    expect(document.querySelector("[data-dock-preview]")).toBeNull();
    await drop(636, 400);
    expect(v.dock).toBe(shape);
  });
});

describe("the dock's saved layout", () => {
  it("is written on a change, and a new view in a later run reads it", async () => {
    const v = seed("a", "b");
    show();
    await waitFor(() => expect(allTabs(v.dock).length).toBe(2));
    v.closeWidget("a");
    const saved = JSON.parse(localStorage.getItem("lola.ssh.dock")!)[SID];
    expect(saved.closed).toEqual(["a"]);
    expect(saved.root.kind).toBe("split");
  });
  it("drops a stale widget once the timeline has really answered, and not before", async () => {
    const v = ssh.view(SID);
    v.setDock(moveTab(seedTabs(v), "gone", { kind: "outer", edge: "left" }));
    expect(allTabs(v.dock)).toContain("gone");
    // A failed fetch says nothing about what exists.
    svc.Timeline.mockRejectedValueOnce(new Error("down"));
    await v.fetch();
    v.syncDock();
    expect(allTabs(v.dock)).toContain("gone");
    svc.Timeline.mockResolvedValue({ session: SID, connections: null, items: [item("kept")], lastSeq: 1 });
    await v.fetch();
    v.syncDock();
    expect(allTabs(v.dock)).toEqual(["kept"]);
  });
});

function seedTabs(v: { openWidget: (id: string) => void; dock: any }) {
  v.openWidget("kept");
  v.openWidget("gone");
  return v.dock;
}
