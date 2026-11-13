import { render, screen, fireEvent, waitFor, cleanup } from "@testing-library/svelte";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// xterm and the Wails bridge are mocked: jsdom has no canvas, and the point
// here is the run's lifecycle (start, frames, stored, error, cancel).
const m = vi.hoisted(() => {
  const handlers = new Map<string, (e: unknown) => void>();
  return {
    handlers,
    start: vi.fn(),
    write: vi.fn(async (_run: number, _data: string) => {}),
    resize: vi.fn(async (_run: number, _cols: number, _rows: number) => {}),
    cancel: vi.fn(async (_run: number) => {}),
    termWrite: vi.fn(),
    dispose: vi.fn(),
    onData: undefined as undefined | ((d: string) => void),
    fire(name: string, data: unknown) {
      handlers.get(name)?.({ data });
    },
  };
});

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    public cols = 90;
    public rows = 14;
    public options: Record<string, unknown> = {};
    public loadAddon = vi.fn();
    public open = vi.fn();
    public onData = (fn: (d: string) => void) => {
      m.onData = fn;
    };
    public onResize = vi.fn();
    public focus = vi.fn();
    public write = m.termWrite;
    public dispose = m.dispose;
  },
}));
vi.mock("@xterm/addon-fit", () => ({ FitAddon: class { public fit = vi.fn(); public activate = vi.fn(); public dispose = vi.fn(); } }));
vi.mock("@xterm/addon-web-links", () => ({ WebLinksAddon: class { public activate = vi.fn(); public dispose = vi.fn(); } }));
vi.mock("$lib/store.svelte", () => ({ store: { openURL: vi.fn() } }));
vi.mock("@wailsio/runtime", () => ({
  Events: {
    On: (name: string, fn: (e: unknown) => void) => {
      m.handlers.set(name, fn);
      return () => m.handlers.delete(name);
    },
  },
}));
vi.mock("@bindings/desktop", () => ({
  SandboxLoginService: {
    Start: (c: number, r: number) => m.start(c, r),
    Write: (run: number, d: string) => m.write(run, d),
    Resize: (run: number, c: number, r: number) => m.resize(run, c, r),
    Cancel: (run: number) => m.cancel(run),
  },
}));

import SetupTokenRunner from "./SetupTokenRunner.svelte";

globalThis.ResizeObserver ??= class {
  observe() {}
  disconnect() {}
  unobserve() {}
} as unknown as typeof ResizeObserver;

describe("SetupTokenRunner", () => {
  beforeEach(() => {
    m.start.mockResolvedValue(7);
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
    m.handlers.clear();
  });

  it("starts a run, draws its frames, forwards keys and reports the stored token", async () => {
    const onStored = vi.fn();
    render(SetupTokenRunner, { onStored });
    await fireEvent.click(screen.getByRole("button", { name: "Log in with Claude" }));
    expect(await screen.findByText("A browser window opens. Finish the login there.")).toBeInTheDocument();
    expect(m.start).toHaveBeenCalledWith(90, 14);
    expect(screen.getByLabelText("Claude login terminal")).toBeInTheDocument();

    m.fire("sandboxlogin:frame", { run: 6, data: "stale run" });
    m.fire("sandboxlogin:frame", { run: 7, data: "Opening browser" });
    expect(m.termWrite).toHaveBeenCalledWith("Opening browser");
    expect(m.termWrite).not.toHaveBeenCalledWith("stale run");

    m.onData?.("code-123\r");
    expect(m.write).toHaveBeenCalledWith(7, "code-123\r");

    m.fire("sandboxlogin:stored", { run: 7, stored: true, cancelled: false, error: "" });
    await waitFor(() => expect(onStored).toHaveBeenCalledOnce());
    expect(screen.queryByLabelText("Claude login terminal")).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("Logged in. Token stored.");
    expect(m.dispose).toHaveBeenCalled();
  });

  it("shows the error when the run ends without a token", async () => {
    render(SetupTokenRunner);
    await fireEvent.click(screen.getByRole("button", { name: "Log in with Claude" }));
    await screen.findByLabelText("Claude login terminal");
    m.fire("sandboxlogin:done", { run: 7, stored: false, cancelled: false, error: "claude setup-token ended without a token." });
    expect(await screen.findByRole("alert")).toHaveTextContent("ended without a token");
    expect(screen.getByRole("button", { name: "Log in with Claude" })).toBeInTheDocument();
  });

  it("shows a start failure such as a missing claude", async () => {
    m.start.mockRejectedValue(new Error("claude was not found on PATH"));
    render(SetupTokenRunner);
    await fireEvent.click(screen.getByRole("button", { name: "Log in with Claude" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("claude was not found on PATH");
    expect(screen.queryByLabelText("Claude login terminal")).not.toBeInTheDocument();
  });

  it("cancels the run and goes back to the button", async () => {
    render(SetupTokenRunner);
    await fireEvent.click(screen.getByRole("button", { name: "Log in with Claude" }));
    await screen.findByLabelText("Claude login terminal");
    await waitFor(() => expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument());
    await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(m.cancel).toHaveBeenCalledWith(7);
    expect(screen.getByRole("button", { name: "Log in with Claude" })).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("cancels a running login when it unmounts", async () => {
    const { unmount } = render(SetupTokenRunner);
    await fireEvent.click(screen.getByRole("button", { name: "Log in with Claude" }));
    await screen.findByText("A browser window opens. Finish the login there.");
    await waitFor(() => expect(m.start).toHaveBeenCalled());
    await Promise.resolve();
    unmount();
    expect(m.cancel).toHaveBeenCalledWith(7);
  });
});
