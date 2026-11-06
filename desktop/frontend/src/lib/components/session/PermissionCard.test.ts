import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { freshSession, permission, setTalk, ssh, talk, talkSvc, terms, AGENT } from "./talk.testkit";
import PermissionCard from "./PermissionCard.svelte";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.clearAllMocks();
});

const armed = () => vi.advanceTimersByTimeAsync(410);
const show = (id: string, p = permission(), narrow = false) => render(PermissionCard, { sessionId: id, prompt: p as never, narrow });
const card = () => document.querySelector("[data-card]") as HTMLElement;

describe("the permission card", () => {
  it("draws labels verbatim in pane order", () => {
    const id = freshSession();
    show(id);
    const rows = [...document.querySelectorAll("[data-choice]")].map((b) => b.getAttribute("data-choice"));
    expect(rows).toEqual(["1", "2", "3", "4"]);
    const labels = [...document.querySelectorAll("[data-choice]")].map((b) => b.querySelector("span:nth-child(2)")?.textContent);
    expect(labels).toEqual(permission().choices.map((c) => c.label));
    expect(card().textContent).toContain('psql "$TEST_DB" -f migrations/0042_tenants.sql');
  });

  it("primary only on an exact Yes", () => {
    const id = freshSession();
    show(id);
    expect([...document.querySelectorAll("[data-primary]")].map((b) => b.getAttribute("data-choice"))).toEqual(["1"]);
    cleanup();
    show(id, permission({ choices: [{ key: "1", label: "Yes, and always allow" }, { key: "2", label: "No" }] }));
    expect(document.querySelectorAll("[data-primary]").length).toBe(0);
  });

  it("keys dead for 400ms", async () => {
    vi.useFakeTimers();
    const id = freshSession();
    show(id);
    await fireEvent.keyDown(card(), { key: "1" });
    await vi.advanceTimersByTimeAsync(300);
    await fireEvent.keyDown(card(), { key: "1" });
    expect(talkSvc.Choose).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(110);
    await fireEvent.keyDown(card(), { key: "1" });
    expect(talkSvc.Choose).toHaveBeenCalledTimes(1);
  });

  it("re-arms when the text changes, and not when a poll replaces the object", async () => {
    vi.useFakeTimers();
    const id = freshSession();
    const r = show(id);
    await armed();
    await r.rerender({ sessionId: id, prompt: permission() as never });
    await fireEvent.keyDown(card(), { key: "4" });
    expect(talkSvc.Choose).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(0);
    talkSvc.Choose.mockClear();
    await r.rerender({ sessionId: id, prompt: permission({ hash: "hash-2", lines: ["rm -rf /"] }) as never });
    await fireEvent.keyDown(card(), { key: "1" });
    expect(talkSvc.Choose).not.toHaveBeenCalled();
  });

  it("repeat ignored", async () => {
    vi.useFakeTimers();
    const id = freshSession();
    show(id);
    await armed();
    await fireEvent.keyDown(card(), { key: "1", repeat: true });
    expect(talkSvc.Choose).not.toHaveBeenCalled();
  });

  it("presses once, however many keys follow", async () => {
    vi.useFakeTimers();
    const id = freshSession();
    show(id);
    await armed();
    await fireEvent.keyDown(card(), { key: "1" });
    await fireEvent.keyDown(card(), { key: "1" });
    expect(talkSvc.Choose).toHaveBeenCalledTimes(1);
  });

  it("Enter does nothing", async () => {
    vi.useFakeTimers();
    const id = freshSession();
    show(id);
    await armed();
    const notPrevented = await fireEvent.keyDown(card(), { key: "Enter" });
    expect(notPrevented).toBe(false);
    expect(talkSvc.Choose).not.toHaveBeenCalled();
  });

  it("ignores a digit the pane did not offer", async () => {
    vi.useFakeTimers();
    const id = freshSession();
    show(id);
    await armed();
    await fireEvent.keyDown(card(), { key: "7" });
    expect(talkSvc.Choose).not.toHaveBeenCalled();
  });

  it("long input arms only at its end", async () => {
    vi.useFakeTimers();
    const id = freshSession();
    const top = vi.spyOn(HTMLSpanElement.prototype, "getBoundingClientRect").mockReturnValue({ top: 900 } as DOMRect);
    const bottom = vi.spyOn(HTMLDivElement.prototype, "getBoundingClientRect").mockReturnValue({ bottom: 300 } as DOMRect);
    show(id, permission({ lines: Array.from({ length: 200 }, (_, i) => `line ${i}`) }));
    await armed();
    expect(document.querySelector("[data-read-hint]")).not.toBeNull();
    await fireEvent.keyDown(card(), { key: "1" });
    expect(talkSvc.Choose).not.toHaveBeenCalled();
    expect((document.querySelector('[data-choice="1"]') as HTMLButtonElement).disabled).toBe(true);
    top.mockReturnValue({ top: 200 } as DOMRect);
    await fireEvent.scroll(document.querySelector("[data-card-body]")!);
    await fireEvent.keyDown(card(), { key: "1" });
    expect(talkSvc.Choose).toHaveBeenCalledTimes(1);
    top.mockRestore();
    bottom.mockRestore();
  });

  it("sends the hash", async () => {
    vi.useFakeTimers();
    const id = freshSession();
    show(id);
    await armed();
    await fireEvent.click(document.querySelector('[data-choice="4"]')!);
    expect(talkSvc.Choose).toHaveBeenCalledWith({ session: id, hash: "hash-1", key: "4" });
  });

  it("stale reply redraws", async () => {
    const id = freshSession();
    const next = permission({ hash: "hash-2", lines: ["Bash command", "rm -rf build", "Do you want to proceed?"] });
    talkSvc.Choose.mockResolvedValue({ sent: false, why: "The prompt changed. Read it again.", prompt: next });
    await setTalk(id, { composer: "prompt", prompt: permission() });
    talkSvc.State.mockResolvedValue({ session: id, caps: {}, composer: "prompt", prompt: next, feedSeq: 0, health: "ok" });
    const before = talk.state(id)!.prompt!;
    show(id, before as never);
    await new Promise((r) => setTimeout(r, 420));
    await fireEvent.click(document.querySelector('[data-choice="1"]')!);
    await waitFor(() => expect(talk.state(id)?.prompt?.hash).toBe("hash-2"));
    expect(document.querySelector("[data-card-refusal]")?.textContent).toBe("The prompt changed. Read it again.");
  });

  it("a press that landed clears the prompt at once", async () => {
    const id = freshSession();
    await setTalk(id, { composer: "prompt", prompt: permission() });
    talkSvc.State.mockResolvedValue({ session: id, caps: {}, composer: "working", feedSeq: 0, health: "ok" });
    await talk.choose(id, "hash-1", "1");
    expect(talk.state(id)?.prompt).toBeUndefined();
  });

  it("a poll asked before the press cannot bring the card back", async () => {
    const id = freshSession();
    await setTalk(id, { composer: "prompt", prompt: permission() });
    let late: (v: unknown) => void = () => {};
    talkSvc.State.mockReturnValueOnce(new Promise((r) => (late = r)));
    const poll = talk.refresh(id);
    await talk.choose(id, "hash-1", "1");
    late({ session: id, caps: {}, composer: "prompt", prompt: permission(), feedSeq: 0, health: "ok" });
    await poll;
    expect(talk.state(id)?.prompt).toBeUndefined();
  });

  it("carries no lock or logo, and offers the terminal", async () => {
    const id = freshSession();
    show(id);
    expect(card().textContent).not.toMatch(/🔒|verified|trusted/i);
    expect(card().querySelector("img,svg")).toBeNull();
    const select = vi.spyOn(terms, "select");
    await fireEvent.click(screen.getByRole("button", { name: /Terminal/ }));
    expect(select).toHaveBeenCalledWith(id, AGENT);
    select.mockRestore();
  });

  it("Esc returns to the composer", async () => {
    const id = freshSession();
    const v = ssh.view(id);
    show(id);
    const before = v.composerFocus;
    await fireEvent.keyDown(card(), { key: "Escape" });
    expect(v.composerFocus).toBe(before + 1);
  });

  it("narrow keeps Yes and No in view, and shows the rest in full under More", async () => {
    const id = freshSession();
    show(id, permission(), true);
    expect([...document.querySelectorAll("[data-choice]")].map((b) => b.getAttribute("data-choice"))).toEqual(["1", "4"]);
    await fireEvent.click(document.querySelector("[data-choice-more]")!);
    expect([...document.querySelectorAll("[data-choice]")].map((b) => b.getAttribute("data-choice"))).toEqual(["1", "4", "2", "3"]);
    expect(card().textContent).toContain("Yes, and always allow access to migrations/ from this project");
  });
});
