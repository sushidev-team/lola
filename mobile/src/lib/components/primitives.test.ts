import { readFileSync } from "node:fs";
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import { createRawSnippet } from "svelte";
import ListGroup from "./ListGroup.svelte";
import ListRow from "./ListRow.svelte";
import Switch from "./Switch.svelte";
import EmptyState from "./EmptyState.svelte";
import LargeTitle from "./LargeTitle.svelte";

const snip = (html: string) => createRawSnippet(() => ({ render: () => html }));

describe("ListGroup", () => {
  it("is an inset grouped panel with a header, count and footer", () => {
    const { container } = render(ListGroup, {
      props: { header: "Agents", count: 3, footer: "Pick one.", children: snip("<span>rows</span>") },
    });
    expect(screen.getByRole("heading", { name: "Agents 3" })).toBeInTheDocument();
    expect(container.querySelector("[data-list-group].mx-gutter.overflow-hidden.rounded-button.bg-edge-soft")).not.toBeNull();
    expect(screen.getByText("Pick one.").className).toContain("pt-1.5");
  });

  it("draws neither header nor footer unasked", () => {
    render(ListGroup, { props: { children: snip("<span>rows</span>") } });
    expect(screen.queryByRole("heading")).toBeNull();
  });
});

describe("ListRow", () => {
  it("is a div without onclick and a button with it", async () => {
    const a = render(ListRow, { props: { label: "Mac name", value: "Studio" } });
    expect(screen.queryByRole("button")).toBeNull();
    expect(a.container.querySelector("div[data-list-row]")).not.toBeNull();
    a.unmount();
    const onclick = vi.fn();
    render(ListRow, { props: { label: "About", onclick, chevron: true } });
    await fireEvent.click(screen.getByRole("button", { name: "About" }));
    expect(onclick).toHaveBeenCalledOnce();
  });

  it("is 44pt, 52pt with an icon and a 52px separator inset", () => {
    const a = render(ListRow, { props: { label: "A" } });
    expect(a.container.querySelector("[data-list-row]")!.className).toContain("min-h-11");
    a.unmount();
    const b = render(ListRow, { props: { label: "B", icon: "folder" } });
    const row = b.container.querySelector("[data-list-row]") as HTMLElement;
    expect(row.className).toContain("min-h-[52px]");
    expect(row.getAttribute("style")).toContain("--row-inset: 52px");
  });

  it("puts the detail on line 2 and the value on the right", () => {
    render(ListRow, { props: { label: "Agent", detail: "Claude Code", value: "Default" } });
    expect(screen.getByText("Claude Code").className).toContain("text-footnote");
    expect(screen.getByText("Default").className).toContain("tabular-nums");
  });

  it("a check replaces the value", () => {
    const { container } = render(ListRow, { props: { label: "Dark", value: "x", checked: true, onclick: () => {} } });
    expect(container.querySelector("[data-row-check]")).not.toBeNull();
    expect(screen.queryByText("x")).toBeNull();
  });

  it("current is a checked row at full colour that cannot be pressed", () => {
    const onclick = vi.fn();
    const { container } = render(ListRow, { props: { label: "Claude", current: true, disabled: true, onclick } });
    const row = container.querySelector("[data-list-row]") as HTMLElement;
    expect(row.tagName).toBe("DIV");
    expect(row).toHaveAttribute("aria-current", "true");
    expect(row.className).not.toContain("opacity-40");
    expect(screen.getByText("Claude").className).toContain("text-ink");
    expect(container.querySelector("[data-row-check]")!.className).toContain("text-accent");
  });

  it("centres an action-sheet row at 56pt semibold", () => {
    const { container } = render(ListRow, { props: { label: "Cancel", align: "center", tone: "accent", onclick: () => {} } });
    expect((container.querySelector("[data-list-row]") as HTMLElement).className).toContain("min-h-14");
    expect(screen.getByText("Cancel").className).toContain("font-semibold");
  });

  it("draws a mono detail", () => {
    render(ListRow, { props: { label: "Port 3000", detail: "/app", detailMono: true } });
    expect(screen.getByText("/app").className).toContain("font-mono");
  });

  it("tones the label and dims when disabled", () => {
    const { container } = render(ListRow, { props: { label: "Remove", tone: "danger", disabled: true, onclick: () => {} } });
    expect(screen.getByText("Remove").className).toContain("text-bad");
    expect(container.querySelector("button")).toBeDisabled();
  });
});

describe("Switch", () => {
  it("is a named switch that reports its state and toggles", async () => {
    const onchange = vi.fn();
    render(Switch, { props: { checked: false, onchange, label: "Match system" } });
    const sw = screen.getByRole("switch", { name: "Match system" });
    expect(sw).toHaveAttribute("aria-checked", "false");
    expect(sw.className).toContain("w-[51px]");
    await fireEvent.click(sw);
    expect(onchange).toHaveBeenCalledWith(true);
  });

  it("is accent on and edge off", () => {
    const a = render(Switch, { props: { checked: true, onchange: () => {}, label: "x" } });
    expect(screen.getByRole("switch").className).toContain("bg-accent");
    a.unmount();
    render(Switch, { props: { checked: false, onchange: () => {}, label: "x" } });
    expect(screen.getByRole("switch").className).toContain("bg-edge");
  });
});

describe("EmptyState", () => {
  it("sheet size is a compact block: 28px glyph, no h-full, py-6", () => {
    const { container } = render(EmptyState, {
      props: { icon: "globe", size: "sheet", title: "No dev servers", body: "Start one.", action: { label: "Start", onclick: () => {} } },
    });
    const root = container.querySelector("[data-empty]") as HTMLElement;
    expect(root.className).toContain("py-6");
    expect(root.className).not.toContain("h-full");
    expect(container.querySelector("svg")!.getAttribute("width")).toBe("28");
    expect(screen.getByText("Start one.").className).toContain("text-callout");
    expect(screen.getByRole("button", { name: "Start" })).toBeInTheDocument();
  });

  it("centres a glyph, headline, body and a primary action", async () => {
    const onclick = vi.fn();
    const { container } = render(EmptyState, {
      props: { icon: "inbox", title: "All clear", body: "Nothing needs you.", action: { label: "Show all", onclick } },
    });
    expect(container.firstElementChild!.className).toContain("place-content-center");
    expect(screen.getByRole("heading", { name: "All clear" })).toBeInTheDocument();
    expect(screen.getByText("Nothing needs you.").className).toContain("text-faint");
    await fireEvent.click(screen.getByRole("button", { name: "Show all" }));
    expect(onclick).toHaveBeenCalledOnce();
  });

  it("the good tone paints the glyph green", () => {
    const { container } = render(EmptyState, { props: { icon: "circle-check", tone: "good", title: "Done" } });
    expect(container.querySelector(".text-good")).not.toBeNull();
  });
});

describe("LargeTitle", () => {
  it("pins the top inset and sets the title", () => {
    const { container } = render(LargeTitle, { props: { title: "Projects" } });
    expect(container.querySelector("header")!.className).toContain("var(--lola-top-inset,env(safe-area-inset-top,0px))");
    expect(screen.getByRole("heading", { name: "Projects" }).className).toContain("text-title");
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("draws a back row first and calls it", async () => {
    const onclick = vi.fn();
    render(LargeTitle, { props: { title: "Detail", back: { label: "Projects", onclick }, trailing: snip("<b>+</b>") } });
    await fireEvent.click(screen.getByRole("button", { name: "Projects" }));
    expect(onclick).toHaveBeenCalledOnce();
    expect(screen.getByText("+")).toBeInTheDocument();
  });
});

describe("gutter utilities", () => {
  it("app.css defines mx-gutter, the margin ListGroup relies on", () => {
    const css = readFileSync(`${process.cwd()}/src/app.css`, "utf8");
    expect(css).toContain("@utility mx-gutter");
  });
});
