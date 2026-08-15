import { describe, it, expect, vi, beforeEach } from "vitest";

// TermService is only reached by the async paths (refresh / openShell / close);
// these tests drive the pure view logic, so the binding is stubbed away.
vi.mock("@bindings/desktop", () => ({
  TermService: { Shells: vi.fn(async () => []), Shell: vi.fn(async () => ""), CloseShell: vi.fn(async () => {}) },
}));

const { terms, AGENT, isReviewTab, isDevTab, devTabIndex } = await import("./terms.svelte");
const { store } = await import("./store.svelte");

// The store is a singleton; give each test its own session id instead of
// resetting it, so ordering can never matter.
let n = 0;
const newId = () => `s${++n}`;

// seed installs a discovered tab list the way refresh() would.
function seed(id: string, names: string[]) {
  (terms as unknown as { shells: Map<string, string[]> }).shells.set(id, names);
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("review pane tabs", () => {
  it("recognises a review pane by its suffix", () => {
    expect(isReviewTab("lola-nori-app-nor-357-review")).toBe(true);
    expect(isReviewTab("lola-nori-app-nor-357-shell-1")).toBe(false);
    expect(isReviewTab("lola-nori-app-nor-357")).toBe(false);
  });

  it("labels the review pane 'Review' and leaves shell numbering alone", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-shell-2`, `${id}-review`]);
    expect(terms.labelFor(id, `${id}-shell-1`)).toBe("Shell 1");
    expect(terms.labelFor(id, `${id}-shell-2`)).toBe("Shell 2");
    expect(terms.labelFor(id, `${id}-review`)).toBe("Review");
  });

  it("numbers a new shell past the existing shells, ignoring the review pane", async () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-review`]);
    const { TermService } = await import("@bindings/desktop");
    await terms.openShell(id, "/tmp/wt");
    expect(TermService.Shell).toHaveBeenCalledWith(`${id}-shell-2`, "/tmp/wt");
  });

  it("cycles through the review pane like any other tab", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-review`]);
    terms.select(id, AGENT);
    terms.cycleTab(id, 1);
    expect(terms.activeTab(id)).toBe(`${id}-shell-1`);
    terms.cycleTab(id, 1);
    expect(terms.activeTab(id)).toBe(`${id}-review`);
    terms.cycleTab(id, 1);
    expect(terms.activeTab(id)).toBe(AGENT);
  });

  it("falls back off a review tab that has gone away", async () => {
    const id = newId();
    seed(id, [`${id}-review`]);
    terms.select(id, `${id}-review`);
    expect(terms.activeTab(id)).toBe(`${id}-review`);
    seed(id, []);
    expect(terms.activeTab(id)).toBe(AGENT);
  });
});

describe("drag-to-sort tabs", () => {
  it("reorders the tabs and renumbers the labels by position", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-shell-2`, `${id}-shell-3`]);
    terms.moveTab(id, 2, 0);
    expect(terms.shellsFor(id)).toEqual([`${id}-shell-3`, `${id}-shell-1`, `${id}-shell-2`]);
    expect(terms.labelFor(id, `${id}-shell-3`)).toBe("Shell 1");
    expect(terms.labelFor(id, `${id}-shell-1`)).toBe("Shell 2");
  });

  it("ignores a no-op or out-of-range move", () => {
    const id = newId();
    const names = [`${id}-shell-1`, `${id}-shell-2`];
    seed(id, names);
    terms.moveTab(id, 0, 0);
    terms.moveTab(id, 1, 5);
    terms.moveTab(id, -1, 0);
    expect(terms.shellsFor(id)).toEqual(names);
  });

  it("survives a refresh, and appends a shell discovered after the sort", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-shell-2`]);
    terms.moveTab(id, 1, 0);
    seed(id, [`${id}-shell-1`, `${id}-shell-2`, `${id}-shell-3`]); // discovery order, as refresh writes it
    expect(terms.shellsFor(id)).toEqual([`${id}-shell-2`, `${id}-shell-1`, `${id}-shell-3`]);
  });

  it("drops a closed tab from the sorted order", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-shell-2`, `${id}-review`]);
    terms.moveTab(id, 2, 0);
    seed(id, [`${id}-shell-1`, `${id}-shell-2`]);
    expect(terms.shellsFor(id)).toEqual([`${id}-shell-1`, `${id}-shell-2`]);
  });

  it("keeps a hand-given name on the tab it was given to, through a sort", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-shell-2`]);
    terms.rename(id, `${id}-shell-2`, "  Tests  ");
    expect(terms.labelFor(id, `${id}-shell-2`)).toBe("Tests"); // trimmed
    terms.moveTab(id, 1, 0);
    expect(terms.labelFor(id, `${id}-shell-2`)).toBe("Tests"); // not renumbered with the position
    expect(terms.labelFor(id, `${id}-shell-1`)).toBe("Shell 2");
  });

  it("falls back to the default when a name is cleared", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`]);
    terms.rename(id, `${id}-shell-1`, "Logs");
    terms.rename(id, `${id}-shell-1`, "   ");
    expect(terms.labelFor(id, `${id}-shell-1`)).toBe("Shell 1");
  });

  it("names the review pane too, and forgets the name when the tab closes", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-review`]);
    terms.rename(id, `${id}-review`, "QA");
    expect(terms.labelFor(id, `${id}-review`)).toBe("QA");
    terms.shellExited(id, `${id}-review`); // a reused tmux name must not inherit it
    seed(id, [`${id}-shell-1`, `${id}-review`]);
    expect(terms.labelFor(id, `${id}-review`)).toBe("Review");
  });

  it("persists names across a reload of the module", async () => {
    const id = newId();
    seed(id, [`${id}-shell-1`]);
    terms.rename(id, `${id}-shell-1`, "Deploy");
    vi.resetModules();
    const fresh = (await import("./terms.svelte")).terms;
    (fresh as unknown as { shells: Map<string, string[]> }).shells.set(id, [`${id}-shell-1`]);
    expect(fresh.labelFor(id, `${id}-shell-1`)).toBe("Deploy");
  });

  it("cycles in the sorted order", () => {
    const id = newId();
    seed(id, [`${id}-shell-1`, `${id}-shell-2`]);
    terms.moveTab(id, 1, 0);
    terms.select(id, AGENT);
    terms.cycleTab(id, 1);
    expect(terms.activeTab(id)).toBe(`${id}-shell-2`);
  });
});

describe("dev tabs", () => {
  it("recognises a dev tab only when it carries an index", () => {
    expect(isDevTab("lola-app-eng-1-dev-1")).toBe(true);
    expect(isDevTab("lola-app-eng-1-dev-12")).toBe(true);
    expect(isDevTab("lola-app-eng-1-dev")).toBe(false);
    expect(isDevTab("lola-app-open-dev-branch")).toBe(false);
    expect(isDevTab("lola-app-eng-1-shell-1")).toBe(false);
  });

  it("binds a dev tab to its EXACT parent (a shorter id is not a prefix match)", () => {
    expect(devTabIndex("lola-fe-42", "lola-fe-42-dev-2")).toBe(2);
    expect(devTabIndex("lola-fe-4", "lola-fe-42-dev-1")).toBe(0);
    expect(devTabIndex("lola-fe-42", "lola-fe-42-shell-1")).toBe(0);
  });

  it("labels a dev tab with the command it runs and leaves shell numbering alone", () => {
    const id = newId();
    store.sessions = [{ id, devCommands: ["composer dev", "npm run dev"] } as never];
    seed(id, [`${id}-dev-1`, `${id}-dev-2`, `${id}-shell-1`, `${id}-review`]);
    expect(terms.labelFor(id, `${id}-dev-1`)).toBe("composer dev");
    expect(terms.labelFor(id, `${id}-dev-2`)).toBe("npm run dev");
    // The dev tabs must not shift the shells' numbering — they are the
    // project's tabs, sitting ahead of this session's own.
    expect(terms.labelFor(id, `${id}-shell-1`)).toBe("Shell 1");
    expect(terms.labelFor(id, `${id}-review`)).toBe("Review");
  });

  it("falls back to 'Dev N' when config no longer describes that tab", () => {
    const id = newId();
    store.sessions = [{ id, devCommands: ["composer dev"] } as never];
    seed(id, [`${id}-dev-1`, `${id}-dev-2`]);
    expect(terms.labelFor(id, `${id}-dev-2`)).toBe("Dev 2");
  });

  it("closes a dev tab through the daemon toggle, not by killing the tmux session", async () => {
    const id = newId();
    store.sessions = [{ id, devCommands: ["composer dev"], devActive: true } as never];
    seed(id, [`${id}-dev-1`]);
    const devSpy = vi.spyOn(store, "dev").mockResolvedValue(undefined as never);
    const { TermService } = await import("@bindings/desktop");

    await terms.closeShell(id, `${id}-dev-1`);

    expect(devSpy).toHaveBeenCalledWith(id, false);
    expect(TermService.CloseShell).not.toHaveBeenCalled();
    devSpy.mockRestore();
  });

  it("numbers a new shell past the shells only, ignoring dev tabs", async () => {
    const id = newId();
    seed(id, [`${id}-dev-1`, `${id}-dev-2`, `${id}-shell-1`]);
    const { TermService } = await import("@bindings/desktop");
    await terms.openShell(id, "/tmp/wt");
    expect(TermService.Shell).toHaveBeenCalledWith(`${id}-shell-2`, "/tmp/wt");
  });
});
