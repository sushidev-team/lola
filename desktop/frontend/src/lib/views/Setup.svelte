<script lang="ts">
  import { ConfigService } from "@bindings/desktop";
  import { store } from "$lib/store.svelte";
  import PresetInput from "$lib/components/PresetInput.svelte";
  import { POLL_INTERVALS } from "$lib/settingPresets";
  import HelpText from "$lib/components/HelpText.svelte";
  import Button from "$lib/components/Button.svelte";

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
  let branches = $state<string[]>([]);
  let concurrencyCap = $state(2);
  let globalCap = $state(4);
  let pollInterval = $state("60s");

  let submitting = $state(false);
  let error = $state("");
  let picking = $state(false);
  let inspecting = $state(false);
  let inspectedPath = $state("");
  let nameAuto = true;
  let repoAuto = true;
  let branchAuto = true;
  let inspection = 0;
  // Whether the path is a checkout, so the one thing this screen cannot fix
  // later is visible now — every worktree forks from this repository.
  let isRepo = $state<boolean | null>(null);

  const canSubmit = $derived(key.trim() !== "" && projectName.trim() !== "" && projectPath.trim() !== "" && inspectedPath === projectPath.trim() && isRepo === true && !picking && !inspecting && !submitting);

  /**
   * One folder pick fills the rest of the project block: name, GitHub repo and
   * the branch worktrees fork from. Fill-only — anything already typed stays —
   * and an unknown stays empty rather than being guessed at.
   */
  async function pickFolder() {
    if (picking) return;
    picking = true;
    try {
      const chosen = await ConfigService.PickFolder(projectPath.trim());
      if (!chosen) return; // cancelled
      projectPath = chosen;
      await inspectFolder();
    } catch (e) {
      error = String(e);
    } finally {
      picking = false;
    }
  }

  async function inspectFolder() {
    const path = projectPath.trim();
    const request = ++inspection;
    inspectedPath = "";
    isRepo = null;
    branches = [];
    error = "";
    if (!path) { inspecting = false; return; }
    inspecting = true;
    try {
      const info = await ConfigService.InspectPath(path);
      if (request !== inspection || projectPath.trim() !== path) return;
      projectPath = info.path || path;
      inspectedPath = projectPath;
      isRepo = info.isRepo;
      branches = info.branches ?? [];
      if (nameAuto || !projectName.trim()) projectName = info.suggestedLabel || info.suggestedId || "";
      if (repoAuto || !repo.trim()) repo = info.repo || "";
      if (branchAuto || !branch.trim()) branch = info.defaultBranch || "main";
    } catch (e) {
      if (request === inspection && projectPath.trim() === path) error = String(e);
    } finally {
      if (request === inspection) inspecting = false;
    }
  }

  async function validateKey() {
    if (!key.trim()) return;
    const checkedKey = key;
    keyState = "checking";
    keyMsg = "";
    try {
      await ConfigService.ValidateLinearKey(checkedKey);
      if (key !== checkedKey) return;
      keyState = "ok";
      keyMsg = "key is valid";
    } catch (e) {
      if (key !== checkedKey) return;
      keyState = "bad";
      keyMsg = String(e);
    }
  }

  async function submit() {
    if (!canSubmit) return;
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
    <h1 class="mb-1 text-xl text-ink">Welcome to lola</h1>
    <p class="copy mb-5 text-sm text-faint">Connect Linear. Choose a repository.</p>

    <div class="space-y-4 rounded-xl border border-edge bg-panel p-5">
      <!-- Linear key -->
      <div>
        <div class="mb-1 label text-faint">Linear API key</div>
        <div class="flex gap-2">
          <input
            aria-label="Linear API key"
            type="password"
            class="min-w-0 flex-1 rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder"
            placeholder="lin_api_…"
            bind:value={key}
            oninput={() => (keyState = "idle")}
          />
          <Button variant="secondary" size="md" disabled={!key.trim() || keyState === "checking"} onclick={validateKey}>
            {keyState === "checking" ? "Checking…" : "Validate"}
          </Button>
        </div>
        {#if keyState === "ok"}<p class="mt-1 text-sm text-good">✓ {keyMsg}</p>{/if}
        {#if keyState === "bad"}<p class="mt-1 text-sm text-bad">✗ {keyMsg}</p>{/if}
        <p class="mt-1 text-sm text-faint">Stored in macOS Keychain.</p>
      </div>

      <!-- Project -->
      <div class="block">
        <span class="mb-1 block label text-faint">Project path</span>
        <div class="flex gap-2">
          <input
            class="min-w-0 flex-1 rounded border border-edge bg-canvas px-2 py-1.5 font-mono text-sm text-ink outline-none focus:border-accent placeholder:text-placeholder"
            aria-label="Project path"
            placeholder="/path/to/repo"
            bind:value={projectPath}
            onblur={inspectFolder}
          />
          <Button variant="secondary" size="md" disabled={picking} onclick={pickFolder}>
            {picking ? "Choosing…" : "Choose folder…"}
          </Button>
        </div>
        {#if inspectedPath === projectPath.trim() && isRepo === false}
          <p class="mt-1 text-sm text-warn">Not a git checkout — worktrees fork from this repository.</p>
        {:else}
          <p class="mt-1 text-sm text-faint">Project details detected automatically.</p>
        {/if}
      </div>
      <details class="border-t border-edge pt-3">
        <summary class="cursor-pointer text-ink">Project details{projectName ? ` · ${projectName}` : ""}</summary>
        <div class="mt-3 space-y-3">
          <div class="grid grid-cols-2 gap-3">
            <label class="block">
              <span class="mb-1 block label text-faint">Project name</span>
              <input class="w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder" placeholder="my-app" bind:value={projectName} oninput={() => (nameAuto = false)} />
            </label>
            <div class="block">
              <span class="mb-1 block label text-faint">Default branch</span>
              <PresetInput label="Default branch" value={branch} options={branches.map((value) => ({ value, label: value }))}
              onChange={(v) => { branch = v; branchAuto = false; }} placeholder="main" />
            </div>
          </div>
          <label class="block">
            <span class="mb-1 block label text-faint">GitHub repo</span>
            <input class="w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder" placeholder="owner/name" bind:value={repo} oninput={() => (repoAuto = false)} />
          </label>

        </div>
      </details>

      <details class="border-t border-edge pt-3">
        <summary class="cursor-pointer text-faint">Performance settings</summary>
        <HelpText label="performance defaults" summary="2 per project · 4 total · every minute." detail="These are the initial session limits and polling frequency. You can change them later in General settings." />
        <div class="grid grid-cols-3 gap-3">
          <label class="block">
            <span class="mb-1 block label text-faint">Per project</span>
            <input type="number" min="1" class="w-full rounded border border-edge bg-canvas num px-2 py-1.5 text-ink outline-none focus:border-accent" bind:value={concurrencyCap} />
          </label>
          <label class="block">
            <span class="mb-1 block label text-faint">Total agents</span>
            <input type="number" min="1" class="w-full rounded border border-edge bg-canvas num px-2 py-1.5 text-ink outline-none focus:border-accent" bind:value={globalCap} />
          </label>
          <div class="block">
            <span class="mb-1 block label text-faint">Poll interval</span>
            <PresetInput label="Poll interval" value={pollInterval} options={POLL_INTERVALS} onChange={(v) => { pollInterval = v; }} />
          </div>
        </div>

      </details>

      {#if error}<div class="rounded border border-bad/40 bg-bad/10 px-3 py-2 text-sm text-bad">✗ {error}</div>{/if}

      <div class="flex items-center justify-end gap-2 pt-1">
        <Button variant="primary" size="md" class="px-4" disabled={!canSubmit} onclick={submit}>
          {submitting ? "Starting…" : "Start Lola"}
        </Button>
      </div>
    </div>
  </div>
</div>
