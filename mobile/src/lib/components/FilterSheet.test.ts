import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import FilterSheet from "./FilterSheet.svelte";
import type { SessionInfo } from "$lib/store.svelte";

// The overlay the search field and the triage chips moved into.
//
// Nothing here needs a device: `fireEvent` is a real interaction against real
// DOM, which is the whole reason behaviour lives in this file and not in a
// screenshot. What a screenshot is for — the sheet's proportions on a phone —
// is verified separately and by eye.

function s(over: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id: "x",
    project: "nori-app",
    issue: "NOR-1",
    title: "t",
    status: "needs_input",
    agentState: "",
    delivery: "",
    interpretedState: "",
    age: "1h",
    prNumber: 0,
    ...over,
  } as unknown as SessionInfo;
}

const sessions = [s({ id: "a" }), s({ id: "b", status: "dead" })];

describe("FilterSheet", () => {
  it("is a dialog named for what it holds", () => {
    render(FilterSheet, { props: { sessions, matched: 2, onclose: () => {} } });
    expect(screen.getByRole("dialog", { name: "Filters" })).toBeTruthy();
  });

  it("names both numbers, so a filtered list cannot read as a short one", () => {
    render(FilterSheet, {
      props: { sessions, matched: 1, triage: "Needs You", onclose: () => {} },
    });
    expect(screen.getByText("1 of 2")).toBeTruthy();
  });

  it("filters by chip, and the chip carries the bucket's count", async () => {
    let triage = "";
    render(FilterSheet, {
      props: {
        sessions,
        matched: 2,
        get triage() {
          return triage;
        },
        set triage(v: string) {
          triage = v;
        },
        onclose: () => {},
      },
    });

    // One needs_input, one dead: the buckets come from theme.ts's KANBAN_COLUMNS
    // and are never re-derived here.
    const chip = screen.getByRole("button", { name: /^Needs You/ });
    expect(chip.textContent).toContain("1");

    await fireEvent.click(chip);
    expect(triage).toBe("Needs You");

    // A second tap on the selected chip clears it, which is the strip's own
    // toggle behaviour and the reason "All" is not the only way back.
    await fireEvent.click(screen.getByRole("button", { name: /^Needs You/ }));
    expect(triage).toBe("");
  });

  it("searches by text", async () => {
    let query = "";
    render(FilterSheet, {
      props: {
        sessions,
        matched: 2,
        get query() {
          return query;
        },
        set query(v: string) {
          query = v;
        },
        onclose: () => {},
      },
    });

    const field = screen.getByLabelText("Search sessions");
    await fireEvent.input(field, { target: { value: "NOR-1" } });
    expect(query).toBe("NOR-1");
  });

  it("clears both filters in one tap, and offers nothing to clear when nothing is set", async () => {
    let triage = "Needs You";
    let query = "ingest";
    const { rerender } = render(FilterSheet, {
      props: {
        sessions,
        matched: 1,
        get triage() {
          return triage;
        },
        set triage(v: string) {
          triage = v;
        },
        get query() {
          return query;
        },
        set query(v: string) {
          query = v;
        },
        onclose: () => {},
      },
    });

    const clear = screen.getByRole("button", { name: "Clear filters" });
    expect(clear).not.toBeDisabled();
    await fireEvent.click(clear);
    expect(triage).toBe("");
    expect(query).toBe("");

    await rerender({ sessions, matched: 2, triage: "", query: "" });
    expect(screen.getByRole("button", { name: "Clear filters" })).toBeDisabled();
  });

  it("closes from Done and from the backdrop", async () => {
    const onclose = vi.fn();
    render(FilterSheet, { props: { sessions, matched: 2, onclose } });

    await fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(onclose).toHaveBeenCalledTimes(1);

    // The tap-to-dismiss target is a real, named button rather than a bare div,
    // so VoiceOver has the same way out that a thumb does.
    await fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onclose).toHaveBeenCalledTimes(2);
  });

  // Escape belongs to Sheet, the one modal shape all three sheets mount, rather
  // than to whichever call site remembered it. It used to live in ViewSettings
  // alone, which meant a Magic Keyboard on an iPad could dismiss the view
  // settings and was trapped by this sheet and the connection one. The handler
  // is asserted from a call site rather than from Sheet directly because that
  // is where the regression would actually be felt.
  it("closes on Escape, which Sheet owns for every sheet", async () => {
    const onclose = vi.fn();
    render(FilterSheet, { props: { sessions, matched: 2, onclose } });

    await fireEvent.keyDown(window, { key: "Escape" });
    expect(onclose).toHaveBeenCalledTimes(1);
  });
});
