import { render, screen, fireEvent, within } from "@testing-library/svelte";
import { describe, expect, it, vi } from "vitest";
import { FLAVORS } from "../catppuccin";
import SystemThemePicker from "./SystemThemePicker.svelte";
import { appearancePreference, LIGHT_THEME_IDS, DARK_THEME_IDS } from "../appearance";

describe("system theme pickers", () => {
  it("offers only matching tags, including all five new light palettes", async () => {
    const onchange = vi.fn();
    const value = appearancePreference({ followSystem: true });
    render(SystemThemePicker, { value, onchange });
    const light = within(screen.getByRole("group", { name: "Light theme" }));
    const dark = within(screen.getByRole("group", { name: "Dark theme" }));
    expect(light.getAllByRole("button").map(b => b.getAttribute("aria-label"))).toEqual(LIGHT_THEME_IDS.map(id => FLAVORS[id].label));
    expect(dark.getAllByRole("button").map(b => b.getAttribute("aria-label"))).toEqual(DARK_THEME_IDS.map(id => FLAVORS[id].label));
    expect(light.getByRole("button", { name: "GitHub Light" })).toHaveAttribute("aria-pressed", "true");
    expect(dark.getByRole("button", { name: "Midnight" })).toHaveAttribute("aria-pressed", "true");
    await fireEvent.click(light.getByRole("button", { name: "One Light" }));
    expect(onchange).toHaveBeenCalledWith({ ...value, lightTheme: "one-light" });
  });
  it("hides the system pair in fixed mode", () => {
    render(SystemThemePicker, { value: appearancePreference(undefined), onchange: vi.fn() });
    expect(screen.queryByRole("group", { name: "Light theme" })).toBeNull();
    expect(screen.getByRole("checkbox", { name: "Follow system appearance" })).not.toBeChecked();
  });
});
