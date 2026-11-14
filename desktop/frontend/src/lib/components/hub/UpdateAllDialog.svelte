<script lang="ts">
  // "Update all dependencies": one agent fixes a repository's open advisories
  // AND updates its other dependencies, in one pull request. The person picks
  // which release levels to take and may add a note for the agent; the
  // daemon renders the task from lola's own template and appends the note.
  //
  // Start stays off until at least one level is ticked: an update with no
  // level is only the advisories, which "Fix all" already does. Major gets a
  // line of its own, because it is the one level that may break the build.
  import Button from "$lib/components/Button.svelte";
  import Checkbox from "$lib/components/Checkbox.svelte";
  import Modal from "$lib/components/Modal.svelte";
  import type { HubUpdateScope } from "@bindings/internal/protocol";

  let {
    where,
    advisories,
    busy = false,
    error = "",
    onStart,
    onClose,
  }: {
    /** The project the session starts in. */
    where: string;
    /** How many open advisories come along. */
    advisories: number;
    busy?: boolean;
    error?: string;
    onStart: (scope: HubUpdateScope) => void;
    onClose: () => void;
  } = $props();

  const LEVELS = [
    { key: "patch", label: "Patch", hint: "Bug fixes" },
    { key: "minor", label: "Minor", hint: "New features, compatible" },
    { key: "major", label: "Major", hint: "May break; the agent adapts the code" },
  ] as const;

  let levels = $state({ patch: true, minor: true, major: false });
  let note = $state("");
  const id = $props.id();
  const MAX = 2000;
  const anyLevel = $derived(levels.patch || levels.minor || levels.major);
</script>

<Modal title="Update all dependencies" {onClose} width="520px"
  onkey={(e) => { if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); onClose(); } }}>
  <div class="grid gap-5">
    <p class="text-ink">
      One agent in {where} fixes the {advisories === 1 ? "open advisory" : `${advisories} open advisories`} and updates the other dependencies, in one pull request.
    </p>
    <fieldset class="grid gap-2">
      <legend class="mb-2 text-ink">Include updates</legend>
      {#each LEVELS as l (l.key)}
        <label class="flex cursor-pointer items-center gap-3 rounded-lg border border-edge px-3 py-2.5 {levels[l.key] ? 'bg-sel' : ''}" data-level={l.key}>
          <Checkbox bind:checked={levels[l.key]} />
          <span class="grid min-w-0">
            <span class="text-ink">{l.label}</span>
            <span class="text-sm text-faint">{l.hint}</span>
          </span>
        </label>
      {/each}
      {#if !anyLevel}<p class="text-sm text-warn" data-no-level>Tick at least one level to update.</p>{/if}
      {#if levels.major}
        <p class="flex items-start gap-2 rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn" data-major-warning>
          Major updates can change how a library works. The agent adapts the code, but review the pull request closely before you merge it.
        </p>
      {/if}
    </fieldset>
    <div class="grid gap-1.5">
      <label for={`${id}-note`} class="text-ink">Instructions for the agent <span class="text-faint">(optional)</span></label>
      <textarea id={`${id}-note`} rows="3" maxlength={MAX} bind:value={note}
        class="w-full resize-y rounded border border-edge bg-canvas px-2.5 py-2 text-ink outline-none placeholder:text-placeholder focus:border-accent"
        placeholder="Keep vite on 5.x. Leave the PHP dependencies alone."></textarea>
    </div>
    {#if error}<p role="alert" class="text-sm text-bad">{error}</p>{/if}
  </div>
  {#snippet footer()}
    <div class="dialog-actions">
      <Button class="cancel" size="md" onclick={onClose}>Cancel</Button>
      <Button variant="primary" size="md" loading={busy} disabled={busy || !anyLevel}
        onclick={() => onStart({ patch: levels.patch, minor: levels.minor, major: levels.major, note: note.trim() })}>Start update</Button>
    </div>
  {/snippet}
</Modal>
