import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import PlanGateBanner from "./PlanGateBanner.svelte";
import { store } from "$lib/store.svelte";
import type { SessionInfo } from "@bindings/internal/protocol";

// The plan-approval gate blocks a session's file edits until a human answers,
// so the banner IS the session's only way forward from the app.
describe("PlanGateBanner", () => {
  const session = (plan?: SessionInfo["plan"]) => ({ id: "lola-app-eng-1", issue: "ENG-1", plan }) as SessionInfo;

  beforeEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders nothing for a session without a gate or with an approved plan", () => {
    render(PlanGateBanner, { session: session() });
    expect(screen.queryByRole("region")).not.toBeInTheDocument();
    cleanup();
    render(PlanGateBanner, { session: session({ gate: "approved" }) });
    expect(screen.queryByRole("region")).not.toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("shows the submitted plan as text and approves it", async () => {
    const decide = vi.spyOn(store, "decidePlan").mockResolvedValue(true);
    render(PlanGateBanner, { session: session({ gate: "submitted", text: "## Approach\n<b>not markup</b>", round: 2 }) });
    const region = screen.getByRole("region", { name: "Plan awaiting approval" });
    expect(region).toHaveTextContent("<b>not markup</b>");
    expect(region).toHaveTextContent("round 2");
    await fireEvent.click(screen.getByRole("button", { name: "Approve plan" }));
    expect(decide).toHaveBeenCalledWith("lola-app-eng-1", true, "");
  });

  it("requires a comment to request changes, and sends it", async () => {
    const decide = vi.spyOn(store, "decidePlan").mockResolvedValue(true);
    render(PlanGateBanner, { session: session({ gate: "submitted", text: "plan" }) });
    const reject = screen.getByRole("button", { name: "Request changes" });
    expect(reject).toBeDisabled();
    await fireEvent.input(screen.getByLabelText("Feedback for the agent"), { target: { value: "add tests" } });
    expect(reject).not.toBeDisabled();
    await fireEvent.click(reject);
    expect(decide).toHaveBeenCalledWith("lola-app-eng-1", false, "add tests");
  });

  it("offers to skip the gate while the agent is still planning, and shows the last feedback", async () => {
    const decide = vi.spyOn(store, "decidePlan").mockResolvedValue(true);
    render(PlanGateBanner, { session: session({ gate: "planning", feedback: "split the migration" }) });
    expect(screen.getByRole("status")).toHaveTextContent("split the migration");
    await fireEvent.click(screen.getByRole("button", { name: "Skip plan" }));
    expect(decide).toHaveBeenCalledWith("lola-app-eng-1", true, "");
  });
});
