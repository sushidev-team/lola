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

  it("has nothing to send until something is written", async () => {
    render(DiffView, { sessionId: "s1" });
    await screen.findByText("origin/main");
    expect(screen.getByRole("button", { name: /Send to agent/ })).toBeDisabled();
  });
});
