import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";

const attachClipboard = vi.fn();
vi.mock("@bindings/desktop", () => ({ TalkService: { AttachClipboardImage: (...a: unknown[]) => attachClipboard(...a) } }));

import Attach, { requestImage, settleImage } from "./attach.svelte";
import { store } from "$lib/store.svelte";
import { router } from "@mobile/lib/router.svelte";

describe("attach sheet", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    store.sessions = [{ id: "s1", worktree: "/w/s1" } as never];
    router.stack = [{ name: "tab", tab: "inbox" }, { name: "session", id: "s1", seg: "conversation" }];
    router.sheet = "";
  });

  it("requestImage opens the sheet and a newer request cancels the older", async () => {
    const first = requestImage();
    expect(router.sheet).toBe("attach");
    const second = requestImage();
    expect(await first).toBeNull();
    settleImage({ path: "/a.png", thumb: "" });
    expect(await second).toEqual({ path: "/a.png", thumb: "" });
  });

  it("Paste uploads from the clipboard for this session's worktree and settles", async () => {
    attachClipboard.mockResolvedValue({ path: "/p.png", thumb: "t" });
    const pending = requestImage();
    render(Attach);
    await fireEvent.click(screen.getByRole("button", { name: "Paste" }));
    expect(attachClipboard).toHaveBeenCalledWith("/w/s1");
    expect(await pending).toEqual({ path: "/p.png", thumb: "t" });
    expect(router.sheet).toBe("");
  });

  it("an empty clipboard shows the sentence and keeps the request open", async () => {
    attachClipboard.mockRejectedValue(new Error("No image to paste."));
    let settled = false;
    void requestImage().then(() => (settled = true));
    render(Attach);
    await fireEvent.click(screen.getByRole("button", { name: "Paste" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("No image to paste.");
    expect(settled).toBe(false);
  });

  it("closing without a choice settles with null", async () => {
    const pending = requestImage();
    const { unmount } = render(Attach);
    unmount();
    await waitFor(async () => expect(await pending).toBeNull());
  });
});
