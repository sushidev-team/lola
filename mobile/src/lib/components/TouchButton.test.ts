import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/svelte";
import TouchButtonHarness from "./TouchButtonHarness.test.svelte";

describe("TouchButton", () => {
  it("is 44pt by default", () => {
    render(TouchButtonHarness);
    expect(screen.getByRole("button", { name: "Go" }).className).toContain("h-11!");
  });

  it("primary is one filled style: primary fill under the measured label", () => {
    render(TouchButtonHarness, { props: { variant: "primary" } });
    const cls = screen.getByRole("button", { name: "Go" }).className;
    for (const c of ["bg-primary-fill!", "text-on-primary!", "border-transparent!", "disabled:opacity-40!"]) {
      expect(cls).toContain(c);
    }
  });

  it("secondary stays tinted, not filled", () => {
    render(TouchButtonHarness, { props: { variant: "secondary" } });
    expect(screen.getByRole("button", { name: "Go" }).className).not.toContain("bg-accent!");
  });

  it("prominent is a 50pt, 12pt-radius, full-width callout 600 CTA", () => {
    render(TouchButtonHarness, { props: { variant: "primary", prominent: true } });
    const cls = screen.getByRole("button", { name: "Go" }).className;
    for (const c of ["h-[50px]!", "rounded-[12px]!", "w-full!", "text-callout!", "font-semibold!"]) {
      expect(cls).toContain(c);
    }
  });

  it("dims when disabled", () => {
    render(TouchButtonHarness, { props: { variant: "primary", disabled: true } });
    expect(screen.getByRole("button", { name: "Go" })).toBeDisabled();
  });
});
