import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";

const load = vi.fn();
vi.mock("$lib/changes.svelte", () => ({ changes: { load: (id: string) => load(id) } }));
vi.mock("$lib/components/session/ChangesView.svelte", async () => await import("./Stub.test.svelte"));

import { tick } from "svelte";
import ChangesSegment, { CHANGES_DEBOUNCE_MS } from "./ChangesSegment.svelte";
import { setPushSource } from "$lib/pushsource";
import { store } from "$lib/store.svelte";
import { feedback } from "$lib/feedback.svelte";
import { router } from "@mobile/lib/router.svelte";

describe("ChangesSegment", () => {
  let fire: (rev: number) => void;
  let refuse: (e: string) => void;
  const stop = vi.fn();
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
    setPushSource({
      diff: (_id: string, on: (rev: number) => void, onError: (e: string) => void) => {
        fire = on;
        refuse = onError;
        return stop;
      },
    } as never);
  });
  afterEach(() => {
    vi.useRealTimers();
    setPushSource(null);
    vi.clearAllMocks();
  });

  it("re-reads the totals once, after the debounce, for a burst of revisions", () => {
    render(ChangesSegment, { props: { id: "s1" } });
    fire(1);
    fire(2);
    vi.advanceTimersByTime(CHANGES_DEBOUNCE_MS - 1);
    expect(load).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1);
    expect(load).toHaveBeenCalledExactlyOnceWith("s1");
  });

  it("unwatches when unmounted and drops a pending read", () => {
    const { unmount } = render(ChangesSegment, { props: { id: "s1" } });
    fire(1);
    unmount();
    expect(stop).toHaveBeenCalledOnce();
    vi.advanceTimersByTime(CHANGES_DEBOUNCE_MS * 2);
    expect(load).not.toHaveBeenCalled();
  });

  it("re-reads on every agent state change once the topic is refused", async () => {
    store.sessions = [{ id: "s1", agentState: "working" } as never];
    render(ChangesSegment, { props: { id: "s1" } });
    refuse("limit");
    await tick();
    expect(load).toHaveBeenCalledExactlyOnceWith("s1");
    store.sessions = [{ id: "s1", agentState: "idle" } as never];
    await tick();
    expect(load).toHaveBeenCalledTimes(2);
  });

  it("reads nothing more for a session the daemon does not know", async () => {
    store.sessions = [{ id: "s1", agentState: "working" } as never];
    render(ChangesSegment, { props: { id: "s1" } });
    refuse("unknown_session");
    store.sessions = [{ id: "s1", agentState: "idle" } as never];
    await tick();
    expect(load).not.toHaveBeenCalled();
  });

  it("shows the feedback button only while there are drafts, and opens the sheet", async () => {
    vi.useRealTimers();
    feedback.clear("s1");
    render(ChangesSegment, { props: { id: "s1" } });
    expect(screen.queryByRole("button", { name: /Send feedback/ })).toBeNull();
    feedback.add("s1", { path: "a.go", side: "new" as never, line: 1, endLine: 1, quote: "", body: "x" });
    await tick();
    await fireEvent.click(await screen.findByRole("button", { name: "Send feedback (1)" }));
    expect(router.sheet).toBe("feedback");
    feedback.clear("s1");
  });

  it("hints once at zero drafts and never again after the first draft", () => {
    try { localStorage.removeItem("lola.changes.draftHintDone"); } catch { /* ignore */ }
    feedback.clear("s1");
    const first = render(ChangesSegment, { props: { id: "s1" } });
    expect(document.querySelector("[data-drafts-hint]")).toHaveTextContent("Comment on a line to send feedback.");
    first.unmount();
    feedback.add("s1", { path: "a.go", side: "new" as never, line: 1, endLine: 1, quote: "", body: "x" });
    render(ChangesSegment, { props: { id: "s1" } });
    expect(document.querySelector("[data-drafts-hint]")).toBeNull();
    feedback.clear("s1");
    cleanup();
    render(ChangesSegment, { props: { id: "s1" } });
    expect(document.querySelector("[data-drafts-hint]")).toBeNull();
    try { localStorage.removeItem("lola.changes.draftHintDone"); } catch { /* ignore */ }
  });
});
