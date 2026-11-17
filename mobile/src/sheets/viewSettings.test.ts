import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/svelte";

import ViewSettingsSheet from "./viewSettings.svelte";
import { router } from "@mobile/lib/router.svelte";
import { terminalView, type GridReport } from "@mobile/lib/terminalgeometry.svelte";
import { tiers } from "@mobile/lib/tiers.svelte";
import { DEFAULT_MODES } from "@mobile/lib/keybytes";

const report: GridReport = {
  modes: DEFAULT_MODES,
  cols: 200,
  rows: 50,
  shown: 43,
  shownRows: 22,
  capCols: 43,
  capRows: 22,
  first: 1,
  panning: true,
  font: 12,
  canFit: true,
  fitActive: false,
  fitSize: 8,
};

beforeEach(() => {
  globalThis.localStorage?.clear();
  terminalView.reset();
  terminalView.apply(report);
  terminalView.switchPane("lola-fe-42");
  tiers.set(["read", "shell"]);
  router.open("viewSettings");
});

afterEach(() => {
  router.close();
  tiers.clear();
  terminalView.reset();
});

describe("viewSettings sheet", () => {
  it("is titled Text size when the stepper is its only control", () => {
    tiers.set(["read"]);
    terminalView.apply({ ...report, canFit: false });
    render(ViewSettingsSheet);
    expect(screen.getByText("Text size")).toBeInTheDocument();
    expect(screen.queryByText("View")).toBeNull();
  });

  it("drives the live terminal's font through the registered actions", async () => {
    const setFont = vi.fn();
    terminalView.actions = { setFont, toggleFit: () => {} };
    render(ViewSettingsSheet);
    await fireEvent.click(screen.getByRole("button", { name: "Larger text" }));
    expect(setFont).toHaveBeenCalledWith(13);
  });

  it("fit closes the sheet because it changes what is behind it", async () => {
    const toggleFit = vi.fn();
    terminalView.actions = { setFont: () => {}, toggleFit };
    render(ViewSettingsSheet);
    await fireEvent.click(screen.getByRole("button", { name: /Fit the width/ }));
    expect(toggleFit).toHaveBeenCalled();
    expect(router.sheet).toBe("");
  });

  it("turns the pin on for the attached pane and remembers it", async () => {
    render(ViewSettingsSheet);
    await fireEvent.click(screen.getByRole("switch"));
    expect(terminalView.pinActive).toBe(true);
    expect(globalThis.localStorage?.getItem("lola.mobile.pinPaneSize")).toBe("1");
  });

  it("offers no pin without the shell tier", () => {
    tiers.set(["read"]);
    render(ViewSettingsSheet);
    expect(screen.queryByRole("switch")).toBeNull();
  });

  it("closes from Done", async () => {
    render(ViewSettingsSheet);
    await fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(router.sheet).toBe("");
  });
});
