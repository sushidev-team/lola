import { describe, expect, it } from "vitest";
import { render } from "@testing-library/svelte";
import { kanbanDotText, statusText } from "$lib/theme";
import { statusTone } from "@mobile/lib/statustone";
import StatusDot from "./StatusDot.svelte";

// What the dot draws, and what it contributes to the accessibility tree — which
// is nothing, deliberately.
//
// THE COLOURS ARE ASSERTED AGAINST THE SHARED TABLES, never against a list of
// hexes or utility names copied into this file. A copied table is a fourth
// colour vocabulary, and desktop/state_parity_test.go exists to keep the count
// at two. The component takes a whole class string, so what these tests actually
// pin is that it renders the caller's class untouched — which is the property
// rule 4 cares about, since a composed class would compile to nothing.

function mount(props: { tone: string; size?: 5 | 6; class?: string }) {
  return render(StatusDot, { props });
}

function dot(container: HTMLElement): HTMLElement {
  return container.querySelector("span")!;
}

describe("StatusDot", () => {
  it("renders the caller's colour class verbatim", () => {
    // The filter rail's chips lead with their BUCKET's colour, which is not a
    // status at all — `kanbanDotText` in $lib/theme, keyed by column. That is
    // the only production call site.
    for (const key of ["needs", "working", "fixing", "review", "done"]) {
      const tone = kanbanDotText(key);
      const { container, unmount } = mount({ tone });
      expect(dot(container).className).toContain(tone);
      unmount();
    }
  });

  it("carries a status colour just as well, from either shared table", () => {
    // Not used in production today, but the contract is "a class from a table
    // Tailwind has already seen" and both tables qualify. `statusTone` is
    // theme.ts's own answer plus the single phone-local step-down for the
    // family theme.ts leaves at `text-ink` — see statustone.ts.
    for (const status of ["needs_input", "working", "ci_failed", "approved"]) {
      const { container, unmount } = mount({ tone: statusTone(status) });
      expect(dot(container).className).toContain(statusText(status));
      unmount();
    }
    for (const status of ["review_pending", "draft", "ci_pending"]) {
      const { container, unmount } = mount({ tone: statusTone(status) });
      expect(dot(container).className).toContain("text-faint");
      expect(dot(container).className).not.toContain("text-ink");
      unmount();
    }
  });

  it("draws the two transcribed sizes and nothing between them", () => {
    const { container: six } = mount({ tone: "text-faint" });
    expect(dot(six).className).toContain("h-1.5"); // 6px, the default
    const { container: five } = mount({ tone: "text-faint", size: 5 });
    expect(dot(five).className).toContain("h-[5px]");
  });

  it("takes its colour through `bg-current`, so shape and colour cannot drift", () => {
    const { container } = mount({ tone: "text-orange" });
    expect(dot(container).className).toContain("bg-current");
    expect(dot(container).className).toContain("rounded-full");
  });

  it("says nothing at all to a screen reader", () => {
    // It sits inside a chip that is already labelled with the bucket it
    // colours, so announcing it would double what is said. It is a mark, and it
    // has no animated state to describe — that belongs to LivePulse, on the
    // agent axis, which SessionRow renders instead.
    const { container } = mount({ tone: "text-orange" });
    expect(dot(container).getAttribute("aria-hidden")).toBe("true");
    expect(container.textContent!.trim()).toBe("");
    expect(container.querySelector(".animate-ping")).toBeNull();
  });
});
