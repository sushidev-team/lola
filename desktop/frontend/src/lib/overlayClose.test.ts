import { describe, it, expect, vi, beforeEach } from "vitest";
import { overlayClose } from "./overlayClose";

// overlayClose is a singleton, so a registration would leak between tests. The
// reset has to go through register+unregister with the SAME function: unregister
// is identity-checked, so handing it a fresh arrow clears nothing.
beforeEach(() => {
  const noop = () => {};
  overlayClose.register(noop);
  overlayClose.unregister(noop);
});

describe("overlayClose", () => {
  it("reports nothing to close when no form is registered", () => {
    expect(overlayClose.request()).toBe(false);
  });

  it("routes a request to the registered handler and reports it handled", () => {
    const close = vi.fn();
    overlayClose.register(close);

    expect(overlayClose.request()).toBe(true);
    expect(close).toHaveBeenCalledTimes(1);

    overlayClose.unregister(close);
    expect(overlayClose.request()).toBe(false);
  });

  it("keeps the incoming handler when an overlay swap unregisters the old one late", () => {
    // Svelte can run the outgoing form's onDestroy AFTER the incoming form's
    // onMount, so unregister is identity-checked: a stale unregister must not
    // clear the handler that replaced it.
    const oldClose = vi.fn();
    const newClose = vi.fn();
    overlayClose.register(oldClose);
    overlayClose.register(newClose); // new form mounts
    overlayClose.unregister(oldClose); // old form's late teardown

    expect(overlayClose.request()).toBe(true);
    expect(newClose).toHaveBeenCalledTimes(1);
    expect(oldClose).not.toHaveBeenCalled();

    overlayClose.unregister(newClose);
  });
});
