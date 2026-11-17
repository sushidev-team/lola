import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { appearancePreference } from "$lib/appearance";
import { appearance } from "$lib/theme-runtime.svelte";

const { GetAppearance, SetAppearance } = vi.hoisted(() => ({ GetAppearance: vi.fn(), SetAppearance: vi.fn() }));
vi.mock("@bindings/desktop", () => ({ ConfigService: { GetAppearance: () => GetAppearance(), SetAppearance: (value: unknown) => SetAppearance(value) } }));

const system = appearancePreference({ theme: "catppuccin-mocha", followSystem: true, lightTheme: "one-light", darkTheme: "lola-midnight" });
let matches = true;
let listeners: Set<(event: MediaQueryListEvent) => void>;
function change(dark: boolean) {
  matches = dark;
  for (const listener of listeners) listener({ matches: dark } as MediaQueryListEvent);
}

beforeEach(async () => {
  localStorage.clear();
  matches = true;
  listeners = new Set();
  vi.stubGlobal("matchMedia", vi.fn(() => ({
    get matches() { return matches; },
    addEventListener: (_: string, fn: (e: MediaQueryListEvent) => void) => listeners.add(fn),
    removeEventListener: (_: string, fn: (e: MediaQueryListEvent) => void) => listeners.delete(fn),
  })));
  GetAppearance.mockReset().mockResolvedValue(appearancePreference(undefined));
  SetAppearance.mockReset().mockResolvedValue(undefined);
  await appearance.init();
  expect(GetAppearance).toHaveBeenCalled();
});
afterEach(() => vi.unstubAllGlobals());

describe("system appearance", () => {
  it("switches chrome, terminal and snapshots on OS changes", () => {
    appearance.preview(system);
    change(false);
    expect(appearance.id).toBe("one-light");
    expect(document.documentElement.style.colorScheme).toBe("light");
    expect(appearance.term.background).toBe(appearance.flavor.base);
    expect(appearance.ansi.bg).toBe(appearance.flavor.base);
    change(true);
    expect(appearance.id).toBe("lola-midnight");
    expect(document.documentElement.style.colorScheme).toBe("dark");
  });

  it("fixed themes ignore OS changes and restoring a preview retains its mode", () => {
    appearance.preview(system);
    const baseline = appearance.snapshot();
    appearance.preview({ ...system, followSystem: false, theme: "gruvbox-light" });
    change(false); change(true);
    expect(appearance.id).toBe("gruvbox-light");
    appearance.preview(baseline);
    change(false);
    expect(appearance.id).toBe("one-light");
    expect(listeners.size).toBe(1);
  });

  it("caches the pair only after saving and resolves it against the OS on next boot", async () => {
    appearance.preview(system);
    await appearance.commit(system);
    expect(SetAppearance).toHaveBeenCalledWith(system);
    expect(JSON.parse(localStorage.getItem("lola.appearance")!)).toEqual(system);
    GetAppearance.mockRejectedValue(new Error("offline"));
    matches = false;
    const boot = appearance.init();
    expect(appearance.id).toBe("one-light"); // synchronous cached paint
    await boot;
    expect(appearance.id).toBe("one-light");
    expect(listeners.size).toBe(1);
  });

  it("failed saves cannot overwrite the cache", async () => {
    const before = localStorage.getItem("lola.appearance");
    SetAppearance.mockRejectedValue(new Error("read only"));
    appearance.preview(system);
    await expect(appearance.commit(system)).rejects.toThrow("read only");
    expect(localStorage.getItem("lola.appearance")).toBe(before);
  });

  it("a late startup read cannot replace an active preview", async () => {
    let answer!: (value: unknown) => void;
    GetAppearance.mockReturnValue(new Promise(resolve => { answer = resolve; }));
    const boot = appearance.init();
    appearance.preview({ ...system, followSystem: false, theme: "rose-pine-dawn" });
    answer(system);
    await boot;
    expect(appearance.id).toBe("rose-pine-dawn");
  });

  it("rejects wrong light/dark tags in cached preferences", () => {
    const value = appearancePreference({ ...system, lightTheme: "lola-midnight", darkTheme: "github-light" });
    expect(value.lightTheme).toBe("github-light");
    expect(value.darkTheme).toBe("lola-midnight");
  });
});
