import { describe, it, expect, vi, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";

const { Status, CreateTeam, Invite, TeamSettings } = vi.hoisted(() => ({
  Status: vi.fn(),
  CreateTeam: vi.fn(),
  Invite: vi.fn(),
  TeamSettings: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  HubService: {
    Status,
    CreateTeam,
    Invite,
    TeamSettings,
    Board: vi.fn().mockResolvedValue({ items: [] }),
    Org: vi.fn().mockResolvedValue({ devices: [] }),
  },
  ConfigService: {},
}));

import HubAdmin from "./HubAdmin.svelte";
import HubCreateTeam from "./HubCreateTeam.svelte";
import { confirm } from "$lib/confirm.svelte";
import { hub } from "$lib/hub.svelte";

const CODE = `lola-hub-create:https://hub.example.com#${"C".repeat(32)}`;
const KEY = "lin_api_" + "a".repeat(40);
const HOOK = "https://hooks.slack.com/services/T0001/B0001/abcDEF123";
const view = (over = {}) => ({
  org: "acme", secretsAvailable: true, linearKeySet: false, digestSet: false, teams: [], ...over,
});

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  confirm.cancel();
  hub.pending = {};
  hub.status = null;
  Status.mockResolvedValue({ enabled: false, joined: false, sessions: [] });
});

describe("HubCreateTeam", () => {
  it("creates a team from one code and a name, then forgets the code", async () => {
    CreateTeam.mockResolvedValue({ org: "acme", deviceName: "mac", adminKeyId: "k1", fingerprint: "ab12 cd34 ef56 7890" });
    const onCreated = vi.fn();
    render(HubCreateTeam, { props: { onCreated } });
    const create = screen.getByRole("button", { name: "Create team" });
    expect(create).toBeDisabled();

    const code = screen.getByLabelText("Create code") as HTMLInputElement;
    expect(code.type).toBe("password");
    await fireEvent.input(code, { target: { value: CODE } });
    expect(screen.getByText("https://hub.example.com")).toBeInTheDocument();
    // What a person types becomes a valid team name.
    const name = screen.getByLabelText("Team name") as HTMLInputElement;
    await fireEvent.input(name, { target: { value: "Acme Web" } });
    expect(name.value).toBe("acme-web");
    expect(create).toBeEnabled();

    await fireEvent.click(create);
    await waitFor(() => expect(onCreated).toHaveBeenCalled());
    expect(CreateTeam).toHaveBeenCalledWith({ code: CODE, team: "acme-web", name: "", replaceAdminKey: false });
    expect(code.value).toBe("");
  });

  it("says what is wrong with a pasted invite and sends nothing", async () => {
    render(HubCreateTeam);
    await fireEvent.input(screen.getByLabelText("Create code"), { target: { value: `lola-hub:https://hub.example.com#${"T".repeat(32)}` } });
    await fireEvent.input(screen.getByLabelText("Team name"), { target: { value: "acme" } });
    expect(screen.getByText(/Join a team/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Create team" })).toBeDisabled();
    expect(CreateTeam).not.toHaveBeenCalled();
  });

  it("asks before discarding another team's admin key", async () => {
    CreateTeam.mockRejectedValueOnce(new Error("hub: this machine already holds a team admin key (team old at https://h); creating a team would discard it"));
    CreateTeam.mockResolvedValueOnce({ org: "acme", deviceName: "mac", adminKeyId: "k1", fingerprint: "f" });
    const onCreated = vi.fn();
    render(HubCreateTeam, { props: { onCreated } });
    await fireEvent.input(screen.getByLabelText("Create code"), { target: { value: CODE } });
    await fireEvent.input(screen.getByLabelText("Team name"), { target: { value: "acme" } });
    await fireEvent.click(screen.getByRole("button", { name: "Create team" }));
    await waitFor(() => expect(confirm.request?.title).toMatch(/Discard/));
    expect(onCreated).not.toHaveBeenCalled();
    confirm.accept();
    await waitFor(() => expect(onCreated).toHaveBeenCalled());
    expect(CreateTeam).toHaveBeenLastCalledWith({ code: CODE, team: "acme", name: "", replaceAdminKey: true });
  });

  it("shows a refusal in plain words without the code", async () => {
    CreateTeam.mockRejectedValue(new Error("hub: HTTP 401 unauthorized: refused"));
    render(HubCreateTeam);
    await fireEvent.input(screen.getByLabelText("Create code"), { target: { value: CODE } });
    await fireEvent.input(screen.getByLabelText("Team name"), { target: { value: "acme" } });
    await fireEvent.click(screen.getByRole("button", { name: "Create team" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/works once/);
    expect(alert.textContent).not.toContain("C".repeat(32));
  });
});

describe("HubAdmin", () => {
  it("shows status only and stores a Linear key write-only", async () => {
    TeamSettings.mockResolvedValueOnce(view());
    render(HubAdmin);
    await waitFor(() => expect(document.querySelector("[data-linear-state]")?.textContent).toMatch(/No API key yet/));
    expect(TeamSettings).toHaveBeenCalledWith({ teams: true });

    const key = screen.getByLabelText("Linear API key") as HTMLInputElement;
    expect(key.type).toBe("password");
    const save = screen.getByRole("button", { name: "Save key" });
    await fireEvent.input(key, { target: { value: "hunter2" } });
    expect(save).toBeDisabled();
    await fireEvent.input(key, { target: { value: KEY } });
    TeamSettings.mockResolvedValueOnce(view({ linearKeySet: true, teams: [{ id: "t-1", key: "ENG", name: "Engineering" }] }));
    await fireEvent.click(save);
    await waitFor(() => expect(document.querySelector("[data-linear-state]")?.textContent).toMatch(/is set/));
    expect(TeamSettings).toHaveBeenLastCalledWith({ linearKey: KEY });
    // The key is gone from the page.
    expect(key.value).toBe("");
    expect(document.body.innerHTML).not.toContain(KEY);
    expect(screen.getByLabelText("Linear team")).toBeInTheDocument();
    expect(screen.getByText(/Pick the team/)).toBeInTheDocument();
  });

  it("clears a refused key from the field and says why", async () => {
    TeamSettings.mockResolvedValueOnce(view());
    render(HubAdmin);
    const key = (await screen.findByLabelText("Linear API key")) as HTMLInputElement;
    await waitFor(() => expect(key).toBeEnabled());
    await fireEvent.input(key, { target: { value: KEY } });
    TeamSettings.mockRejectedValueOnce(new Error("hub: Linear refused that key; nothing was changed"));
    await fireEvent.click(screen.getByRole("button", { name: "Save key" }));
    expect((await screen.findByText(/did not accept that key/)).textContent).not.toContain(KEY);
    expect(key.value).toBe("");
  });

  it("accepts only a Slack webhook for the digest", async () => {
    TeamSettings.mockResolvedValueOnce(view());
    render(HubAdmin);
    const hook = (await screen.findByLabelText("Slack webhook")) as HTMLInputElement;
    await waitFor(() => expect(hook).toBeEnabled());
    const save = screen.getByRole("button", { name: "Save webhook" });
    await fireEvent.input(hook, { target: { value: "https://169.254.169.254/latest/meta-data" } });
    expect(save).toBeDisabled();
    expect(screen.getByText(/Only a Slack incoming webhook/)).toBeInTheDocument();
    await fireEvent.input(hook, { target: { value: HOOK } });
    TeamSettings.mockResolvedValueOnce(view({ digestSet: true }));
    await fireEvent.click(save);
    await waitFor(() => expect(document.querySelector("[data-digest-state]")?.textContent).toMatch(/is set/));
    expect(TeamSettings).toHaveBeenLastCalledWith({ digestWebhook: HOOK });
    expect(document.body.innerHTML).not.toContain(HOOK);
  });

  it("disables secrets on a hub that cannot store them", async () => {
    TeamSettings.mockResolvedValue(view({ secretsAvailable: false }));
    render(HubAdmin);
    await waitFor(() => expect(document.querySelector("[data-no-secrets]")).not.toBeNull());
    expect(screen.getByLabelText("Linear API key")).toBeDisabled();
    expect(screen.getByLabelText("Slack webhook")).toBeDisabled();
  });

  it("mints an invite, shows it once with the key fingerprint, and drops it on Done", async () => {
    TeamSettings.mockResolvedValue(view());
    const invite = `lola-hub:https://hub.example.com#${"T".repeat(32)}#k1:${"A".repeat(43)}`;
    Invite.mockResolvedValue({ invite, expiresAt: "2026-11-21T10:00:00Z", keyId: "k1", fingerprint: "ab12 cd34 ef56 7890" });
    render(HubAdmin);
    const create = screen.getByRole("button", { name: "Create invite" });
    expect(create).toBeDisabled();
    await fireEvent.input(screen.getByLabelText("Teammate’s name"), { target: { value: "Ada" } });
    await fireEvent.click(create);
    await waitFor(() => expect(document.querySelector("[data-invite-minted]")).not.toBeNull());
    expect(Invite).toHaveBeenCalledWith({ label: "Ada", ttlSeconds: 7 * 24 * 3600 });
    expect((screen.getByLabelText("Invite") as HTMLInputElement).value).toBe(invite);
    expect(document.querySelector("[data-fingerprint]")?.textContent).toBe("ab12 cd34 ef56 7890");
    await fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(document.querySelector("[data-invite-minted]")).toBeNull();
    expect(document.body.innerHTML).not.toContain("T".repeat(32));
  });
});
