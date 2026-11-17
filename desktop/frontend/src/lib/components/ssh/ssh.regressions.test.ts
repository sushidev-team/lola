import { describe, it, expect, vi } from "vitest";
import { SSHBlock, SSHElement, SSHApproval, SSHGoCard, SSHConnCard, SSHEnded, Timeline, Tray, Workspace, svc, ssh, SID, conn, block, el, useSession, seed, useSSHKit } from "./ssh.testkit";
import { confirm } from "$lib/confirm.svelte";
import { tick } from "svelte";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

useSSHKit();

describe("review regressions", () => {
  const pending = { id: "b1", kind: "block", order: 1, seq: 1, at: "", block: block({ status: "pending_approval", approval: { granted: false } }) };
  const approvalProps = (o: Record<string, unknown> = {}) => ({
    sessionId: SID,
    conn: conn() as never,
    block: block({ status: "pending_approval", approval: { granted: false }, ...o }) as never,
  });

  it("mounts the workspace for a session nothing has viewed yet", () => {
    // No seed() and no ssh.view: the first call is the one inside $derived, where a
    // reactive write would throw state_unsafe_mutation.
    useSession({ location: "ssh" });
    const { container } = render(Workspace, { sessionId: SID });
    expect(container.querySelector("[data-ssh-workspace]")).not.toBeNull();
  });

  it("does not answer on a held key", async () => {
    vi.useFakeTimers();
    seed([]);
    const { container } = render(SSHApproval, approvalProps());
    await vi.advanceTimersByTimeAsync(800);
    const card = container.querySelector("[data-card]") as HTMLElement;
    await fireEvent.keyDown(card, { key: "Y", repeat: true });
    expect(svc.Approve).not.toHaveBeenCalled();
    await fireEvent.keyDown(card, { key: "Y" });
    expect(svc.Approve).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });

  it("does not give a go on a held key", async () => {
    vi.useFakeTimers();
    seed([]);
    const { container } = render(SSHGoCard, { sessionId: SID, conn: conn({ state: "requested" }) as never });
    await vi.advanceTimersByTimeAsync(800);
    await fireEvent.keyDown(container.querySelector("[data-card]") as HTMLElement, { key: "Y", repeat: true });
    expect(svc.Go).not.toHaveBeenCalled();
    vi.useRealTimers();
  });

  it("holds the confirm until the end of the command has been on screen", async () => {
    vi.useFakeTimers();
    seed([]);
    const top = vi.spyOn(HTMLSpanElement.prototype, "getBoundingClientRect").mockReturnValue({ top: 900, bottom: 900 } as DOMRect);
    const bottom = vi.spyOn(HTMLDivElement.prototype, "getBoundingClientRect").mockReturnValue({ top: 0, bottom: 300 } as DOMRect);
    const long = Array.from({ length: 20 }, (_, i) => `line${i}`).join("\n");
    const { container } = render(SSHApproval, approvalProps({ command: long }));
    await vi.advanceTimersByTimeAsync(800);
    const run = screen.getByRole("button", { name: /Run once/ });
    expect(run).toBeDisabled();
    expect(screen.getByText(/Scroll to the end to run this/)).toBeInTheDocument();
    await fireEvent.keyDown(container.querySelector("[data-card]") as HTMLElement, { key: "Y" });
    expect(svc.Approve).not.toHaveBeenCalled();
    top.mockReturnValue({ top: 280, bottom: 290 } as DOMRect);
    await fireEvent.scroll(container.querySelector("[data-card-body]") as HTMLElement);
    expect(run).not.toBeDisabled();
    top.mockRestore();
    bottom.mockRestore();
    vi.useRealTimers();
  });

  it("pins the decision outside the card's scroller, and has only one scroller", () => {
    seed([]);
    const { container } = render(SSHApproval, approvalProps({ command: "x\n".repeat(40) }));
    const body = container.querySelector("[data-card-body]") as HTMLElement;
    const actions = container.querySelector("[data-card-actions]") as HTMLElement;
    expect(body.contains(screen.getByRole("button", { name: /Deny/ }))).toBe(false);
    expect(actions.contains(screen.getByRole("button", { name: /Run once/ }))).toBe(true);
    expect((container.querySelector("[data-command]") as HTMLElement).className).not.toContain("overflow-y");
    // The command leads. The small print is folded below it, and does not gate the confirm.
    expect(body.querySelector("[data-warnings]")?.compareDocumentPosition(body.querySelector("[data-command]") as Node)).toBe(
      Node.DOCUMENT_POSITION_PRECEDING,
    );
  });

  it("gives focus to the card, so its keys work after A", async () => {
    vi.useFakeTimers();
    const v = seed([pending]);
    render(Tray, { sessionId: SID });
    v.dockFocus++;
    await vi.advanceTimersByTimeAsync(800);
    const card = document.querySelector("[data-card]") as HTMLElement;
    expect(document.activeElement).toBe(card);
    await fireEvent.keyDown(document.activeElement as HTMLElement, { key: "Y" });
    expect(svc.Approve).toHaveBeenCalledTimes(1);
    vi.useRealTimers();
  });

  it("does not repeat the transcript sentence the daemon already wrote", () => {
    seed([
      {
        id: "s1",
        kind: "system",
        order: 1,
        seq: 1,
        at: "",
        system: { kind: "handed_back", text: "You handed the wheel back after 12 minutes. Transcript sent.", hasTranscript: true, conn: "c1" },
      },
    ]);
    render(Timeline, { sessionId: SID });
    expect(screen.getByRole("log").textContent?.match(/Transcript sent\./g)).toHaveLength(1);
  });


  it("folds a transcript behind the shared disclosure, with its since-time", async () => {
    const at = "2026-01-01T14:02:00Z";
    seed([
      { id: "s1", kind: "system", order: 1, seq: 1, at, system: { kind: "wheel_taken", text: "x", conn: "c1" } },
      { id: "h1", kind: "human", order: 2, seq: 2, at, human: { origin: "handback", conn: "c1", text: "done", transcript: "uptime", delivered: true } },
    ]);
    const { container } = render(Timeline, { sessionId: SID });
    expect(container.querySelector("details")).toBeNull();
    const btn = screen.getByRole("button", { name: /Your terminal since .* · 6 B/ });
    expect(btn).toHaveAttribute("aria-expanded", "false");
    await fireEvent.click(btn);
    expect(btn).toHaveAttribute("aria-expanded", "true");
  });

  it("the auth card shows a parked terminal under the narrow split", async () => {
    const v = seed([]);
    v.narrow = "stream";
    render(SSHConnCard, { sessionId: SID, conn: conn({ state: "authenticating" }) as never, kind: "auth" });
    const before = v.terminalFocus;
    await fireEvent.click(screen.getByRole("button", { name: "Open terminal" }));
    expect(v.narrow).toBe("terminal");
    expect(v.terminalFocus).toBe(before + 1);
  });

  it("authentication starting leaves the timeline where it is", async () => {
    useSession({ ssh: { connections: [conn({ state: "authenticating" })], lastSeq: 0 } });
    const v = seed([], [conn({ state: "authenticating" })]);
    v.narrow = "stream";
    render(Workspace, { sessionId: SID });
    await tick();
    expect(v.narrow).toBe("stream");
  });

  it("uses the singular for one lost command", () => {
    seed([]);
    render(SSHConnCard, { sessionId: SID, conn: conn({ state: "disconnected" }) as never, kind: "reconnect", running: 1 });
    expect(screen.getByText(/1 command was running\. Its result is unknown, and it may still be running/)).toBeInTheDocument();
  });

  it("the ended bar does not talk about stopping", () => {
    seed([], [conn({ state: "closed", commandsRun: 2 })]);
    render(SSHEnded, { sessionId: SID });
    expect(screen.getByText("The remote process may keep running if any were still going.")).toBeInTheDocument();
    expect(screen.queryByText(/Stopping ends/)).toBeNull();
  });

  it("tells the story of a command in its info panel", async () => {
    const v = seed([]);
    v.expanded.add("b1");
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ startedAt: "2026-01-01T00:00:00Z", endedAt: "2026-01-01T00:00:01Z", durationMs: 1000 }) as never });
    await fireEvent.click(screen.getByRole("button", { name: "About this command" }));
    expect(container.querySelector("[data-info-result]")?.textContent).toContain("Exit 0");
    expect(container.querySelector("[data-info-elapsed]")?.textContent).toBe("1.0s");
    expect(container.querySelector("[data-info-where]")?.textContent).toContain("prod-api");
    expect(container.querySelector("[data-info-who]")?.textContent).toMatch(/Claude asked/);
  });
  it("says a failure and your own command plainly in the info panel", async () => {
    const v = seed([]);
    v.expanded.add("b1");
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ status: "failed", exitCode: 3, actor: "human", origin: "terminal" }) as never });
    await fireEvent.click(screen.getByRole("button", { name: "About this command" }));
    expect(container.querySelector("[data-info-result]")?.textContent).toContain("Exit 3");
    expect(container.querySelector("[data-info-who]")?.textContent).toContain("You ran this in your shell");
  });

  it("wraps why once open, and never clips lola's own status sentence", () => {
    const v = seed([]);
    const { container, unmount } = render(SSHBlock, { sessionId: SID, block: block({ status: "stopped", startedAt: "2026-01-01T00:00:00Z", why: "because" }) as never });
    const clipped = [...container.querySelectorAll("button span")].find((n) => n.textContent?.includes("may keep running")) as HTMLElement;
    expect(clipped.className).not.toContain("truncate");
    // Plan 2.5: the why is a claim, so it left the plate. It sits above it on the ground and wraps
    // at every depth, where it used to clip until the block was opened.
    const closedWhy = container.querySelector('[data-claim="why"].selectable') as HTMLElement;
    expect(closedWhy.textContent).toContain("because");
    expect(closedWhy.className).not.toContain("truncate");
    expect(closedWhy.className).toContain("break-words");
    unmount();
    v.expanded.add("b1");
    const open = render(SSHBlock, { sessionId: SID, block: block({ why: "because" }) as never });
    const openWhy = open.container.querySelector('[data-claim="why"].selectable') as HTMLElement;
    expect(openWhy.className).not.toContain("truncate");
    expect(openWhy.className).toContain("break-words");
  });

  it("keeps a finished command to one quiet line, and still says when it did not work", () => {
    seed([]);
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ actor: "human", preview: "secret tail" }) as never });
    const row = container.querySelector("button")?.textContent ?? "";
    // Plan 2.5: a plate always shows its exit at the right edge, and bubbles no longer say whose it
    // is, so the word does. What stays pinned is the point of this test: a success shows no output.
    expect(row).toMatch(/exit 0 · 0\.4s/);
    expect(row).not.toMatch(/secret tail/);
    cleanup();
    const failed = render(SSHBlock, { sessionId: SID, block: block({ status: "failed", exitCode: 3 }) as never });
    expect(failed.container.querySelector("button")?.textContent).toMatch(/exit 3/);
  });

  it("folds an open block from its whole upper section, but not from its actions or a selection", async () => {
    const v = seed([]);
    v.expanded.add("b1");
    const { container } = render(SSHBlock, { sessionId: SID, block: block({ actor: "human", private: true }) as never });
    await fireEvent.click(screen.getByRole("button", { name: "Share with Claude" }));
    expect(v.expanded.has("b1")).toBe(true);
    const sel = vi.spyOn(window, "getSelection").mockReturnValue({ toString: () => "picked" } as never);
    await fireEvent.click(container.querySelector("[data-output-head]") as HTMLElement);
    expect(v.expanded.has("b1")).toBe(true);
    sel.mockRestore();
    await fireEvent.click(container.querySelector("[data-output-head]") as HTMLElement);
    expect(v.expanded.has("b1")).toBe(false);
  });

  it("the story lets the session's keys through, and keeps the ones it uses", async () => {
    seed([{ id: "b1", kind: "block", order: 1, seq: 1, at: "", block: block() }]);
    render(Timeline, { sessionId: SID });
    const seen: string[] = [];
    const spy = (e: KeyboardEvent) => seen.push(e.key);
    window.addEventListener("keydown", spy);
    for (const key of ["W", "A", "?", "x", "j"]) await fireEvent.keyDown(screen.getByRole("log"), { key });
    window.removeEventListener("keydown", spy);
    expect(seen).toEqual(["W", "A", "?", "x"]);
  });

  it("brings a proposal's run check to the human and hands focus back on cancel", async () => {
    seed([]);
    const into = vi.fn();
    const original = Element.prototype.scrollIntoView;
    Element.prototype.scrollIntoView = into;
    render(SSHElement, { sessionId: SID, element: el({ kind: "proposal", title: "Restart", command: "x", state: "open" }) as never });
    await fireEvent.click(screen.getByRole("button", { name: /Run on/ }));
    await waitFor(() => expect(document.activeElement).toBe(document.querySelector("[data-card]")));
    expect(into).toHaveBeenCalledWith({ block: "nearest" });
    await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: /Run on/ })));
    Element.prototype.scrollIntoView = original;
  });

});
