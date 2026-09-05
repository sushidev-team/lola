import { describe, expect, it } from "vitest";
import { render } from "@testing-library/svelte";
import { ALL_STATUSES, isAttention, statusLabel } from "$lib/theme";
import StatusChip from "./StatusChip.svelte";

// The chip's TONE MAPPING and its accessible output.
//
// The mapping is the only judgement this component makes, and the test is
// written against `$lib/theme` rather than against a copied list of status
// words on purpose: the whole point of deriving from `pillKind` is that a
// status added to Go's internal/state, ported into theme.ts and pinned by
// desktop/state_parity_test.go arrives here already classified. A test that
// spelled the four attention statuses out again would be the third mirror of a
// list the repository keeps in exactly two, and it would keep passing after the
// vocabulary moved underneath it.

function mount(props: Record<string, unknown>) {
  return render(StatusChip, { props });
}

/** The chip element itself — the component's whole output is one span. */
function chip(container: HTMLElement): HTMLElement {
  return container.querySelector("span")!;
}

/** The 5px mark, which only the two loud grounds carry. */
function dot(container: HTMLElement): Element | null {
  return container.querySelector("[aria-hidden='true']");
}

describe("StatusChip", () => {
  it("gives the blocked-on-a-human status the urgent ground and a dot", () => {
    const { container } = mount({ status: "needs_input" });

    expect(chip(container).className).toContain("bg-pill-urgent-soft");
    expect(chip(container).className).toContain("text-pill-urgent-soft-fg");
    expect(dot(container)).toBeTruthy();
    // The dot takes the chip's own foreground by construction, so the two can
    // never drift apart.
    expect(dot(container)!.className).toContain("bg-current");
  });

  it("gives the broken family its own ground rather than the urgent one", () => {
    // Being blocked on a human and being broken are different colours in this
    // design; `isAttention` covers both and cannot tell them apart, which is
    // why the component derives from `pillKind` instead.
    for (const status of ["ci_failed", "merge_conflict", "changes_requested"]) {
      const { container, unmount } = mount({ status });
      expect(chip(container).className).toContain("bg-pill-broken-soft");
      expect(chip(container).className).toContain("text-pill-broken-soft-fg");
      expect(chip(container).className).not.toContain("urgent");
      expect(dot(container)).toBeTruthy();
      unmount();
    }
  });

  it("draws exactly theme.ts's attention set loudly, and everything else grey", () => {
    // THE DRIFT GUARD. `urgent ∪ broken` must stay identical to
    // ATTENTION_STATUSES: a status that joined one set and not the other would
    // give the phone a fifth opinion about what needs a human, silently.
    for (const status of ALL_STATUSES) {
      const { container, unmount } = mount({ status });
      const loud = !chip(container).className.includes("bg-pill-grey");
      expect({ status, loud }).toEqual({ status, loud: isAttention(status) });
      // A dot on a quiet chip is a lit indicator saying nothing.
      expect(!!dot(container)).toBe(isAttention(status));
      unmount();
    }
  });

  it("treats a status no build has ever seen as quiet", () => {
    // A phone outlives the Mac's daemon, so a newer word will arrive here.
    const { container } = mount({ status: "wharrgarbl" });
    expect(chip(container).className).toContain("bg-pill-grey");
    expect(dot(container)).toBeFalsy();
  });

  it("prints the shared spelling and shouts it in CSS, not in the string", () => {
    const { container } = mount({ status: "ci_failed" });

    // statusLabel("ci_failed") === "ci failed". Uppercasing in JS would put
    // "CI FAILED" in the accessibility tree, where a screen reader reads
    // capitals as an initialism and spells some of them out.
    expect(chip(container).textContent!.trim()).toBe(statusLabel("ci_failed"));
    expect(chip(container).textContent).not.toContain("CI FAILED");
    expect(chip(container).className).toContain("uppercase");
  });

  it("carries a caller's own text at a forced tone", () => {
    // The design's second chip on a hero card: a fact the status vocabulary has
    // no word for, so there is no status to derive a ground from.
    const { container } = mount({ label: "retry 1/2", tone: "grey" });

    expect(chip(container).textContent!.trim()).toBe("retry 1/2");
    expect(chip(container).className).toContain("bg-pill-grey");
    expect(dot(container)).toBeFalsy();
  });

  it("keeps the design's odd padding rather than rounding it to the scale", () => {
    // 7px in / 8px out is the transcribed geometry, and at a 21pt chip a 1px
    // rounding is visible next to the neighbouring one.
    const { container } = mount({ status: "needs_input" });
    expect(chip(container).className).toContain("pl-[7px]");
    expect(chip(container).className).toContain("pr-2");
    expect(chip(container).className).toContain("py-[3px]");
  });
});
