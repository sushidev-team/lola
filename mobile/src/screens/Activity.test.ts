import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import Activity, { activityItems, ENDED_VISIBLE, endedItems } from "./Activity.svelte";
import { store } from "$lib/store.svelte";
import { talk } from "$lib/talk.svelte";
import { router } from "@mobile/lib/router.svelte";

beforeEach(() => {
  store.activity = [];
  store.sessions = [];
  router.stack = [{ name: "tab", tab: "activity" }];
  vi.spyOn(talk, "loadEnded").mockResolvedValue();
  vi.spyOn(talk, "endedList").mockReturnValue([]);
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

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

describe("Activity opening sessions", () => {
  const at = () => new Date(Date.now() - 5 * 60_000).toISOString();

  it("opens the live session an event is about", async () => {
    store.sessions = [{ id: "live1", project: "p", issue: "NOR-1", title: "Fix login" } as never];
    store.activity = [{ id: "live1", issue: "NOR-1", title: "Fix login", from: "working", to: "needs_input", ago: "5m", at: at() }];
    render(Activity);
    await fireEvent.click(screen.getByRole("button", { name: /Fix login/ }));
    expect(router.top).toMatchObject({ name: "session", id: "live1", seg: "conversation" });
  });

  it("leaves an event for an unreadable session as plain text", () => {
    store.activity = [{ id: "gone9", issue: "NOR-9", title: "Old work", from: "working", to: "merged", ago: "5m", at: at() }];
    render(Activity);
    expect(screen.queryByRole("button", { name: /Old work/ })).toBeNull();
    expect(screen.getByRole("group", { name: /Old work/ })).toBeTruthy();
  });

  it("opens an event whose session ended but is still readable", async () => {
    vi.spyOn(talk, "endedList").mockReturnValue([{ id: "e1", title: "Done thing", issue: "NOR-2", reason: "PR #4 merged", endedAt: at() }]);
    store.activity = [{ id: "e1", issue: "NOR-2", title: "Done thing", from: "approved", to: "merged", ago: "5m", at: at() }];
    render(Activity);
    const rows = screen.getAllByRole("button", { name: /Done thing/ });
    await fireEvent.click(rows[0]);
    expect(router.top).toMatchObject({ name: "session", id: "e1" });
  });

  it("lists recently ended sessions and opens one, even with no events", async () => {
    vi.spyOn(talk, "endedList").mockReturnValue([
      { id: "e1", project: "p", title: "Ship it", issue: "NOR-2", reason: "PR #4 merged", endedAt: at() },
    ]);
    render(Activity);
    expect(screen.queryByText("Nothing yet.")).toBeNull();
    expect(screen.getByRole("heading", { name: "Recently ended" })).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: /Ship it, PR #4 merged/ }));
    expect(router.top).toMatchObject({ name: "session", id: "e1", seg: "conversation" });
    expect(talk.loadEnded).toHaveBeenCalled();
  });
});

describe("endedItems", () => {
  const mk = (n: number) => Array.from({ length: n }, (_, i) => ({ id: `e${i}` }) as never);
  it("is empty when nothing ended, else a header and the newest few", () => {
    expect(endedItems([])).toEqual([]);
    const items = endedItems(mk(ENDED_VISIBLE + 5));
    expect(items).toHaveLength(ENDED_VISIBLE + 1);
    expect(items[0]).toMatchObject({ kind: "ended-head" });
    expect(items[1].key).toBe("ended:e0");
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
    expect(items.map((i) => (i.kind === "day" ? i.label : i.kind === "event" ? i.ev.id : i.key))).toEqual([
      "Today", "a", "b", "Yesterday", "c", "Sunday, November 1", "d", "n",
    ]);
    expect(items.filter((i) => i.kind === "day").map((i) => i.key)).toEqual(["day:2026-11-14", "day:2026-11-13", "day:2026-11-1"]);
  });
});
