import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/svelte";

const { ShareMembers, ShareOffered, ShareTrust, ShareUntrust } = vi.hoisted(() => ({
  ShareMembers: vi.fn(),
  ShareOffered: vi.fn(),
  ShareTrust: vi.fn(),
  ShareUntrust: vi.fn(),
}));
vi.mock("@bindings/desktop", () => ({ HubService: { ShareMembers, ShareOffered, ShareTrust, ShareUntrust, SetShared: vi.fn() } }));

import TeamShares from "./TeamShares.svelte";
import { share } from "$lib/share.svelte";

const ada = { deviceId: "d1", name: "Ada · office mini", fingerprint: "aaaa bbbb cccc dddd", allowed: false, trusted: false };
const bob = { deviceId: "d2", name: "Bob · laptop", fingerprint: "1111 2222 3333 4444", allowed: true, trusted: true };

function offer(over: Record<string, unknown> = {}) {
  return { id: "a".repeat(32), title: "api work", level: "view", ownerDevice: "d1", ownerName: "office mini", ownerMember: "Ada", online: true, trusted: true, ...over };
}

async function teammateRow(name: string): Promise<HTMLElement> {
  const list = await waitFor(() => document.querySelector("[data-team-teammates]") as HTMLElement);
  return (await within(list).findByText(name)).closest("li") as HTMLElement;
}

beforeEach(() => {
  ShareMembers.mockReset().mockResolvedValue({ members: [ada, bob] });
  ShareOffered.mockReset().mockResolvedValue({ offers: [offer()] });
  ShareTrust.mockReset();
  ShareUntrust.mockReset();
  share.attachedId = "";
  share.error = "";
});

describe("shared sessions", () => {
  it("lists offers by machine and attaches only a trusted, online one", async () => {
    ShareOffered.mockResolvedValue({
      offers: [
        offer(),
        offer({ id: "b".repeat(32), title: "docs", ownerDevice: "d2", ownerName: "laptop", ownerMember: "Bob", online: false }),
        offer({ id: "c".repeat(32), title: "infra", ownerDevice: "d3", ownerName: "pc", ownerMember: "Eve", trusted: false }),
      ],
    });
    render(TeamShares);
    const row = await screen.findByText("api work");
    const ok = within(row.closest("li") as HTMLElement).getByRole("button", { name: "Attach to api work" });
    expect(ok).toBeEnabled();
    expect(screen.getByRole("button", { name: "docs: laptop is offline" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "infra: Trust pc first" })).toBeDisabled();
    await fireEvent.click(ok);
    expect(share.attachedId).toBe("a".repeat(32));
  });

  it("says plainly when nothing is shared", async () => {
    ShareOffered.mockResolvedValue({ offers: [] });
    render(TeamShares);
    expect(await screen.findByText("Nothing is shared with you yet.")).toBeInTheDocument();
  });
});

describe("teammates", () => {
  it("pins only after the person says the fingerprint matches, and sends exactly what they saw", async () => {
    ShareTrust.mockResolvedValue({ members: [{ ...ada, allowed: true }, bob] });
    render(TeamShares);
    const row = await teammateRow("Ada · office mini");
    expect(within(row).getByText("aaaa bbbb cccc dddd")).toBeInTheDocument();
    await fireEvent.click(within(row).getByRole("button", { name: "Allow Ada · office mini to attach to your sessions" }));
    const confirm = await waitFor(() => row.querySelector("[data-pin-confirm]") as HTMLElement);
    expect(within(confirm).getByText("aaaa bbbb cccc dddd")).toBeInTheDocument();
    const go = row.querySelector("[data-pin-confirm-button]") as HTMLButtonElement;
    expect(go).toBeDisabled();
    expect(ShareTrust).not.toHaveBeenCalled();
    await fireEvent.click(within(confirm).getByRole("checkbox"));
    expect(go).toBeEnabled();
    await fireEvent.click(go);
    await waitFor(() => expect(ShareTrust).toHaveBeenCalledWith({ deviceId: "d1", fingerprint: "aaaa bbbb cccc dddd", allow: true, trust: false }));
    await waitFor(() => expect(row.querySelector("[data-pin-confirm]")).toBeNull());
  });

  it("keeps the dialog open and shows the daemon's refusal when the key changed", async () => {
    ShareTrust.mockRejectedValue(new Error("share: the fingerprint changed since you looked; compare it again"));
    render(TeamShares);
    const row = await teammateRow("Ada · office mini");
    await fireEvent.click(within(row).getByRole("button", { name: /Trust Ada · office mini's sessions/ }));
    const confirm = await waitFor(() => row.querySelector("[data-pin-confirm]") as HTMLElement);
    await fireEvent.click(within(confirm).getByRole("checkbox"));
    await fireEvent.click(row.querySelector("[data-pin-confirm-button]") as HTMLElement);
    expect(await screen.findByRole("alert")).toHaveTextContent("the fingerprint changed");
    expect(row.querySelector("[data-pin-confirm]")).not.toBeNull();
  });

  it("removes one direction, or both", async () => {
    ShareUntrust.mockResolvedValue({ members: [bob] });
    render(TeamShares);
    const row = await teammateRow("Bob · laptop");
    await fireEvent.click(within(row).getByRole("button", { name: "Bob · laptop may attach to your sessions" }));
    await waitFor(() => expect(ShareUntrust).toHaveBeenLastCalledWith({ deviceId: "d2", allow: true, trust: false }));
    await fireEvent.click(within(row).getByRole("button", { name: "You trust Bob · laptop's sessions" }));
    await waitFor(() => expect(ShareUntrust).toHaveBeenLastCalledWith({ deviceId: "d2", allow: false, trust: true }));
    await fireEvent.click(within(row).getByRole("button", { name: "Remove Bob · laptop" }));
    await waitFor(() => expect(ShareUntrust).toHaveBeenLastCalledWith({ deviceId: "d2", allow: false, trust: false }));
  });

  it("an unpinned teammate has no remove button", async () => {
    render(TeamShares);
    const row = await teammateRow("Ada · office mini");
    expect(within(row).queryByRole("button", { name: /^Remove/ })).toBeNull();
  });
});
