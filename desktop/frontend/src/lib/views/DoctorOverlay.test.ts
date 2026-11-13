import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import DoctorOverlay from "./DoctorOverlay.svelte";

// DoctorService.Run() bridges into the Wails runtime, which is absent under
// jsdom — mock the module so the component sees a fixed report. Hoisted so the
// re-run tests can assert the call count and hang a run mid-flight.
const { runMock, cliInfoMock, installMock, report } = vi.hoisted(() => ({
  runMock: vi.fn(),
  cliInfoMock: vi.fn(),
  installMock: vi.fn(),
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
  DaemonService: { CLIInfo: cliInfoMock, InstallCLI: installMock },
}));

// A CLI resolved from the app's own bundle: the DMG-only install this whole
// section exists for.
const bundledCLI = {
  path: "/Applications/Lola.app/Contents/Resources/bin/lola",
  source: "bundled",
  version: "lola 1.4.0",
  found: true,
  error: "",
  bundled: true,
  bundledVersion: "lola 1.4.0",
  skewed: false,
  appVersion: "1.4.0",
};

describe("DoctorOverlay", () => {
  beforeEach(() => {
    cleanup();
    runMock.mockReset();
    runMock.mockResolvedValue(report);
    cliInfoMock.mockReset();
    cliInfoMock.mockResolvedValue(bundledCLI);
    installMock.mockReset();
    installMock.mockResolvedValue({ path: "/usr/local/bin/lola", onPath: true });
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

  // The `lola` CLI section. It is the one row here the app can FIX itself, which
  // is why it carries an action the read-only check list does not.
  describe("lola CLI", () => {
    it("reports which binary the app will run, and offers to install a bundled one", async () => {
      render(DoctorOverlay);
      expect(await screen.findByText("lola CLI")).toBeInTheDocument();
      expect(screen.getByText(/Contents\/Resources\/bin\/lola \(bundled\)/)).toBeInTheDocument();

      await fireEvent.click(screen.getByRole("button", { name: "Install CLI" }));
      expect(installMock).toHaveBeenCalledTimes(1);
      expect(await screen.findByText(/Installed at \/usr\/local\/bin\/lola/)).toBeInTheDocument();
    });

    // Installing into a directory the shell cannot see would otherwise look like
    // the action silently did nothing.
    it("says so when the install target is not on PATH", async () => {
      installMock.mockResolvedValue({ path: "/Users/x/.local/bin/lola", onPath: false });
      render(DoctorOverlay);
      await screen.findByText("lola CLI");

      await fireEvent.click(screen.getByRole("button", { name: "Install CLI" }));
      expect(await screen.findByText(/not on your PATH/)).toBeInTheDocument();
    });

    // A CLI already on PATH needs no install button — the action would only be
    // able to refuse.
    it("offers no install when a PATH binary already wins", async () => {
      cliInfoMock.mockResolvedValue({ ...bundledCLI, source: "PATH", path: "/opt/homebrew/bin/lola" });
      render(DoctorOverlay);
      await screen.findByText("lola CLI");
      expect(screen.queryByRole("button", { name: "Install CLI" })).toBeNull();
    });

    // Version skew is not an error — a developer's own build winning over the
    // bundled copy is the documented dev loop — but it is the cause of "the app
    // has a feature the daemon never heard of", so it must be stated.
    it("flags a PATH binary whose version differs from the bundled one", async () => {
      cliInfoMock.mockResolvedValue({
        ...bundledCLI,
        source: "PATH",
        path: "/Users/x/go/bin/lola",
        version: "lola dev",
        skewed: true,
      });
      render(DoctorOverlay);
      await screen.findByText("lola CLI");
      expect(screen.getByText(/differs from the copy bundled with the app/)).toBeInTheDocument();
    });

    // Nothing resolved anywhere: the message must name the fix, not the lookup.
    it("explains where to get a CLI when none resolves", async () => {
      cliInfoMock.mockResolvedValue({
        ...bundledCLI,
        found: false,
        bundled: false,
        path: "",
        source: "",
        version: "",
        bundledVersion: "",
        error: "the lola CLI could not be found. Reinstall Lola from the DMG (it ships the CLI), install it separately, or set LOLA_BIN to its path",
      });
      render(DoctorOverlay);
      await screen.findByText("lola CLI");
      expect(screen.getByText(/Reinstall Lola from the DMG/)).toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Install CLI" })).toBeNull();
    });

    // A failed probe must cost the section, never the overlay.
    it("hides the section when the probe fails", async () => {
      cliInfoMock.mockRejectedValue(new Error("bridge unavailable"));
      render(DoctorOverlay);
      await screen.findByText("tmux"); // the checks still render
      expect(screen.queryByText("lola CLI")).toBeNull();
    });
  });
});
