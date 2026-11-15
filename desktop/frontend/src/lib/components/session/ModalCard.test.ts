import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import { setTouchSurface } from "$lib/touchsurface";
import { freshSession, terms, AGENT } from "./talk.testkit";
import ModalCard from "./ModalCard.svelte";

afterEach(() => {
  cleanup();
  setTouchSurface(null);
});

const prompt = (lines = ["Select a model", "> opus", "  sonnet"]) => ({ kind: "modal", hash: "h", lines }) as never;

describe("the modal card", () => {
  it("has no field", () => {
    render(ModalCard, { sessionId: freshSession(), prompt: prompt() });
    expect(document.querySelector("input,textarea")).toBeNull();
    expect(document.querySelector("[data-choice]")).toBeNull();
  });

  it("offers Terminal", async () => {
    const id = freshSession();
    render(ModalCard, { sessionId: id, prompt: prompt() });
    const select = vi.spyOn(terms, "select");
    await fireEvent.click(screen.getByRole("button", { name: /Open terminal/ }));
    expect(select).toHaveBeenCalledWith(id, AGENT);
    select.mockRestore();
  });

  it("shows at most 8 lines of the pane", () => {
    render(ModalCard, { sessionId: freshSession(), prompt: prompt(Array.from({ length: 12 }, (_, i) => `l${i}`)) });
    expect(document.querySelectorAll("[data-modal-lines] p").length).toBe(8);
  });

  it("on a phone says Choose in terminal, with the bare duration", () => {
    setTouchSurface({});
    render(ModalCard, { sessionId: freshSession(), prompt: { kind: "modal", hash: "h", lines: ["Select a model"], since: new Date(Date.now() - 120000).toISOString() } as never });
    expect(screen.getByRole("heading").textContent).toBe("Choose in terminal");
    expect(screen.getByRole("region", { name: "Choose in terminal" })).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/open \d/);
  });
});
