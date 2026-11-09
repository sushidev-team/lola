import { render, screen, fireEvent } from "@testing-library/svelte";
import { describe, expect, it, vi } from "vitest";
import SystemThemePicker from "./SystemThemePicker.svelte";
import { appearancePreference, LIGHT_THEME_IDS, DARK_THEME_IDS } from "../appearance";

describe("system theme pickers", () => {
  it("offers only matching tags, including all five new light palettes", async () => {
    const onchange = vi.fn();
    const value = appearancePreference({ followSystem: true });
    render(SystemThemePicker, { value, onchange });
    const light = screen.getByLabelText("Light theme") as HTMLSelectElement;
    const dark = screen.getByLabelText("Dark theme") as HTMLSelectElement;
    expect(Array.from(light.options, o => o.value)).toEqual(LIGHT_THEME_IDS);
    expect(Array.from(dark.options, o => o.value)).toEqual(DARK_THEME_IDS);
    expect(light.options).toHaveLength(6);
    await fireEvent.change(light, { target: { value: "one-light" } });
    expect(onchange).toHaveBeenCalledWith({ ...value, lightTheme: "one-light" });
  });
  it("hides the system pair in fixed mode", () => {
    render(SystemThemePicker, { value: appearancePreference(undefined), onchange: vi.fn() });
    expect(screen.queryByLabelText("Light theme")).toBeNull();
    expect(screen.getByRole("checkbox", { name: "Follow system appearance" })).not.toBeChecked();
  });
});
