import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/svelte";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const { status, setImage, setToken, clearToken, setApiKey } = vi.hoisted(() => ({
  setApiKey: vi.fn(),
  status: vi.fn(),
  setImage: vi.fn(),
  setToken: vi.fn(),
  clearToken: vi.fn(),
}));

vi.mock("@bindings/desktop", () => ({
  ConfigService: {
    SandboxStatus: () => status(),
    SetSandboxImage: (i: string) => setImage(i),
    SetSandboxToken: (t: string) => setToken(t),
    ClearSandboxToken: () => clearToken(),
    SetSandboxUseAPIKey: (on: boolean) => setApiKey(on),
  },
}));

// The runner has its own tests; here it is a button that reports a stored token.
vi.mock("./SetupTokenRunner.svelte", async () => ({ default: (await import("./SetupTokenRunnerStub.test.svelte")).default }));

import SandboxSettings from "./SandboxSettings.svelte";
import { confirm } from "$lib/confirm.svelte";

function sample(over: Record<string, unknown> = {}) {
  return {
    image: "",
    defaultImage: "ghcr.io/sushidev-team/lola-sandbox:1.2.3",
    docker: "docker",
    dockerOk: true,
    missing: ["ghcr.io/sushidev-team/lola-sandbox:1.2.3"],
    tokenStored: false,
    claudeSource: "",
    githubSource: "gh auth token",
    useApiKey: false,
    apiKeySet: true,
    codexLogin: true,
    openCodeLogin: false,
    ...over,
  };
}

describe("SandboxSettings", () => {
  beforeEach(() => {
    status.mockResolvedValue(sample());
    setImage.mockResolvedValue(undefined);
    setToken.mockResolvedValue(undefined);
    clearToken.mockResolvedValue(undefined);
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
    confirm.cancel();
  });

  it("shows readiness and the default image as the placeholder", async () => {
    render(SandboxSettings);
    expect(await screen.findByText(/Docker running/)).toBeInTheDocument();
    expect(screen.getByText(/Image pulls on first use/)).toBeInTheDocument();
    expect(screen.getByText("Claude login: Not set")).toBeInTheDocument();
    expect(screen.getByText("Codex login found on this Mac")).toBeInTheDocument();
    expect(screen.getByText("No OpenCode login")).toBeInTheDocument();
    expect(screen.getByLabelText("Default image")).toHaveAttribute("placeholder", "ghcr.io/sushidev-team/lola-sandbox:1.2.3");
  });

  it("says plainly when docker is down, with the fix inline", async () => {
    status.mockResolvedValue(sample({ dockerOk: false }));
    render(SandboxSettings);
    expect(await screen.findByText("Docker not running")).toBeInTheDocument();
    const row = document.querySelector('[data-sandbox-row="docker"]') as HTMLElement;
    expect(row.dataset.state).toBe("needed");
    // The fix is on the page, not behind a popover.
    expect(row).toHaveTextContent("Start Docker Desktop, OrbStack or Colima, then check again.");
  });

  it("draws one state per row: a missing optional login is not a warning", async () => {
    status.mockResolvedValue(sample({ useApiKey: true, apiKeySet: true }));
    render(SandboxSettings);
    await screen.findByText(/Docker running/);
    const state = (id: string) => (document.querySelector(`[data-sandbox-row="${id}"]`) as HTMLElement).dataset.state;
    expect(state("claude")).toBe("ok");
    expect(state("opencode")).toBe("optional");
    expect(state("codex")).toBe("ok");
    expect(state("image")).toBe("info");
    expect(document.querySelector('[data-sandbox-row="claude"] .text-warn')).toBeNull();
  });

  it("checks again on request and when the window comes back", async () => {
    render(SandboxSettings);
    await screen.findByText(/Docker running/);
    expect(status).toHaveBeenCalledTimes(1);
    status.mockResolvedValue(sample({ dockerOk: false }));
    await fireEvent.click(screen.getByRole("button", { name: "Check again" }));
    expect(await screen.findByText("Docker not running")).toBeInTheDocument();
    status.mockResolvedValue(sample());
    window.dispatchEvent(new Event("focus"));
    expect(await screen.findByText("Docker running")).toBeInTheDocument();
    expect(status).toHaveBeenCalledTimes(3);
  });

  it("shows errors without Go's Error: prefix", async () => {
    setImage.mockRejectedValue(new Error("an image reference has no spaces"));
    render(SandboxSettings);
    await fireEvent.input(await screen.findByLabelText("Default image"), { target: { value: "a b" } });
    await fireEvent.click(screen.getByRole("button", { name: "Save image" }));
    const msg = await screen.findByText("an image reference has no spaces");
    expect(msg.textContent).not.toMatch(/^Error/);
  });

  it("opens the paste path when the login fails", async () => {
    render(SandboxSettings);
    await fireEvent.click(await screen.findByRole("button", { name: "Fail the login" }));
    expect(screen.getByLabelText("Token")).toBeInTheDocument();
  });

  it("saves an image override and clears back to the default", async () => {
    render(SandboxSettings);
    const input = await screen.findByLabelText("Default image");
    const save = screen.getByRole("button", { name: "Save image" });
    expect(save).toBeDisabled();
    await fireEvent.input(input, { target: { value: " acme/base:1 " } });
    await fireEvent.click(save);
    await waitFor(() => expect(setImage).toHaveBeenCalledWith("acme/base:1"));
  });

  it("stores the token write-only and clears the field", async () => {
    render(SandboxSettings);
    await fireEvent.click(await screen.findByRole("button", { name: "Paste a token instead" }));
    const input = screen.getByLabelText("Token") as HTMLInputElement;
    expect(input.type).toBe("password");
    await fireEvent.input(input, { target: { value: "sk-ant-oat01-secret" } });
    status.mockResolvedValue(sample({ tokenStored: true }));
    await fireEvent.click(screen.getByRole("button", { name: "Save token" }));
    await waitFor(() => expect(setToken).toHaveBeenCalledWith("sk-ant-oat01-secret"));
    expect(await screen.findByText("Claude login: Token stored in the keychain")).toBeInTheDocument();
    expect(screen.getByText("Logged in")).toBeInTheDocument();
    expect(screen.queryByLabelText("Token")).not.toBeInTheDocument();
    expect(document.body.textContent).not.toContain("sk-ant-oat01-secret");
  });

  it("refreshes the status after Log in with Claude stored a token", async () => {
    render(SandboxSettings);
    await screen.findByText("Claude login: Not set");
    status.mockResolvedValue(sample({ tokenStored: true }));
    await fireEvent.click(screen.getByRole("button", { name: "Log in with Claude" }));
    expect(await screen.findByText("Claude login: Token stored in the keychain")).toBeInTheDocument();
  });

  it("asks before removing the stored token", async () => {
    status.mockResolvedValue(sample({ tokenStored: true }));
    const ask = vi.spyOn(confirm, "ask");
    render(SandboxSettings);
    await fireEvent.click(await screen.findByRole("button", { name: "Remove token" }));
    expect(ask).toHaveBeenCalledTimes(1);
    expect(clearToken).not.toHaveBeenCalled();
    ask.mock.calls[0][0].onConfirm([]);
    await waitFor(() => expect(clearToken).toHaveBeenCalledTimes(1));
  });

  it("shows a stored login as a card and replaces it on request", async () => {
    status.mockResolvedValue(sample({ tokenStored: true }));
    render(SandboxSettings);
    expect(await screen.findByText("Token in the macOS keychain")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Log in with Claude" })).not.toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Replace…" }));
    expect(screen.getByRole("button", { name: "Log in with Claude" })).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.getByText("Logged in")).toBeInTheDocument();
  });

  it("shows an environment login without a remove button", async () => {
    status.mockResolvedValue(sample({ claudeSource: "CLAUDE_CODE_OAUTH_TOKEN" }));
    render(SandboxSettings);
    expect(await screen.findByText("From CLAUDE_CODE_OAUTH_TOKEN")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Remove token" })).not.toBeInTheDocument();
  });

  it("passes the API key only after a confirmed choice", async () => {
    setApiKey.mockResolvedValue(undefined);
    render(SandboxSettings);
    const box = (await screen.findByRole("checkbox", { name: /Use an API key instead/ })) as HTMLInputElement;
    expect(box.checked).toBe(false);

    await fireEvent.click(box);
    expect(confirm.request?.title).toMatch(/ANTHROPIC_API_KEY/);
    expect(box.checked).toBe(false);
    confirm.cancel();
    expect(setApiKey).not.toHaveBeenCalled();

    await fireEvent.click(box);
    status.mockResolvedValue(sample({ useApiKey: true }));
    confirm.accept();
    await waitFor(() => expect(setApiKey).toHaveBeenCalledWith(true));
    expect(await screen.findByText("Billed per token to the key's account.")).toBeInTheDocument();
    expect(box.checked).toBe(true);
  });

  it("turns the API key off without asking", async () => {
    setApiKey.mockResolvedValue(undefined);
    status.mockResolvedValue(sample({ useApiKey: true }));
    render(SandboxSettings);
    const box = (await screen.findByRole("checkbox", { name: /Use an API key instead/ })) as HTMLInputElement;
    await waitFor(() => expect(box.checked).toBe(true));
    await fireEvent.click(box);
    expect(confirm.request).toBeNull();
    await waitFor(() => expect(setApiKey).toHaveBeenCalledWith(false));
  });
});
