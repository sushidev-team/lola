import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/svelte";

import DevLinks from "./devLinks.svelte";
import { tiers } from "@mobile/lib/tiers.svelte";
import { store } from "$lib/store.svelte";
import { router } from "@mobile/lib/router.svelte";
import * as openurl from "@mobile/lib/openurl";
import type { SessionInfo } from "$lib/store.svelte";

const session = {
  id: "lola-fe-42",
  devForwards: [
    { url: "http://192.168.20.3:39273", from: "127.0.0.1:8000" },
    { url: "http://192.168.20.3:41005", from: "127.0.0.1:5175" },
  ],
} as unknown as SessionInfo;

beforeEach(() => {
  store.sessions = [session];
  router.stack = [
    { name: "tab", tab: "inbox" },
    { name: "session", id: "lola-fe-42", seg: "terminal" },
  ];
  router.open("devLinks");
});

afterEach(() => {
  store.sessions = [];
  router.close();
  vi.restoreAllMocks();
});

describe("devLinks sheet", () => {
  it("lists the session's forwards by their original port", async () => {
    render(DevLinks);
    expect(await screen.findByText("Port 8000")).toBeInTheDocument();
    expect(screen.getByText("Port 5175")).toBeInTheDocument();
  });

  it("opens the chosen link on the phone and closes", async () => {
    const open = vi.spyOn(openurl, "openExternal").mockResolvedValue(true);
    render(DevLinks);
    await fireEvent.click(await screen.findByText("Port 5175"));
    expect(open).toHaveBeenCalledWith("http://192.168.20.3:41005");
    await waitFor(() => expect(router.sheet).toBe(""));
  });

  it("stays up and says so when no browser accepts the link", async () => {
    vi.spyOn(openurl, "openExternal").mockResolvedValue(false);
    render(DevLinks);
    await fireEvent.click(await screen.findByText("Port 8000"));
    expect(await screen.findByText("Couldn't open that link.")).toBeInTheDocument();
    expect(router.sheet).toBe("devLinks");
  });

  it("closes from Done", async () => {
    render(DevLinks);
    await fireEvent.click(await screen.findByRole("button", { name: "Done" }));
    expect(router.sheet).toBe("");
  });

  it("says there are no dev servers, and offers to start one only when it can", async () => {
    tiers.set(["read", "control"]);
    store.sessions = [{ id: "lola-fe-42", devForwards: [], devCommands: ["npm run dev"] } as unknown as SessionInfo];
    const dev = vi.spyOn(store, "dev").mockResolvedValue(undefined as never);
    const a = render(DevLinks);
    expect(await screen.findByText("No dev servers")).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Start dev server" }));
    expect(dev).toHaveBeenCalledWith("lola-fe-42", true);
    expect(router.sheet).toBe("");
    a.unmount();
    store.sessions = [{ id: "lola-fe-42", devForwards: [], devCommands: null } as unknown as SessionInfo];
    router.open("devLinks");
    render(DevLinks);
    expect(await screen.findByText("No dev servers")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Start dev server" })).toBeNull();
  });
});
