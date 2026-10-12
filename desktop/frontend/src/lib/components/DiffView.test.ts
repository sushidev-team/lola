import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

const Diff = vi.fn();
const SendFeedback = vi.fn();
vi.mock("@bindings/desktop", () => ({
  DaemonService: {
    Diff: (...a: unknown[]) => Diff(...a),
    SendFeedback: (...a: unknown[]) => SendFeedback(...a),
    Alive: vi.fn().mockResolvedValue(false),
    Sessions: vi.fn(),
    Projects: vi.fn(),
    Status: vi.fn(),
  },
  ConfigService: {},
  TermService: {},
}));

const { default: DiffView } = await import("./DiffView.svelte");
const { feedback } = await import("$lib/feedback.svelte");

const DATA = {
  session: "s1",
  base: "origin/main",
  mergeBase: "0123456789abcdef",
  files: [
    {
      path: "internal/foo/foo.go",
      status: "modified",
      additions: 1,
      deletions: 1,
      patch: "@@ -1,3 +1,3 @@\n package foo\n-var x = 1\n+var x = 2\n // end\n",
    },
  ],
};

describe("DiffView", () => {
  beforeEach(() => {
    cleanup();
    feedback.clear("s1");
    Diff.mockReset().mockResolvedValue(DATA);
    SendFeedback.mockReset().mockResolvedValue({ delivered: true, queued: false });
  });

  it("shows the files changed against the merge-base", async () => {
    render(DiffView, { sessionId: "s1" });
    expect(await screen.findByText("origin/main")).toBeInTheDocument();
    expect(Diff).toHaveBeenCalledWith("s1");
    expect(screen.getByRole("navigation", { name: "changed files" })).toHaveTextContent("foo.go");
  });

  it("renders both diff layouts in sidebar order, independent of folder folding", async () => {
    // Git's path order puts FrontChat.tsx before its sibling directories.
    const paths = [
      "resources/js/components/FrontChat.tsx",
      "resources/js/components/__tests__/FrontChat.test.tsx",
      "resources/js/components/layout/__tests__/support-button.test.tsx",
      "resources/js/components/layout/support-button.tsx",
    ];
    const expected = [paths[1], paths[2], paths[3], paths[0]];
    Diff.mockResolvedValue({ ...DATA, files: paths.map((path) => ({ ...DATA.files[0], path })) });
    const { container } = render(DiffView, { sessionId: "s1" });
    await screen.findByText("origin/main");
    const nav = screen.getByRole("navigation", { name: "changed files" });
    const sidebarPaths = Array.from(nav.querySelectorAll("button[aria-label]"))
      .map((button) => button.getAttribute("aria-label")?.replace(/, Modified$/, ""));
    const diffPaths = () => Array.from(container.querySelectorAll("section[data-file]"))
      .map((section) => section.getAttribute("data-file"));
    expect(sidebarPaths).toEqual(expected);
    expect(diffPaths()).toEqual(sidebarPaths);
    expect(screen.getByRole("button", { name: `${expected[0]}, Modified` })).toHaveAttribute("aria-current", "location");

    await fireEvent.click(screen.getByRole("button", { name: "Split" }));
    expect(diffPaths()).toEqual(expected);
    await fireEvent.click(screen.getByRole("button", { name: "layout" }));
    expect(screen.queryByRole("button", { name: `${paths[3]}, Modified` })).not.toBeInTheDocument();
    expect(diffPaths()).toEqual(expected);
    await fireEvent.click(screen.getByRole("button", { name: "Unified" }));
    expect(diffPaths()).toEqual(expected);
  });

  it("collapses a directory without hiding its siblings, and preserves nested folds", async () => {
    Diff.mockResolvedValue({ ...DATA, files: [
      DATA.files[0],
      { ...DATA.files[0], path: "internal/bar.go" },
      { ...DATA.files[0], path: "tests/foo.test.ts" },
    ] });
    render(DiffView, { sessionId: "s1" });
    await screen.findByText("origin/main");
    const parent = screen.getByRole("button", { name: "internal" });
    const child = screen.getByRole("button", { name: "foo" });
    await fireEvent.click(child);
    expect(child).toHaveAttribute("aria-expanded", "false");
    expect(child).toHaveClass("contains-active");
    expect(screen.queryByRole("button", { name: "internal/foo/foo.go, Modified" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "internal/bar.go, Modified" })).toBeInTheDocument();
    await fireEvent.click(parent);
    expect(screen.queryByRole("button", { name: "internal/bar.go, Modified" })).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "tests/foo.test.ts, Modified" })).toBeInTheDocument();
    await fireEvent.click(parent);
    expect(screen.getByRole("button", { name: "foo" })).toHaveAttribute("aria-expanded", "false");
    await fireEvent.click(screen.getByRole("button", { name: "foo" }));
    expect(screen.getByRole("button", { name: "internal/foo/foo.go, Modified" })).toBeInTheDocument();
  });

  it("links scrolling and clicking to the current file without moving keyboard focus", async () => {
    Diff.mockResolvedValue({ ...DATA, files: [DATA.files[0], { ...DATA.files[0], path: "tests/foo.test.ts" }] });
    const { container } = render(DiffView, { sessionId: "s1" });
    await screen.findByText("origin/main");
    const pane = screen.getByRole("region", { name: "File diffs" });
    Object.defineProperties(pane, { clientHeight: { value: 300 }, scrollHeight: { value: 2000 } });
    pane.getBoundingClientRect = () => ({ top: 50 }) as DOMRect;
    const sections = Array.from(container.querySelectorAll<HTMLElement>("section[data-file]"));
    sections.forEach((section, i) => {
      section.getBoundingClientRect = () => ({ top: 50 + i * 1000 - pane.scrollTop }) as DOMRect;
    });
    pane.scrollTo = vi.fn((options?: ScrollToOptions | number, y?: number) => {
      pane.scrollTop = typeof options === "number" ? y ?? 0 : options?.top ?? 0;
    });
    pane.focus();
    pane.scrollTop = 1100;
    await fireEvent.scroll(pane);
    const second = screen.getByRole("button", { name: "tests/foo.test.ts, Modified" });
    await waitFor(() => expect(second).toHaveAttribute("aria-current", "location"));
    expect(sections[1]).toHaveClass("is-current");
    expect(pane).toHaveFocus();

    await fireEvent.click(screen.getByRole("button", { name: "tests" }));
    expect(screen.getByRole("button", { name: "tests" })).toHaveClass("contains-active");
    const first = screen.getByRole("button", { name: "internal/foo/foo.go, Modified" });
    await fireEvent.click(first);
    expect(pane.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: "instant" });
    expect(first).toHaveAttribute("aria-current", "location");
    expect(sections[0]).toHaveClass("is-current");
    expect(screen.getByRole("button", { name: "tests" })).not.toHaveClass("contains-active");
  });

  it("keeps reading and button keys local while allowing Escape to reach the app", async () => {
    render(DiffView, { sessionId: "s1" });
    await screen.findByText("origin/main");
    const globalKey = vi.fn();
    document.addEventListener("keydown", globalKey);
    try {
      const pane = screen.getByRole("region", { name: "File diffs" });
      const arrow = new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true, cancelable: true });
      pane.dispatchEvent(arrow);
      expect(arrow.defaultPrevented).toBe(false); // native scrolling still runs
      const file = screen.getByRole("button", { name: "internal/foo/foo.go, Modified" });
      await fireEvent.keyDown(file, { key: "Enter" });
      expect(globalKey).not.toHaveBeenCalled();
      await fireEvent.keyDown(pane, { key: "Escape" });
      expect(globalKey).toHaveBeenCalledTimes(1);
    } finally {
      document.removeEventListener("keydown", globalKey);
    }
  });

  // The acceptance path: select a line, write a comment, send — the daemon gets
  // path:line context and the quoted code.
  it("sends a line comment with its path and line", async () => {
    render(DiffView, { sessionId: "s1" });
    await screen.findByText("origin/main");
    // The added line is new-side line 2.
    const gutters = screen.getAllByTitle(/comment on this line/);
    await fireEvent.click(gutters[2]); // rows: ctx(1), del(old 2), add(new 2)
    const box = await screen.findByPlaceholderText("What should the agent change here?");
    await fireEvent.input(box, { target: { value: "Why 2?" } });
    await fireEvent.click(screen.getByRole("button", { name: "Add comment" }));
    expect(feedback.count("s1")).toBe(1);
    expect(screen.getByText("Why 2?")).toBeInTheDocument();

    await fireEvent.click(screen.getByRole("button", { name: /Send to agent \(1\)/ }));
    await waitFor(() => expect(SendFeedback).toHaveBeenCalled());
    expect(SendFeedback.mock.calls[0][0]).toEqual({
      session: "s1",
      comments: [{ path: "internal/foo/foo.go", line: 2, endLine: 0, side: "new", quote: "var x = 2", body: "Why 2?" }],
      note: "",
    });
    await waitFor(() => expect(feedback.count("s1")).toBe(0));
  });

  it("marks a comment on a removed line as old-side", async () => {
    render(DiffView, { sessionId: "s1" });
    await screen.findByText("origin/main");
    await fireEvent.click(screen.getAllByTitle(/comment on this line/)[1]);
    const box = await screen.findByPlaceholderText("What should the agent change here?");
    await fireEvent.input(box, { target: { value: "keep this" } });
    await fireEvent.click(screen.getByRole("button", { name: "Add comment" }));
    expect(feedback.draftsFor("s1")[0]).toMatchObject({ side: "old", line: 2, quote: "var x = 1" });
  });

  // A refused send must not throw the review away.
  it("keeps the drafts when the daemon refuses the batch", async () => {
    SendFeedback.mockRejectedValue(new Error("no"));
    feedback.add("s1", { path: "a.go", side: "new", line: 1, endLine: 1, quote: "", body: "x" });
    render(DiffView, { sessionId: "s1" });
    await screen.findByText("origin/main");
    await fireEvent.click(screen.getByRole("button", { name: /Send to agent/ }));
    await waitFor(() => expect(SendFeedback).toHaveBeenCalled());
    expect(feedback.count("s1")).toBe(1);
  });

  // Comments written while the request travels were never sent: a successful
  // reply must clear only the batch that went out.
  it("keeps drafts added while a send is in flight", async () => {
    let resolve!: (v: unknown) => void;
    SendFeedback.mockReturnValue(new Promise((r) => (resolve = r)));
    feedback.add("s1", { path: "a.go", side: "new", line: 1, endLine: 1, quote: "", body: "sent" });
    feedback.setNote("s1", "note v1");
    const sending = feedback.send("s1");
    feedback.add("s1", { path: "a.go", side: "new", line: 2, endLine: 2, quote: "", body: "late" });
    feedback.setNote("s1", "note v2");
    resolve({ delivered: true, queued: false });
    await sending;
    expect(feedback.draftsFor("s1").map((d) => d.body)).toEqual(["late"]);
    expect(feedback.noteFor("s1")).toBe("note v2");
  });

  // Two Refreshes can finish out of order; only the latest may land.
  it("ignores a stale diff response", async () => {
    let first!: (v: unknown) => void;
    Diff.mockReturnValueOnce(new Promise((r) => (first = r))).mockResolvedValueOnce({ ...DATA, base: "origin/new" });
    render(DiffView, { sessionId: "s1" });
    await fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    expect(await screen.findByText("origin/new")).toBeInTheDocument();
    first({ ...DATA, base: "origin/stale" });
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByText("origin/stale")).not.toBeInTheDocument();
  });

  it("has nothing to send until something is written", async () => {
    render(DiffView, { sessionId: "s1" });
    await screen.findByText("origin/main");
    expect(screen.getByRole("button", { name: /Send to agent/ })).toBeDisabled();
  });
});
