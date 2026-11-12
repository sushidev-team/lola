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
    expect(screen.getByText(/Image not pulled yet/)).toBeInTheDocument();
    expect(screen.getByText("Claude login: Not set")).toBeInTheDocument();
    expect(screen.getByLabelText("Default image")).toHaveAttribute("placeholder", "ghcr.io/sushidev-team/lola-sandbox:1.2.3");
  });

  it("says plainly when docker is down", async () => {
    status.mockResolvedValue(sample({ dockerOk: false }));
    render(SandboxSettings);
    expect(await screen.findByText("Docker not running")).toBeInTheDocument();
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
    const input = (await screen.findByLabelText("Token")) as HTMLInputElement;
    expect(input.type).toBe("password");
    await fireEvent.input(input, { target: { value: "sk-ant-oat01-secret" } });
    status.mockResolvedValue(sample({ tokenStored: true }));
    await fireEvent.click(screen.getByRole("button", { name: "Save token" }));
    await waitFor(() => expect(setToken).toHaveBeenCalledWith("sk-ant-oat01-secret"));
    await waitFor(() => expect(input.value).toBe(""));
    expect(await screen.findByText("Claude login: Token stored in the keychain")).toBeInTheDocument();
    expect(document.body.textContent).not.toContain("sk-ant-oat01-secret");
  });

  it("asks before removing the stored token", async () => {
    status.mockResolvedValue(sample({ tokenStored: true }));
    const ask = vi.spyOn(confirm, "ask");
    render(SandboxSettings);
    await fireEvent.click(await screen.findByRole("button", { name: "Remove token…" }));
    expect(ask).toHaveBeenCalledTimes(1);
    expect(clearToken).not.toHaveBeenCalled();
    ask.mock.calls[0][0].onConfirm([]);
    await waitFor(() => expect(clearToken).toHaveBeenCalledTimes(1));
  });

  it("passes the API key only after a confirmed choice", async () => {
    setApiKey.mockResolvedValue(undefined);
    render(SandboxSettings);
    const box = (await screen.findByRole("checkbox", { name: /Use ANTHROPIC_API_KEY instead/ })) as HTMLInputElement;
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
    const box = (await screen.findByRole("checkbox", { name: /Use ANTHROPIC_API_KEY instead/ })) as HTMLInputElement;
    await waitFor(() => expect(box.checked).toBe(true));
    await fireEvent.click(box);
    expect(confirm.request).toBeNull();
    await waitFor(() => expect(setApiKey).toHaveBeenCalledWith(false));
  });
});
