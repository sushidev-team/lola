import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/svelte";
import QuotaBars from "./QuotaBars.svelte";
import { quotaTone, budgetLabel } from "$lib/usage";

afterEach(cleanup);

describe("QuotaBars", () => {
  it("draws one meter per limit window, filled and toned by usage", () => {
    render(QuotaBars, {
      quotas: [
        { agent: "claude", at: "", windows: [{ label: "5h", usedPercent: 42.4, resetsAt: "" }, { label: "7d", usedPercent: 96, resetsAt: "" }] },
        { agent: "codex", at: "", windows: [{ label: "7d", usedPercent: 4, resetsAt: "" }] },
      ],
    });
    const meters = screen.getAllByRole("meter");
    expect(meters.map((m) => m.getAttribute("aria-label"))).toEqual(["Claude 5h limit", "Claude 7d limit", "Codex 7d limit"]);
    expect(meters[0].getAttribute("aria-valuenow")).toBe("42");
    expect(meters[0].textContent).toContain("42%");
    expect(meters[1].querySelector(".bg-bad")).not.toBeNull();
    const fill = meters[0].querySelector(".bg-accent") as HTMLElement;
    expect(fill.style.width).toBe("42.4%");
  });

  it("tones and labels", () => {
    expect([quotaTone(10), quotaTone(80), quotaTone(95)]).toEqual(["ok", "warn", "bad"]);
    expect(budgetLabel({ day: "", tokens: 0, weighted: 30, todayUsd: 0, budgetTokens: 100 })).toBe("30% of budget");
    expect(budgetLabel({ day: "", tokens: 0, weighted: 30, todayUsd: 0 })).toBe("");
  });
});
