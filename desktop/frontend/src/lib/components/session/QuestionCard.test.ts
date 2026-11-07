import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { freshSession, talkSvc, terms, AGENT } from "./talk.testkit";
import QuestionCard from "./QuestionCard.svelte";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.clearAllMocks();
});

const choices = [
  { key: "1", label: "Red Choose red" },
  { key: "2", label: "Green Choose green" },
  { key: "3", label: "Type something" },
];

describe("the question card", () => {
  it("keeps Yes and No questions neutral and all answers visible when narrow", () => {
    render(QuestionCard, { sessionId: freshSession(), prompt: { kind: "question", hash: "yes-no", lines: ["Continue?"], choices: [
      { key: "1", label: "Yes" }, { key: "2", label: "No" }, { key: "3", label: "Discuss first" },
    ] }, narrow: true } as never);
    expect(document.querySelectorAll("[data-choice]")).toHaveLength(3);
    expect(document.querySelector("[data-choice-more]")).toBeNull();
    expect(document.querySelector("[data-primary]")).toBeNull();
  });

  it("keeps long descriptions with their answers inside the scrollable body", async () => {
    vi.useFakeTimers();
    const id = freshSession();
    const detail = "Open or unlock 1Password and enable its SSH agent, then tell me to retry.";
    render(QuestionCard, { sessionId: id, prompt: { kind: "question", hash: "signing", lines: ["Signing"], choices: [
      { key: "1", label: "I'll unlock 1Password (Recommended)", detail },
    ] } } as never);
    const row = document.querySelector('[data-card-body] [data-choice="1"]') as HTMLButtonElement;
    expect(row.textContent).toContain(detail);
    expect(row.disabled).toBe(true);
    await vi.advanceTimersByTimeAsync(410);
    expect(row.disabled).toBe(false);
    expect(document.querySelector("[data-read-hint]")).toBeNull();
    await fireEvent.click(row);
    expect(talkSvc.Choose).toHaveBeenCalledWith({ session: id, hash: "signing", key: "1" });
    expect(row.disabled).toBe(true);
  });

  it("requires reading a long question and tolerates fractional scroll rounding at its end", async () => {
    vi.useFakeTimers();
    const end = vi.spyOn(HTMLSpanElement.prototype, "getBoundingClientRect").mockReturnValue({ top: 900 } as DOMRect);
    vi.spyOn(HTMLDivElement.prototype, "getBoundingClientRect").mockReturnValue({ bottom: 300 } as DOMRect);
    const id = freshSession();
    render(QuestionCard, { sessionId: id, prompt: { kind: "question", hash: "long", lines: ["A long question"], choices } } as never);
    await vi.advanceTimersByTimeAsync(410);
    const card = document.querySelector("[data-card]")!;
    await fireEvent.keyDown(card, { key: "1" });
    expect(talkSvc.Choose).not.toHaveBeenCalled();
    expect(document.querySelector("[data-read-hint]")).not.toBeNull();
    end.mockReturnValue({ top: 300.5 } as DOMRect);
    await fireEvent.scroll(document.querySelector("[data-card-body]")!);
    await fireEvent.keyDown(card, { key: "1" });
    expect(talkSvc.Choose).toHaveBeenCalledWith({ session: id, hash: "long", key: "1" });
  });

  it("narrow still shows every answer, since a question has no Yes or No to keep", () => {
    const id = freshSession();
    render(QuestionCard, { sessionId: id, prompt: { kind: "question", hash: "h", lines: ["Pick a color"], choices }, narrow: true } as never);
    expect(document.querySelectorAll("[data-choice]").length).toBe(3);
    expect(document.querySelector("[data-choice-more]")).toBeNull();
  });
  it("multi-select offers Terminal only", async () => {
    const id = freshSession();
    render(QuestionCard, { sessionId: id, prompt: { kind: "question_multi", hash: "h", lines: ["Pick colors"], choices } as never });
    expect(screen.getByText("Pick colors")).toBeTruthy();
    expect(document.querySelector("[data-choice]")).toBeNull();
    const select = vi.spyOn(terms, "select");
    await fireEvent.click(screen.getByRole("button", { name: /Answer in terminal/ }));
    expect(select).toHaveBeenCalledWith(id, AGENT);
    select.mockRestore();
    await fireEvent.keyDown(document.querySelector("[data-card]")!, { key: "1" });
    expect(talkSvc.Choose).not.toHaveBeenCalled();
  });

  it("single-select numbers its options with their descriptions, none primary", () => {
    render(QuestionCard, { sessionId: freshSession(), prompt: { kind: "question", hash: "h", lines: ["Pick a color"], choices } as never });
    expect(document.querySelector('[data-choice="1"]')?.textContent).toContain("Red Choose red");
    expect(document.querySelectorAll("[data-primary]").length).toBe(0);
  });

  it("a row that needs typing opens the terminal and presses nothing", async () => {
    const id = freshSession();
    render(QuestionCard, { sessionId: id, prompt: { kind: "question", hash: "h", lines: ["Pick a color"], choices } as never });
    await new Promise((r) => setTimeout(r, 420));
    const select = vi.spyOn(terms, "select");
    await fireEvent.click(document.querySelector('[data-choice="3"]')!);
    expect(talkSvc.Choose).not.toHaveBeenCalled();
    expect(select).toHaveBeenCalledWith(id, AGENT);
    select.mockRestore();
    await fireEvent.click(document.querySelector('[data-choice="2"]')!);
    await waitFor(() => expect(talkSvc.Choose).toHaveBeenCalledWith({ session: id, hash: "h", key: "2" }));
  });
});
