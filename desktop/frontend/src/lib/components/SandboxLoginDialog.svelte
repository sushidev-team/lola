<script lang="ts">
  // Shown by sandboxLogin.ensure before a sandboxed session would start
  // without a login. Claude logs in right here; codex and opencode keep their
  // login on this Mac, which Lola copies into the container, so the dialog
  // names the command and checks again. Cancel starts nothing.
  import Button from "$lib/components/Button.svelte";
  import Modal from "$lib/components/Modal.svelte";
  import SandboxLoginPrompt from "$lib/components/SandboxLoginPrompt.svelte";
  import { sandboxLogin } from "$lib/sandboxlogin.svelte";
  import { store } from "$lib/store.svelte";

  const AGENTS: Record<string, { name: string; command: string }> = {
    claude: { name: "Claude", command: "claude setup-token" },
    codex: { name: "Codex", command: "codex login" },
    opencode: { name: "OpenCode", command: "opencode auth login" },
  };

  const req = $derived(sandboxLogin.request);
  const info = $derived(AGENTS[req?.agent ?? "claude"] ?? AGENTS.claude);
  const label = $derived.by(() => {
    const p = store.projects.find((x) => x.name === req?.project);
    return p?.label || req?.project || "";
  });
  let checking = $state(false);
  let stillMissing = $state(false);
  let copied = $state(false);

  async function check() {
    checking = true;
    stillMissing = false;
    stillMissing = !(await sandboxLogin.recheck());
    checking = false;
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(info.command);
      copied = true;
      setTimeout(() => (copied = false), 1500);
    } catch {
      copied = false;
    }
  }
</script>

{#if req}
  <Modal title={`Log in to ${info.name} first`} onClose={() => sandboxLogin.finish(false)} width="520px">
    <div class="grid gap-3">
      <p class="text-ink">{label} runs its agents in Docker. They need a {info.name} login to work.</p>
      {#if req.agent === "claude"}
        <SandboxLoginPrompt always bare onDone={() => sandboxLogin.finish(true)} />
      {:else}
        <div class="flex flex-wrap items-center gap-2 text-sm text-ink">
          <span>In a terminal on this Mac, run</span>
          <code class="rounded bg-canvas px-1.5 py-0.5 font-mono">{info.command}</code>
          <Button size="xs" onclick={copy}>{copied ? "Copied" : "Copy"}</Button>
        </div>
        <p class="text-sm text-faint">Lola copies that login into each new {info.name} session.</p>
        {#if stillMissing}<p role="alert" class="text-sm text-warn">Still no {info.name} login found.</p>{/if}
      {/if}
    </div>
    {#snippet footer()}
      <div class="dialog-actions">
        <Button size="md" onclick={() => sandboxLogin.finish(false)}>Cancel</Button>
        {#if req.agent !== "claude"}
          <Button variant="primary" size="md" loading={checking} onclick={check}>Check again</Button>
        {/if}
      </div>
    {/snippet}
  </Modal>
{/if}
