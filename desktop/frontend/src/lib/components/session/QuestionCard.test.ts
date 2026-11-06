import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
import { freshSession, talkSvc, terms, AGENT } from "./talk.testkit";
import QuestionCard from "./QuestionCard.svelte";

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

const choices = [
  { key: "1", label: "Red Choose red" },
  { key: "2", label: "Green Choose green" },
  { key: "3", label: "Type something" },
];

describe("the question card", () => {
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
