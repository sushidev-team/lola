import { render, screen, fireEvent, cleanup } from "@testing-library/svelte";
import { describe, it, expect, vi, afterEach } from "vitest";
import { flushSync } from "svelte";

const { needed } = vi.hoisted(() => ({ needed: vi.fn() }));

vi.mock("@bindings/desktop", () => ({
  ConfigService: {
    SandboxLoginNeeded: (p: string, a: string) => needed(p, a),
    SandboxStatus: () => Promise.resolve({ tokenStored: false, claudeSource: "" }),
    SetSandboxToken: () => Promise.resolve(),
  },
}));
vi.mock("$lib/store.svelte", () => ({ store: { projects: [{ name: "web", label: "Web app" }] } }));
vi.mock("./SetupTokenRunner.svelte", async () => ({ default: (await import("./SetupTokenRunnerStub.test.svelte")).default }));

import SandboxLoginDialog from "./SandboxLoginDialog.svelte";
import { sandboxLogin } from "$lib/sandboxlogin.svelte";

describe("sandbox login gate", () => {
  afterEach(() => {
    cleanup();
    sandboxLogin.finish(false);
    vi.clearAllMocks();
  });

  it("passes straight through when no login is missing", async () => {
    needed.mockResolvedValue(false);
    expect(await sandboxLogin.ensure("web", "claude")).toBe(true);
    expect(sandboxLogin.request).toBeNull();
  });

  it("asks before the start and goes on once logged in", async () => {
    needed.mockResolvedValue(true);
    render(SandboxLoginDialog);
    const ok = sandboxLogin.ensure("web");
    await vi.waitFor(() => expect(sandboxLogin.request).not.toBeNull());
    flushSync();
    expect(await screen.findByText(/Web app runs its agents in Docker/)).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Log in with Claude" }));
    expect(await ok).toBe(true);
    expect(sandboxLogin.request).toBeNull();
  });

  it("starts nothing when cancelled", async () => {
    needed.mockResolvedValue(true);
    render(SandboxLoginDialog);
    const ok = sandboxLogin.ensure("web");
    await fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    expect(await ok).toBe(false);
  });
});
