import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/svelte";
import SectionHeader from "./SectionHeader.svelte";

// Two things, and the first is the one a screenshot cannot show: the heading is
// a HEADING. The list it introduces is a run of buttons with nothing between
// them, so without a real <h2> the rotor offers one flat list of forty controls
// and no way to skip the settled ones — which is the whole reason the design
// partitions the list.

describe("SectionHeader", () => {
  it("is a real heading whose name carries the count", () => {
    render(SectionHeader, { props: { title: "Needs you", count: 2 } });

    // The count belongs to what the section says, so it is inside the heading
    // rather than a loose number beside it.
    const h = screen.getByRole("heading", { name: "Needs you 2" });
    expect(h.tagName).toBe("H2");
  });

  it("draws an empty bucket rather than hiding it", () => {
    // A zero says the bucket is empty; a missing count says the list is broken.
    render(SectionHeader, { props: { title: "In review", count: 0 } });
    expect(screen.getByRole("heading", { name: "In review 0" })).toBeTruthy();
  });

  it("shouts the label in CSS and keeps the DOM spelling", () => {
    const { container } = render(SectionHeader, { props: { title: "Needs you", count: 2 } });
    const label = container.querySelector("span")!;

    // `text-xs` is the 10/12 bold tracked label step and carries no transform
    // of its own, so `uppercase` is the call site's. Uppercasing the STRING
    // would put an initialism in the accessibility tree.
    expect(label.className).toContain("text-xs");
    expect(label.className).toContain("uppercase");
    expect(label.textContent).toBe("Needs you");
  });

  it("hides the rule from the accessibility tree and lets it take the slack", () => {
    const { container } = render(SectionHeader, { props: { title: "Done", count: 9 } });
    const rule = container.querySelector("[aria-hidden='true']")!;

    // A flex ITEM, because it takes whatever the label and the count leave —
    // a border could not flex. `edge-soft` is the design's list hairline;
    // plain `edge` is the heavier panel border and is wrong at this weight.
    expect(rule.className).toContain("h-px");
    expect(rule.className).toContain("flex-1");
    expect(rule.className).toContain("bg-edge-soft");
    // ...and it contributes nothing to the heading's name.
    expect(screen.getByRole("heading", { name: "Done 9" })).toBeTruthy();
  });
});
