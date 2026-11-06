import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";
import HelpOverlay from "./HelpOverlay.svelte";

// HelpOverlay is a static cheat-sheet (nav singleton + Modal only, no Wails
// bridge), so it renders straight under jsdom.
describe("HelpOverlay", () => {
  it("renders the grouped shortcut reference", () => {
    render(HelpOverlay);

    // The three sections.
    expect(screen.getByText("Overview")).toBeInTheDocument();
    expect(screen.getByText("In a session")).toBeInTheDocument();
    expect(screen.getByText("Session actions")).toBeInTheDocument();
    expect(screen.getByText("Global")).toBeInTheDocument();

    // A representative key from each group — the ones the trimmed footer drops.
    expect(screen.getByText("open session")).toBeInTheDocument();
    expect(screen.getByText("cycle lens · list / board / terminals")).toBeInTheDocument();
    expect(screen.getByText("revive dead session")).toBeInTheDocument();
    // Provider-agnostic wording, and the Session-menu chord next to the bare key.
    expect(screen.getByText("review · configured provider")).toBeInTheDocument();
    expect(screen.getByText("c · ⌘⇧R")).toBeInTheDocument();

    // The chords that lost their connection gate, and the new ones.
    expect(screen.getByText("h · ⌘\\")).toBeInTheDocument();
    expect(screen.getByText("⌘J")).toBeInTheDocument();
    expect(screen.getByText("⌘.")).toBeInTheDocument();
    expect(screen.getByText("f · ⌘⇧D")).toBeInTheDocument();

    // The overlay is a labelled modal dialog (focus-trapped, esc-closable).
    expect(screen.getByRole("dialog", { name: "Keyboard shortcuts" })).toBeInTheDocument();
  });
});
