<script lang="ts">
  // Which projects take team chores, and the two things a chore needs from a
  // project: its GitHub repository and (unless the person turned the rule off)
  // the Docker sandbox. Every change saves AT ONCE through
  // ConfigService.SaveHubProject — like join and leave, not through the
  // overlay's Save — because each one is a [[project]] key, not a settings
  // field. The daemon reloads after each save.
  import { onMount } from "svelte";
  import Button from "$lib/components/Button.svelte";
  import Checkbox from "$lib/components/Checkbox.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import { ConfigService } from "@bindings/desktop";
  import type { HubProjectDTO, HubProjectPatchDTO } from "@bindings/desktop/models";

  let { requireSandbox = true }: { requireSandbox?: boolean } = $props();

  const REPO_RE = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;

  let projects = $state<HubProjectDTO[] | null>(null);
  let loadError = $state("");
  let busy = $state<Record<string, boolean>>({});
  let errors = $state<Record<string, string>>({});
  /** The project whose repo field is open, and its draft. */
  let editing = $state("");
  let draft = $state("");

  onMount(() => void load());

  async function load() {
    try {
      projects = (await ConfigService.HubProjects()) ?? [];
      loadError = "";
    } catch (e) {
      projects = [];
      loadError = String(e);
    }
  }

  async function save(p: HubProjectDTO, patch: Partial<Omit<HubProjectPatchDTO, "name">>) {
    busy = { ...busy, [p.name]: true };
    errors = { ...errors, [p.name]: "" };
    try {
      await ConfigService.SaveHubProject({ repo: null, hubChores: null, sandboxed: null, ...patch, name: p.name });
      await load();
      return true;
    } catch (e) {
      errors = { ...errors, [p.name]: String(e).replace(/^Error:\s*/, "") };
      return false;
    } finally {
      busy = { ...busy, [p.name]: false };
    }
  }

  function edit(p: HubProjectDTO) {
    editing = p.name;
    draft = p.repo || p.suggestedRepo || "";
  }

  async function saveRepo(p: HubProjectDTO) {
    const repo = draft.trim();
    if (repo && !REPO_RE.test(repo)) {
      errors = { ...errors, [p.name]: "Enter the repository as owner/name." };
      return;
    }
    if (await save(p, { repo })) editing = "";
  }

  const on = $derived((projects ?? []).filter((p) => p.hubChores).length);
</script>

{#if projects === null}
  <p role="status" class="text-sm text-faint">Loading projects…</p>
{:else if loadError}
  <p role="alert" class="text-sm text-bad">{loadError}</p>
{:else if projects.length === 0}
  <div class="grid justify-items-center gap-2 rounded-lg border border-dashed border-edge px-4 py-8 text-center">
    <span class="text-faint"><Icon name="folder" size={22} /></span>
    <p class="text-ink">No projects yet</p>
    <p class="text-sm text-faint">Add a project, then let it take team chores here.</p>
  </div>
{:else}
  <div class="flex items-center justify-between text-sm text-faint">
    <span class="num" data-hub-projects-count>{on} of {projects.length} take chores</span>
  </div>
  <ul class="grid min-w-0 divide-y divide-edge rounded-lg border border-edge" aria-label="Projects">
    {#each projects as p (p.name)}
      {@const noRepo = !REPO_RE.test(p.repo)}
      {@const needsSandbox = requireSandbox && !p.sandboxed}
      <li class="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-x-3 gap-y-1.5 px-3 py-2.5" data-hub-project={p.name}>
        <span class="pt-0.5">
          <Checkbox checked={p.hubChores} disabled={busy[p.name] || (noRepo && !p.hubChores)}
            aria-label={`Team chores for ${p.label}`}
            title={noRepo && !p.hubChores ? "Set the repository first" : ""}
            onchange={() => void save(p, { hubChores: !p.hubChores })} />
        </span>
        <span class="grid min-w-0 gap-0.5">
          <span class="flex min-w-0 items-baseline gap-1.5">
            <span class="truncate text-ink">{p.label}</span>
            {#if p.stack}<span class="truncate text-sm text-faint">in {p.stack}</span>{/if}
          </span>
          {#if editing === p.name}
            <span class="flex min-w-0 flex-wrap items-center gap-1.5">
              <!-- svelte-ignore a11y_autofocus -->
              <input class="min-w-[12rem] flex-1 rounded border border-edge bg-canvas px-2 py-1 font-mono text-sm text-ink outline-none focus:border-accent placeholder:text-placeholder"
                aria-label={`Repository for ${p.label}`} placeholder="owner/name" spellcheck="false" autocomplete="off" autofocus
                bind:value={draft}
                onkeydown={(e) => { if (e.key === "Enter") void saveRepo(p); if (e.key === "Escape") { e.stopPropagation(); editing = ""; } }} />
              <Button size="xs" variant="secondary" loading={busy[p.name]} onclick={() => void saveRepo(p)}>Save</Button>
              <Button size="xs" onclick={() => (editing = "")}>Cancel</Button>
            </span>
          {:else if !noRepo}
            <span class="flex min-w-0 items-center gap-1">
              <span class="truncate font-mono text-sm text-faint">{p.repo}</span>
              <Button size="xs" icon aria-label={`Edit repository for ${p.label}`} onclick={() => edit(p)}><Icon name="pencil" size={12} /></Button>
            </span>
            {#if p.suggestedRepo}
              <span class="text-sm text-warn">The checkout points at <span class="font-mono">{p.suggestedRepo}</span>.
                <Button size="xs" onclick={() => void save(p, { repo: p.suggestedRepo })}>Use it</Button></span>
            {/if}
          {:else}
            <span class="flex min-w-0 flex-wrap items-center gap-1.5 text-sm">
              <span class="text-faint">No repository</span>
              {#if p.suggestedRepo}
                <Button size="xs" variant="secondary" loading={busy[p.name]} onclick={() => void save(p, { repo: p.suggestedRepo })}>
                  Use <span class="font-mono">{p.suggestedRepo}</span>
                </Button>
              {/if}
              <Button size="xs" onclick={() => edit(p)}>{p.suggestedRepo ? "Other…" : "Set…"}</Button>
            </span>
          {/if}
        </span>
        <span class="flex items-center gap-1.5 pt-0.5">
          {#if p.sandboxed}
            <span class="inline-flex items-center gap-1 rounded-full border border-edge px-2 py-0.5 text-sm text-faint" title="Runs in the Docker sandbox">
              <Icon name="package" size={12} /> Docker
            </span>
          {:else if needsSandbox}
            <Button size="xs" variant="secondary" loading={busy[p.name]} onclick={() => void save(p, { sandboxed: true })}
              title="Chores run only in sandboxed projects">
              <Icon name="package" size={12} /> Use Docker
            </Button>
          {/if}
        </span>
        {#if p.hubChores && needsSandbox}
          <p class="col-start-2 col-end-4 flex items-center gap-1.5 text-sm text-warn">
            <Icon name="triangle-alert" size={12} /> Skipped until it uses Docker.
          </p>
        {/if}
        {#if errors[p.name]}<p role="alert" class="col-start-2 col-end-4 text-sm text-bad">{errors[p.name]}</p>{/if}
      </li>
    {/each}
  </ul>
{/if}
