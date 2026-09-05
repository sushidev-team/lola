import { beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import TabBar from "./TabBar.svelte";
import { store } from "$lib/store.svelte";
import type { SessionInfo } from "$lib/store.svelte";
import { nav } from "@mobile/lib/nav.svelte";

// The bottom bar, as behaviour.
//
// Everything it reads is a module singleton with `$state` fields — `store` and
// `nav` — so the fixture is assignment rather than a mock layer, exactly as
// Sessions.test.ts argues: a mocked store would let this component pass while
// the real one has renamed a field underneath it.

function s(over: Partial<SessionInfo> = {}): SessionInfo {
  return {
    id: "nori-app-nor-401",
    project: "nori-app",
    issue: "NOR-401",
    title: "Email ingest",
    status: "working",
    agentState: "",
    delivery: "",
    age: "2h",
    prNumber: 0,
    ...over,
  } as unknown as SessionInfo;
}

beforeEach(() => {
  store.sessions = [];
  nav.tab = "sessions";
  nav.screen = "sessions";
  nav.sheet = "";
});

describe("TabBar", () => {
  it("draws the four destinations, in the design's order", () => {
    render(TabBar);
    const names = screen
      .getAllByRole("button")
      .map((b) => b.textContent?.trim());
    expect(names).toEqual(["Sessions", "Activity", "Projects", "Settings"]);
  });

  it("marks the active tab as the current page, and only it", () => {
    nav.tab = "projects";
    render(TabBar);

    // aria-current="page", not role="tablist". These are four destinations, not
    // panels inside one document, and "tab 2 of 4" describes the wrong thing.
    expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute(
      "aria-current",
      "page",
    );
    for (const name of ["Sessions", "Activity", "Settings"]) {
      expect(screen.getByRole("button", { name })).not.toHaveAttribute("aria-current");
    }
  });

  it("paints the active tab in the accent and the rest faint", () => {
    nav.tab = "activity";
    render(TabBar);
    expect(screen.getByRole("button", { name: "Activity" }).className).toContain("text-accent");
    expect(screen.getByRole("button", { name: "Sessions" }).className).toContain("text-faint");
  });

  it("switches tabs, and closes whatever sheet was open over the last one", async () => {
    // A sheet belongs to the screen it was opened over: carried across, the
    // sessions list's filter sheet would sit in front of Settings with nothing
    // on that screen able to close it.
    nav.sheet = "filter";
    render(TabBar);

    await fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    expect(nav.tab).toBe("settings");
    expect(nav.screen).toBe("sessions");
    expect(nav.sheet).toBe("");
  });

  it("badges the Sessions glyph when a session needs a human, and says so in the name", () => {
    store.sessions = [
      s({ id: "a", status: "needs_input" }),
      s({ id: "b", status: "ci_failed" }),
      s({ id: "c", status: "working" }),
    ];
    const { container } = render(TabBar);

    // The dot is decoration — the count it stands for has to reach VoiceOver
    // somehow, and the button's accessible name is where it goes. Same trade
    // the sessions header's filter button makes.
    expect(screen.getByRole("button", { name: "Sessions — 2 need you" })).toBeTruthy();

    // A ring in the bar's own ground is what makes it read as a badge rather
    // than as a smudge on the glyph it overlaps.
    const dot = container.querySelector(".bg-orange")!;
    expect(dot.className).toContain("size-[9px]");
    expect(dot.className).toContain("ring-crust");
    expect(dot).toHaveAttribute("aria-hidden", "true");
  });

  it("counts attention the shared way, so it can never disagree with the header", () => {
    // `attentionCount` is theme.ts's, a port of Go's internal/state pinned by
    // desktop/state_parity_test.go. A quiet status must not badge the bar.
    store.sessions = [s({ status: "review_pending" }), s({ status: "merged" })];
    const { container } = render(TabBar);
    expect(container.querySelector(".bg-orange")).toBeNull();
    // With nothing to add, the visible label IS the accessible name.
    expect(screen.getByRole("button", { name: "Sessions" })).toBeTruthy();
  });

  it("pays the bottom safe-area inset itself, on the bar's own ground", () => {
    const { container } = render(TabBar);
    const bar = container.querySelector("nav")!;

    // The screens above it therefore do not: a screen that also paid it would
    // leave a band of canvas between its last row and a bar already clear of
    // the home indicator. Ground and border sit on the <nav> so the inset strip
    // under the tabs is crust too.
    expect(bar.className).toContain("bg-crust");
    expect(bar.className).toContain("border-edge-soft");

    // ONCE, not twice. The design's 82pt box already ends at the bottom of the
    // 844pt frame, so its own 22pt of bottom padding IS the home-indicator
    // allowance — a `pb-safe-b` stacked on top of it came to ~116pt of chrome
    // against Apple's own 83. `max()` pays the larger of the drawn 22 and the
    // real inset, so a device without one still gets the design's box.
    expect(bar.getAttribute("style")).toContain("max(22px, env(safe-area-inset-bottom");
    expect(bar.className).not.toContain("pb-safe-b");

    // The 60pt band above the inset — `pt-2` plus the design's 51pt row — in
    // four equal columns. It is the part that does not move between devices.
    const row = bar.firstElementChild as HTMLElement;
    expect(row.className).toContain("h-[60px]");
    expect(row.className).toContain("grid-cols-4");
  });

  it("gives every tab the 44pt minimum", () => {
    render(TabBar);
    for (const b of screen.getAllByRole("button")) {
      // `tap` states the guarantee rather than leaving it to be re-derived from
      // the bar's height minus two paddings.
      expect(b.className).toContain("tap");
    }
  });
});
