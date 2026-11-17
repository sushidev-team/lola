import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, waitFor } from "@testing-library/svelte";
import { conn, freshSession, ssh } from "./talk.testkit";
import ActionZone from "./ActionZone.svelte";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("the action zone", () => {
  it("handback replaces the composer", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    render(ActionZone, { sessionId: id });
    expect(document.querySelector("[data-composer]")).not.toBeNull();
    v.handback = { conn: "c", note: "", transcript: "", loading: false, truncated: false, failed: false, busy: false, since: "" };
    await waitFor(() => expect(document.querySelector("[data-composer]")).toBeNull());
    expect(document.querySelector('[aria-label^="Hand back"]')).not.toBeNull();
  });

  it("a sign-in card sits in the tray", async () => {
    const id = freshSession();
    ssh.view(id).connections = [conn("a", { state: "authenticating" })];
    render(ActionZone, { sessionId: id });
    await waitFor(() => expect(document.querySelector('[data-tray] [data-card="auth"]')).not.toBeNull());
    expect(document.querySelector("[data-composer]")).not.toBeNull();
  });

  it("a shell's question sits in the tray", async () => {
    const id = freshSession();
    ssh.view(id).connections = [conn("a", { prompt: { id: "p", stage: "command", kind: "secret", line: "Password:" } })];
    render(ActionZone, { sessionId: id });
    await waitFor(() => expect(document.querySelector('[data-tray] [data-card="prompt"]')).not.toBeNull());
  });

  it("a card on another host still shows under All", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.connections = [conn("a"), conn("b")];
    v.activeConn = "a";
    v.items = [{ id: "x", at: "2026-01-01T00:00:01Z", block: { id: "x", conn: "b", status: "pending_approval", command: "uptime" } }] as never;
    render(ActionZone, { sessionId: id });
    await waitFor(() => expect(document.querySelector('[data-tray] [data-card="approval"]')).not.toBeNull());
  });

  it("an ended connection leaves the composer, and its sentence, in a session with an agent", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.connections = [conn("a", { state: "closed", commandsRun: 2 })];
    v.scope = "a";
    render(ActionZone, { sessionId: id });
    expect(document.querySelector("[data-composer]")).not.toBeNull();
    expect(document.querySelector("[data-ended]")).toBeNull();
    expect(document.querySelector("[data-ended-note]")?.textContent).toContain("may keep running");
  });

  it("a direct session with no live host keeps the ended bar", () => {
    const id = freshSession({ location: "ssh" });
    ssh.view(id).connections = [conn("a", { state: "closed" })];
    render(ActionZone, { sessionId: id });
    expect(document.querySelector("[data-ended]")).not.toBeNull();
    expect(document.querySelector("[data-composer]")).toBeNull();
  });

  it("queue rows sit above the composer, and Edit returns the text to an empty field", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.items = [{ id: "q", human: { text: "also cover the rollback", delivered: false } }] as never;
    render(ActionZone, { sessionId: id });
    const row = document.querySelector('[data-queue-row="q"]') as HTMLElement;
    expect(row.compareDocumentPosition(document.querySelector("[data-composer]")!) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(row.className).not.toMatch(/border/);
    (row.querySelector("button") as HTMLButtonElement).click();
    await waitFor(() => expect(v.agentDraft).toBe("also cover the rollback"));
  });
});
