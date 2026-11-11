import { describe, it, expect, vi, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/svelte";

const { Status, Join, Leave, Sync, DefaultDeviceName, Kinds } = vi.hoisted(() => ({
  Status: vi.fn(),
  Join: vi.fn(),
  Leave: vi.fn(),
  Sync: vi.fn(),
  DefaultDeviceName: vi.fn(),
  Kinds: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  HubService: { Status, Join, Leave, Sync, DefaultDeviceName, Kinds, Board: vi.fn(), Org: vi.fn() },
}));

import Harness from "./HubSettingsHarness.test.svelte";
import { trustedKeyError } from "./HubSettings.svelte";
import { confirm } from "$lib/confirm.svelte";
import { hub } from "$lib/hub.svelte";

const PUB = "A".repeat(43);
const initial = {
  hubAcceptKinds: [],
  hubMaxClaimsPerDay: 4,
  hubRequireSandbox: true,
  hubPollSeconds: 300,
  hubApplyPolicy: true,
  hubTrustedKeys: [],
};
const joinedStatus = {
  enabled: true,
  joined: true,
  url: "https://hub.example.com",
  org: "acme",
  deviceName: "studio-mac",
  device: "dev_1",
  lastHeartbeat: new Date().toISOString(),
  claimsToday: 1,
  maxClaimsPerDay: 4,
  pendingOutcomes: 0,
  sessions: [],
  policy: {
    version: 3,
    keyId: "kadmin1",
    issuedAt: "2026-11-01T10:00:00Z",
    keys: ["max_concurrency_hint", "accept_kinds"],
    summary: [],
    applied: true,
    appliedKeys: [{ key: "max_concurrency_hint", value: "2", local: "6" }],
  },
};
const lockedPolicy = {
  available: true, joined: true, org: "acme", version: 3, applied: true, note: "", keys: ["accept_kinds"], summary: [],
  locked: [
    { key: "max_concurrency_hint", label: "Global concurrency cap", value: "2", local: "6", configKey: "defaults.global_cap", field: "globalCap" },
    { key: "accept_kinds", label: "Accepted chore kinds", value: "dep_advisory", local: "all", configKey: "hub.accept_kinds", field: "hubAcceptKinds" },
  ],
};

function mount(opts: { policy?: unknown; onIdentityChange?: () => void } = {}) {
  const sink = { values: {} as Record<string, unknown> };
  render(Harness, { initial: { ...initial }, sink, policy: (opts.policy ?? null) as never, onIdentityChange: opts.onIdentityChange });
  return sink;
}

beforeEach(() => {
  cleanup();
  confirm.cancel();
  hub.reset();
  for (const f of [Status, Join, Leave, Sync, DefaultDeviceName, Kinds]) f.mockReset();
  Status.mockResolvedValue({ enabled: false, joined: false, claimsToday: 0, maxClaimsPerDay: 4, pendingOutcomes: 0, sessions: [] });
  DefaultDeviceName.mockResolvedValue("studio-mac");
  Kinds.mockResolvedValue([
    { id: "dep_advisory", label: "Security advisories" },
    { id: "red_bump_pr", label: "Failing dependency bumps" },
  ]);
});

describe("HubSettings — not joined", () => {
  it("joins with the typed values, clears the token and reports the org", async () => {
    const onIdentityChange = vi.fn();
    Join.mockResolvedValue({ org: "acme", deviceName: "studio-mac", deviceId: "dev_1", url: "https://hub.example.com" });
    mount({ onIdentityChange });
    const url = await screen.findByLabelText("Hub URL");
    await waitFor(() => expect(screen.getByLabelText("Device name")).toHaveAttribute("placeholder", "studio-mac"));
    const join = screen.getByRole("button", { name: "Join hub" });
    expect(join).toBeDisabled();
    await fireEvent.input(url, { target: { value: "https://hub.example.com" } });
    const token = screen.getByLabelText("Enrollment token") as HTMLInputElement;
    expect(token.type).toBe("password");
    await fireEvent.input(token, { target: { value: "tok_0123456789abcdef" } });
    expect(join).toBeEnabled();
    Status.mockResolvedValue({ ...joinedStatus, policy: undefined });
    await fireEvent.click(join);
    await waitFor(() => expect(Join).toHaveBeenCalledWith({ url: "https://hub.example.com", token: "tok_0123456789abcdef", name: "" }));
    await screen.findByText("Joined acme as studio-mac.");
    expect(onIdentityChange).toHaveBeenCalled();
    // The join form is gone, and with it the token.
    expect(screen.queryByLabelText("Enrollment token")).toBeNull();
  });

  it("shows a refused join inline and keeps the form", async () => {
    Join.mockRejectedValue(new Error("hub: join: enroll token rejected"));
    mount();
    await fireEvent.input(await screen.findByLabelText("Hub URL"), { target: { value: "https://hub.example.com" } });
    await fireEvent.input(screen.getByLabelText("Enrollment token"), { target: { value: "tok_bad" } });
    await fireEvent.click(screen.getByRole("button", { name: "Join hub" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("enroll token rejected");
    expect(screen.getByLabelText("Enrollment token")).toBeInTheDocument();
  });

  it("refuses a plain-http URL that is not loopback", async () => {
    mount();
    await fireEvent.input(await screen.findByLabelText("Hub URL"), { target: { value: "http://hub.example.com" } });
    expect(screen.getByText("Use https (plain http only for localhost).")).toBeInTheDocument();
    await fireEvent.input(screen.getByLabelText("Hub URL"), { target: { value: "http://127.0.0.1:8080" } });
    expect(screen.queryByText(/Use https/)).toBeNull();
  });

  it("tells the person to update an older daemon", async () => {
    Status.mockRejectedValue(new Error('unknown cmd "hubStatus"'));
    mount();
    expect(await screen.findByText("Update the lola daemon to use Team hub")).toBeInTheDocument();
    expect(screen.queryByLabelText("Hub URL")).toBeNull();
  });
});

describe("HubSettings — joined", () => {
  beforeEach(() => Status.mockResolvedValue({ ...joinedStatus }));

  it("shows the connection card and the policy table", async () => {
    mount({ policy: lockedPolicy });
    const card = await screen.findByLabelText("Hub connection");
    expect(within(card).getByText("acme")).toBeInTheDocument();
    expect(within(card).getByText("studio-mac")).toBeInTheDocument();
    expect(within(card).getByText("Connected")).toBeInTheDocument();
    expect(within(card).getByText("1 of 4")).toBeInTheDocument();
    const policy = screen.getByLabelText("Policy details");
    expect(within(policy).getByText("v3")).toBeInTheDocument();
    expect(within(policy).getByText("kadmin1")).toBeInTheDocument();
    const row = within(policy).getByText("Global concurrency cap").closest("tr")!;
    expect(within(row).getByText("2")).toBeInTheDocument();
    expect(within(row).getByText("6")).toBeInTheDocument();
  });

  it("shows the hub's error on the card", async () => {
    Status.mockResolvedValue({ ...joinedStatus, lastError: "heartbeat: hub not reachable", lastErrorAt: new Date(Date.now() + 1000).toISOString() });
    mount();
    const card = await screen.findByLabelText("Hub connection");
    expect(within(card).getByText("Error")).toBeInTheDocument();
    expect(within(card).getByRole("alert")).toHaveTextContent("heartbeat: hub not reachable");
  });

  it("leaves through the confirm store, with the option to forget the key", async () => {
    const onIdentityChange = vi.fn();
    Leave.mockResolvedValue({ left: true, keyDeleted: true });
    mount({ onIdentityChange });
    await fireEvent.click(await screen.findByRole("button", { name: "Leave hub…" }));
    expect(Leave).not.toHaveBeenCalled();
    expect(confirm.request?.title).toBe("Leave the team hub?");
    confirm.request!.choices!.items[0].checked = true;
    confirm.accept();
    await waitFor(() => expect(Leave).toHaveBeenCalledWith(true));
    await waitFor(() => expect(onIdentityChange).toHaveBeenCalled());
  });

  it("syncs on request", async () => {
    Sync.mockResolvedValue({ queued: true });
    mount();
    await fireEvent.click(await screen.findByRole("button", { name: "Sync now" }));
    expect(await screen.findByText("Sync queued.")).toBeInTheDocument();
  });
});

describe("HubSettings — local rules", () => {
  it("edits the [hub] keys and warns when the sandbox is off", async () => {
    const sink = mount();
    await fireEvent.click(await screen.findByLabelText("Security advisories", { exact: false }));
    expect(sink.values.hubAcceptKinds).toEqual(["dep_advisory"]);
    await fireEvent.input(screen.getByLabelText("Claims per day"), { target: { value: "2" } });
    expect(sink.values.hubMaxClaimsPerDay).toBe(2);
    await fireEvent.input(screen.getByLabelText("Claims per day"), { target: { value: "500" } });
    expect(screen.getByText("Enter a whole number from 0 to 100.")).toBeInTheDocument();
    await fireEvent.input(screen.getByLabelText("Check every (seconds)"), { target: { value: "10" } });
    expect(screen.getByText("Enter seconds from 60 to 86400.")).toBeInTheDocument();
    expect(screen.queryByText(/run directly on this Mac/)).toBeNull();
    await fireEvent.click(screen.getByRole("checkbox", { name: "Only work chores in sandboxed projects" }));
    expect(sink.values.hubRequireSandbox).toBe(false);
    expect(screen.getByText(/run directly on this Mac/)).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("checkbox", { name: "Apply the team policy" }));
    expect(sink.values.hubApplyPolicy).toBe(false);
  });

  it("adds, validates, splits a pasted entry and removes trusted keys", async () => {
    const sink = mount();
    await fireEvent.click(await screen.findByRole("button", { name: "Add key" }));
    expect(sink.values.hubTrustedKeys).toEqual([{ keyId: "", publicKey: "" }]);
    await fireEvent.input(screen.getByLabelText("Key id 1"), { target: { value: "bad id!" } });
    expect(screen.getByText(/Key ids use letters/)).toBeInTheDocument();
    await fireEvent.input(screen.getByLabelText("Key id 1"), { target: { value: `kadmin1:${PUB}` } });
    expect(sink.values.hubTrustedKeys).toEqual([{ keyId: "kadmin1", publicKey: PUB }]);
    expect(screen.queryByText(/Key ids use letters|Public keys are/)).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Remove key 1" }));
    expect(sink.values.hubTrustedKeys).toEqual([]);
  });

  it("locks the accepted kinds when the team policy sets them", async () => {
    mount({ policy: lockedPolicy });
    const box = await screen.findByLabelText("Security advisories", { exact: false });
    await waitFor(() => expect(box).toBeDisabled());
    expect(screen.getByText("Set by team policy v3")).toBeInTheDocument();
  });
});

describe("trustedKeyError", () => {
  it("accepts a well-formed key and names each fault", () => {
    const ok = { keyId: "kadmin1", publicKey: PUB };
    expect(trustedKeyError(ok, [ok])).toBe("");
    expect(trustedKeyError({ keyId: "", publicKey: "" }, [])).toBe("");
    expect(trustedKeyError({ keyId: "", publicKey: PUB }, [])).toMatch(/key id/);
    expect(trustedKeyError({ keyId: "kadmin1", publicKey: "short" }, [])).toMatch(/43/);
    expect(trustedKeyError(ok, [ok, { ...ok }])).toMatch(/twice/);
  });
});
