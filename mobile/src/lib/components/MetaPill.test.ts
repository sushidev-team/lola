import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/svelte";
import { createRawSnippet } from "svelte";
import MetaPill from "./MetaPill.svelte";

// What this pins is the SHAPE the pill takes, because that is the part a call
// site cannot see and the part that breaks quietly: a <button> nested inside a
// row's own <button> does not parse — the parser closes the outer one and the
// row stops being clickable — so "not interactive unless asked" is a
// correctness property here, not a styling one. The tone and ground maps are
// pinned for rule 4's reason: a composed class compiles to nothing and the pill
// renders transparent with no error anywhere.
//
// `createRawSnippet` rather than a .test.svelte harness: the children are a
// string of text, and a harness file would be a fifth component in a directory
// that has four.

const text = (s: string) => createRawSnippet(() => ({ render: () => `<span>${s}</span>` }));

function mount(props: Record<string, unknown> = {}) {
  return render(MetaPill, { props: { children: text("#341"), ...props } });
}

describe("MetaPill", () => {
  it("is a span, not a button, unless it is given something to do", () => {
    // The hero card and the compact row are themselves buttons, and this pill
    // sits inside them.
    const { container } = mount();
    expect(container.querySelector("button")).toBeNull();
    expect(container.querySelector("span")).toBeTruthy();
    expect(container.textContent).toContain("#341");
  });

  it("becomes a real 44pt target when it is given a handler", async () => {
    const onclick = vi.fn();
    const { container } = mount({ onclick, ariaLabel: "open pull request #341" });

    // The accessible name is the aria-label: "#341" names nothing on its own.
    const button = screen.getByRole("button", { name: "open pull request #341" });
    expect(button).toBeTruthy();
    // `tap` is the app.css utility carrying Apple's minimum in BOTH axes; the
    // pill itself stays ~21pt tall and the rest of the target is invisible.
    expect(button.className).toContain("tap");
    // The visible chip is INSIDE the button, not the button itself — a 44pt
    // filled badge would be the tallest thing in a header built around a 15px
    // title.
    const pill = container.querySelector("button > span")!;
    expect(pill.className).toContain("rounded-md");
    expect(button.className).not.toContain("bg-sel");

    button.click();
    expect(onclick).toHaveBeenCalledOnce();
  });

  it("takes its foreground and its ground from the two maps", () => {
    const cases = [
      { tone: "magenta", ground: "sel", fg: "text-magenta", bg: "bg-sel" },
      { tone: "bad", ground: "sel", fg: "text-bad", bg: "bg-sel" },
      { tone: "good", ground: "sel", fg: "text-good", bg: "bg-sel" },
      { tone: "grey", ground: "grey", fg: "text-pill-grey-fg", bg: "bg-pill-grey" },
    ];
    for (const c of cases) {
      const { container, unmount } = mount({ tone: c.tone, ground: c.ground });
      const pill = container.querySelector("span")!;
      expect(pill.className).toContain(c.fg);
      expect(pill.className).toContain(c.bg);
      unmount();
    }
  });

  it("renders a leading glyph before its children", () => {
    // The caller passes <BranchIcon /> for a PR badge; the pill neither knows
    // nor chooses which glyph that is.
    const { container } = mount({ leading: text("[branch]") });
    // Order matters: the glyph leads.
    expect(container.textContent!.replace(/\s+/g, "")).toBe("[branch]#341");
  });

  it("keeps mono figures and the design's lopsided padding", () => {
    // `num` is tabular figures — a PR number and an age both change under an
    // observer push, and a proportional "1" would reflow the whole line.
    const { container } = mount();
    const pill = container.querySelector("span")!;
    expect(pill.className).toContain("num");
    expect(pill.className).toContain("text-sm");
    expect(pill.className).toContain("font-medium");
    expect(pill.className).toContain("pl-[7px]");
    expect(pill.className).toContain("pr-2");
    expect(pill.className).toContain("py-[3px]");
  });
});
