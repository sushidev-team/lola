import { afterEach, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/svelte";
import SpendCard from "./SpendCard.svelte";

afterEach(cleanup);

it("explains unavailable quota without a fabricated percentage or timestamp", () => {
  render(SpendCard, { usage: { day: "", tokens: 0, weighted: 0, todayUsd: 0, quotas: [
    { agent: "claude", at: "0001-01-01T00:00:00Z", windows: [], stale: true, detail: "Claude login expired; open Claude Code to refresh it" },
  ] } });
  expect(screen.getByText("Usage unavailable")).toBeInTheDocument();
  expect(screen.getByText("Claude login expired; open Claude Code to refresh it")).toBeInTheDocument();
  expect(document.body.textContent).not.toMatch(/0%|updated|stale/);
});

it("shows the age and error beside retained quota figures", () => {
  render(SpendCard, { usage: { day: "", tokens: 0, weighted: 0, todayUsd: 0, quotas: [
    { agent: "claude", at: new Date(Date.now() - 600000).toISOString(), windows: [{ label: "7d", usedPercent: 25, resetsAt: "" }], stale: true, detail: "Cannot reach Claude usage; will retry automatically" },
  ] } });
  expect(screen.getByText(/stale · updated/)).toBeInTheDocument();
  expect(screen.getByText("25%")).toBeInTheDocument();
  expect(screen.getByText(/Cannot reach Claude usage/)).toBeInTheDocument();
});
