<script lang="ts">
  // The container sandbox's machine-side settings ([sandbox]): the default
  // image and the Claude Code token sandboxed agents log in with.
  //
  // Self-contained like the Linear key section: it reads and writes through its
  // own service calls and never touches the settings DTO or its dirty baseline.
  // The token is WRITE-ONLY: the status reports whether one is stored, never the
  // value, and the input is cleared the moment it is saved.
  import { onMount } from "svelte";
  import Button from "$lib/components/Button.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import SettingsGroup from "$lib/components/SettingsGroup.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { ConfigService } from "@bindings/desktop";
  import type { SandboxStatusDTO } from "@bindings/desktop/models";

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

  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder";
  const rowCls = "settings-field grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] items-baseline gap-x-6 gap-y-1.5";
  const hintCls = "mt-1 block text-sm text-faint";
  const msgCls = { good: "text-good", bad: "text-bad" } as const;

  async function load() {
    loadErr = "";
    try {
      status = await ConfigService.SandboxStatus();
      image = savedImage = status?.image ?? "";
    } catch (e) {
      status = null;
      loadErr = String(e);
    }
  }
  onMount(load);

  async function saveImage() {
    imageBusy = true;
    imageMsg = "";
    try {
      await ConfigService.SetSandboxImage(image.trim());
      imageMsg = image.trim() ? "Image saved." : "Using Lola’s sandbox image.";
      imageMsgKind = "good";
      await load();
    } catch (e) {
      imageMsg = String(e);
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
      tokenMsg = "Token stored. New sandboxed sessions use it.";
      tokenMsgKind = "good";
      await load();
    } catch (e) {
      tokenMsg = String(e);
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
      tokenMsg = String(e);
      tokenMsgKind = "bad";
    } finally {
      tokenBusy = false;
    }
  }

  const claudeLine = $derived.by(() => {
    if (!status) return "";
    if (status.tokenStored) return "Token stored in the keychain";
    if (status.claudeSource) return `Using ${status.claudeSource}`;
    return "Not set";
  });
</script>

<SettingsGroup title="Status" description="Projects set to Docker workspace run their agent in a container.">
  {#if loadErr}
    <p role="alert" class="text-sm text-bad">{loadErr}</p>
  {:else if !status}
    <p class="text-sm text-faint">Checking…</p>
  {:else}
    <ul class="grid gap-1.5 text-sm" aria-label="Sandbox status">
      <li class={status.dockerOk ? "text-good" : "text-bad"}>
        <Icon name={status.dockerOk ? "check" : "x"} size={14} />
        {status.dockerOk ? `Docker is running (${status.docker})` : `Docker is not running (${status.docker}). Sandboxed sessions cannot start.`}
      </li>
      {#if status.dockerOk}
        <li class="text-ink">
          <Icon name="check" size={14} />
          {(status.missing ?? []).length > 0
            ? `${(status.missing ?? []).join(", ")} will be pulled on the first sandboxed session`
            : "Images present"}
        </li>
      {/if}
      <li class={status.tokenStored || status.claudeSource ? "text-good" : "text-warn"}>
        <Icon name={status.tokenStored || status.claudeSource ? "check" : "triangle-alert"} size={14} />
        Claude login: {claudeLine}
      </li>
      <li class={status.githubSource ? "text-ink" : "text-warn"}>
        <Icon name={status.githubSource ? "check" : "triangle-alert"} size={14} />
        {status.githubSource
          ? `GitHub token from ${status.githubSource}. Sandboxed agents can push and open PRs as you.`
          : "No GitHub token. Sandboxed agents cannot push. Run gh auth login."}
      </li>
    </ul>
  {/if}
</SettingsGroup>

<SettingsGroup title="Image" description="Any image with the coding agent and git works. Lola mounts its own binary.">
  <div class={rowCls}>
    <label for={`${formId}-image`} class="text-faint">Default image</label>
    <span>
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
      <span class={hintCls}>Leave empty for Lola’s sandbox image. A project can override it.</span>
    </span>
  </div>
  <div class={rowCls}>
    <span></span>
    <span class="flex flex-wrap items-center gap-2">
      <Button variant="secondary" disabled={image.trim() === savedImage || imageBusy} loading={imageBusy} onclick={saveImage}>Save image</Button>
      <span role="status" class="text-sm {msgCls[imageMsgKind]}">{imageMsg}</span>
    </span>
  </div>
</SettingsGroup>

<SettingsGroup title="Claude login" description="A container cannot read your keychain. Create a long-lived token with claude setup-token and store it here.">
  <div class={rowCls}>
    <label for={`${formId}-token`} class="text-faint">{status?.tokenStored ? "New token" : "Token"}</label>
    <span>
      <input
        id={`${formId}-token`}
        class="{inputCls} font-mono"
        type="password"
        autocomplete="off"
        placeholder="sk-ant-oat01-…"
        bind:value={token}
        disabled={tokenBusy}
        oninput={() => (tokenMsg = "")}
      />
      <span class={hintCls}>Stored in the macOS keychain. Lola never shows it again.</span>
    </span>
  </div>
  <div class={rowCls}>
    <span></span>
    <span class="flex flex-wrap items-center gap-2">
      <Button variant="primary" disabled={!token.trim() || tokenBusy} loading={tokenBusy} onclick={saveToken}>Save token</Button>
      {#if status?.tokenStored}
        <Button disabled={tokenBusy} onclick={askClearToken}>Remove token…</Button>
      {/if}
      <span role="status" class="text-sm {msgCls[tokenMsgKind]}">{tokenMsg}</span>
    </span>
  </div>
</SettingsGroup>
