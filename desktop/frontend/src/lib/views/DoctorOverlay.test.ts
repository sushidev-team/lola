import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/svelte";
import DoctorOverlay from "./DoctorOverlay.svelte";

// DoctorService.Run() bridges into the Wails runtime, which is absent under
// jsdom — mock the module so the component sees a fixed report.
vi.mock("@bindings/desktop", () => ({
  DoctorService: {
    Run: vi.fn(async () => ({
      results: [
        { name: "tmux", ok: true, detail: "/usr/bin/tmux", critical: true },
        { name: "gh", ok: false, detail: "not found", critical: true },
        { name: "slack", ok: false, detail: "no webhook configured", critical: false },
      ],
      summary: "2 of 3 checks passed",
      ok: false,
    })),
  },
}));

describe("DoctorOverlay", () => {
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
});
