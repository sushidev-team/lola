import { describe, it, expect, vi, afterEach } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/svelte";
import TagCombobox from "./TagCombobox.svelte";

const suggestions = [
  { value: "node", label: "Node" },
  { value: "php", label: "PHP" },
  { value: "go", label: "Go", hint: "detected" },
];

function mount(onAdd = vi.fn(() => true)) {
  const onRemove = vi.fn();
  render(TagCombobox, { label: "Toolchains", tags: [{ value: "ruby", label: "Ruby" }], suggestions, onAdd, onRemove });
  return { box: screen.getByRole("combobox", { name: "Toolchains" }), onAdd, onRemove };
}

afterEach(cleanup);

describe("TagCombobox", () => {
  it("opens a labelled listbox from the keyboard and wraps through its options", async () => {
    const { box } = mount();
    expect(box).toHaveAttribute("aria-expanded", "false");
    expect(box).not.toHaveAttribute("aria-controls");
    await fireEvent.keyDown(box, { key: "ArrowDown" });
    const list = screen.getByRole("listbox", { name: "Toolchains suggestions" });
    expect(box).toHaveAttribute("aria-expanded", "true");
    expect(box).toHaveAttribute("aria-controls", list.id);
    const options = screen.getAllByRole("option");
    expect(options).toHaveLength(3);
    expect(box).toHaveAttribute("aria-activedescendant", options[0].id);
    expect(options[0]).toHaveAttribute("aria-selected", "true");
    await fireEvent.keyDown(box, { key: "ArrowUp" });
    expect(box).toHaveAttribute("aria-activedescendant", options[2].id);
    await fireEvent.keyDown(box, { key: "ArrowDown" });
    expect(box).toHaveAttribute("aria-activedescendant", options[0].id);
    await fireEvent.keyDown(box, { key: "End" });
    expect(box).toHaveAttribute("aria-activedescendant", options[2].id);
    await fireEvent.keyDown(box, { key: "Home" });
    expect(box).toHaveAttribute("aria-activedescendant", options[0].id);
  });

  it("adds the highlighted option on Enter, or the typed text", async () => {
    const { box, onAdd } = mount();
    await fireEvent.keyDown(box, { key: "ArrowUp" });
    await fireEvent.keyDown(box, { key: "Enter" });
    expect(onAdd).toHaveBeenLastCalledWith("go");
    await fireEvent.input(box, { target: { value: "ffmpeg" } });
    await fireEvent.keyDown(box, { key: "Enter" });
    expect(onAdd).toHaveBeenLastCalledWith("ffmpeg");
  });

  it("closes on Escape without letting it reach a dialog, and adds on click", async () => {
    const { box, onAdd } = mount();
    await fireEvent.keyDown(box, { key: "ArrowDown" });
    const outer = vi.fn();
    document.addEventListener("keydown", outer);
    await fireEvent.keyDown(box, { key: "Escape" });
    document.removeEventListener("keydown", outer);
    expect(outer).not.toHaveBeenCalled();
    expect(screen.queryByRole("listbox")).toBeNull();
    await fireEvent.focus(box);
    await fireEvent.click(screen.getByRole("option", { name: /PHP/ }));
    expect(onAdd).toHaveBeenLastCalledWith("php");
  });

  it("removes the last chip on Backspace in an empty field", async () => {
    const { box, onRemove } = mount();
    await fireEvent.keyDown(box, { key: "Backspace" });
    expect(onRemove).toHaveBeenCalledWith("ruby");
  });
});
