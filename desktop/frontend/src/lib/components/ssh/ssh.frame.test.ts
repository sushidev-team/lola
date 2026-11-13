import { describe, it, expect, vi } from "vitest";
import { Workspace, SSHChip, SSHHostSettings, NewSSHSession, svc, ssh, store, ConfigService, SID, conn, block, useSession, seed, useSSHKit } from "./ssh.testkit";
import { sshTabs } from "$lib/ssh";
import { confirm } from "$lib/confirm.svelte";
import { tick } from "svelte";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

useSSHKit();

describe("the daemon is down", () => {
  it("keeps a session with ssh readable and sends nothing", async () => {
    store.alive = true;
    store.sessions = [{ id: SID, title: "t", ssh: { connections: [], lastSeq: 0 } } as never, { id: `${SID}-plain`, title: "p" } as never];
    const v = seed([]);
    store.sessions = store.sessions.filter((s) => s.id !== `${SID}-plain`);
    (store as unknown as { retained: unknown[] }).retained = store.sessions.filter((s) => s.ssh);
    store.sessions = [];
    store.alive = false;
    expect(store.sessionById(SID)?.title).toBe("t");
    expect(store.sessionById(`${SID}-plain`)).toBeUndefined();
    await ssh.say(SID, "are you there");
    expect(svc.Say).not.toHaveBeenCalled();
    expect(v.flash).toContain("daemon is down");
    store.alive = true;
    expect(store.sessionById(SID)).toBeUndefined();
  });
});

describe("the workspace", () => {
  it("fetches on mount and carries the band and the host bar", async () => {
    useSession({ ssh: { connections: [conn()], lastSeq: 7 } });
    const v = ssh.view(SID);
    v.connections = [conn()] as never;
    render(Workspace, { sessionId: SID });
    await waitFor(() => expect(svc.Timeline).toHaveBeenCalledWith({ session: SID, after: 0 }));
    expect(screen.getAllByText("prod-api").length).toBeGreaterThan(0);
    expect(screen.getAllByText("PROD").length).toBeGreaterThan(0);
  });
  it("tier band follows the connection", async () => {
    const stg = conn({ id: "c2", host: "staging", alias: "staging", tier: "staging" });
    useSession({ ssh: { connections: [conn(), stg], lastSeq: 7 } });
    const v = ssh.view(SID);
    v.connections = [conn(), stg] as never;
    render(Workspace, { sessionId: SID });
    const band = () => (document.querySelector("[data-tier-band]") as HTMLElement).className;
    // Under All it is the highest live tier, and a host in scope speaks for itself.
    expect(band()).toContain("bg-bad/70");
    v.scope = "c2";
    await tick();
    expect(band()).not.toContain("bg-bad/70");
    v.scope = "";
    v.connections = [conn({ state: "closed" }), stg] as never;
    await tick();
    expect(band()).not.toContain("bg-bad/70");
  });
  it("says what will happen when nothing is connected yet", () => {
    useSession({ ssh: { connections: [conn({ state: "requested" })], lastSeq: 1 } });
    ssh.view(SID).connections = [conn({ state: "requested" })] as never;
    render(Workspace, { sessionId: SID });
    expect(screen.getByText("No connection yet. Nothing has been attempted.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Connect/ })).toBeInTheDocument();
  });
  it("offers a new shell when the human left theirs", () => {
    useSession({ ssh: { connections: [conn({ tabAlive: false, idleLingerSeconds: 900 })], lastSeq: 1 } });
    ssh.view(SID).connections = [conn({ tabAlive: false, idleLingerSeconds: 900 })] as never;
    render(Workspace, { sessionId: SID });
    expect(screen.getByRole("button", { name: "Open a new shell" })).toBeInTheDocument();
    expect(screen.getByText(/It closes by itself after 15 minutes/)).toBeInTheDocument();
  });
  it("shows one view at a time, and keeps the terminal mounted behind the timeline", async () => {
    useSession({ ssh: { connections: [conn()], lastSeq: 1 } });
    const v = ssh.view(SID);
    v.connections = [conn()] as never;
    render(Workspace, { sessionId: SID });
    const t = screen.getByTestId("terminal");
    expect(t.dataset.name).toBe("s-ssh-1");
    expect(t.closest(".invisible")).not.toBeNull();
    expect(screen.getByRole("textbox")).toBeInTheDocument();
    v.narrow = "terminal";
    await tick();
    // The parked terminal is never remounted, which is why a host's shell keeps its size and text.
    expect(screen.getByTestId("terminal")).toBe(t);
    expect(t.closest(".invisible")).toBeNull();
    expect(screen.queryByRole("textbox")).toBeNull();
    v.narrow = "stream";
    await tick();
    expect(screen.getByTestId("terminal")).toBe(t);
    expect(t.closest(".invisible")).not.toBeNull();
  });
  it("words a dead daemon as a state, keeps the story, and the controls go quiet", () => {
    useSession({ ssh: { connections: [conn()], lastSeq: 1 } });
    ssh.view(SID).connections = [conn()] as never;
    store.alive = false;
    render(Workspace, { sessionId: SID });
    expect(document.querySelector("[data-ssh-offline]")).toHaveTextContent("The daemon is down. This is the last record it sent, and nothing here can act until it is back. Your terminal still works.");
    expect(document.querySelector(".ssh-offline")).not.toBeNull();
    store.alive = true;
  });
  it("asks again by itself after a daemon that predated ssh is replaced", async () => {
    vi.useFakeTimers();
    try {
      useSession({ ssh: { connections: [conn()], lastSeq: 1 } });
      const v = ssh.view(SID);
      v.connections = [conn()] as never;
      const fetch = vi.spyOn(v, "fetch").mockResolvedValue();
      v.predates = true;
      render(Workspace, { sessionId: SID });
      await vi.advanceTimersByTimeAsync(0);
      fetch.mockClear();
      await vi.advanceTimersByTimeAsync(3100);
      expect(fetch).toHaveBeenCalled();
      fetch.mockRestore();
    } finally {
      vi.useRealTimers();
    }
  });
  it("says closed, and offers the next step for a direct session", () => {
    useSession({ location: "ssh", ssh: { connections: [conn({ state: "closed", commandsRun: 2 })], lastSeq: 1 } });
    ssh.view(SID).connections = [conn({ state: "closed", commandsRun: 2 })] as never;
    render(Workspace, { sessionId: SID });
    expect(screen.getByRole("button", { name: "New connection" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "End session…" })).toBeInTheDocument();
    expect(screen.getByText(/The remote process may keep running/)).toBeInTheDocument();
  });
});

describe("a tab per connection", () => {
  const a = () => conn({ id: "ca", host: "h", state: "connected" });
  const b = () => conn({ id: "cb", host: "h", state: "connected" });
  const block = (id: string, c: string) => ({ id, kind: "block", seq: 1, order: 1, block: { id, conn: c, command: id, status: "succeeded" } });

  it("numbers a second tab to one host, and gives a denied request none", () => {
    const rows = sshTabs([a(), b(), conn({ id: "cd", host: "x", state: "denied" })] as never);
    expect(rows.map((r) => r.conn.id)).toEqual(["ca", "cb"]);
    expect(rows[1].label).toBe(`${rows[0].label} 2`);
    expect(sshTabs([conn({ id: "cp", state: "connected", prompt: { id: "p" } })] as never)[0].count).toBe(1);
  });

  it("keeps each tab's timeline and draft apart", () => {
    const v = ssh.view(SID);
    v.connections = [a(), b()] as never;
    v.items = [block("b1", "ca"), block("b2", "cb"), { id: "e0", kind: "element", element: { id: "e0", kind: "narration", title: "both", revision: 1 } }] as never;
    v.activeConn = "ca";
    v.setCommandDraft("ca", "rm -rf /tmp/x");
    expect(v.tabItems.map((i) => i.id)).toEqual(["b1", "e0"]);
    v.activeConn = "cb";
    expect(v.tabItems.map((i) => i.id)).toEqual(["b2", "e0"]);
    expect(v.commandDraft("cb")).toBe("");
    v.activeConn = "ca";
    expect(v.commandDraft("ca")).toBe("rm -rf /tmp/x");
    v.setCommandDraft("ca", "");
  });

  it("once the host bar has asked, deletes and lands on the tab beside it", async () => {
    const v = ssh.view(SID);
    v.connections = [a(), b()] as never;
    v.items = [block("b1", "ca"), block("b2", "cb")] as never;
    v.activeConn = "ca";
    svc.Trash.mockReset().mockResolvedValue({});
    ssh.confirmTrash(SID, a() as never);
    await waitFor(() => expect(svc.Trash).toHaveBeenCalledWith("ca"));
    await waitFor(() => expect(v.activeConn).toBe("cb"));
    expect(v.connections.map((c) => c.id)).toEqual(["cb"]);
    expect(v.items.map((i) => i.id)).toEqual(["b2"]);
  });

  it("drops what a tab trashed elsewhere owned at the next fetch", async () => {
    const v = ssh.view(SID);
    v.bind(SID);
    v.connections = [a(), b()] as never;
    v.items = [block("b1", "ca"), block("b2", "cb")] as never;
    svc.Timeline.mockResolvedValue({ items: [], connections: [b()], lastSeq: 9 });
    await v.fetch();
    expect(v.items.map((i) => i.id)).toEqual(["b2"]);
  });
});

describe("the chip", () => {
  it("reads tier, host and the most urgent fact, and opens the workspace", async () => {
    const open = vi.spyOn(ssh, "open");
    render(SSHChip, { sessionId: SID, info: { connections: [conn({ pendingApprovals: 2 })], needsHuman: true, lastSeq: 1 } as never });
    expect(screen.getByText("PROD")).toBeInTheDocument();
    expect(screen.getByText("2 to approve")).toHaveClass("text-orange");
    await fireEvent.click(screen.getByText("2 to approve"));
    expect(open).toHaveBeenCalledWith(SID, { conn: "c1", dock: true });
  });
  it("draws nothing once every connection ended", () => {
    const { container } = render(SSHChip, { sessionId: SID, info: { connections: [conn({ state: "closed" })], lastSeq: 1 } as never });
    expect(container.querySelector("[data-ssh-chip]")).toBeNull();
  });
});

describe("new session", () => {
  it("offers no hosts yet a way to add one", async () => {
    render(NewSSHSession);
    expect(await screen.findByRole("button", { name: "Open SSH host settings" })).toBeInTheDocument();
  });
  it("disables a refused host, shows why, and opens with the go", async () => {
    svc.Hosts.mockResolvedValue({
      hosts: [
        { name: "billing", alias: "billing", tier: "production", grantMaxMinutes: 0, directRefused: "billing is tier production and no sandbox is available." },
        { name: "prod", alias: "prod", tier: "production", grantMaxMinutes: 0, directSandbox: "docker", directAgent: "claude" },
      ],
    });
    svc.Open.mockRejectedValue("ssh is missing");
    render(NewSSHSession);
    const refused = await screen.findByRole("radio", { name: /billing/ });
    expect(refused).toBeDisabled();
    expect(screen.getByText(/no sandbox is available/)).toBeInTheDocument();
    const prod = await screen.findByRole("radio", { name: /prod/ });
    await waitFor(() => expect(prod).toHaveAttribute("aria-checked", "true"));
    await fireEvent.click(screen.getByRole("button", { name: "Connect to prod" }));
    await screen.findByRole("alert");
    expect(screen.getByRole("alert")).toHaveTextContent("ssh is missing");
    expect(svc.Open).toHaveBeenCalledWith(expect.objectContaining({ host: "prod" }));
  });
  it("has no who-drives switch, and one short line for an unsandboxed agent", async () => {
    svc.Hosts.mockResolvedValue({ hosts: [{ name: "dev", alias: "dev", tier: "dev", grantMaxMinutes: 120, directSandbox: "none" }] });
    render(NewSSHSession);
    await screen.findByText(/runs unsandboxed on this Mac/);
    expect(screen.queryByRole("radio", { name: "You" })).toBeNull();
  });
});

describe("host settings", () => {
  it("selects a config host and inherits its connection settings", async () => {
    vi.mocked(ConfigService.SSHConfigAliases).mockResolvedValueOnce(["web-1", "staging"]);
    render(SSHHostSettings);
    await fireEvent.click(await screen.findByRole("button", { name: /Add host/ }));
    await screen.findByRole("option", { name: "staging" });
    await fireEvent.change(screen.getByLabelText("Connection"), { target: { value: "web-1" } });
    expect(await screen.findByText("deploy@web-1:22")).toBeInTheDocument();
    expect(screen.queryByLabelText("Hostname or IP")).toBeNull();
    await fireEvent.input(screen.getByLabelText("Label"), { target: { value: "Web" } });
    await fireEvent.click(screen.getByRole("button", { name: "Save host" }));
    await waitFor(() => expect(ConfigService.SaveSSHHost).toHaveBeenLastCalledWith("", expect.objectContaining({ alias:"web-1", label:"Web", user:"", port:0, identityFile:"" })));
    expect(svc.Go).not.toHaveBeenCalled();
  });

  it("saves custom data when no SSH config is available", async () => {
    vi.mocked(ConfigService.SSHConfigAliases).mockRejectedValueOnce(new Error("unreadable"));
    render(SSHHostSettings);
    await fireEvent.click(await screen.findByRole("button", { name: /Add host/ }));
    expect(await screen.findByText(/Could not read SSH config/)).toBeInTheDocument();
    await fireEvent.change(screen.getByLabelText("Connection"), { target: { value: ":custom" } });
    await fireEvent.input(screen.getByLabelText("Hostname or IP"), { target: { value: "192.0.2.10" } });
    await fireEvent.input(screen.getByLabelText("Port"), { target: { value: "2222" } });
    await fireEvent.input(screen.getByLabelText("Identity file"), { target: { value: "~/.ssh/custom key" } });
    await fireEvent.input(screen.getByLabelText("Remote user"), { target: { value: "ops" } });
    await fireEvent.click(screen.getByRole("button", { name: "Save host" }));
    await waitFor(() => expect(ConfigService.SaveSSHHost).toHaveBeenLastCalledWith("", expect.objectContaining({ alias:"192.0.2.10", user:"ops", port:2222, identityFile:"~/.ssh/custom key" })));
  });

  it("ignores an older host preview arriving after the selected host", async () => {
    let finish!: (value: never) => void;
    vi.mocked(ConfigService.SSHConfigAliases).mockResolvedValueOnce(["first", "second"]);
    vi.mocked(ConfigService.ResolveSSHHost).mockImplementationOnce(() => new Promise((r) => { finish = r; }) as never)
      .mockResolvedValueOnce({ hostname:"second.example", user:"ops", port:"22" } as never);
    render(SSHHostSettings);
    await fireEvent.click(await screen.findByRole("button", { name: /Add host/ }));
    await screen.findByRole("option", { name: "first" });
    await fireEvent.change(screen.getByLabelText("Connection"), { target: { value: "first" } });
    await fireEvent.change(screen.getByLabelText("Connection"), { target: { value: "second" } });
    expect(await screen.findByText("ops@second.example:22")).toBeInTheDocument();
    finish({ hostname:"first.example", user:"wrong", port:"22" } as never);
    await tick();
    expect(screen.queryByText(/wrong@first/)).toBeNull();
    expect(screen.getByText("ops@second.example:22")).toBeInTheDocument();
  });

  it("hides grants and shows the override only on production, and saves through config", async () => {
    render(SSHHostSettings);
    await fireEvent.click(await screen.findByRole("button", { name: /Add host/ }));
    expect(screen.getByRole("checkbox")).toBeInTheDocument();
    expect(screen.queryByLabelText("Longest grant")).toBeNull();
    await fireEvent.click(screen.getByRole("radio", { name: "Dev" }));
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(screen.getByLabelText("Longest grant")).toBeInTheDocument();
    await fireEvent.change(screen.getByLabelText("Connection"), { target: { value: ":custom" } });
    await fireEvent.input(screen.getByLabelText("Hostname or IP"), { target: { value: "web-1" } });
    await fireEvent.click(screen.getByRole("button", { name: "Save host" }));
    await waitFor(() => expect(ConfigService.SaveSSHHost).toHaveBeenCalled());
    const [, dto] = (ConfigService.SaveSSHHost as ReturnType<typeof vi.fn>).mock.calls.at(-1) as [string, { alias: string; tier: string }];
    expect(dto).toMatchObject({ alias: "web-1", tier: "dev" });
  });
});

describe("opening a session", () => {
  it("ssh.open opens it rather than changing a Glance nobody can act in, and never closes it", async () => {
    const { nav } = await import("$lib/nav.svelte");
    useSession({ ssh: { connections: [conn()], lastSeq: 1 } });
    nav.focusedTerm = "";
    ssh.open(SID, { conn: "c1" });
    ssh.open(SID, { conn: "c1" });
    expect(nav.focusedTerm).toBe(SID);
    nav.focusedTerm = "";
  });
  it("the chip opens it, and the same chip on the open session keeps it open", async () => {
    const { nav } = await import("$lib/nav.svelte");
    const info = { connections: [conn()], lastSeq: 1 } as never;
    useSession({ ssh: info });
    nav.focusedTerm = "";
    const { container } = render(SSHChip, { sessionId: SID, info });
    const chip = container.querySelector("[data-ssh-chip]") as HTMLElement;
    await fireEvent.click(chip);
    await fireEvent.click(chip);
    expect(nav.focusedTerm).toBe(SID);
    nav.focusedTerm = "";
  });
});
