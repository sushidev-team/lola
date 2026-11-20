import { beforeEach, describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";
import TabBar, { needsYouCount } from "./TabBar.svelte";
import { store } from "$lib/store.svelte";
import type { SessionInfo } from "$lib/store.svelte";
import { router } from "@mobile/lib/router.svelte";

// The bottom bar as behaviour. Its inputs are two module singletons with `$state`
// fields, so the fixture is assignment, not a mock layer.

function s(over: Partial<SessionInfo> = {}): SessionInfo {
  return { id: "a", agentState: "working", delivery: "", ...over } as unknown as SessionInfo;
}

beforeEach(() => {
  store.sessions = [];
  router.stack = [{ name: "tab", tab: "inbox" }];
  router.sheet = "";
});

describe("needsYouCount", () => {
  it("counts blocked, regressed and approved sessions only", () => {
    expect(
      needsYouCount([
        s({ agentState: "waiting_input" }),
        s({ delivery: "ci_failed" }),
        s({ delivery: "approved" }),
        s({ delivery: "review_pending" }),
        s({ agentState: "working" }),
        s({ delivery: "merged" }),
      ]),
    ).toBe(3);
  });
});

describe("TabBar", () => {
  it("draws the three destinations in the router's order", () => {
    render(TabBar);
    expect(screen.getAllByRole("button").map((b) => b.textContent?.trim())).toEqual([
      "Sessions",
      "Projects",
      "Settings",
    ]);
  });

  it("marks the active tab as the current page, and only it", () => {
    router.stack = [{ name: "tab", tab: "projects" }];
    render(TabBar);
    expect(screen.getByRole("button", { name: "Projects" })).toHaveAttribute("aria-current", "page");
    for (const name of ["Sessions", "Settings"]) {
      expect(screen.getByRole("button", { name })).not.toHaveAttribute("aria-current");
    }
  });

  it("replaces the route when a tab is pressed", async () => {
    render(TabBar);
    await fireEvent.click(screen.getByRole("button", { name: "Settings" }));
    expect(router.top).toEqual({ name: "tab", tab: "settings" });
    expect(router.stack).toHaveLength(1);
  });

  it("does nothing for the tab already showing", async () => {
    router.stack = [{ name: "tab", tab: "inbox", filter: "needs" }];
    render(TabBar);
    await fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
    expect(router.top).toEqual({ name: "tab", tab: "inbox", filter: "needs" });
  });

  it("puts the waiting count in the Inbox button's name", () => {
    store.sessions = [s({ agentState: "waiting_input" }), s({ delivery: "approved" })];
    render(TabBar);
    expect(screen.getByRole("button", { name: "Sessions, 2 need you" })).toBeInTheDocument();
  });

  it("names the Inbox plainly when nothing waits", () => {
    store.sessions = [s()];
    render(TabBar);
    expect(screen.getByRole("button", { name: "Sessions" })).toBeInTheDocument();
  });

  it("draws the count as a capsule on the Inbox glyph, capped at 99+", () => {
    store.sessions = [s({ agentState: "waiting_input" }), s({ delivery: "approved" })];
    const { container } = render(TabBar);
    const badge = container.querySelector("[data-badge]") as HTMLElement;
    expect(badge.textContent?.trim()).toBe("2");
    expect(badge.className).toContain("h-[18px]");
    expect(badge.className).toContain("min-w-[18px]")
    expect(badge.className).toContain("ring-crust");
    expect(badge.className).toContain("bg-bad");
    store.sessions = Array.from({ length: 120 }, () => s({ delivery: "approved" }));
    return Promise.resolve().then(() => {
      expect(container.querySelector("[data-badge]")?.textContent?.trim()).toBe("99+");
    });
  });

  it("shows no badge when nothing waits", () => {
    const { container } = render(TabBar);
    expect(container.querySelector("[data-badge]")).toBeNull();
  });

  it("is 49pt over the bare inset, translucent, with a measured idle label", () => {
    const { container } = render(TabBar);
    const nav = container.querySelector("nav") as HTMLElement;
    // jsdom drops env() and color-mix() from the CSSOM, so read the attribute itself.
    const style = nav.getAttribute("style") ?? "";
    expect(style).toContain("padding-bottom: env(safe-area-inset-bottom, 0px)");
    expect(style).toContain("color-mix(in srgb, var(--color-canvas) 80%, transparent)");
    expect(style).toContain("blur(20px) saturate(180%)");
    expect(container.querySelector("div.grid")?.className).toContain("h-[49px]");
    expect(screen.getByRole("button", { name: "Projects" }).className).toContain("text-tab-idle");
  });

  it("draws only the selected tab's glyph filled", () => {
    router.stack = [{ name: "tab", tab: "projects" }];
    const { container } = render(TabBar);
    const filled = container.querySelectorAll("[data-filled]");
    expect(filled).toHaveLength(1);
    expect(filled[0].closest("button")).toHaveAccessibleName("Projects");
  });
});
