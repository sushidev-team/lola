import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";

import Feedback from "./feedback.svelte";
import { feedback } from "$lib/feedback.svelte";
import { router } from "@mobile/lib/router.svelte";

describe("feedback sheet", () => {
  beforeEach(() => {
    feedback.clear("s1");
    router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: "s1", seg: "review" }];
    router.sheet = "feedback";
  });

  it("lists drafts, removes one, and keeps Send off while empty", async () => {
    feedback.add("s1", { path: "a.go", side: "new" as never, line: 3, endLine: 3, quote: "", body: "rename this" });
    render(Feedback);
    expect(screen.getByText("rename this")).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Remove comment" }));
    expect(screen.queryByText("rename this")).toBeNull();
    expect(screen.getByRole("heading", { name: "0 comments" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
  });

  it("sends the batch and closes only when accepted", async () => {
    feedback.add("s1", { path: "a.go", side: "new" as never, line: 3, endLine: 3, quote: "", body: "x" });
    const send = vi.spyOn(feedback, "send").mockResolvedValueOnce(false).mockResolvedValueOnce(true);
    render(Feedback);
    await fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(send).toHaveBeenCalledTimes(1));
    expect(router.sheet).toBe("feedback");
    await fireEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(router.sheet).toBe(""));
  });

  it("titles itself by the count, shows basename:line and keeps the note in the sheet", () => {
    feedback.add("s1", { path: "internal/a.go", side: "new" as never, line: 3, endLine: 3, quote: "", body: "x" });
    const { unmount } = render(Feedback);
    expect(screen.getByRole("heading", { name: "1 comment" })).toBeInTheDocument();
    expect(screen.getByText("a.go:3")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("Add a note")).toBeInTheDocument();
    unmount();
    feedback.add("s1", { path: "b.go", side: "new" as never, line: 1, endLine: 1, quote: "", body: "y" });
    render(Feedback);
    expect(screen.getByRole("heading", { name: "2 comments" })).toBeInTheDocument();
  });

  it("is dirty once a note is typed: dismissing asks first", async () => {
    feedback.setNote("s1", "remember this");
    render(Feedback);
    await fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(router.sheet).toBe("feedback");
  });
});
