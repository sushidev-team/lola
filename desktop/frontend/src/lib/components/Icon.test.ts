import { cleanup, render, screen } from "@testing-library/svelte";
import { beforeEach, describe, expect, it } from "vitest";
import Icon from "./Icon.svelte";
import GlyphIcon from "./GlyphIcon.svelte";
import StateGlyph from "./StateGlyph.svelte";

beforeEach(cleanup);

describe("shared Lucide icons", () => {
  it("hides decoration while forwarding size and control styling", () => {
    const { container } = render(Icon, { name: "check", size: 14, class: "text-good", "data-icon": "check" });
    const svg = container.querySelector("svg")!;
    expect(svg).toHaveAttribute("aria-hidden", "true");
    expect(svg).toHaveAttribute("width", "14");
    expect(svg).toHaveAttribute("stroke-width", "2");
    expect(svg).toHaveClass("text-good");
    expect(svg).toHaveAttribute("data-icon", "check");
  });

  it("exposes explicitly labelled status icons to assistive technology", () => {
    render(Icon, { name: "triangle-alert", role: "img", "aria-label": "Check failed" });
    expect(screen.getByRole("img", { name: "Check failed" })).not.toHaveAttribute("aria-hidden", "true");
  });

  it("adapts shared legacy status strings without swallowing their labels or keyboard chords", () => {
    const { container, rerender } = render(GlyphIcon, { glyph: "✓ CI pass" });
    expect(container.querySelector("svg")).not.toBeNull();
    expect(screen.getByText("CI pass")).toBeInTheDocument();
    return rerender({ glyph: "⌘K" }).then(() => {
      expect(screen.getByText("⌘K")).toBeInTheDocument();
      expect(container.querySelector("svg")).toBeNull();
    });
  });

  it("uses distinct library marks for review, stopped and orphaned sessions", () => {
    const review = render(StateGlyph, { glyph: "review" });
    expect(review.container.querySelector('[data-state-badge="review"] .lucide-circle')).not.toBeNull();
    const stopped = render(StateGlyph, { glyph: "✕", label: "Gone" });
    expect(stopped.container.querySelector(".lucide-circle-minus")).not.toBeNull();
    const orphaned = render(StateGlyph, { glyph: "✕", label: "Orphaned" });
    expect(orphaned.container.querySelector(".lucide-circle-help")).not.toBeNull();
  });
});
