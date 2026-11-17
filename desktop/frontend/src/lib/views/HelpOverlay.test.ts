import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";
import HelpOverlay from "./HelpOverlay.svelte";

// HelpOverlay is a static cheat-sheet (nav singleton + Modal only, no Wails
// bridge), so it renders straight under jsdom.
describe("HelpOverlay", () => {
  it("renders the grouped shortcut reference", () => {
    render(HelpOverlay);

    expect(screen.getByText("Navigation")).toBeInTheDocument();
    expect(screen.getByText("Session")).toBeInTheDocument();
    expect(screen.getByText("Project Git")).toBeInTheDocument();
    expect(screen.getByText("Focused controls")).toBeInTheDocument();
    expect(screen.getByText("⌘⇧G")).toBeInTheDocument();
    expect(screen.getByText("Git workspace / previous view")).toBeInTheDocument();
    expect(screen.getByText("⌘B")).toBeInTheDocument();
    expect(screen.getByText("⌘⇧E")).toBeInTheDocument();
    expect(screen.queryByText("x · ⌘⇧K")).not.toBeInTheDocument();
    expect(screen.queryByText("j / k · g / G")).not.toBeInTheDocument();

    // The overlay is a labelled modal dialog (focus-trapped, esc-closable).
    expect(screen.getByRole("dialog", { name: "Keyboard shortcuts" })).toBeInTheDocument();
  });
});
