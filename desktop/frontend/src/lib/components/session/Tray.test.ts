import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { conn, freshSession, permission, setTalk, ssh } from "./talk.testkit";
import Tray from "./Tray.svelte";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const approval = (id: string, c: string, at: string) => ({ id, at, block: { id, conn: c, status: "pending_approval", command: `cmd ${id}`, createdAt: at } }) as never;

describe("the tray", () => {
  it("a card never takes focus by appearing", async () => {
    const id = freshSession();
    const other = document.body.appendChild(document.createElement("textarea"));
    other.focus();
    render(Tray, { sessionId: id });
    await setTalk(id, { composer: "prompt", prompt: permission() });
    await waitFor(() => expect(document.querySelector('[data-card="permission"]')).not.toBeNull());
    expect(document.activeElement).toBe(other);
    other.blur();
    other.remove();
    // With nothing holding focus it does not take it either.
    expect(document.activeElement).toBe(document.body);
  });

  it("oldest first, stable", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.connections = [conn("a")];
    v.items = [approval("late", "a", "2026-01-01T00:00:05Z"), approval("early", "a", "2026-01-01T00:00:01Z")];
    render(Tray, { sessionId: id });
    await waitFor(() => expect(document.querySelector("[data-command]")?.textContent).toContain("cmd early"));
    expect(screen.getByText("1 of 2")).toBeTruthy();
    // A newer one arriving changes neither the card nor its place.
    v.items = [...v.items, approval("later", "a", "2026-01-01T00:00:09Z")];
    await waitFor(() => expect(screen.getByText("1 of 3")).toBeTruthy());
    expect(document.querySelector("[data-command]")?.textContent).toContain("cmd early");
    await fireEvent.click(screen.getByRole("button", { name: "Newer card" }));
    await waitFor(() => expect(document.querySelector("[data-command]")?.textContent).toContain("cmd late"));
    expect(screen.getByText("2 of 3")).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Older card" }));
    await waitFor(() => expect(document.querySelector("[data-command]")?.textContent).toContain("cmd early"));
  });

  it("⌘J focuses it", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    render(Tray, { sessionId: id });
    await setTalk(id, { composer: "prompt", prompt: permission() });
    await waitFor(() => expect(document.querySelector('[data-card="permission"]')).not.toBeNull());
    // ⌘J lands by moving this counter: see ssh.nextWaiting.
    v.dockFocus++;
    await waitFor(() => expect(document.activeElement).toBe(document.querySelector('[data-card="permission"]')));
  });

  it("no role alert on the wrapper", async () => {
    const id = freshSession();
    render(Tray, { sessionId: id });
    await setTalk(id, { composer: "prompt", prompt: permission() });
    await waitFor(() => expect(document.querySelector('[data-card="permission"]')).not.toBeNull());
    expect(document.querySelector('[role="alert"]')).toBeNull();
  });

  it("announces one short line through status", async () => {
    const id = freshSession();
    render(Tray, { sessionId: id });
    await setTalk(id, { composer: "prompt", prompt: permission() });
    await waitFor(() => expect(document.querySelector("[data-tray-status]")?.textContent).toBe("Claude is asking for permission."));
    expect(document.querySelector("[data-tray-status]")?.getAttribute("role")).toBe("status");
  });

  it("counts only tier 1, and shows an agent's own question after them", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    v.connections = [conn("a")];
    v.items = [{ id: "q", at: "2026-01-01T00:00:00Z", element: { id: "q", kind: "question", title: "Which one?", revision: 1 } }, approval("b", "a", "2026-01-01T00:00:09Z")] as never;
    render(Tray, { sessionId: id });
    await waitFor(() => expect(document.querySelector("[data-command]")).not.toBeNull());
    expect(document.querySelector("[data-tray-pager]")).toBeNull();
    v.items = [v.items[0]];
    await waitFor(() => expect(document.querySelector("[data-tray-claim]")?.textContent).toContain("Which one?"));
  });

  it("shows a dev clash last, and neutral", async () => {
    const clash = { port: 8000, pid: 1, proc: "node", command: "npm run dev" };
    const id = freshSession({ devClash: clash });
    render(Tray, { sessionId: id });
    await waitFor(() => expect(document.querySelector('[data-card="dev-clash"]')).not.toBeNull());
    expect(document.querySelector('[data-card="dev-clash"]')!.className).not.toMatch(/warn|orange/);
    await setTalk(id, { composer: "prompt", prompt: permission() });
    await waitFor(() => expect(document.querySelector('[data-card="permission"]')).not.toBeNull());
    expect(document.querySelector('[data-card="dev-clash"]')).toBeNull();
  });
});
