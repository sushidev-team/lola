import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/svelte";

const { ShareInfo, On } = vi.hoisted(() => ({ ShareInfo: vi.fn(), On: vi.fn() }));
vi.mock("@bindings/desktop", () => ({ TermService: { ShareInfo }, HubService: { ShareMembers: vi.fn(), ShareOffered: vi.fn() } }));
vi.mock("@wailsio/runtime", () => ({ Events: { On } }));
// The pane's job is the header and the stream name; the terminal itself is covered by LiveTerminal's tests.
vi.mock("../session/TerminalCell.svelte", async () => ({ default: (await import("../TerminalExitHarness.test.svelte")).default }));

import SharedSessionPane from "./SharedSessionPane.svelte";
import { share } from "$lib/share.svelte";

const ID = "a".repeat(32);
const info = (over = {}) => ({ id: ID, title: "api work", level: "view", pane: "lola-a", attached: [], ...over });

beforeEach(() => {
  ShareInfo.mockReset().mockResolvedValue(info());
  On.mockReset().mockReturnValue(() => {});
  share.offers = [{ id: ID, title: "api work", level: "view", ownerDevice: "d1", ownerName: "office mini", ownerMember: "Ada", online: true, trusted: true } as never];
  share.attachedId = "";
});

describe("SharedSessionPane", () => {
  it("is hidden and quiet while nothing is attached", () => {
    render(SharedSessionPane);
    expect(document.querySelector("[data-shared-pane]")).toHaveAttribute("data-shared-pane", "closed");
    expect(On).not.toHaveBeenCalled();
    expect(ShareInfo).not.toHaveBeenCalled();
  });

  it("names whose session it is and what you may do in it", async () => {
    share.attach(ID);
    render(SharedSessionPane);
    expect(document.querySelector("[data-shared-pane]")).toHaveAttribute("data-shared-pane", "open");
    expect(screen.getByText("api work")).toBeInTheDocument();
    expect(screen.getByText("Ada · office mini")).toBeInTheDocument();
    expect(document.querySelector("[data-shared-level]")).toHaveAttribute("data-shared-level", "view");
    await waitFor(() => expect(ShareInfo).toHaveBeenCalledWith(`share-${ID}`));
  });

  it("shows who is attached, you included, from the owner's roster", async () => {
    ShareInfo.mockResolvedValue(info({ level: "type", attached: [{ name: "Ada", since: 1 }, { name: "Bob Stone", me: true, since: 2 }] }));
    share.attach(ID);
    render(SharedSessionPane);
    const list = await screen.findByRole("list", { name: "Attached: Ada and Bob Stone" });
    expect(list.querySelectorAll("[role=listitem]")).toHaveLength(2);
    expect(list).toHaveTextContent("A");
    expect(list).toHaveTextContent("BS");
    expect(document.querySelector("[data-shared-level]")).toHaveAttribute("data-shared-level", "type");
  });

  it("follows the pushed roster for its own stream and nothing else", async () => {
    let push: ((e: { data: unknown }) => void) | undefined;
    On.mockImplementation((name: string, cb: (e: { data: unknown }) => void) => {
      if (name === `share:share-${ID}`) push = cb;
      return () => {};
    });
    ShareInfo.mockRejectedValue(new Error("not attached"));
    share.attach(ID);
    render(SharedSessionPane);
    await waitFor(() => expect(push).toBeDefined());
    push!({ data: info({ attached: [{ name: "Cy", since: 3 }] }) });
    expect(await screen.findByRole("list", { name: "Attached: Cy" })).toBeInTheDocument();
  });

  it("ignores a roster read that lands after the stream moved on", async () => {
    const B = "b".repeat(32);
    let late: ((v: unknown) => void) | undefined;
    ShareInfo.mockImplementation((n: string) => (n === `share-${ID}` ? new Promise((r) => (late = r)) : Promise.resolve(info({ id: B, title: "web work" }))));
    share.offers = [...share.offers, { id: B, title: "web work", level: "view", ownerDevice: "d1", ownerName: "office mini", online: true, trusted: true } as never];
    share.attach(ID);
    render(SharedSessionPane);
    await waitFor(() => expect(late).toBeDefined());
    share.attach(B);
    expect(await screen.findByText("web work")).toBeInTheDocument();
    late!(info({ title: "api work", level: "type" }));
    await Promise.resolve();
    await Promise.resolve();
    expect(document.querySelector("[data-shared-title]")).toHaveTextContent("web work");
  });

  it("the back button leaves the pane", async () => {
    share.attach(ID);
    render(SharedSessionPane);
    screen.getByRole("button", { name: "Back to the team" }).click();
    await waitFor(() => expect(share.attachedId).toBe(""));
    expect(document.querySelector("[data-shared-pane]")).toHaveAttribute("data-shared-pane", "closed");
  });
});
