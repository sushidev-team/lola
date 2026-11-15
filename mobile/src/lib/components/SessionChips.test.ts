import { cleanup, render, screen } from "@testing-library/svelte";
import { afterEach, describe, expect, it } from "vitest";
import SessionChips, { pillTone, sessionChips } from "./SessionChips.svelte";

afterEach(cleanup);

describe("SessionChips", () => {
  it("draws nothing for a session with neither", () => {
    const { container } = render(SessionChips, { props: { session: {} } });
    expect(container.querySelector("[data-session-chips]")).toBeNull();
    expect(sessionChips(null)).toEqual([]);
  });

  it("shows the queue place and the overlap, from the desktop's wording", () => {
    render(SessionChips, {
      props: { session: { mergeQueue: { position: 2, step: "waiting" }, overlaps: [{ session: "s2", issue: "NOR-9", files: ["a.go", "b.go", "c.go"] }] } },
    });
    expect(screen.getByText("Queue #2 · waiting")).toBeTruthy();
    expect(screen.getByText("Overlaps NOR-9")).toBeTruthy();
  });

  it("adds each chip's explanation with detail", () => {
    render(SessionChips, {
      props: { detail: true, session: { mergeQueue: { position: 1, step: "merging" }, overlaps: [{ session: "s2", issue: "NOR-9", files: ["a.go"] }] } },
    });
    expect(screen.getByText("next to land — lola issued the merge")).toBeTruthy();
    expect(screen.getByText(/NOR-9: a\.go/)).toBeTruthy();
  });

  it("maps every stage tone to a pill tone", () => {
    expect(pillTone("bad")).toBe("bad");
    expect(pillTone("warn")).toBe("warn");
    expect(pillTone("good")).toBe("good");
    expect(pillTone("neutral")).toBe("grey");
  });
});
