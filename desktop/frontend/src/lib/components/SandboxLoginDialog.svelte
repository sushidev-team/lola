<script lang="ts">
  // Shown by sandboxLogin.ensure before a sandboxed session would start
  // without a Claude login. Logging in (or pasting a token) starts the session;
  // Cancel starts nothing.
  import Button from "$lib/components/Button.svelte";
  import Modal from "$lib/components/Modal.svelte";
  import SandboxLoginPrompt from "$lib/components/SandboxLoginPrompt.svelte";
  import { sandboxLogin } from "$lib/sandboxlogin.svelte";
  import { store } from "$lib/store.svelte";

  const req = $derived(sandboxLogin.request);
  const label = $derived.by(() => {
    const p = store.projects.find((x) => x.name === req?.project);
    return p?.label || req?.project || "";
  });
</script>

{#if req}
  <Modal title="Log in to Claude first" onClose={() => sandboxLogin.finish(false)} width="520px">
    <div class="grid gap-3">
      <p class="text-ink">{label} runs its agents in Docker. They need a Claude login to work.</p>
      <SandboxLoginPrompt always bare onDone={() => sandboxLogin.finish(true)} />
    </div>
    {#snippet footer()}
      <div class="dialog-actions">
        <Button size="md" onclick={() => sandboxLogin.finish(false)}>Cancel</Button>
      </div>
    {/snippet}
  </Modal>
{/if}
