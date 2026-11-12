import { render, screen, fireEvent, cleanup } from "@testing-library/svelte";
import { describe, it, expect, vi, afterEach } from "vitest";

const { detect } = vi.hoisted(() => ({ detect: vi.fn() }));
vi.mock("@bindings/desktop", () => ({ ConfigService: { DetectToolchains: (p: string) => detect(p) } }));

import SandboxToolchains from "./SandboxToolchains.svelte";

const info = { detected: [{ kind: "php", version: "8.5" }], extras: ["redis", "bun"], kinds: ["node", "php", "python", "go", "rust", "ruby"] };

describe("SandboxToolchains", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("starts from the detected toolchains and turns a toggle into an explicit list", async () => {
    detect.mockResolvedValue(info);
    const onChange = vi.fn();
    render(SandboxToolchains, { path: "/repo", toolchains: [], packages: [], onChange });
    const php = await screen.findByRole("button", { name: "PHP 8.5" });
    expect(php).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText(/Detected from the repository · redis, bun/)).toBeInTheDocument();
    await fireEvent.click(screen.getByRole("button", { name: "Rust" }));
    expect(onChange).toHaveBeenLastCalledWith(["php@8.5", "rust"], []);
    await fireEvent.click(php);
    expect(onChange).toHaveBeenLastCalledWith(["none"], []);
  });

  it("goes back to detection when the choice matches it, and parses packages", async () => {
    detect.mockResolvedValue(info);
    const onChange = vi.fn();
    render(SandboxToolchains, { path: "/repo", toolchains: ["php@8.5", "go"], packages: [], onChange });
    await fireEvent.click(await screen.findByRole("button", { name: "Go" }));
    expect(onChange).toHaveBeenLastCalledWith([], []);
    await fireEvent.input(screen.getByLabelText("Extra packages"), { target: { value: "ffmpeg, libvips-dev " } });
    expect(onChange).toHaveBeenLastCalledWith(["php@8.5", "go"], ["ffmpeg", "libvips-dev"]);
  });
});
