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

function offer(over: Record<string, unknown> = {}) {
  return { id: "a".repeat(32), title: "api work", level: "view", ownerDevice: "d1", ownerName: "office mini", ownerMember: "Ada", online: true, trusted: true, ...over };
}

beforeEach(() => {
  ShareMembers.mockReset().mockResolvedValue({ members: [] });
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
