import { describe, it, expect, vi } from "vitest";
import { applyRoute, followFocus, markConversationSeen, sheetClosed, sheetForOverlay, type DeskNav } from "./desknav";

function fake(over: Partial<DeskNav> = {}): DeskNav {
  return {
    view: "home",
    selectedId: "",
    focusedTerm: "",
    overlay: null,
    markSeen: vi.fn(),
    closeOverlay: vi.fn(),
    ...over,
  };
}

describe("desk-nav adapter", () => {
  it("focuses a session route and clears the focus when leaving", () => {
    const nav = fake();
    applyRoute({ name: "session", id: "s1", seg: "conversation" }, nav);
    expect(nav).toMatchObject({ view: "cockpit", selectedId: "s1", focusedTerm: "s1" });
    applyRoute({ name: "tab", tab: "inbox" }, nav);
    expect(nav.focusedTerm).toBe("");
    expect(nav.selectedId).toBe("s1");
  });

  it("maps the pr-review overlay to the reviewPackage sheet", () => {
    expect(sheetForOverlay(fake({ overlay: "pr-review" }))).toBe("reviewPackage");
    expect(sheetForOverlay(fake({ overlay: "help" }))).toBe("");
    expect(sheetForOverlay(fake())).toBe("");
  });

  it("closing that sheet closes the overlay, and only that one", () => {
    const nav = fake({ overlay: "pr-review" });
    sheetClosed(nav, "merge");
    expect(nav.closeOverlay).not.toHaveBeenCalled();
    sheetClosed(nav, "reviewPackage");
    expect(nav.closeOverlay).toHaveBeenCalledOnce();
    const other = fake({ overlay: "help" });
    sheetClosed(other, "reviewPackage");
    expect(other.closeOverlay).not.toHaveBeenCalled();
  });

  it("turns a focus change to another id (Fork) into a push target", () => {
    const route = { name: "session", id: "s1", seg: "review" } as const;
    expect(followFocus(route, fake({ focusedTerm: "s1" }))).toBeNull();
    expect(followFocus(route, fake({ focusedTerm: "" }))).toBeNull();
    expect(followFocus(route, fake({ focusedTerm: "s2" }))).toEqual({
      name: "session",
      id: "s2",
      seg: "conversation",
    });
    expect(followFocus({ name: "tab", tab: "inbox" }, fake({ focusedTerm: "s2" }))).toBeNull();
  });

  it("marks seen only on the Conversation segment", () => {
    const nav = fake();
    markConversationSeen({ name: "session", id: "s1", seg: "changes" }, nav);
    expect(nav.markSeen).not.toHaveBeenCalled();
    markConversationSeen({ name: "session", id: "s1", seg: "conversation" }, nav);
    expect(nav.markSeen).toHaveBeenCalledWith("s1");
  });
});
