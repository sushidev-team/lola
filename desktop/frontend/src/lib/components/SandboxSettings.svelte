<script lang="ts">
  // The container sandbox's machine-side settings ([sandbox]): the default
  // image and the Claude Code token sandboxed agents log in with.
  //
  // Self-contained like the Linear key section: it reads and writes through its
  // own service calls and never touches the settings DTO or its dirty baseline.
  // The token is WRITE-ONLY: the status reports whether one is stored, never the
  // value, and the input is cleared the moment it is saved.
  import { onDestroy, onMount } from "svelte";
  import Button from "$lib/components/Button.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import Checkbox from "$lib/components/Checkbox.svelte";
  import HelpText from "$lib/components/HelpText.svelte";
  import SettingsGroup from "$lib/components/SettingsGroup.svelte";
  import SetupTokenRunner from "$lib/components/SetupTokenRunner.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { ConfigService } from "@bindings/desktop";
  import type { SandboxStatusDTO } from "@bindings/desktop/models";
  import type { IconName } from "$lib/icons";

  let { formId = "sandbox" }: { formId?: string } = $props();

  let status = $state<SandboxStatusDTO | null>(null);
  let loadErr = $state("");
  let image = $state("");
  let savedImage = $state("");
  let imageBusy = $state(false);
  let imageMsg = $state("");
  let imageMsgKind = $state<"good" | "bad">("good");
  let token = $state("");
  let tokenBusy = $state(false);
  let tokenMsg = $state("");
  let tokenMsgKind = $state<"good" | "bad">("good");
  // The login form shows when no login exists, or while replacing one.
  let replacing = $state(false);
  let manual = $state(false);
  // The API-key switch mirrors status.useApiKey; it turns ON only through the
  // confirm dialog, so a click that is cancelled leaves it off.
  let apiOn = $state(false);
  let apiBusy = $state(false);
  let apiErr = $state("");

  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder";
  const rowCls = "settings-field grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] items-baseline gap-x-6 gap-y-1.5";
  const msgCls = { good: "text-good", bad: "text-bad" } as const;
  /** Go errors arrive as "Error: …"; the person reads only the sentence. */
  const clean = (e: unknown) => String(e instanceof Error ? e.message : e).replace(/^Error:\s*/, "");

  let checking = $state(false);
  async function load() {
    loadErr = "";
    checking = true;
    try {
      status = await ConfigService.SandboxStatus();
      // A re-check keeps an image edit in progress.
      if (image === savedImage) image = status?.image ?? "";
      savedImage = status?.image ?? "";
      apiOn = !!status?.useApiKey;
    } catch (e) {
      status = null;
      loadErr = clean(e);
    } finally {
      checking = false;
    }
  }
  // Docker started, a login made in a terminal: coming back to the window
  // checks again, so the list never waits for a reopen.
  const onFocus = () => {
    if (!checking) void load();
  };
  onMount(() => {
    void load();
    window.addEventListener("focus", onFocus);
  });
  onDestroy(() => window.removeEventListener("focus", onFocus));

  async function saveImage() {
    imageBusy = true;
    imageMsg = "";
    try {
      await ConfigService.SetSandboxImage(image.trim());
      imageMsg = image.trim() ? "Image saved." : "Using Lola’s sandbox image.";
      imageMsgKind = "good";
      await load();
    } catch (e) {
      imageMsg = clean(e);
      imageMsgKind = "bad";
    } finally {
      imageBusy = false;
    }
  }

  async function saveToken() {
    if (!token.trim()) return;
    tokenBusy = true;
    tokenMsg = "";
    try {
      await ConfigService.SetSandboxToken(token);
      // Never keep a live secret in the DOM longer than the save.
      token = "";
      replacing = manual = false;
      await load();
    } catch (e) {
      tokenMsg = clean(e);
      tokenMsgKind = "bad";
    } finally {
      tokenBusy = false;
    }
  }

  function askClearToken() {
    confirm.ask({
      title: "Remove the sandbox Claude token?",
      body: "New sandboxed sessions can no longer log in to Claude with it.",
      detail: "Running sessions keep the token they started with.",
      confirmLabel: "Remove",
      onConfirm: () => void clearToken(),
    });
  }

  async function clearToken() {
    tokenBusy = true;
    tokenMsg = "";
    try {
      await ConfigService.ClearSandboxToken();
      tokenMsg = "Token removed.";
      tokenMsgKind = "good";
      await load();
    } catch (e) {
      tokenMsg = clean(e);
      tokenMsgKind = "bad";
    } finally {
      tokenBusy = false;
    }
  }

  function toggleApiKey() {
    apiErr = "";
    if (!apiOn) {
      void setApiKey(false);
      return;
    }
    // Revert until confirmed: the key bills per token and wins over the token.
    apiOn = false;
    confirm.ask({
      title: "Pass ANTHROPIC_API_KEY to sandboxed agents?",
      body: "Claude Code then uses the API key instead of your Claude plan. Usage is billed per token to the key's Anthropic account.",
      detail: "Lola copies the key from the daemon's environment into each new sandboxed session. Running sessions keep what they started with.",
      confirmLabel: "Use API key",
      onConfirm: () => void setApiKey(true),
    });
  }

  async function setApiKey(on: boolean) {
    apiBusy = true;
    try {
      await ConfigService.SetSandboxUseAPIKey(on);
      await load();
    } catch (e) {
      apiErr = clean(e);
      apiOn = !!status?.useApiKey;
    } finally {
      apiBusy = false;
    }
  }

  function stored() {
    tokenMsg = "";
    replacing = manual = false;
    void load();
  }

  // The runner failed (cancelled, timed out, claude missing): the paste path
  // is the way on, so it opens instead of hiding behind a button.
  function loginFailed() {
    manual = true;
  }

  const loggedIn = $derived(!!status && (status.tokenStored || !!status.claudeSource));
  const claudeLine = $derived.by(() => {
    if (!status) return "";
    if (status.useApiKey && status.apiKeySet) return "API key, billed per token";
    if (status.tokenStored) return "Token stored in the keychain";
    if (status.claudeSource) return `Using ${status.claudeSource}`;
    return "Not set";
  });

  // ONE state per row, drawn one way: ok = check in green, needed = x in red,
  // optional = a dash in grey. The fix is a line under the row, never a popover.
  type RowState = "ok" | "needed" | "optional" | "info";
  type Row = { id: string; label: string; term?: string; state: RowState; hint?: string };
  const look: Record<RowState, { icon: IconName; cls: string; word: string }> = {
    ok: { icon: "check", cls: "text-good", word: "Ready" },
    needed: { icon: "x", cls: "text-bad", word: "Needs attention" },
    optional: { icon: "minus", cls: "text-faint", word: "Optional" },
    info: { icon: "download", cls: "text-faint", word: "Note" },
  };
  const rows = $derived.by<Row[]>(() => {
    if (!status) return [];
    const out: Row[] = [];
    out.push(status.dockerOk
      ? { id: "docker", label: "Docker running", term: status.docker, state: "ok" }
      : { id: "docker", label: "Docker not running", term: status.docker, state: "needed", hint: "Start Docker Desktop, OrbStack or Colima, then check again." });
    if (status.dockerOk) {
      const missing = status.missing ?? [];
      out.push({ id: "image", label: missing.length ? "Image pulls on first use" : "Image ready", term: status.image || status.defaultImage, state: missing.length ? "info" : "ok" });
    }
    const viaKey = !!(status.useApiKey && status.apiKeySet);
    out.push({
      id: "claude",
      label: `Claude login: ${claudeLine}`,
      term: viaKey ? "ANTHROPIC_API_KEY" : "claude setup-token",
      state: loggedIn || viaKey ? "ok" : "needed",
      hint: loggedIn || viaKey ? undefined : "Log in below, once for all projects.",
    });
    for (const a of [{ id: "codex", name: "Codex", ok: status.codexLogin, cmd: "codex login" }, { id: "opencode", name: "OpenCode", ok: status.openCodeLogin, cmd: "opencode auth login" }]) {
      out.push(a.ok
        ? { id: a.id, label: `${a.name} login found on this Mac`, state: "ok" }
        : { id: a.id, label: `No ${a.name} login`, term: a.cmd, state: "optional", hint: `Only needed for ${a.name} sessions. Run ${a.cmd} on this Mac; Lola copies the login into each new sandboxed session.` });
    }
    out.push(status.githubSource
      ? { id: "github", label: "GitHub access", term: status.githubSource, state: "ok" }
      : { id: "github", label: "No GitHub access", term: "gh auth login", state: "needed", hint: "Sandboxed agents cannot push. Run gh auth login on this Mac, then check again." });
    return out;
  });
</script>

<SettingsGroup title="Status">
  {#snippet aside()}
    <Button size="sm" loading={checking} disabled={checking} onclick={() => void load()}>Check again</Button>
  {/snippet}
  {#if loadErr}
    <p role="alert" class="text-sm text-bad">{loadErr}</p>
  {:else if !status}
    <p class="text-sm text-faint">Checking…</p>
  {:else}
    <ul class="grid gap-2.5 text-sm" aria-label="Sandbox status">
      {#each rows as r (r.id)}
        {@const l = look[r.state]}
        <li class="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] gap-x-1.5 gap-y-0.5" data-sandbox-row={r.id} data-state={r.state}>
          <span class="pt-0.5 {l.cls}" title={l.word}><Icon name={l.icon} size={14} /></span>
          <span class="flex min-w-0 flex-wrap items-baseline gap-x-2">
            <span class="text-ink">{r.label}</span>
            {#if r.term}<span class="min-w-0 truncate font-mono text-faint">{r.term}</span>{/if}
          </span>
          {#if r.hint}<span class="col-start-2 text-faint">{r.hint}</span>{/if}
        </li>
      {/each}
    </ul>
  {/if}
</SettingsGroup>

<SettingsGroup title="Image" help="Empty uses Lola’s image. Any image with sh, git and the coding agent works; a project can set its own.">
  <div class={rowCls}>
    <label for={`${formId}-image`} class="text-faint">Default image</label>
    <input
      id={`${formId}-image`}
      class="{inputCls} font-mono"
      autocomplete="off"
      spellcheck="false"
      placeholder={status?.defaultImage ?? ""}
      bind:value={image}
      disabled={imageBusy}
      oninput={() => (imageMsg = "")}
    />
  </div>
  <div class={rowCls}>
    <span></span>
    <span class="flex flex-wrap items-center gap-2">
      <Button variant="secondary" disabled={image.trim() === savedImage || imageBusy} loading={imageBusy} onclick={saveImage}>Save image</Button>
      <span role="status" class="text-sm {msgCls[imageMsgKind]}">{imageMsg}</span>
    </span>
  </div>
</SettingsGroup>

<SettingsGroup title="Claude login" help="A container cannot read your keychain, so sandboxed agents log in with a Claude token. Lola keeps it in the macOS keychain and never shows it again.">
  {#if loggedIn && !replacing && status}
    <div class="flex min-w-0 flex-wrap items-center gap-3 rounded-lg border border-good/40 bg-good/5 px-3 py-2.5" data-claude-login="ok">
      <span class="text-good"><Icon name="circle-check" size={18} /></span>
      <span class="grid min-w-0 flex-1">
        <span class="text-ink">Logged in</span>
        <span class="truncate text-sm text-faint">{status.tokenStored ? "Token in the macOS keychain" : `From ${status.claudeSource}`}</span>
      </span>
      <Button size="sm" variant="secondary" disabled={tokenBusy} onclick={() => (replacing = true)}>Replace…</Button>
      {#if status.tokenStored}
        <Button size="sm" icon aria-label="Remove token" title="Remove token" disabled={tokenBusy} loading={tokenBusy} onclick={askClearToken}><Icon name="x" size={14} /></Button>
      {/if}
    </div>
  {:else}
    <div class="grid gap-3">
      <span class="flex flex-wrap items-center gap-2">
        <SetupTokenRunner onStored={stored} onFailed={loginFailed} />
      </span>
      {#if !manual}
        <span class="flex flex-wrap items-center gap-2">
          <Button size="xs" onclick={() => (manual = true)}>Paste a token instead</Button>
          {#if replacing}<Button size="xs" onclick={() => { replacing = manual = false; token = ""; }}>Cancel</Button>{/if}
        </span>
      {:else}
        <span class="flex min-w-0 flex-wrap items-center gap-2">
          <input
            id={`${formId}-token`}
            class="{inputCls} min-w-[16rem] flex-1 font-mono"
            type="password"
            autocomplete="off"
            aria-label="Token"
            placeholder="Token from claude setup-token"
            bind:value={token}
            disabled={tokenBusy}
            oninput={() => (tokenMsg = "")}
            onkeydown={(e) => { if (e.key === "Enter") void saveToken(); }}
          />
          <Button variant="secondary" disabled={!token.trim() || tokenBusy} loading={tokenBusy} onclick={saveToken}>Save token</Button>
          <Button onclick={() => { manual = false; token = ""; if (replacing) replacing = false; }}>Cancel</Button>
        </span>
      {/if}
    </div>
  {/if}
  {#if tokenMsg}<p role="status" class="text-sm {msgCls[tokenMsgKind]}">{tokenMsg}</p>{/if}
  <div class="grid gap-1">
    <span class="flex items-center gap-2 text-sm text-ink">
      <label class="flex items-center gap-2">
        <Checkbox bind:checked={apiOn} disabled={apiBusy || !status} onchange={toggleApiKey} />
        Use an API key instead
        <span class="font-mono text-sm text-faint">ANTHROPIC_API_KEY</span>
      </label>
      <HelpText label="API key" detail="Sandboxed agents get ANTHROPIC_API_KEY from the daemon's environment instead of your Claude plan, billed per token." />
    </span>
    {#if status?.useApiKey}
      <span class="text-sm {status.apiKeySet ? 'text-warn' : 'text-faint'}">
        {status.apiKeySet ? "Billed per token to the key's account." : "No ANTHROPIC_API_KEY found, so nothing is passed."}
      </span>
    {/if}
    {#if apiErr}<span role="alert" class="text-sm text-bad">{apiErr}</span>{/if}
  </div>
</SettingsGroup>
