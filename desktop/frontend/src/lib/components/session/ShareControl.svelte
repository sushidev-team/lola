<script lang="ts">
  // The share control on a session's bar: one icon button, and a small dialog
  // for who and how much. Compact on purpose; words appear only where they carry
  // a decision (who, view or type, and the host-session warning).
  //
  // Nothing here decides who may attach. The daemon applies the share and the
  // owner's pinned teammates; a refusal comes back in its words and is shown as is.
  import Button from "../Button.svelte";
  import Checkbox from "../Checkbox.svelte";
  import Icon from "../Icon.svelte";
  import Modal from "../Modal.svelte";
  import { nav } from "$lib/nav.svelte";
  import { share } from "$lib/share.svelte";
  import { LEVELS, attachedLine, audienceChoices, levelOf, shareTitle, type ShareLevel } from "$lib/sharing";
  import { tooltip } from "$lib/tooltip";
  import { store } from "$lib/store.svelte";

  let { sessionId }: { sessionId: string } = $props();

  const session = $derived(store.sessionById(sessionId));
  const current = $derived(session?.share ?? null);
  const attached = $derived(current?.attached ?? []);
  const hosted = $derived(!session?.sandbox);

  let open = $state(false);
  let level = $state<ShareLevel>("view");
  /** The teammates picked; empty means every teammate this Mac allowed. */
  let picked = $state<string[]>([]);
  let confirmHost = $state(false);

  const choices = $derived(audienceChoices(share.members));
  const busy = $derived(!!share.busy[sessionId]);
  const needsConfirm = $derived(level === "type" && hosted);

  function show() {
    level = levelOf(current?.level);
    picked = [...(current?.with ?? [])];
    confirmHost = false;
    share.error = "";
    open = true;
    void share.refresh();
  }

  function toggle(id: string) {
    picked = picked.includes(id) ? picked.filter((p) => p !== id) : [...picked, id];
  }

  async function apply() {
    const r = await share.setShared({
      session: sessionId,
      on: true,
      level,
      with: picked,
      confirmUnsandboxed: confirmHost,
    });
    if (r) open = false;
  }

  async function stop() {
    const r = await share.setShared({ session: sessionId, on: false });
    if (r) open = false;
  }

  function openTeam() {
    open = false;
    nav.goTeam();
  }
</script>

<Button
  icon
  size="xs"
  selected={!!current}
  aria-label={shareTitle(!!current, attached.length)}
  aria-haspopup="dialog"
  onclick={show}
  data-share-control
  data-shared={current ? "on" : "off"}
>
  <span use:tooltip={{ text: shareTitle(!!current, attached.length) }} class="relative inline-flex">
    <Icon name={current ? "users" : "share-2"} size={14} />
    {#if attached.length > 0}
      <span class="absolute -top-1.5 -right-2 min-w-3.5 rounded-full bg-accent-fill px-1 text-center text-[10px] leading-3.5 text-accent-ink" data-share-count>{attached.length}</span>
    {/if}
  </span>
</Button>

{#if open}
  <Modal title="Share with team" width="440px" onClose={() => (open = false)}>
    <div class="grid gap-4" data-share-dialog>
      {#if attached.length > 0}
        <p class="flex items-center gap-2 text-sm text-ink-2" data-share-attached>
          <Icon name="users" size={14} />
          <span class="min-w-0 truncate">{attachedLine(attached)} attached now</span>
        </p>
      {/if}

      <div class="grid gap-1.5">
        <div class="flex items-center gap-1" role="group" aria-label="What teammates can do">
          {#each LEVELS as l (l.id)}
            <Button icon size="sm" selected={level === l.id} aria-label={l.label} aria-pressed={level === l.id} onclick={() => (level = l.id)} data-share-level={l.id}>
              <span use:tooltip={{ text: l.hint }} class="inline-flex"><Icon name={l.icon} size={15} /></span>
            </Button>
          {/each}
        </div>
        {#if needsConfirm}
          <label class="flex items-start gap-2 text-sm text-ink-2" data-share-host-warning>
            <Checkbox bind:checked={confirmHost} />
            <span>This session runs on this Mac. Typing lets a teammate act as you in it.</span>
          </label>
        {/if}
      </div>

      {#if choices.length === 0}
        <p class="copy text-sm text-ink-2" data-share-nobody>
          No teammate is allowed to attach to your sessions yet.
          <Button variant="bare" size="xs" class="h-auto! px-0! text-info underline" onclick={openTeam}>Allow a teammate</Button>
        </p>
      {:else}
        <ul class="grid gap-1" aria-label="Who can attach">
          {#each choices as m (m.deviceId)}
            <li>
              <label class="flex items-center gap-2 text-ink">
                <Checkbox checked={picked.includes(m.deviceId)} onchange={() => toggle(m.deviceId)} aria-label={m.name} />
                <span class="min-w-0 truncate">{m.name}</span>
              </label>
            </li>
          {/each}
        </ul>
        <p class="text-sm text-faint">{picked.length === 0 ? "Everyone you allowed" : `${picked.length} chosen`}</p>
      {/if}

      {#if share.error}<p role="alert" class="text-bad" data-share-error>{share.error}</p>{/if}
    </div>
    {#snippet footer()}
      <div class="dialog-actions">
        {#if current}
          <Button variant="danger" size="md" disabled={busy} onclick={stop} data-share-stop>Stop sharing</Button>
        {/if}
        <Button class="cancel" size="md" onclick={() => (open = false)} disabled={busy}>Cancel</Button>
        <Button variant="primary" size="md" loading={busy} disabled={choices.length === 0 || (needsConfirm && !confirmHost)} onclick={apply} data-share-apply>{current ? "Save" : "Share"}</Button>
      </div>
    {/snippet}
  </Modal>
{/if}
