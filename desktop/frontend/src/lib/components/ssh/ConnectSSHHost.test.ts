import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent, waitFor } from "@testing-library/svelte";

const svc = vi.hoisted(() => ({
  Hosts: vi.fn(),
  Connect: vi.fn(),
  Timeline: vi.fn(async () => ({ items: [], connections: [] })),
}));
vi.mock("@bindings/desktop", async (orig) => ({ ...(await orig<object>()), SSHService: svc }));

import ConnectSSHHost from "./ConnectSSHHost.svelte";
import { connectRows } from "$lib/ssh";
import { nav } from "$lib/nav.svelte";
import { store } from "$lib/store.svelte";

const dev = { name: "dev", alias: "dev-box", tier: "dev", grantMaxMinutes: 30 };
const prod = { name: "prod", alias: "prod-api", user: "ro", tier: "production", grantMaxMinutes: 0 };

function seed(session: object) {
  store.sessions = [{ id: "s1", title: "Fix login", agent: "claude", ...session } as never];
  nav.openOverlay("sshconnect", "s1");
}

describe("connectRows", () => {
  it("refuses production for an unsandboxed session only", () => {
    expect(connectRows([prod as never], {})[0].refused).toMatch(/sandboxed session/);
    expect(connectRows([prod as never], { sandbox: "docker" })[0].refused).toBe("");
    expect(connectRows([{ ...prod, allowUnsandboxed: true } as never], {})[0].refused).toBe("");
  });

  it("reads an unknown tier as production", () => {
    expect(connectRows([{ ...dev, tier: "" } as never], {})[0].refused).not.toBe("");
  });

  it("finds a live connection, but not a closed or waiting one", () => {
    const with_ = (state: string) => connectRows([dev as never], { ssh: { connections: [{ id: "c1", host: "dev", state }] } as never })[0].live;
    expect(with_("connected")?.id).toBe("c1");
    expect(with_("closed")).toBeUndefined();
    expect(with_("requested")).toBeUndefined();
  });
});

describe("ConnectSSHHost", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    svc.Connect.mockResolvedValue({ conn: { id: "c9", host: "dev", state: "connecting" } });
    vi.spyOn(store, "refresh").mockResolvedValue(undefined as never);
  });
  afterEach(() => {
    cleanup();
    nav.closeOverlay();
    vi.restoreAllMocks();
  });

  it("connects this session to the chosen host", async () => {
    svc.Hosts.mockResolvedValue({ hosts: [dev] });
    seed({});
    render(ConnectSSHHost);
    const button = await screen.findByRole("button", { name: "Connect to dev" });
    await fireEvent.click(button);
    await waitFor(() => expect(svc.Connect).toHaveBeenCalledWith({ session: "s1", host: "dev" }));
    await waitFor(() => expect(nav.overlay).toBe(null));
  });

  it("asks only one short thing of an unsandboxed session, and nothing of a sandboxed one", async () => {
    svc.Hosts.mockResolvedValue({ hosts: [dev] });
    seed({});
    render(ConnectSSHHost);
    await screen.findByRole("button", { name: "Connect to dev" });
    expect(document.querySelector("[data-unsandboxed]")?.textContent).toBe("Claude runs unsandboxed on this Mac.");
    expect(screen.queryByText("Who drives first")).toBeNull();
    cleanup();
    seed({ sandbox: "docker" });
    render(ConnectSSHHost);
    await screen.findByRole("button", { name: "Connect to dev" });
    expect(document.querySelector("[data-unsandboxed]")).toBeNull();
  });

  it("disables a refused host and cannot submit it", async () => {
    svc.Hosts.mockResolvedValue({ hosts: [prod] });
    seed({});
    render(ConnectSSHHost);
    await screen.findByText(/Production needs a sandboxed session/);
    expect((screen.getByRole("radio", { name: /prod/ }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: /Connect to/ }) as HTMLButtonElement).disabled).toBe(true);
    expect(svc.Connect).not.toHaveBeenCalled();
  });

  it("opens another tab on a host that is already connected", async () => {
    svc.Hosts.mockResolvedValue({ hosts: [dev] });
    svc.Connect.mockResolvedValue({ conn: { id: "c2", host: "dev", state: "connected" } });
    seed({ ssh: { connections: [{ id: "c1", host: "dev", state: "connected" }] } });
    render(ConnectSSHHost);
    expect(await screen.findByText("Uses the open login")).toBeInTheDocument();
    await fireEvent.click(await screen.findByRole("button", { name: /^Connect to / }));
    await waitFor(() => expect(svc.Connect).toHaveBeenCalled());
    await waitFor(() => expect(nav.overlay).toBe(null));
  });

  it("goes to host settings when there are no hosts", async () => {
    svc.Hosts.mockResolvedValue({ hosts: [] });
    seed({});
    render(ConnectSSHHost);
    await waitFor(() => expect(nav.overlay).toBe("settings"));
    expect(nav.overlayTab).toBe("sshhosts");
  });

  it("reads again after restarting a daemon that predates ssh", async () => {
    svc.Hosts.mockRejectedValueOnce(new Error('unknown cmd "sshHosts"')).mockResolvedValue({ hosts: [dev] });
    vi.spyOn(store, "restartDaemon").mockResolvedValue(undefined as never);
    seed({});
    render(ConnectSSHHost);
    await fireEvent.click(await screen.findByRole("button", { name: "Restart daemon" }));
    expect(await screen.findByRole("button", { name: "Connect to dev" })).toBeTruthy();
    expect(screen.queryByText(/predates/)).toBeNull();
  });

  it("keeps reading while the restarted daemon comes up", async () => {
    svc.Hosts.mockRejectedValueOnce(new Error('unknown cmd "sshHosts"'))
      .mockRejectedValueOnce(new Error("connection refused"))
      .mockResolvedValue({ hosts: [dev] });
    vi.spyOn(store, "restartDaemon").mockResolvedValue(undefined as never);
    seed({});
    render(ConnectSSHHost);
    await fireEvent.click(await screen.findByRole("button", { name: "Restart daemon" }));
    expect(await screen.findByRole("button", { name: "Connect to dev" }, { timeout: 2000 })).toBeTruthy();
  });

  it("goes to host settings when a restart reveals no hosts", async () => {
    svc.Hosts.mockRejectedValueOnce(new Error('unknown cmd "sshHosts"')).mockResolvedValue({ hosts: [] });
    vi.spyOn(store, "restartDaemon").mockResolvedValue(undefined as never);
    seed({});
    render(ConnectSSHHost);
    await fireEvent.click(await screen.findByRole("button", { name: "Restart daemon" }));
    await waitFor(() => expect(nav.overlay).toBe("settings"));
  });

  it("shows a daemon refusal and stays open", async () => {
    svc.Hosts.mockResolvedValue({ hosts: [dev] });
    svc.Connect.mockRejectedValue(new Error("host dev has an invalid configuration"));
    seed({});
    render(ConnectSSHHost);
    await fireEvent.click(await screen.findByRole("button", { name: "Connect to dev" }));
    expect((await screen.findByRole("alert")).textContent).toContain("invalid configuration");
    expect(nav.overlay).toBe("sshconnect");
  });
});
