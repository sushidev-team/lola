import { describe, it, expect, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";
import PushErrorBanner from "./PushErrorBanner.svelte";
import { store } from "$lib/store.svelte";

// The 2s push path used to swallow per-command errors, so an out-of-date daemon
// silently blanked reads (the Rail, status). The banner surfaces it.
describe("PushErrorBanner", () => {
  beforeEach(() => {
    cleanup();
    store.pushErrors = {};
  });

  it("renders nothing when there is no push error", () => {
    render(PushErrorBanner);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("surfaces the failing command and message when a push errors", () => {
    store.pushErrors = { projects: 'unknown cmd "projects"' };
    render(PushErrorBanner);
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.getByText(/out of date/i)).toBeInTheDocument();
    expect(screen.getByText(/unknown cmd "projects"/)).toBeInTheDocument();
    // Only the version-skew case offers the restart, because only it is fixed
    // by restarting.
    expect(screen.getByRole("button", { name: /restart/i })).toBeInTheDocument();
  });

  // A LIVE daemon failing a command it DOES know is a real error, not a version
  // skew — diagnosing it as "out of date" would send the user after the wrong
  // thing, and restarting would not fix it.
  it("does not blame the version when the error isn't an unknown command", () => {
    store.pushErrors = { sessions: "gh: rate limit exceeded" };
    render(PushErrorBanner);
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.queryByText(/out of date/i)).not.toBeInTheDocument();
    expect(screen.getByText(/gh: rate limit exceeded/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /restart/i })).not.toBeInTheDocument();
  });

  it("dismisses on the ✕ button and stays dismissed", async () => {
    store.pushErrors = { projects: "boom" };
    render(PushErrorBanner);
    expect(screen.getByRole("alert")).toBeInTheDocument();
    await fireEvent.click(screen.getByLabelText("dismiss"));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(store.pushErrors).toEqual({});
  });
});
