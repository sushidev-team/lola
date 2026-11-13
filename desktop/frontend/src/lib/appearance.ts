import { DEFAULT_THEME_ID, FLAVORS, THEME_IDS, flavorFor, type ThemeId } from "./catppuccin";

export interface AppearancePreference {
  theme: ThemeId;
  followSystem: boolean;
  lightTheme: ThemeId;
  darkTheme: ThemeId;
}

export const LIGHT_THEME_IDS = THEME_IDS.filter(id => !FLAVORS[id].dark);
export const DARK_THEME_IDS = THEME_IDS.filter(id => FLAVORS[id].dark);
export const DEFAULT_LIGHT_THEME_ID: ThemeId = "github-light";

/** Validate cached/older bridge data; persisted input is also validated by Go. */
export function appearancePreference(value: unknown): AppearancePreference {
  const raw = value && typeof value === "object" ? value as Partial<AppearancePreference> : {};
  const tagged = (id: unknown, dark: boolean, fallback: ThemeId): ThemeId =>
    typeof id === "string" && THEME_IDS.includes(id as ThemeId) && FLAVORS[id as ThemeId].dark === dark
      ? id as ThemeId : fallback;
  return {
    theme: flavorFor(typeof raw.theme === "string" ? raw.theme : undefined).id,
    followSystem: raw.followSystem === true,
    lightTheme: tagged(raw.lightTheme, false, DEFAULT_LIGHT_THEME_ID),
    darkTheme: tagged(raw.darkTheme, true, DEFAULT_THEME_ID),
  };
}

export function resolveAppearance(preference: AppearancePreference, dark: boolean): ThemeId {
  return preference.followSystem ? (dark ? preference.darkTheme : preference.lightTheme) : preference.theme;
}
