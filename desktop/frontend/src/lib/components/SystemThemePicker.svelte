<script lang="ts">
  import Checkbox from './Checkbox.svelte';
  import Select from './Select.svelte';
  import { FLAVORS, THEME_IDS, type ThemeId } from '../catppuccin';
  import type { AppearancePreference } from '../appearance';

  let { value, themeIds = THEME_IDS, onchange }: {
    value: AppearancePreference;
    themeIds?: ThemeId[];
    onchange: (value: AppearancePreference) => void;
  } = $props();
  const uid = $props.id();
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
        <div class="grid min-w-0 gap-1.5">
          <label for="{uid}-{slot.key}" class="text-sm text-ink-2">{slot.label}</label>
          <Select id="{uid}-{slot.key}" class="w-full" value={slot.dark ? value.darkTheme : value.lightTheme}
            onchange={(e) => onchange({ ...value, [slot.key]: e.currentTarget.value as ThemeId })}>
            {#each themeIds.filter(id => FLAVORS[id].dark === slot.dark) as id}
              <option value={id}>{FLAVORS[id].label}</option>
            {/each}
          </Select>
        </div>
      {/each}
    </div>
  {/if}
</div>
