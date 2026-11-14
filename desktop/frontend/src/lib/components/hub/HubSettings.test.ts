import { describe, it, expect, vi, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/svelte";

const { Status, Join, Leave, Sync, DefaultDeviceName, Kinds, SaveHubRules, PinPolicyKey, PolicyKeyFingerprint, HubReadiness } = vi.hoisted(() => ({
  Status: vi.fn(),
  Join: vi.fn(),
  Leave: vi.fn(),
  Sync: vi.fn(),
  DefaultDeviceName: vi.fn(),
  Kinds: vi.fn(),
  SaveHubRules: vi.fn(),
  PinPolicyKey: vi.fn(),
  PolicyKeyFingerprint: vi.fn(),
  HubReadiness: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  HubService: { Status, Join, Leave, Sync, DefaultDeviceName, Kinds, Board: vi.fn().mockResolvedValue({ items: [] }), Org: vi.fn().mockResolvedValue({ devices: [] }) },
  ConfigService: {
    HubProjects: () => Promise.resolve([]),
    SaveHubProject: () => Promise.resolve(),
    SaveHubRules,
    PinPolicyKey,
    PolicyKeyFingerprint,
    HubReadiness,
  },
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

function mount(opts: { policy?: unknown; onIdentityChange?: () => void; onOpenTab?: (t: string) => void } = {}) {
  const sink = { values: {} as Record<string, unknown>, dirty: false, saved: [] as Record<string, unknown>[] };
  render(Harness, { initial: { ...initial }, sink, policy: (opts.policy ?? null) as never, onIdentityChange: opts.onIdentityChange, onOpenTab: opts.onOpenTab });
  return sink;
}

const INVITE = `lola-hub:https://hub.example.com#tok_0123456789abcdef#kadmin1:${PUB}`;

beforeEach(() => {
  cleanup();
  confirm.cancel();
  hub.reset();
  for (const f of [Status, Join, Leave, Sync, DefaultDeviceName, Kinds, SaveHubRules, PinPolicyKey, PolicyKeyFingerprint, HubReadiness]) f.mockReset();
  SaveHubRules.mockResolvedValue(undefined);
  PinPolicyKey.mockResolvedValue(true);
  PolicyKeyFingerprint.mockResolvedValue("ab12 cd34 ef56 7890");
  HubReadiness.mockResolvedValue([]);
  Status.mockResolvedValue({ enabled: false, joined: false, claimsToday: 0, maxClaimsPerDay: 4, pendingOutcomes: 0, sessions: [] });
  DefaultDeviceName.mockResolvedValue("studio-mac");
  Kinds.mockResolvedValue([
    { id: "dep_advisory", label: "Security advisories" },
    { id: "red_bump_pr", label: "Failing dependency bumps" },
  ]);
});

describe("HubSettings — not joined", () => {
  it("says the hub removed this Mac above the join form, and only then", async () => {
    Status.mockResolvedValue({ enabled: true, joined: false, membership: "revoked", claimsToday: 0, maxClaimsPerDay: 4, pendingOutcomes: 0, sessions: [] });
    mount();
    await screen.findByLabelText("Invite");
    expect(document.querySelector("[data-hub-revoked]")).toHaveTextContent("The hub no longer accepts this Mac");
    cleanup();
    Status.mockResolvedValue({ enabled: false, joined: false, membership: "not joined", claimsToday: 0, maxClaimsPerDay: 4, pendingOutcomes: 0, sessions: [] });
    mount();
    await screen.findByLabelText("Invite");
    // The store is shared: the alert goes once the new status is read.
    await waitFor(() => expect(document.querySelector("[data-hub-revoked]")).toBeNull());
  });

  it("joins with one pasted invite, clears it and reports the org", async () => {
    const onIdentityChange = vi.fn();
    Join.mockResolvedValue({ org: "acme", deviceName: "studio-mac", deviceId: "dev_1", url: "https://hub.example.com" });
    mount({ onIdentityChange });
    const field = (await screen.findByLabelText("Invite")) as HTMLInputElement;
    expect(field.type).toBe("password");
    await waitFor(() => expect(screen.getByLabelText("Device name")).toHaveAttribute("placeholder", "studio-mac"));
    const join = screen.getByRole("button", { name: "Join hub" });
    expect(join).toBeDisabled();
    await fireEvent.input(field, { target: { value: `lola-hub:https://hub.example.com#tok_0123456789abcdef` } });
    expect(screen.getByText("https://hub.example.com")).toBeInTheDocument();
    expect(join).toBeEnabled();
    Status.mockResolvedValue({ ...joinedStatus, policy: undefined });
    await fireEvent.click(join);
    // The daemon parses the invite itself: no URL is sent beside it.
    await waitFor(() => expect(Join).toHaveBeenCalledWith({ url: "", token: "lola-hub:https://hub.example.com#tok_0123456789abcdef", name: "" }));
    await screen.findByText("Joined acme as studio-mac.");
    expect(onIdentityChange).toHaveBeenCalled();
    expect(screen.queryByLabelText("Invite")).toBeNull();
    expect(document.body.innerHTML).not.toContain("tok_0123456789abcdef");
    expect(PinPolicyKey).not.toHaveBeenCalled();
  });

  it("shows the invite's policy key and pins it only when the person ticks trust", async () => {
    Join.mockResolvedValue({ org: "acme", deviceName: "studio-mac", deviceId: "dev_1", url: "https://hub.example.com" });
    const sink = mount();
    await fireEvent.input(await screen.findByLabelText("Invite"), { target: { value: INVITE } });
    const card = document.querySelector("[data-invite-key]") as HTMLElement;
    expect(within(card).getByText("kadmin1")).toBeInTheDocument();
    await waitFor(() => expect(within(card).getByText("ab12 cd34 ef56 7890")).toBeInTheDocument());
    expect(PolicyKeyFingerprint).toHaveBeenCalledWith(PUB);
    const trust = within(card).getByRole("checkbox", { name: "Trust this key for team settings" }) as HTMLInputElement;
    expect(trust.checked).toBe(false);
    await fireEvent.click(trust);
    Status.mockResolvedValue({ ...joinedStatus, policy: undefined });
    await fireEvent.click(screen.getByRole("button", { name: "Join hub" }));
    await waitFor(() => expect(PinPolicyKey).toHaveBeenCalledWith("kadmin1", PUB));
    await waitFor(() => expect(sink.values.hubTrustedKeys).toEqual([{ keyId: "kadmin1", publicKey: PUB }]));
  });

  it("never pins an untrusted invite key", async () => {
    Join.mockResolvedValue({ org: "acme", deviceName: "studio-mac", deviceId: "dev_1", url: "https://hub.example.com" });
    mount();
    await fireEvent.input(await screen.findByLabelText("Invite"), { target: { value: INVITE } });
    Status.mockResolvedValue({ ...joinedStatus, policy: undefined });
    await fireEvent.click(screen.getByRole("button", { name: "Join hub" }));
    await screen.findByText("Joined acme as studio-mac.");
    expect(PinPolicyKey).not.toHaveBeenCalled();
  });

  it("says what is wrong with an invite without echoing it", async () => {
    mount();
    await fireEvent.input(await screen.findByLabelText("Invite"), { target: { value: "lola-hub:https://hub.example.com#tok_secret#bad!id:x" } });
    expect(screen.getByText("The invite’s policy key id is malformed.")).toBeInTheDocument();
    expect(document.body.textContent).not.toContain("tok_secret");
    expect(screen.getByRole("button", { name: "Join hub" })).toBeDisabled();
  });

  it("joins by hand with a URL and a token, carrying over a pasted bare token", async () => {
    Join.mockResolvedValue({ org: "acme", deviceName: "studio-mac", deviceId: "dev_1", url: "https://hub.example.com" });
    mount();
    await fireEvent.input(await screen.findByLabelText("Invite"), { target: { value: "tok_0123456789abcdef" } });
    expect(screen.getByText(/token without the hub’s address/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Join hub" })).toBeDisabled();
    await fireEvent.click(screen.getByRole("button", { name: "Enter details by hand" }));
    const token = screen.getByLabelText("Enrollment token") as HTMLInputElement;
    expect(token.value).toBe("tok_0123456789abcdef");
    await fireEvent.input(screen.getByLabelText("Hub URL"), { target: { value: "https://hub.example.com" } });
    Status.mockResolvedValue({ ...joinedStatus, policy: undefined });
    await fireEvent.click(screen.getByRole("button", { name: "Join hub" }));
    await waitFor(() => expect(Join).toHaveBeenCalledWith({ url: "https://hub.example.com", token: "tok_0123456789abcdef", name: "" }));
  });

  it("shows a refused join in plain words and keeps the form", async () => {
    Join.mockRejectedValue(new Error("hub: join: enroll token rejected"));
    mount();
    await fireEvent.input(await screen.findByLabelText("Invite"), { target: { value: INVITE } });
    await fireEvent.click(screen.getByRole("button", { name: "Join hub" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("did not accept this invite");
    expect(screen.getByLabelText("Invite")).toBeInTheDocument();
  });

  it("says a slow join is still waiting", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    try {
      let resolve: (v: unknown) => void = () => {};
      Join.mockReturnValue(new Promise((r) => (resolve = r)));
      mount();
      await fireEvent.input(await screen.findByLabelText("Invite"), { target: { value: INVITE } });
      await fireEvent.click(screen.getByRole("button", { name: "Join hub" }));
      expect(await screen.findByText("Contacting the hub…")).toBeInTheDocument();
      await vi.advanceTimersByTimeAsync(11_000);
      expect(await screen.findByText(/up to 90 seconds/)).toBeInTheDocument();
      resolve({ org: "acme", deviceName: "studio-mac", deviceId: "dev_1", url: "https://hub.example.com" });
    } finally {
      vi.useRealTimers();
    }
  });

  it("refuses a plain-http URL that is not loopback", async () => {
    mount();
    await fireEvent.click(await screen.findByRole("button", { name: "Enter details by hand" }));
    await fireEvent.input(screen.getByLabelText("Hub URL"), { target: { value: "http://hub.example.com" } });
    expect(screen.getByText("Use https (plain http only for localhost).")).toBeInTheDocument();
    await fireEvent.input(screen.getByLabelText("Hub URL"), { target: { value: "http://127.0.0.1:8080" } });
    expect(screen.queryByText(/Use https/)).toBeNull();
  });

  it("tells the person to update an older daemon", async () => {
    Status.mockRejectedValue(new Error('unknown cmd "hubStatus"'));
    mount();
    expect(await screen.findByText("Update the lola daemon to use Team hub")).toBeInTheDocument();
    expect(screen.queryByLabelText("Invite")).toBeNull();
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
    expect(within(policy).getByText("· v3")).toBeInTheDocument();
    expect(within(policy).getByText("kadmin1")).toBeInTheDocument();
    const row = within(policy).getByText("Global concurrency cap").closest("tr")!;
    expect(within(row).getByText("2")).toBeInTheDocument();
    expect(within(row).getByText("6")).toBeInTheDocument();
  });

  it("shows the hub's error on the card", async () => {
    Status.mockResolvedValue({ ...joinedStatus, lastError: "heartbeat: hub not reachable", lastErrorAt: new Date(Date.now() + 1000).toISOString() });
    mount();
    const card = await screen.findByLabelText("Hub connection");
    // The label is lib/hub.svelte's connectionState (lane-owned wording).
    expect(card.querySelector('[data-connection="bad"]')).not.toBeNull();
    expect(within(card).getByRole("alert")).toHaveTextContent("heartbeat: hub not reachable");
  });

  it("leaves through the confirm store, with the option to forget the key", async () => {
    const onIdentityChange = vi.fn();
    Leave.mockResolvedValue({ left: true, keyDeleted: true });
    mount({ onIdentityChange });
    await fireEvent.click(await screen.findByRole("button", { name: "Leave hub…" }));
    expect(Leave).not.toHaveBeenCalled();
    expect(confirm.request?.title).toBe("Leave the team hub?");
    expect(confirm.request?.detail).toMatch(/handed back to the team/);
    confirm.request!.choices!.items[0].checked = true;
    confirm.accept();
    await waitFor(() => expect(Leave).toHaveBeenCalledWith(true));
    await waitFor(() => expect(onIdentityChange).toHaveBeenCalled());
  });

  it("syncs on request", async () => {
    Sync.mockResolvedValue({ queued: true });
    mount();
    await fireEvent.click(await screen.findByRole("button", { name: "Sync now" }));
    expect(await screen.findByRole("button", { name: "Syncing…" })).toBeDisabled();
    expect(Sync).toHaveBeenCalled();
  });
});

describe("HubSettings — local rules", () => {
  beforeEach(() => Status.mockResolvedValue({ ...joinedStatus }));

  it("saves each rule at once and folds it into the form", async () => {
    const sink = mount();
    await fireEvent.click(await screen.findByLabelText("Security advisories", { exact: false }));
    await waitFor(() => expect(SaveHubRules).toHaveBeenCalledWith(expect.objectContaining({ acceptKinds: ["dep_advisory"], maxClaimsPerDay: null })));
    await waitFor(() => expect(sink.values.hubAcceptKinds).toEqual(["dep_advisory"]));
    expect(sink.saved.at(-1)).toEqual({ hubAcceptKinds: ["dep_advisory"] });

    const claims = screen.getByLabelText("Claims per day");
    await fireEvent.input(claims, { target: { value: "2" } });
    expect(sink.dirty).toBe(true);
    await fireEvent.change(claims);
    await waitFor(() => expect(SaveHubRules).toHaveBeenCalledWith(expect.objectContaining({ maxClaimsPerDay: 2, acceptKinds: null })));
    await waitFor(() => expect(sink.values.hubMaxClaimsPerDay).toBe(2));
    expect(sink.dirty).toBe(false);

    await fireEvent.input(claims, { target: { value: "500" } });
    expect(screen.getByText("Enter a whole number from 0 to 100.")).toBeInTheDocument();
    await fireEvent.change(claims);
    expect(SaveHubRules).not.toHaveBeenCalledWith(expect.objectContaining({ maxClaimsPerDay: 500 }));
    expect(sink.dirty).toBe(true);

    await fireEvent.input(screen.getByLabelText("Check every (seconds)"), { target: { value: "1000" } });
    expect(screen.getByText("Enter seconds from 60 to 900.")).toBeInTheDocument();

    expect(screen.queryByText(/run directly on this Mac/)).toBeNull();
    await fireEvent.click(screen.getByRole("checkbox", { name: "Sandboxed only" }));
    await waitFor(() => expect(sink.values.hubRequireSandbox).toBe(false));
    expect(screen.getByText(/run directly on this Mac/)).toBeInTheDocument();
  });

  it("says that not applying the policy is a local opt-out the team can see", async () => {
    const sink = mount();
    await fireEvent.click(await screen.findByRole("checkbox", { name: "Apply the team policy" }));
    await waitFor(() => expect(sink.values.hubApplyPolicy).toBe(false));
    expect(screen.getByText(/local opt-out/)).toHaveTextContent(/your team can see/);
  });

  it("shows a refused rule save and keeps the form value", async () => {
    SaveHubRules.mockRejectedValue(new Error("hub.poll_seconds must be between 60 and 900 seconds"));
    const sink = mount();
    await fireEvent.click(await screen.findByRole("checkbox", { name: "Sandboxed only" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("between 60 and 900");
    expect(sink.values.hubRequireSandbox).toBe(true);
  });

  it("keeps trusted keys as a guarded draft until Save keys", async () => {
    const sink = mount();
    await fireEvent.click(await screen.findByRole("button", { name: "Add key" }));
    expect(sink.dirty).toBe(true);
    expect(screen.getByText("Keys not saved yet")).toBeInTheDocument();
    await fireEvent.input(screen.getByLabelText("Key id 1"), { target: { value: "bad id!" } });
    expect(screen.getByText(/Key ids use letters/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save keys" })).toBeDisabled();
    await fireEvent.input(screen.getByLabelText("Key id 1"), { target: { value: `kadmin1:${PUB}` } });
    expect(screen.queryByText(/Key ids use letters|Public keys are/)).toBeNull();
    expect(SaveHubRules).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole("button", { name: "Save keys" }));
    await waitFor(() => expect(SaveHubRules).toHaveBeenCalledWith(expect.objectContaining({ trustedKeys: [{ keyId: "kadmin1", publicKey: PUB }] })));
    await waitFor(() => expect(sink.values.hubTrustedKeys).toEqual([{ keyId: "kadmin1", publicKey: PUB }]));
    expect(sink.dirty).toBe(false);
    expect(screen.getByText("Keys saved")).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Remove key 1" }));
    expect(sink.dirty).toBe(true);
    await fireEvent.click(screen.getByRole("button", { name: "Discard" }));
    expect(screen.getByLabelText("Key id 1")).toHaveValue("kadmin1");
    expect(sink.dirty).toBe(false);
  });

  it("shows the policy's escalate_after whenever it is set", async () => {
    mount({ policy: { ...lockedPolicy, escalateAfter: 3 } });
    expect(await screen.findByText(/after 3 failed attempts/)).toBeInTheDocument();
  });

  it("shows the readiness list right after a join and opens the Sandbox tab for its fix", async () => {
    Status.mockResolvedValue({ enabled: false, joined: false, claimsToday: 0, maxClaimsPerDay: 4, pendingOutcomes: 0, sessions: [] });
    HubReadiness.mockResolvedValue([{ name: "hub sandbox login", ok: false, detail: "x", fix: "sandbox_token" }]);
    Join.mockResolvedValue({ org: "acme", deviceName: "studio-mac", deviceId: "dev_1", url: "https://hub.example.com" });
    const onOpenTab = vi.fn();
    mount({ onOpenTab });
    await fireEvent.input(await screen.findByLabelText("Invite"), { target: { value: INVITE } });
    Status.mockResolvedValue({ ...joinedStatus });
    await fireEvent.click(screen.getByRole("button", { name: "Join hub" }));
    const list = await screen.findByLabelText("Ready for chores");
    await fireEvent.click(await within(list).findByRole("button", { name: "Log in" }));
    expect(onOpenTab).toHaveBeenCalledWith("sandbox");
  });

  it("locks the accepted kinds when the team policy sets them", async () => {
    mount({ policy: lockedPolicy });
    const box = await screen.findByLabelText("Security advisories", { exact: false });
    await waitFor(() => expect(box).toBeDisabled());
    expect(screen.getByText("Set by team policy v3")).toBeInTheDocument();
  });
});

describe("HubSettings — no policy", () => {
  it("shows an empty state that adds the first key", async () => {
    Status.mockResolvedValue({ ...joinedStatus, policy: null });
    const sink = mount();
    const empty = await screen.findByText("No team policy");
    expect(screen.getByText("Add your admin’s key to verify one.")).toBeInTheDocument();
    expect(screen.queryByRole("checkbox", { name: "Apply the team policy" })).toBeNull();
    await fireEvent.click(within(empty.closest("[data-policy-empty]") as HTMLElement).getByRole("button", { name: "Add key" }));
    expect(screen.getByLabelText("Key id 1")).toHaveValue("");
    expect(sink.dirty).toBe(true);
  });

  it("shows only the join form before joining", async () => {
    mount();
    await screen.findByLabelText("Invite");
    expect(screen.queryByText("Chores on this Mac")).toBeNull();
    expect(screen.queryByText("Team policy")).toBeNull();
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
