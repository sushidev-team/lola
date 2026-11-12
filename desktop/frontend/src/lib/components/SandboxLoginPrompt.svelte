<script lang="ts">
  // The one-time Claude login for Docker workspaces. A container cannot read
  // the macOS keychain the host claude logs in through, so sandboxed agents
  // need a long-lived token (`claude setup-token`). "Log in with Claude" runs
  // that command here and stores the token itself (SetupTokenRunner); pasting a
  // token stays as the manual path. This card shows only while no credential
  // exists (keychain or daemon env) and disappears once one is stored, so it
  // asks once. The token is WRITE-ONLY: cleared from the input the moment it is
  // saved, never read back.
  import { onMount } from "svelte";
  import Button from "$lib/components/Button.svelte";
  import HelpText from "$lib/components/HelpText.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import SetupTokenRunner from "$lib/components/SetupTokenRunner.svelte";
  import { ConfigService } from "@bindings/desktop";

  const COMMAND = "claude setup-token";

  let needed = $state(false);
  let token = $state("");
  let busy = $state(false);
  let err = $state("");
  let copied = $state(false);
  let manual = $state(false);
  const id = $props.id();

  onMount(async () => {
    try {
      const st = await ConfigService.SandboxStatus();
      needed = !!st && !st.tokenStored && !st.claudeSource;
    } catch {
      // Unknown status: say nothing rather than nag. Settings → Sandbox has it.
      needed = false;
    }
  });

  async function copy() {
    try {
      await navigator.clipboard.writeText(COMMAND);
      copied = true;
      setTimeout(() => (copied = false), 1500);
    } catch {
      copied = false;
    }
  }

  async function save() {
    if (!token.trim()) return;
    busy = true;
    err = "";
    try {
      await ConfigService.SetSandboxToken(token);
      token = "";
      needed = false;
    } catch (e) {
      err = String(e);
    } finally {
      busy = false;
    }
  }
</script>

{#if needed}
  <section class="grid gap-3 rounded border border-warn/50 bg-warn/5 p-3 text-sm" aria-labelledby={`${id}-title`}>
    <div class="flex items-center gap-1.5 text-warn">
      <Icon name="triangle-alert" size={14} />
      <span id={`${id}-title`}>Connect Claude for Docker workspaces</span>
      <HelpText label="Claude login" detail="A container cannot read your keychain, so sandboxed agents log in with a long-lived token. Lola stores it in the macOS keychain and never shows it again. You do this once for all projects." />
    </div>
    <SetupTokenRunner onStored={() => (needed = false)} />
    {#if !manual}
      <span><Button size="xs" onclick={() => (manual = true)}>Paste a token instead</Button></span>
    {:else}
      <div class="flex flex-wrap items-center gap-2 text-ink">
        <span>In a terminal, run</span>
        <code class="rounded bg-canvas px-1.5 py-0.5 font-mono">{COMMAND}</code>
        <Button size="xs" onclick={copy}>{copied ? "Copied" : "Copy"}</Button>
      </div>
      <div class="grid gap-1.5">
        <label for={`${id}-token`} class="text-ink">Paste the token</label>
        <div class="flex flex-wrap items-center gap-2">
          <input
            id={`${id}-token`}
            class="min-w-[16rem] flex-1 rounded border border-edge bg-canvas px-2 py-1.5 font-mono text-ink outline-none placeholder:text-placeholder focus:border-accent"
            type="password"
            autocomplete="off"
            placeholder="sk-ant-oat01-…"
            bind:value={token}
            disabled={busy}
            oninput={() => (err = "")}
          />
          <Button variant="secondary" disabled={!token.trim() || busy} loading={busy} onclick={save}>Save token</Button>
        </div>
        {#if err}<p role="alert" class="text-bad">{err}</p>{/if}
      </div>
    {/if}
  </section>
{/if}
