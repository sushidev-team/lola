<script module lang="ts">
  // Pure helpers for the multiline list fields (symlinks / postCreate / env):
  // a config array <-> a textarea string, one entry per line. Exported so they
  // can be unit-tested without mounting the component or touching the daemon.

  /** Join a config string[] into a textarea value, one entry per line. */
  export function linesToText(a: string[] | undefined): string {
    return (a ?? []).join("\n");
  }

  /** Split a textarea value into a config string[]: trim + drop blank lines. */
  export function textToLines(s: string): string[] {
    return s
      .split("\n")
      .map((l) => l.trim())
      .filter((l) => l.length > 0);
  }
</script>

<script lang="ts">
  import { onMount } from "svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import Modal from "$lib/components/Modal.svelte";
  import { ConfigService } from "@bindings/desktop";
  import type { ProjectFormDTO } from "@bindings/desktop/models";

  // Scalar fields are individual runes so binds stay reactive (a $state holding a
  // class instance is not deeply proxied). The DTO is reassembled on save.
  let loaded = $state(false);
  let saving = $state(false);
  let confirmRemove = $state(false);

  let isNew = $state(false);
  let name = $state("");
  let path = $state("");
  let repo = $state("");
  let defaultBranch = $state("");
  let agent = $state("");
  let symlinksText = $state("");
  let postCreateText = $state("");
  let envText = $state("");

  const agents: { value: string; label: string }[] = [
    { value: "", label: "inherit" },
    { value: "claude", label: "claude" },
    { value: "codex", label: "codex" },
    { value: "opencode", label: "opencode" },
  ];

  // Title works before the async load resolves via the overlay project name.
  const title = $derived(
    loaded
      ? isNew
        ? "add project"
        : `edit project: ${name}`
      : nav.overlayProject === ""
        ? "add project"
        : `edit project: ${nav.overlayProject}`,
  );

  const canSave = $derived(loaded && !saving && name.trim().length > 0);

  onMount(async () => {
    try {
      const d = await ConfigService.GetProject(nav.overlayProject);
      isNew = d.isNew;
      name = d.name;
      path = d.path;
      repo = d.repo;
      defaultBranch = d.defaultBranch;
      agent = d.agent;
      symlinksText = linesToText(d.symlinks);
      postCreateText = linesToText(d.postCreate);
      envText = linesToText(d.env);
      loaded = true;
    } catch (err) {
      store.setFlash(String(err), "bad");
      nav.closeOverlay();
    }
  });

  async function save() {
    if (!canSave) return;
    saving = true;
    const dto = new ProjectFormDTO({
      name: name.trim(),
      path: path.trim(),
      repo: repo.trim(),
      defaultBranch: defaultBranch.trim(),
      agent,
      symlinks: textToLines(symlinksText),
      postCreate: textToLines(postCreateText),
      env: textToLines(envText),
      isNew,
    });
    try {
      await ConfigService.SaveProject(dto);
      store.setFlash(isNew ? `added ${dto.name}` : `saved ${dto.name}`, "good");
      nav.closeOverlay();
    } catch (err) {
      store.setFlash(String(err), "bad");
      saving = false;
    }
  }

  async function remove() {
    try {
      await ConfigService.RemoveProject(name);
      store.setFlash(`removed ${name}`, "warn");
      nav.closeOverlay();
    } catch (err) {
      store.setFlash(String(err), "bad");
      confirmRemove = false;
    }
  }

  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent placeholder:text-faint/50";
</script>

<Modal {title} onClose={() => nav.closeOverlay()} width="600px">
  {#if !loaded}
    <div class="px-3 py-8 text-center text-xs text-faint">loading project…</div>
  {:else}
    <div class="flex flex-col gap-3">
      <!-- name (key when editing) -->
      <label class="flex flex-col gap-1">
        <span class="text-[11px] tracking-wide text-faint uppercase">Name</span>
        <input
          class="{inputCls} font-mono {!isNew ? 'cursor-not-allowed text-faint' : ''}"
          placeholder="my-project"
          readonly={!isNew}
          title={!isNew ? "the project name is the config key and can't be renamed here" : undefined}
          bind:value={name}
        />
      </label>

      <!-- path -->
      <label class="flex flex-col gap-1">
        <span class="text-[11px] tracking-wide text-faint uppercase">Path</span>
        <input class="{inputCls} font-mono" placeholder="/Users/you/code/my-project" bind:value={path} />
      </label>

      <!-- repo -->
      <label class="flex flex-col gap-1">
        <span class="text-[11px] tracking-wide text-faint uppercase">Repo</span>
        <input class="{inputCls} font-mono" placeholder="owner/name" bind:value={repo} />
      </label>

      <!-- default branch -->
      <label class="flex flex-col gap-1">
        <span class="text-[11px] tracking-wide text-faint uppercase">Default branch</span>
        <input class="{inputCls} font-mono" placeholder="main" bind:value={defaultBranch} />
      </label>

      <!-- agent (segmented; inherit = "") -->
      <div class="flex flex-col gap-1">
        <span class="text-[11px] tracking-wide text-faint uppercase">Agent</span>
        <span class="flex w-fit items-center gap-0.5 rounded border border-edge p-0.5">
          {#each agents as a (a.value)}
            <button
              type="button"
              class="rounded px-2 py-[2px] text-[11px]"
              class:bg-accent={agent === a.value}
              class:text-canvas={agent === a.value}
              class:text-faint={agent !== a.value}
              onclick={() => (agent = a.value)}>{a.label}</button
            >
          {/each}
        </span>
      </div>

      <!-- symlinks -->
      <label class="flex flex-col gap-1">
        <span class="text-[11px] tracking-wide text-faint uppercase">Symlinks</span>
        <textarea
          class="{inputCls} resize-y font-mono"
          rows="3"
          spellcheck="false"
          placeholder={".env\nnode_modules"}
          bind:value={symlinksText}
        ></textarea>
        <span class="text-[10px] text-faint">one path per line — linked into each worktree</span>
      </label>

      <!-- post-create -->
      <label class="flex flex-col gap-1">
        <span class="text-[11px] tracking-wide text-faint uppercase">Post-create</span>
        <textarea
          class="{inputCls} resize-y font-mono"
          rows="3"
          spellcheck="false"
          placeholder={"npm install\nmake build"}
          bind:value={postCreateText}
        ></textarea>
        <span class="text-[10px] text-faint">one command per line — run after the worktree is created</span>
      </label>

      <!-- env -->
      <label class="flex flex-col gap-1">
        <span class="text-[11px] tracking-wide text-faint uppercase">Env</span>
        <textarea
          class="{inputCls} resize-y font-mono"
          rows="3"
          spellcheck="false"
          placeholder={"KEY=value\nAPI_URL=http://localhost"}
          bind:value={envText}
        ></textarea>
        <span class="text-[10px] text-faint">one KEY=value per line</span>
      </label>
    </div>
  {/if}

  {#snippet footer()}
    <div class="flex items-center gap-2">
      {#if loaded && !isNew}
        {#if confirmRemove}
          <button
            class="rounded bg-bad/20 px-3 py-1 text-xs text-bad hover:bg-bad/30"
            onclick={remove}>confirm remove</button
          >
          <button class="px-2 py-1 text-xs text-faint hover:text-ink" onclick={() => (confirmRemove = false)}
            >cancel</button
          >
        {:else}
          <button class="px-3 py-1 text-xs text-bad/80 hover:text-bad" onclick={() => (confirmRemove = true)}
            >remove</button
          >
        {/if}
      {/if}
      <button class="ml-auto px-3 py-1 text-xs text-faint hover:text-ink" onclick={() => nav.closeOverlay()}
        >cancel</button
      >
      <button
        class="rounded bg-accent/20 px-3 py-1 text-xs text-accent hover:bg-accent/30 disabled:opacity-40"
        disabled={!canSave}
        onclick={save}>{saving ? "saving…" : "save"}</button
      >
    </div>
  {/snippet}
</Modal>
