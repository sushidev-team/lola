import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";

const { ShareMembers, ShareOffered, SetShared } = vi.hoisted(() => ({ ShareMembers: vi.fn(), ShareOffered: vi.fn(), SetShared: vi.fn() }));
vi.mock("@bindings/desktop", () => ({ HubService: { ShareMembers, ShareOffered, SetShared, ShareTrust: vi.fn(), ShareUntrust: vi.fn() } }));

import ShareControl from "./ShareControl.svelte";
import { share } from "$lib/share.svelte";
import { store } from "$lib/store.svelte";
import { nav } from "$lib/nav.svelte";

const ada = { deviceId: "d1", name: "Ada · mini", fingerprint: "aaaa", allowed: true, trusted: false };
const bob = { deviceId: "d2", name: "Bob · laptop", fingerprint: "bbbb", allowed: true, trusted: false };
const eve = { deviceId: "d3", name: "Eve · pc", fingerprint: "cccc", allowed: false, trusted: true };

function session(over: Record<string, unknown> = {}) {
  store.sessions = [{ id: "s1", project: "p", sandbox: "docker", ...over } as never];
}

beforeEach(() => {
  ShareMembers.mockReset().mockResolvedValue({ members: [ada, bob, eve] });
  ShareOffered.mockReset().mockResolvedValue({ offers: [] });
  SetShared.mockReset().mockResolvedValue({ session: "s1", on: true });
  share.error = "";
  session();
});

describe("ShareControl", () => {
  it("is one icon button that says whether the session is shared", async () => {
    const { unmount } = render(ShareControl, { sessionId: "s1" });
    const off = screen.getByRole("button", { name: "Share with team" });
    expect(off).toHaveAttribute("data-shared", "off");
    unmount();
    session({ share: { id: "x", title: "t", level: "view", attached: [{ name: "Ada", since: 1 }, { name: "Bob", since: 2 }] } });
    render(ShareControl, { sessionId: "s1" });
    const on = screen.getByRole("button", { name: "Shared with your team · 2 attached" });
    expect(on).toHaveAttribute("data-shared", "on");
    expect(document.querySelector("[data-share-count]")).toHaveTextContent("2");
  });

  it("offers only the teammates this Mac allowed, and shares view-only by default", async () => {
    render(ShareControl, { sessionId: "s1" });
    await fireEvent.click(screen.getByRole("button", { name: "Share with team" }));
    await screen.findByRole("checkbox", { name: "Ada · mini" });
    expect(screen.getByRole("checkbox", { name: "Bob · laptop" })).toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: "Eve · pc" })).toBeNull();
    expect(screen.getByText("Everyone you allowed")).toBeInTheDocument();

    await fireEvent.click(screen.getByRole("checkbox", { name: "Ada · mini" }));
    expect(screen.getByText("1 chosen")).toBeInTheDocument();
    await fireEvent.click(document.querySelector("[data-share-apply]") as HTMLElement);
    await waitFor(() =>
      expect(SetShared).toHaveBeenCalledWith({ session: "s1", on: true, level: "view", with: ["d1"], confirmUnsandboxed: false }),
    );
  });

  it("will not give a teammate the keyboard on a host session without an explicit yes", async () => {
    session({ sandbox: "" });
    render(ShareControl, { sessionId: "s1" });
    await fireEvent.click(screen.getByRole("button", { name: "Share with team" }));
    await screen.findByRole("checkbox", { name: "Ada · mini" });
    await fireEvent.click(screen.getByRole("button", { name: "Type" }));
    const apply = document.querySelector("[data-share-apply]") as HTMLButtonElement;
    expect(document.querySelector("[data-share-host-warning]")).not.toBeNull();
    expect(apply.disabled).toBe(true);
    await fireEvent.click(document.querySelector("[data-share-host-warning] input") as HTMLElement);
    expect(apply.disabled).toBe(false);
    await fireEvent.click(apply);
    await waitFor(() => expect(SetShared).toHaveBeenCalledWith(expect.objectContaining({ level: "type", confirmUnsandboxed: true })));
  });

  it("a sandboxed session needs no such confirmation", async () => {
    render(ShareControl, { sessionId: "s1" });
    await fireEvent.click(screen.getByRole("button", { name: "Share with team" }));
    await screen.findByRole("checkbox", { name: "Ada · mini" });
    await fireEvent.click(screen.getByRole("button", { name: "Type" }));
    expect(document.querySelector("[data-share-host-warning]")).toBeNull();
    expect((document.querySelector("[data-share-apply]") as HTMLButtonElement).disabled).toBe(false);
  });

  it("with nobody allowed it points at the Team view instead of sharing with no one", async () => {
    ShareMembers.mockResolvedValue({ members: [eve] });
    render(ShareControl, { sessionId: "s1" });
    await fireEvent.click(screen.getByRole("button", { name: "Share with team" }));
    await screen.findByText(/No teammate is allowed/);
    expect((document.querySelector("[data-share-apply]") as HTMLButtonElement).disabled).toBe(true);
    await fireEvent.click(screen.getByRole("button", { name: "Allow a teammate" }));
    expect(nav.view).toBe("team");
  });

  it("stops sharing, and shows the daemon's refusal in its own words", async () => {
    session({ share: { id: "x", title: "t", level: "view" } });
    render(ShareControl, { sessionId: "s1" });
    await fireEvent.click(screen.getByRole("button", { name: "Shared with your team" }));
    await screen.findByRole("checkbox", { name: "Ada · mini" });
    SetShared.mockRejectedValueOnce(new Error("share: join a team hub first"));
    await fireEvent.click(document.querySelector("[data-share-stop]") as HTMLElement);
    expect(await screen.findByRole("alert")).toHaveTextContent("share: join a team hub first");
    SetShared.mockResolvedValueOnce({ session: "s1", on: false });
    await fireEvent.click(document.querySelector("[data-share-stop]") as HTMLElement);
    await waitFor(() => expect(SetShared).toHaveBeenLastCalledWith({ session: "s1", on: false }));
    await waitFor(() => expect(document.querySelector("[data-share-dialog]")).toBeNull());
  });
});
