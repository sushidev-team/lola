import { render, screen, fireEvent } from "@testing-library/svelte";
import { describe, it, expect, vi, beforeEach } from "vitest";

// A PrRow-shaped fixture (camelCase json field names from the generated model).
const prs = [
  {
    number: 12,
    title: "Fix the flaky dispatch test",
    author: "alice",
    branch: "fix/dispatch",
    isDraft: false,
    isFork: false,
    checks: "pass",
    review: "APPROVED",
    url: "https://example.test/pr/12",
    status: "pr_open",
    alreadyOpen: false,
  },
  {
    number: 34,
    title: "WIP refactor",
    author: "",
    branch: "chore/refactor",
    isDraft: true,
    isFork: false,
    checks: "pending",
    review: "REVIEW_REQUIRED",
    url: "https://example.test/pr/34",
    status: "draft",
    alreadyOpen: true,
  },
];

// vi.mock is hoisted above the module body, so the mock fns must live in
// vi.hoisted (also hoisted) to exist when the factories run.
const { prsMock, openMock, openPrMock, setFlashMock, goCockpitMock, goDetailMock } = vi.hoisted(() => ({
  prsMock: vi.fn(async () => ({ repo: "org/repo", prs, ageSeconds: 5, stale: false })),
  openMock: vi.fn(async () => ({})),
  openPrMock: vi.fn(async () => ({})),
  setFlashMock: vi.fn(),
  goCockpitMock: vi.fn(),
  goDetailMock: vi.fn(),
}));

vi.mock("$lib/store.svelte", () => ({
  store: {
    alive: true,
    prs: prsMock,
    open: openMock,
    openPr: openPrMock,
    openURL: vi.fn(),
    setFlash: setFlashMock,
  },
}));

vi.mock("$lib/nav.svelte", () => ({
  nav: { project: "demo", goCockpit: goCockpitMock, goDetail: goDetailMock },
}));

import PRPicker from "./PRPicker.svelte";

describe("PRPicker", () => {
  beforeEach(() => vi.clearAllMocks());

  it("loads and lists a project's open PRs", async () => {
    render(PRPicker);
    expect(prsMock).toHaveBeenCalledWith("demo", false);
    expect(await screen.findByText("Fix the flaky dispatch test")).toBeInTheDocument();
    expect(screen.getByText("alice")).toBeInTheDocument();
    // empty author renders as an em dash
    expect(screen.getByText("—")).toBeInTheDocument();
    // freshness header
    expect(screen.getByText(/2 open · 5s ago/)).toBeInTheDocument();
    // draft suffix
    expect(screen.getByText("[draft]")).toBeInTheDocument();
  });

  it("opens a PR shell on row click and navigates to the cockpit", async () => {
    render(PRPicker);
    const title = await screen.findByText("Fix the flaky dispatch test");
    await fireEvent.click(title);
    expect(openMock).toHaveBeenCalledWith("demo", "12");
    expect(goCockpitMock).toHaveBeenCalledWith("demo");
  });

  it("refuses to open an already-open PR and just flashes", async () => {
    render(PRPicker);
    const title = await screen.findByText("WIP refactor");
    await fireEvent.click(title);
    expect(openMock).not.toHaveBeenCalled();
    expect(setFlashMock).toHaveBeenCalled();
  });
});
