import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/svelte";
import ButtonHarness from "./ButtonHarness.test.svelte";

// The loading state is a CONTROL affordance, not decoration: the actions behind
// these buttons are not idempotent (activating a session stops another
// session's dev servers), so "in flight" has to be visible AND has to stop a
// second click.
describe("Button loading", () => {
  beforeEach(cleanup);

  it("disables the button and announces it as busy", () => {
    render(ButtonHarness, { loading: true });
    const btn = screen.getByRole("button", { name: /Active/ });
    expect(btn).toBeDisabled();
    expect(btn).toHaveAttribute("aria-busy", "true");
    // Disabled, but NOT wearing the 40% of a dead control — it is working.
    expect(btn.className).toContain("disabled:opacity-100!");
  });

  it("draws a spinner ahead of the label", () => {
    render(ButtonHarness, { loading: true });
    const spinner = screen.getByRole("button", { name: /Active/ }).firstElementChild;
    expect(spinner?.className).toContain("animate-spin");
    // It slows under reduced motion rather than freezing: a still spinner reads
    // as a hung app, which is the opposite of the message.
    expect(spinner?.className).toContain("motion-reduce:animate-[spin_1.8s_linear_infinite]");
  });

  it("is an ordinary enabled button when it is not loading", () => {
    render(ButtonHarness, { loading: false });
    const btn = screen.getByRole("button", { name: /Active/ });
    expect(btn).toBeEnabled();
    expect(btn).not.toHaveAttribute("aria-busy");
    expect(btn.querySelector(".animate-spin")).toBeNull();
  });

  // A call site's own disabled must survive: loading only ever ADDS the state.
  it("keeps a call site's disabled while not loading", () => {
    render(ButtonHarness, { loading: false, disabled: true });
    expect(screen.getByRole("button", { name: /Active/ })).toBeDisabled();
  });
});
