import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/svelte";
import { beforeEach, afterEach, describe, it, expect, vi } from "vitest";

const { pick, inspect, setup, startDaemon, validate } = vi.hoisted(() => ({
  pick: vi.fn(), inspect: vi.fn(), setup: vi.fn(), startDaemon: vi.fn(), validate: vi.fn(),
}));
vi.mock("@bindings/desktop", () => ({ ConfigService: {
  PickFolder: pick, InspectPath: inspect, Setup: setup, ValidateLinearKey: validate,
} }));
vi.mock("$lib/store.svelte", () => ({ store: { hasConfig: false, startDaemon, setFlash: vi.fn() } }));
import Setup from "./Setup.svelte";

const info = (path = "/code/app") => ({ path, isRepo: true, suggestedLabel: "My App", suggestedId: "my-app", repo: "acme/app", defaultBranch: "develop" });

beforeEach(() => {
  vi.resetAllMocks();
  pick.mockResolvedValue("/code/app");
  inspect.mockResolvedValue(info());
  setup.mockResolvedValue({ keychainStored: true });
});
afterEach(cleanup);

describe("first-run setup", () => {
  it("starts from a key and folder using detected details and unchanged defaults", async () => {
    render(Setup);
    expect(screen.getByRole("button", { name: "Start Lola" })).toBeDisabled();
    await fireEvent.input(screen.getByLabelText("Linear API key"), { target: { value: "test-key" } });
    await fireEvent.click(screen.getByRole("button", { name: "Choose folder…" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Start Lola" })).toBeEnabled());
    expect(screen.getByText("Performance settings").closest("details")).not.toHaveAttribute("open");
    await fireEvent.click(screen.getByRole("button", { name: "Start Lola" }));
    expect(setup).toHaveBeenCalledWith({ linearKey: "test-key", projectName: "My App", projectPath: "/code/app", repo: "acme/app", defaultBranch: "develop", concurrencyCap: 2, globalCap: 4, pollInterval: "60s" });
    await waitFor(() => expect(startDaemon).toHaveBeenCalledOnce());
  });

  it("updates automatic details for a new folder but preserves manual edits", async () => {
    render(Setup);
    await fireEvent.click(screen.getByRole("button", { name: "Choose folder…" }));
    await waitFor(() => expect(screen.getByLabelText("GitHub repo")).toHaveValue("acme/app"));
    const details = screen.getByLabelText("Custom Default branch").closest("details")!;
    details.open = true;
    await fireEvent.input(screen.getByLabelText("Custom Default branch"), { target: { value: "main" } });
    pick.mockResolvedValue("/code/other");
    inspect.mockResolvedValue({ ...info("/code/other"), repo: "acme/other", defaultBranch: "release" });
    await fireEvent.click(screen.getByRole("button", { name: "Choose folder…" }));
    await waitFor(() => expect(screen.getByLabelText("GitHub repo")).toHaveValue("acme/other"));
    expect(screen.getByLabelText("Custom Default branch")).toHaveValue("main");
  });

  it("drops late inspection results after the path changes", async () => {
    let resolve!: (value: ReturnType<typeof info>) => void;
    inspect.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    render(Setup);
    const path = screen.getByLabelText("Project path");
    await fireEvent.input(path, { target: { value: "/code/old" } });
    await fireEvent.blur(path);
    await fireEvent.input(path, { target: { value: "/code/new" } });
    resolve(info("/code/old"));
    await waitFor(() => expect(screen.getByRole("button", { name: "Start Lola" })).toBeDisabled());
    expect(path).toHaveValue("/code/new");
    expect(screen.getByLabelText("GitHub repo")).toHaveValue("");
  });

  it.each(["uninspected", "non-repository", "failed", "changed"])("blocks setup for a %s path", async (kind) => {
    render(Setup);
    await fireEvent.input(screen.getByLabelText("Linear API key"), { target: { value: "test-key" } });
    await fireEvent.click(screen.getByRole("button", { name: "Choose folder…" }));
    const start = screen.getByRole("button", { name: "Start Lola" });
    await waitFor(() => expect(start).toBeEnabled());
    const path = screen.getByLabelText("Project path");
    if (kind === "uninspected" || kind === "changed") {
      await fireEvent.input(path, { target: { value: kind === "changed" ? "" : "/code/new" } });
    } else {
      if (kind === "failed") inspect.mockRejectedValueOnce(new Error("Inspection failed"));
      else inspect.mockResolvedValueOnce({ ...info(), isRepo: false });
      await fireEvent.blur(path);
      if (kind === "failed") await screen.findByText(/Inspection failed/);
      else await screen.findByText(/Not a git checkout/);
    }
    expect(start).toBeDisabled();
    await fireEvent.click(start);
    expect(setup).not.toHaveBeenCalled();
  });

  it("keeps setup disabled during a new inspection and ignores its stale success", async () => {
    render(Setup);
    await fireEvent.input(screen.getByLabelText("Linear API key"), { target: { value: "test-key" } });
    await fireEvent.click(screen.getByRole("button", { name: "Choose folder…" }));
    const start = screen.getByRole("button", { name: "Start Lola" });
    await waitFor(() => expect(start).toBeEnabled());
    let resolve!: (value: ReturnType<typeof info>) => void;
    inspect.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    const path = screen.getByLabelText("Project path");
    await fireEvent.blur(path);
    expect(start).toBeDisabled();
    await fireEvent.input(path, { target: { value: "/code/new" } });
    resolve(info());
    await waitFor(() => expect(start).toBeDisabled());
    await fireEvent.click(start);
    expect(setup).not.toHaveBeenCalled();
  });

  it("does not mark a changed key valid when an earlier validation finishes", async () => {
    let resolve!: () => void;
    validate.mockImplementationOnce(() => new Promise<void>((done) => { resolve = done; }));
    render(Setup);
    const key = screen.getByLabelText("Linear API key");
    await fireEvent.input(key, { target: { value: "old-key" } });
    await fireEvent.click(screen.getByRole("button", { name: "Validate" }));
    await fireEvent.input(key, { target: { value: "new-key" } });
    resolve();
    await waitFor(() => expect(screen.getByRole("button", { name: "Validate" })).toBeEnabled());
    expect(screen.queryByText(/key is valid/)).not.toBeInTheDocument();
  });
});
