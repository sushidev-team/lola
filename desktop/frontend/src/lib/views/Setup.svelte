<script lang="ts">
  import { onMount } from "svelte";
  import { ConfigService } from "@bindings/desktop";
  import { store } from "$lib/store.svelte";
  import PresetInput from "$lib/components/PresetInput.svelte";
  import AgentModelFields from "$lib/components/AgentModelFields.svelte";
  import Select from "$lib/components/Select.svelte";
  import { POLL_INTERVALS } from "$lib/settingPresets";
  import { appearance, FLAVORS, THEME_IDS, DEFAULT_THEME_ID, type ThemeId } from "$lib/theme-runtime.svelte";
  import Button from "$lib/components/Button.svelte";

  // First-run setup connects Linear and saves workspace defaults. Repositories
  // are added from the empty workspace after setup, just as in the TUI.
  let key = $state("");
  let keyState = $state<"idle" | "checking" | "ok" | "bad">("idle");
  let keyMsg = $state("");
  let agent = $state("claude");
  let theme = $state<ThemeId>(DEFAULT_THEME_ID);
  let themeIds = $state<ThemeId[]>(THEME_IDS);
  let concurrencyCap = $state<number | null | undefined>(2);
  let globalCap = $state<number | null | undefined>(4);
  let pollInterval = $state("60s");
  let submitting = $state(false);
  let error = $state("");

  const invalidLimit = (value: number | null | undefined) => value != null && (!Number.isInteger(value) || value < 1);
  const limitsInvalid = $derived(invalidLimit(concurrencyCap) || invalidLimit(globalCap));
  const canSubmit = $derived(key.trim() !== "" && !limitsInvalid && keyState !== "checking" && !submitting);

  onMount(() => {
    let active = true;
    void ConfigService.Themes().then((allowed) => {
      const known = THEME_IDS.filter((id) => (allowed ?? []).includes(id));
      if (active && known.length) themeIds = known;
    }).catch(() => { /* Retain the local themes if the catalog is unavailable. */ });
    return () => { active = false; };
  });

  async function validateKey() {
    if (!key.trim()) return;
    const checkedKey = key;
    keyState = "checking";
    keyMsg = "";
    try {
      await ConfigService.ValidateLinearKey(checkedKey.trim());
      if (key !== checkedKey) return;
      keyState = "ok";
      keyMsg = "Key is valid.";
    } catch (e) {
      if (key !== checkedKey) return;
      keyState = "bad";
      keyMsg = String(e);
    }
  }

  async function submit() {
    if (!canSubmit) return;
    const values = {
      linearKey: key.trim(), agent, theme,
      concurrencyCap: concurrencyCap ?? 2,
      globalCap: globalCap ?? 4,
      pollInterval: pollInterval.trim() || "60s",
    };
    submitting = true;
    error = "";
    try {
      // Validate automatically so completing setup needs no separate click.
      if (keyState !== "ok") {
        await ConfigService.ValidateLinearKey(values.linearKey);
        keyState = "ok";
        keyMsg = "Key is valid.";
      }
      const res = await ConfigService.Setup(values);
      key = "";
      store.hasConfig = true;
      // Configuration is saved. A cosmetic bridge read must not hold the
      // first-run screen open or delay starting the daemon.
      void appearance.init().catch(() => {});
      store.setFlash(res.message || "Setup complete. Add a project when you’re ready.", res.keychainStored ? "good" : "warn");
      await store.startDaemon();
    } catch (e) {
      error = String(e);
    } finally {
      submitting = false;
    }
  }
</script>

<div class="grid h-full overflow-y-auto p-4 sm:p-6">
  <div class="m-auto w-full max-w-lg py-4">
    <h1 class="mb-1 text-xl text-ink">Welcome to Lola</h1>
    <p class="mb-5 text-sm text-faint">Connect Linear to get started. Add projects when you’re ready.</p>

    <form onsubmit={(event) => { event.preventDefault(); void submit(); }} class="rounded-xl border border-edge bg-panel p-5">
      <fieldset disabled={submitting} class="min-w-0 space-y-5">
        <div>
          <label for="setup-linear-key" class="mb-1 block label text-faint">Linear API key</label>
          <div class="flex gap-2">
            <input
              id="setup-linear-key"
              type="password"
              autocomplete="off"
              required
              class="min-w-0 flex-1 rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder"
              placeholder="lin_api_…"
              bind:value={key}
              oninput={() => { keyState = "idle"; keyMsg = ""; }}
            />
            <Button variant="secondary" size="md" disabled={!key.trim() || keyState === "checking"} onclick={validateKey}>
              {keyState === "checking" ? "Checking…" : "Validate"}
            </Button>
          </div>
          {#if keyState === "ok"}<p class="mt-1 text-sm text-good" role="status">{keyMsg}</p>{/if}
          {#if keyState === "bad"}<p class="mt-1 text-sm text-bad" role="alert">{keyMsg}</p>{/if}
          <p class="mt-1 text-sm text-faint">Saved in macOS Keychain when you finish setup.</p>
        </div>

        <section class="border-t border-edge pt-4" aria-labelledby="setup-general">
          <h2 id="setup-general" class="text-sm font-medium text-ink">General settings</h2>
          <p class="mt-1 mb-4 text-sm text-faint">Optional. You can change these later in Settings.</p>
          <div class="grid min-w-0 gap-4 sm:grid-cols-2">
            <AgentModelFields provider={agent} providerLabel="Default agent" onProviderChange={(value) => { agent = value; }} />
            <label class="grid min-w-0 gap-1.5">
              <span class="text-faint">Theme</span>
              <Select aria-label="Theme" bind:value={theme}>
                {#each themeIds as id}<option value={id}>{FLAVORS[id].label}</option>{/each}
              </Select>
            </label>
            <label class="grid min-w-0 gap-1.5">
              <span class="text-faint">Agents per project</span>
              <input type="number" min="1" step="1" placeholder="2" aria-invalid={invalidLimit(concurrencyCap)}
                class="w-full rounded border border-edge bg-canvas num px-2 py-1.5 text-ink outline-none focus:border-accent" bind:value={concurrencyCap} />
            </label>
            <label class="grid min-w-0 gap-1.5">
              <span class="text-faint">Total running agents</span>
              <input type="number" min="1" step="1" placeholder="4" aria-invalid={invalidLimit(globalCap)}
                class="w-full rounded border border-edge bg-canvas num px-2 py-1.5 text-ink outline-none focus:border-accent" bind:value={globalCap} />
            </label>
            <div class="grid min-w-0 gap-1.5 sm:col-span-2">
              <span class="text-faint">Poll interval</span>
              <PresetInput label="Poll interval" value={pollInterval} options={POLL_INTERVALS} disabled={submitting} onChange={(value) => { pollInterval = value; }} />
            </div>
          </div>
          {#if limitsInvalid}<p class="mt-2 text-sm text-bad" role="alert">Agent limits must be whole numbers of at least 1.</p>{/if}
        </section>

        {#if error}<p class="text-sm text-bad" role="alert">{error}</p>{/if}

        <div class="flex justify-end">
          <Button type="submit" variant="primary" size="md" class="px-4" disabled={!canSubmit} loading={submitting}>
            {submitting ? "Starting…" : "Start Lola"}
          </Button>
        </div>
      </fieldset>
    </form>
  </div>
</div>
