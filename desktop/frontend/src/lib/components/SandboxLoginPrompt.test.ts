import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/svelte";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

const { status, setToken } = vi.hoisted(() => ({ status: vi.fn(), setToken: vi.fn() }));

vi.mock("@bindings/desktop", () => ({
  ConfigService: {
    SandboxStatus: () => status(),
    SetSandboxToken: (t: string) => setToken(t),
  },
}));

import SandboxLoginPrompt from "./SandboxLoginPrompt.svelte";

describe("SandboxLoginPrompt", () => {
  beforeEach(() => {
    setToken.mockResolvedValue(undefined);
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("asks for a token when no Claude login exists, then disappears once saved", async () => {
    status.mockResolvedValue({ tokenStored: false, claudeSource: "" });
    render(SandboxLoginPrompt);
    expect(await screen.findByText("Connect Claude for Docker workspaces")).toBeInTheDocument();
    expect(screen.getByText("claude setup-token")).toBeInTheDocument();
    const input = screen.getByLabelText("2. Paste the token") as HTMLInputElement;
    expect(input.type).toBe("password");
    await fireEvent.input(input, { target: { value: "sk-ant-oat01-x" } });
    await fireEvent.click(screen.getByRole("button", { name: "Save token" }));
    await waitFor(() => expect(setToken).toHaveBeenCalledWith("sk-ant-oat01-x"));
    await waitFor(() => expect(screen.queryByText("Connect Claude for Docker workspaces")).not.toBeInTheDocument());
  });

  it("stays hidden when a token is stored or the env supplies one", async () => {
    for (const st of [{ tokenStored: true, claudeSource: "" }, { tokenStored: false, claudeSource: "CLAUDE_CODE_OAUTH_TOKEN" }]) {
      status.mockResolvedValue(st);
      render(SandboxLoginPrompt);
      await waitFor(() => expect(status).toHaveBeenCalled());
      expect(screen.queryByText("Connect Claude for Docker workspaces")).not.toBeInTheDocument();
      cleanup();
      status.mockClear();
    }
  });

  it("keeps the prompt and shows the error when saving fails", async () => {
    status.mockResolvedValue({ tokenStored: false, claudeSource: "" });
    setToken.mockRejectedValue(new Error("keychain locked"));
    render(SandboxLoginPrompt);
    const input = (await screen.findByLabelText("2. Paste the token")) as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "sk-ant-oat01-x" } });
    await fireEvent.click(screen.getByRole("button", { name: "Save token" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("keychain locked");
    expect(screen.getByText("Connect Claude for Docker workspaces")).toBeInTheDocument();
  });
});
