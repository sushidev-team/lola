<script lang="ts">
  import Checkbox from './Checkbox.svelte';
  import ThemeSwatch from './ThemeSwatch.svelte';
  import { FLAVORS, THEME_IDS, type ThemeId } from '../catppuccin';
  import type { AppearancePreference } from '../appearance';

  let { value, themeIds = THEME_IDS, onchange }: {
    value: AppearancePreference;
    themeIds?: ThemeId[];
    onchange: (value: AppearancePreference) => void;
  } = $props();
</script>

<div class="grid gap-3" data-system-theme-picker>
  <label class="flex min-h-10 cursor-pointer items-center gap-2.5 text-ink">
    <Checkbox checked={value.followSystem} onchange={(e) => onchange({ ...value, followSystem: e.currentTarget.checked })} />
    <span>Follow system appearance</span>
  </label>
  {#if value.followSystem}
    <p class="text-sm text-ink-2">Switch automatically when your device changes between light and dark.</p>
    <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
      {#each [{ dark: false, key: 'lightTheme', label: 'Light theme' }, { dark: true, key: 'darkTheme', label: 'Dark theme' }] as slot}
        <fieldset class="min-w-0">
          <legend class="mb-2 text-sm text-ink-2">{slot.label}</legend>
          <div class="theme-options">
            {#each themeIds.filter(id => FLAVORS[id].dark === slot.dark) as id (id)}
              <ThemeSwatch {id} compact selected={(slot.dark ? value.darkTheme : value.lightTheme) === id}
                onclick={() => onchange({ ...value, [slot.key]: id })} />
            {/each}
          </div>
        </fieldset>
      {/each}
    </div>
  {/if}
</div>

<style>
  .theme-options {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 9rem), 1fr));
    gap: 8px;
  }
</style>
