import { render, screen, fireEvent, cleanup } from "@testing-library/svelte";
import { afterEach, describe, it, expect, vi } from "vitest";
import PresetInput from "./PresetInput.svelte";

afterEach(cleanup);
const options = [{ value: "", label: "Default" }, { value: "sonnet", label: "Sonnet" }];

describe("PresetInput", () => {
  it("preserves an unknown configured value without emitting a change", async () => {
    const onChange = vi.fn();
    const { rerender } = render(PresetInput, { label: "Model", value: "private/model", options, onChange });
    expect(screen.getByLabelText("Model")).toHaveDisplayValue("Custom…");
    expect(screen.getByLabelText("Custom Model")).toHaveValue("private/model");
    expect(onChange).not.toHaveBeenCalled();
    // An asynchronously loaded catalog may recognize it; this is still no edit.
    await rerender({ options: [...options, { value: "private/model", label: "Private model" }] });
    expect(screen.getByLabelText("Model")).toHaveDisplayValue("Private model");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("opening custom preserves the current value and does not dirty the form", async () => {
    const onChange = vi.fn();
    render(PresetInput, { label: "Model", value: "sonnet", options, onChange });
    await fireEvent.change(screen.getByLabelText("Model"), { target: { value: "custom" } });
    expect(screen.getByLabelText("Custom Model")).toHaveValue("sonnet");
    expect(onChange).not.toHaveBeenCalled();
  });

  it("saves actual option values and lets a custom edit pass through a preset value", async () => {
    const onChange = vi.fn();
    const { rerender } = render(PresetInput, { label: "Model", value: "son", options, onChange });
    const input = screen.getByLabelText("Custom Model");
    await fireEvent.input(input, { target: { value: "sonnet" } });
    expect(onChange).toHaveBeenLastCalledWith("sonnet");
    await rerender({ value: "sonnet" });
    expect(screen.getByLabelText("Custom Model")).toBe(input);
    await fireEvent.input(input, { target: { value: "sonnet[1m]" } });
    expect(onChange).toHaveBeenLastCalledWith("sonnet[1m]");
    await fireEvent.change(screen.getByLabelText("Model"), { target: { value: "0" } });
    expect(onChange).toHaveBeenLastCalledWith("");
  });

  it("disables both controls for a read-only custom value", () => {
    render(PresetInput, { label: "Model", value: "private/model", options, onChange: vi.fn(), disabled: true });
    expect(screen.getByLabelText("Model")).toBeDisabled();
    expect(screen.getByLabelText("Custom Model")).toBeDisabled();
  });
});
