<script lang="ts">
  // Kill-confirmation dialog, shown whenever nav.killTarget is set — both the 'x'
  // shortcut and the SessionEmbed kill button route through here, so killing a
  // session always asks first. Enter confirms / Esc cancels are handled in
  // App.svelte's key handler (this component owns the buttons + copy).
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import Modal from "./Modal.svelte";

  const session = $derived(nav.killTarget ? store.sessionById(nav.killTarget) : undefined);
  const label = $derived(session ? session.issue || session.id.slice(0, 8) : nav.killTarget);

  function doKill() {
    const id = nav.killTarget;
    nav.cancelKill();
    if (id) store.kill(id);
  }
</script>

<Modal title="Kill session?" onClose={() => nav.cancelKill()} width="420px">
  <p class="text-sm leading-relaxed text-ink">
    Kill <span class="font-semibold text-accent-ink">{label}</span>?
    {#if session?.title}<span class="text-faint"> — {session.title}</span>{/if}
  </p>
  <p class="mt-2 text-xs text-faint">This stops its agent and removes the worktree. Unpushed work is lost.</p>
  {#snippet footer()}
    <div class="flex justify-end gap-2">
      <button
        class="rounded border border-edge px-3 py-1 text-xs text-faint hover:text-ink"
        onclick={() => nav.cancelKill()}>Cancel</button
      >
      <button class="rounded bg-bad px-3 py-1 text-xs font-medium text-on-bad hover:opacity-90" onclick={doKill}>Kill</button>
    </div>
  {/snippet}
</Modal>
