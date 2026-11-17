import { describe, it, expect, afterEach, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import "./story.testkit";
import { store } from "$lib/store.svelte";
import { ssh } from "$lib/ssh.svelte";
import { allTabs, groupOf } from "$lib/docklayout";
import WidgetCard from "./WidgetCard.svelte";

let n = 0;
let SID = "";
const element = { id: "w1", kind: "widget", title: "Queue depth", body: "Live count of the jobs waiting", revision: 1, html: "<b>x</b>" };

beforeEach(() => {
  SID = `lola-widget-${++n}`;
  store.sessions = [{ id: SID, agent: "claude", agentState: "idle" }] as never;
});
afterEach(cleanup);

describe("WidgetCard", () => {
  it("open sets consent and selects the tab", async () => {
    const view = ssh.view(SID);
    expect(view.widgetsAllowed).toBe(false);
    render(WidgetCard, { sessionId: SID, element: element as never });
    await fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(view.widgetsAllowed).toBe(true);
    expect(groupOf(view.dock, "w1")).toBeTruthy();
    expect(view.dockPulse?.tab).toBe("w1");
  });

  it("says Shown while its panel shows, so a second press does nothing", async () => {
    render(WidgetCard, { sessionId: SID, element: element as never });
    await fireEvent.click(screen.getByRole("button", { name: "Open" }));
    expect(screen.getByRole("button", { name: "Shown" })).toBeDisabled();
  });

  it("a post creates no panel", () => {
    const view = ssh.view(SID);
    render(WidgetCard, { sessionId: SID, element: element as never });
    expect(allTabs(view.dock)).toEqual([]);
    expect(view.widgetsAllowed).toBe(false);
  });

  it("says who wrote it, in the one colour that means that, and draws one line", () => {
    const { container } = render(WidgetCard, { sessionId: SID, element: element as never });
    expect(screen.getByText("by Claude").className).toContain("text-magenta");
    expect(screen.getByText("Live count of the jobs waiting")).toBeInTheDocument();
    expect(container.querySelector("[data-widget-card]")?.className).not.toContain("magenta");
  });
});
