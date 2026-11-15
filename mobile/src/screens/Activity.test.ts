import { beforeEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/svelte";
import Activity, { activityItems } from "./Activity.svelte";
import { store } from "$lib/store.svelte";
import { afterEach } from "vitest";

beforeEach(() => {
  store.activity = [];
});
afterEach(cleanup);

describe("Activity", () => {
  it("says so when empty", () => {
    render(Activity);
    expect(screen.getByRole("heading", { name: "Activity" })).toBeTruthy();
    expect(screen.getByText("Nothing yet.")).toBeTruthy();
  });

  it("renders title, transition, issue and age from `at`", () => {
    const at = new Date(Date.now() - 5 * 60_000).toISOString();
    store.activity = [{ id: "x", issue: "NOR-1", title: "Fix login", from: "working", to: "needs_input", ago: "99h", at }];
    render(Activity);
    expect(screen.getByText("Fix login")).toBeTruthy();
    expect(screen.getByText("Needs you")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Today" })).toBeTruthy();
    expect(screen.getByText("NOR-1")).toBeTruthy();
    expect(screen.getByText("5m")).toBeTruthy();
  });

  it("falls back to the daemon's age and the transition when `at` and title are absent", () => {
    store.activity = [{ id: "x", issue: "", from: "", to: "working", ago: "2m" }];
    render(Activity);
    expect(screen.getByText("Spawned")).toBeTruthy();
    expect(screen.getByText("2m")).toBeTruthy();
  });

  it("says Restarted for a return to working", () => {
    const at = new Date(Date.now() - 60_000).toISOString();
    store.activity = [{ id: "x", issue: "N-1", title: "T", from: "ci_failed", to: "working", ago: "1m", at }];
    render(Activity);
    expect(screen.getByText("Restarted")).toBeTruthy();
  });

  it("keeps rows mounted as the age moves", async () => {
    const at = new Date(Date.now() - 60_000).toISOString();
    const ev = { id: "x", issue: "N-1", title: "T", from: "a", to: "working", ago: "1m", at };
    store.activity = [ev];
    render(Activity);
    const row = screen.getByText("T");
    store.activity = [{ ...ev, ago: "2m" }];
    await Promise.resolve();
    expect(screen.getByText("T")).toBe(row);
  });
});

describe("activityItems", () => {
  const labels = { today: "Today", yesterday: "Yesterday" };
  const noon = new Date(2026, 10, 14, 12, 0, 0).getTime();
  const ev = (id: string, at: Date | undefined) =>
    ({ id, issue: id, title: id, from: "a", to: "working", ago: "1m", at: at?.toISOString() }) as never;

  it("inserts one non-sticky header per local day, with stable keys", () => {
    const events = [
      ev("a", new Date(2026, 10, 14, 11)),
      ev("b", new Date(2026, 10, 14, 9)),
      ev("c", new Date(2026, 10, 13, 23)),
      ev("d", new Date(2026, 10, 1, 8)),
      ev("n", undefined),
    ];
    const items = activityItems(events, noon, labels, "en-US");
    expect(items.map((i) => (i.kind === "day" ? i.label : i.ev.id))).toEqual([
      "Today", "a", "b", "Yesterday", "c", "Sunday, November 1", "d", "n",
    ]);
    expect(items.filter((i) => i.kind === "day").map((i) => i.key)).toEqual(["day:2026-11-14", "day:2026-11-13", "day:2026-11-1"]);
  });
});
