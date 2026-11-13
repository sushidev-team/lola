import { beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/svelte";
const { PickFolder, PickFile } = vi.hoisted(() => ({ PickFolder: vi.fn(), PickFile: vi.fn() }));
vi.mock("@bindings/desktop", () => ({ ConfigService: { PickFolder, PickFile } }));
import PathInput from "./PathInput.svelte";

beforeEach(() => { cleanup(); PickFolder.mockReset(); PickFile.mockReset(); });

describe("PathInput", () => {
  it("browses from the current folder and preserves spaces in the chosen path", async () => {
    PickFolder.mockResolvedValue("/Users/me/Work projects");
    const onChange = vi.fn();
    render(PathInput, { label: "Directory", value: " ~/work ", onChange });
    await fireEvent.click(screen.getByRole("button", { name: "Browse for directory" }));
    await waitFor(() => expect(screen.getByLabelText("Directory")).toHaveValue("/Users/me/Work projects"));
    expect(PickFolder).toHaveBeenCalledWith("~/work");
    expect(onChange).toHaveBeenCalledWith("/Users/me/Work projects");
    expect(PickFile).not.toHaveBeenCalled();
  });

  it("uses the file chooser for identity files and reports a picked file", async () => {
    PickFile.mockResolvedValue("/Users/me/.ssh/id_ed25519");
    const onPick = vi.fn();
    render(PathInput, { label: "Identity file", kind: "file", value: "~/.ssh/old", onPick });
    await fireEvent.click(screen.getByRole("button", { name: "Browse for identity file" }));
    await waitFor(() => expect(onPick).toHaveBeenCalledWith("/Users/me/.ssh/id_ed25519"));
    expect(PickFile).toHaveBeenCalledWith("~/.ssh/old");
    expect(PickFolder).not.toHaveBeenCalled();
  });

  it("keeps the current path when the chooser is cancelled", async () => {
    PickFolder.mockResolvedValue("");
    const onChange = vi.fn();
    render(PathInput, { label: "Directory", value: "/existing", onChange });
    const button = screen.getByRole("button", { name: "Browse for directory" });
    await fireEvent.click(button);
    await waitFor(() => expect(button).toBeEnabled());
    expect(screen.getByLabelText("Directory")).toHaveValue("/existing");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("keeps manual entry available when the native chooser fails", async () => {
    PickFolder.mockRejectedValue(new Error("Native dialog unavailable"));
    const onChange = vi.fn();
    render(PathInput, { label: "Directory", value: "/existing", onChange });
    await fireEvent.click(screen.getByRole("button", { name: "Browse for directory" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Native dialog unavailable");
    expect(screen.getByLabelText("Directory")).toHaveValue("/existing");
    await fireEvent.input(screen.getByLabelText("Directory"), { target: { value: "/typed" } });
    expect(onChange).toHaveBeenCalledWith("/typed");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("blocks duplicate dialogs and ignores a result after the parent changes the path", async () => {
    let resolve!: (path: string) => void;
    PickFolder.mockReturnValue(new Promise<string>((done) => { resolve = done; }));
    const onChange = vi.fn();
    const view = render(PathInput, { label: "Directory", value: "/original", onChange });
    const button = screen.getByRole("button", { name: "Browse for directory" });
    await fireEvent.click(button);
    expect(button).toBeDisabled();
    expect(screen.getByLabelText("Directory")).toBeDisabled();
    await view.rerender({ value: "/newer" });
    resolve("/stale");
    await waitFor(() => expect(button).toBeEnabled());
    expect(screen.getByLabelText("Directory")).toHaveValue("/newer");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("honors a disabled field", () => {
    render(PathInput, { label: "Directory", disabled: true });
    expect(screen.getByLabelText("Directory")).toBeDisabled();
    expect(screen.getByRole("button", { name: "Browse for directory" })).toBeDisabled();
  });
});
