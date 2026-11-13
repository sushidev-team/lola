import { render, screen, fireEvent, cleanup } from "@testing-library/svelte";
import { describe, it, expect, vi, afterEach } from "vitest";

const { detect } = vi.hoisted(() => ({ detect: vi.fn() }));
vi.mock("@bindings/desktop", () => ({ ConfigService: { DetectToolchains: (p: string) => detect(p) } }));

import SandboxToolchains from "./SandboxToolchains.svelte";

const info = { detected: [{ kind: "php", version: "8.5" }], extras: ["redis"], kinds: ["node", "php", "python", "go", "rust", "ruby"] };

async function field() {
  return (await screen.findByRole("combobox", { name: "Toolchains and packages" })) as HTMLInputElement;
}

describe("SandboxToolchains", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("shows the detected toolchains as tags, without a detection note", async () => {
    detect.mockResolvedValue(info);
    render(SandboxToolchains, { path: "/repo", toolchains: [], packages: ["ffmpeg"], onChange: vi.fn() });
    expect(await screen.findByText("PHP 8.5")).toBeInTheDocument();
    expect(screen.getByText("ffmpeg")).toBeInTheDocument();
    expect(screen.queryByText(/Detected/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Use the detected toolchains" })).not.toBeInTheDocument();
  });

  it("adds a suggested toolchain, a typed package and a typed versioned toolchain", async () => {
    detect.mockResolvedValue(info);
    const onChange = vi.fn();
    render(SandboxToolchains, { path: "/repo", toolchains: [], packages: [], onChange });
    const input = await field();
    await screen.findByText("PHP 8.5");
    await fireEvent.focus(input);
    await fireEvent.mouseDown(screen.getByRole("option", { name: /Rust/ }));
    expect(onChange).toHaveBeenLastCalledWith(["php@8.5", "rust"], []);

    await fireEvent.input(input, { target: { value: "libheif-dev" } });
    await fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).toHaveBeenLastCalledWith([], ["libheif-dev"]);

    await fireEvent.input(input, { target: { value: "Node 22" } });
    await fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).toHaveBeenLastCalledWith(["php@8.5", "node@22"], []);
  });

  it("refuses text that is neither, and removes tags", async () => {
    detect.mockResolvedValue(info);
    const onChange = vi.fn();
    render(SandboxToolchains, { path: "/repo", toolchains: ["php@8.5", "go"], packages: ["ffmpeg"], onChange });
    const input = await field();
    await fireEvent.input(input, { target: { value: "Bad Name!" } });
    await fireEvent.keyDown(input, { key: "Enter" });
    expect(screen.getByRole("alert")).toHaveTextContent("not a toolchain or a Debian package name");
    expect(input.value).toBe("Bad Name!");

    await fireEvent.click(screen.getByRole("button", { name: "Remove Go" }));
    expect(onChange).toHaveBeenLastCalledWith([], ["ffmpeg"]); // back to what was detected
    await fireEvent.click(screen.getByRole("button", { name: "Remove ffmpeg" }));
    expect(onChange).toHaveBeenLastCalledWith(["php@8.5", "go"], []);
    await fireEvent.click(screen.getByRole("button", { name: "Use the detected toolchains" }));
    expect(onChange).toHaveBeenLastCalledWith([], ["ffmpeg"]);
  });

  it("removes the last tag with Backspace in an empty field and closes on Escape without bubbling", async () => {
    detect.mockResolvedValue(info);
    const onChange = vi.fn();
    const outer = vi.fn();
    render(SandboxToolchains, { path: "/repo", toolchains: [], packages: ["ffmpeg"], onChange });
    window.addEventListener("keydown", outer); // where a dialog listens for Escape
    const input = await field();
    await fireEvent.keyDown(input, { key: "Backspace" });
    expect(onChange).toHaveBeenLastCalledWith([], []);
    await fireEvent.focus(input);
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    outer.mockClear();
    await fireEvent.keyDown(input, { key: "Escape" });
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    expect(outer).not.toHaveBeenCalled();
    window.removeEventListener("keydown", outer);
  });
});
