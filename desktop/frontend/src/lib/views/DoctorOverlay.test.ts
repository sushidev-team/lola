import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import DoctorOverlay from "./DoctorOverlay.svelte";

// DoctorService.Run() bridges into the Wails runtime, which is absent under
// jsdom — mock the module so the component sees a fixed report. Hoisted so the
// re-run tests can assert the call count and hang a run mid-flight.
const { runMock, report } = vi.hoisted(() => ({
  runMock: vi.fn(),
  report: {
    results: [
      { name: "tmux", ok: true, detail: "/usr/bin/tmux", critical: true },
      { name: "gh", ok: false, detail: "not found", critical: true },
      { name: "slack", ok: false, detail: "no webhook configured", critical: false },
    ],
    summary: "2 of 3 checks passed",
    ok: false,
  },
}));

vi.mock("@bindings/desktop", () => ({
  DoctorService: { Run: runMock },
}));

describe("DoctorOverlay", () => {
  beforeEach(() => {
    cleanup();
    runMock.mockReset();
    runMock.mockResolvedValue(report);
  });

  it("shows the loading line, then renders one row per result plus the summary", async () => {
    render(DoctorOverlay);

    // Before Run() resolves, the loading line is shown (body + footer both show it).
    expect(screen.getAllByText("running checks…").length).toBeGreaterThan(0);

    // After it resolves, every check name renders…
    expect(await screen.findByText("tmux")).toBeInTheDocument();
    expect(screen.getByText("gh")).toBeInTheDocument();
    expect(screen.getByText("slack")).toBeInTheDocument();

    // …with its detail…
    expect(screen.getByText("not found")).toBeInTheDocument();

    // …and the footer summary reflects the overall (failing) verdict.
    expect(screen.getByText("2 of 3 checks passed")).toBeInTheDocument();
  });

  // After fixing a failing check you have to run the checks again; closing and
  // reopening the overlay to do it was needless.
  it("re-runs the checks in place on demand", async () => {
    render(DoctorOverlay);
    await screen.findByText("tmux"); // initial run resolved
    expect(runMock).toHaveBeenCalledTimes(1);

    await fireEvent.click(screen.getByText("Re-run"));
    expect(runMock).toHaveBeenCalledTimes(2);
  });

  it("shows a pending state while re-running and keeps the last report visible", async () => {
    render(DoctorOverlay);
    await screen.findByText("tmux");

    // Hang the next run so the in-flight state is observable.
    let settle: (v: unknown) => void = () => {};
    runMock.mockReturnValueOnce(new Promise((r) => (settle = r)));
    await fireEvent.click(screen.getByText("Re-run"));

    // The button reflects the run in flight; the previous results stay on screen
    // rather than flashing empty.
    expect(screen.getByText("Running…")).toBeInTheDocument();
    expect(screen.getByText("tmux")).toBeInTheDocument();

    settle(report);
    await screen.findByText("Re-run"); // back to idle
  });
});
