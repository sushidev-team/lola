<script lang="ts">
  import { ConfigService } from "@bindings/desktop";
  import { store } from "$lib/store.svelte";

  // First-run wizard: writes config.toml (Linear key → Keychain, one project,
  // caps/interval), mirroring the TUI's `lola setup`. Shown by App when no config
  // exists yet.
  let key = $state("");
  let keyState = $state<"idle" | "checking" | "ok" | "bad">("idle");
  let keyMsg = $state("");

  let projectName = $state("");
  let projectPath = $state("");
  let repo = $state("");
  let branch = $state("main");
  let concurrencyCap = $state(2);
  let globalCap = $state(4);
  let pollInterval = $state("60s");

  let submitting = $state(false);
  let error = $state("");

  const canSubmit = $derived(key.trim() !== "" && projectName.trim() !== "" && !submitting);

  async function validateKey() {
    if (!key.trim()) return;
    keyState = "checking";
    keyMsg = "";
    try {
      await ConfigService.ValidateLinearKey(key);
      keyState = "ok";
      keyMsg = "key is valid";
    } catch (e) {
      keyState = "bad";
      keyMsg = String(e);
    }
  }

  async function submit() {
    submitting = true;
    error = "";
    try {
      const res = await ConfigService.Setup({
        linearKey: key,
        projectName,
        projectPath,
        repo,
        defaultBranch: branch,
        concurrencyCap,
        globalCap,
        pollInterval,
      });
      store.hasConfig = true;
      store.setFlash(res.message || "config written", res.keychainStored ? "good" : "warn");
      await store.startDaemon();
    } catch (e) {
      error = String(e);
    } finally {
      submitting = false;
    }
  }
</script>

<div class="flex h-full items-center justify-center overflow-auto p-6">
  <div class="w-full max-w-lg">
    <h1 class="mb-1 text-lg font-semibold text-ink">Welcome to lola</h1>
    <p class="mb-5 text-xs text-faint">First-run setup — this writes <span class="font-mono">~/.lola/config.toml</span>.</p>

    <div class="space-y-4 rounded-xl border border-edge bg-panel p-5">
      <!-- Linear key -->
      <div>
        <div class="mb-1 text-[11px] font-semibold tracking-wider text-faint uppercase">Linear API key</div>
        <div class="flex gap-2">
          <input
            type="password"
            class="min-w-0 flex-1 rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent"
            placeholder="lin_api_…"
            bind:value={key}
            oninput={() => (keyState = "idle")}
          />
          <button
            class="rounded border border-edge px-2.5 py-1 text-xs text-faint hover:border-accent hover:text-accent disabled:opacity-40"
            disabled={!key.trim() || keyState === "checking"}
            onclick={validateKey}
          >
            {keyState === "checking" ? "checking…" : "validate"}
          </button>
        </div>
        {#if keyState === "ok"}<p class="mt-1 text-[11px] text-good">✓ {keyMsg}</p>{/if}
        {#if keyState === "bad"}<p class="mt-1 text-[11px] text-bad">✗ {keyMsg}</p>{/if}
        <p class="mt-1 text-[10px] text-faint">Stored in the macOS Keychain — never written to config.</p>
      </div>

      <!-- Project -->
      <div class="grid grid-cols-2 gap-3">
        <label class="block">
          <span class="mb-1 block text-[11px] font-semibold tracking-wider text-faint uppercase">Project name</span>
          <input class="w-full rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent" placeholder="my-app" bind:value={projectName} />
        </label>
        <label class="block">
          <span class="mb-1 block text-[11px] font-semibold tracking-wider text-faint uppercase">Default branch</span>
          <input class="w-full rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent" bind:value={branch} />
        </label>
      </div>
      <label class="block">
        <span class="mb-1 block text-[11px] font-semibold tracking-wider text-faint uppercase">Project path</span>
        <input class="w-full rounded border border-edge bg-canvas px-2 py-1 font-mono text-[11px] text-ink outline-none focus:border-accent" placeholder="/path/to/repo" bind:value={projectPath} />
      </label>
      <label class="block">
        <span class="mb-1 block text-[11px] font-semibold tracking-wider text-faint uppercase">GitHub repo</span>
        <input class="w-full rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent" placeholder="owner/name" bind:value={repo} />
      </label>

      <!-- Caps -->
      <div class="grid grid-cols-3 gap-3">
        <label class="block">
          <span class="mb-1 block text-[11px] font-semibold tracking-wider text-faint uppercase">Concurrency</span>
          <input type="number" min="1" class="w-full rounded border border-edge bg-canvas px-2 py-1 text-xs tabular-nums text-ink outline-none focus:border-accent" bind:value={concurrencyCap} />
        </label>
        <label class="block">
          <span class="mb-1 block text-[11px] font-semibold tracking-wider text-faint uppercase">Global cap</span>
          <input type="number" min="1" class="w-full rounded border border-edge bg-canvas px-2 py-1 text-xs tabular-nums text-ink outline-none focus:border-accent" bind:value={globalCap} />
        </label>
        <label class="block">
          <span class="mb-1 block text-[11px] font-semibold tracking-wider text-faint uppercase">Poll interval</span>
          <input class="w-full rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent" bind:value={pollInterval} />
        </label>
      </div>

      {#if error}<div class="rounded border border-bad/40 bg-bad/10 px-3 py-2 text-xs text-bad">✗ {error}</div>{/if}

      <div class="flex items-center justify-end gap-2 pt-1">
        <button
          class="rounded bg-accent/20 px-4 py-1.5 text-xs font-medium text-accent hover:bg-accent/30 disabled:opacity-40"
          disabled={!canSubmit}
          onclick={submit}
        >
          {submitting ? "writing…" : "Write config & start"}
        </button>
      </div>
    </div>
  </div>
</div>
