import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/svelte";

const Trash = vi.hoisted(() => vi.fn().mockResolvedValue({}));
vi.mock("@bindings/desktop", () => ({
  SSHService: { Trash, Timeline: vi.fn().mockResolvedValue({ connections: null, items: [], lastSeq: 0 }), Hosts: vi.fn().mockResolvedValue({ hosts: [] }) },
  DaemonService: {},
  ConfigService: {},
  TermService: {},
}));

const { default: HostBar } = await import("./HostBar.svelte");
const { ssh } = await import("$lib/ssh.svelte");
const { store } = await import("$lib/store.svelte");
const { confirm } = await import("$lib/confirm.svelte");
const { bandTier } = await import("$lib/ssh");

const prod = { id: "p", host: "prod-api", tier: "production", state: "connected", wheel: "agent", alias: "prod-api", tabAlive: true };
const stg = { id: "s", host: "staging", tier: "staging", state: "connected", wheel: "agent", alias: "staging", tabAlive: true };
let n = 0;
let SID = "";

function show(conns: unknown[] = [prod, stg]) {
  SID = `host${++n}`;
  store.alive = true;
  store.sessions = [{ id: SID, agent: "claude", ssh: { connections: conns, lastSeq: 1 } }] as never;
  ssh.view(SID).connections = conns as never;
  render(HostBar, { sessionId: SID });
  return ssh.view(SID);
}
const radio = (name: RegExp) => screen.getByRole("radio", { name });

beforeEach(() => {
  cleanup();
  confirm.cancel();
  Trash.mockClear();
});

describe("HostBar", () => {
  it("All is default", () => {
    const v = show();
    expect(v.scope).toBe("");
    expect(radio(/^All$/)).toHaveAttribute("aria-checked", "true");
    expect(radio(/prod-api/)).toHaveAttribute("aria-checked", "false");
  });
  it("picking a host makes it the scope and the shell a Run would open", async () => {
    const v = show();
    v.shellConn = "p";
    await fireEvent.click(radio(/staging/));
    expect(v.scope).toBe("s");
    expect(v.shellConn).toBe("s");
  });
  it("band follows highest live tier", () => {
    expect(bandTier([stg, prod] as never, "")).toBe("production");
    expect(bandTier([stg, { ...prod, state: "closed" }] as never, "")).toBe("staging");
    expect(bandTier([{ ...prod, state: "requested" }] as never, "")).toBe("dev");
    expect(bandTier([stg, prod] as never, "s")).toBe("staging");
  });
  it("× is not destructive", async () => {
    const v = show();
    await fireEvent.click(radio(/prod-api/));
    await fireEvent.click(screen.getByRole("button", { name: "Back to All" }));
    expect(v.scope).toBe("");
    expect(confirm.request).toBeNull();
    expect(Trash).not.toHaveBeenCalled();
  });
  it("remove host confirms", async () => {
    const v = show();
    v.scope = "p";
    await fireEvent.click(await screen.findByRole("button", { name: "More host actions" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Remove host and its record…" }));
    expect(Trash).not.toHaveBeenCalled();
    expect(confirm.request?.body).toBe("Removes this host, its commands and their output. Your messages stay.");
    confirm.accept();
    await vi.waitFor(() => expect(Trash).toHaveBeenCalledWith("p"));
  });
  it("acts on no host under All with several live, and on the one when there is one", async () => {
    show();
    expect(screen.queryByRole("button", { name: "Take the wheel" })).toBeNull();
    expect(screen.queryByRole("button", { name: "More host actions" })).toBeNull();
    cleanup();
    show([prod]);
    expect(screen.getByRole("button", { name: "Take the wheel" })).toBeEnabled();
  });
});
