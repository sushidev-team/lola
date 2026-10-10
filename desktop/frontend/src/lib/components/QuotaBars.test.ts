import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/svelte";
import QuotaBars from "./QuotaBars.svelte";
import { budgetLabel } from "$lib/usage";

afterEach(cleanup);

describe("QuotaBars", () => {
  it("draws one unlabelled meter per limit window, stacked per agent", () => {
    render(QuotaBars, {
      quotas: [
        { agent: "claude", at: "", windows: [{ label: "5h", usedPercent: 42.4, resetsAt: "" }, { label: "7d", usedPercent: 96, resetsAt: "" }] },
        { agent: "codex", at: "", windows: [{ label: "7d", usedPercent: 4, resetsAt: "" }] },
      ],
    });
    const meters = screen.getAllByRole("meter");
    expect(meters.map((m) => m.getAttribute("aria-label"))).toEqual(["Claude 5-hour limit", "Claude Weekly limit", "Codex Weekly limit"]);
    expect(meters[0].getAttribute("aria-valuenow")).toBe("42");
    expect(meters[0].textContent).toBe(""); // no "%", no "7d": the card has those
    expect(meters[1].querySelector(".bg-bad")).not.toBeNull();
    expect((meters[0].firstElementChild as HTMLElement).style.width).toBe("42.4%");
    expect(document.body.textContent).not.toContain("7d");
  });

  it("labels the budget", () => {
    expect(budgetLabel({ day: "", tokens: 0, weighted: 30, todayUsd: 0, budgetTokens: 100 })).toBe("30% of budget");
    expect(budgetLabel({ day: "", tokens: 0, weighted: 30, todayUsd: 0 })).toBe("");
  });
});
