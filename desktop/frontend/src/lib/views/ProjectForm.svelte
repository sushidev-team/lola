<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { linesToText, splitLines, cleanLines } from "$lib/lines";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { overlayClose } from "$lib/overlayClose";
  import { deepEqual } from "$lib/deepEqual";
  import Modal from "$lib/components/Modal.svelte";
  import Tabs from "$lib/components/Tabs.svelte";
  import Button from "$lib/components/Button.svelte";
  import Checkbox from "$lib/components/Checkbox.svelte";
  import Select from "$lib/components/Select.svelte";
  import { ConfigService, DaemonService, LinearService } from "@bindings/desktop";
  import type { GroupDTO } from "@bindings/desktop";
  import { slug, slugTyping, displayName } from "$lib/slug";
  import type {
    ProjectFormDTO,
    InheritsDTO,
    PathInfoDTO,
    SettingsDTO,
    LinearTeam,
    LinearTeamMeta,
    LinearOption,
  } from "@bindings/desktop/models";

  // A project IS the poll unit, so this one overlay covers the whole
  // [[project]] table: repo setup, the Linear filter, labels and write-back.
  const TABS = [
    { id: "repo", label: "Repo" },
    { id: "filter", label: "Filter" },
    { id: "labels", label: "Labels" },
    { id: "writeback", label: "Write-back" },
  ];

  // The [defaults]-inheritable keys, mirroring config.ProjectInherits. A set bit
  // means the value shown is [defaults]', not this project's.
  const INHERIT_KEYS = [
    "symlinks",
    "postCreate",
    "env",
    "matchLabels",
    "matchMode",
    "onSentSetLabel",
    "blockedLabelId",
    "dedupMode",
    "prioritySort",
  ] as const;
  type InheritKey = (typeof INHERIT_KEYS)[number];

  // The bindings hand back class instances, which $state does NOT deep-proxy —
  // so the DTO is copied into plain objects on load and reassembled on save.
  let f = $state<ProjectFormDTO | null>(null);
  // [defaults] values, so "revert to inherit" can refill a control with what
  // will actually apply rather than leaving the override text behind.
  let defaults = $state<SettingsDTO | null>(null);
  let loadErr = $state("");
  let saving = $state(false);
  // A save that the daemon rejects used to surface only as a footer flash — behind
  // this backdrop, truncated, gone in 4s — so it read like a save that just didn't
  // close. Held here and shown inline instead; cleared on the next attempt.
  let saveErr = $state("");
  let confirmRemove = $state(false);
  let tab = $state(nav.overlayTab || "repo");

  // The DTO exactly as it was loaded, so a close can tell real edits from an
  // untouched form and only prompt to discard when something changed. Snapshotted
  // AFTER the normalization below so the comparison is against the same shape the
  // form mutates, not the raw backend DTO.
  let loaded = $state<ProjectFormDTO | null>(null);
  const dirty = $derived.by(() => (f && loaded ? !deepEqual($state.snapshot(f), $state.snapshot(loaded)) : false));

  // Everything on this tab is DERIVED from the folder: one InspectPath pass
  // yields the GitHub remote, the branch worktrees fork from, the branch list
  // and a suggested label/id, so adding a project is "pick the repo, press
  // Save". It only ever FILLS: a detected value never overwrites what the user
  // typed, and a checkout with no GitHub remote leaves Repo empty — the safe,
  // fail-closed value that disables PR checks rather than pointing them at the
  // wrong repository. repoAuto/branchAuto drive the hints and gate the fill.
  let inspectedFor = $state("");
  let pathInfo = $state<PathInfoDTO | null>(null);
  let repoAuto = $state(false);
  let branchAuto = $state(false);
  let picking = $state(false);

  // Label vs ID. The label is free text; the id is a path segment and the prefix
  // of every session/tmux name, so it is slugged as it is typed and changing it
  // on an existing project is a RENAME the daemon has to perform.
  //
  // origName is the id the form opened on — what a rename renames FROM, and what
  // SaveProject must find on disk. idAuto keeps the id tracking the label until
  // the human types an id of their own, after which it is never rewritten.
  let origName = $state("");
  let idAuto = $state(false);

  // The checkout's branches, offered as suggestions on Default branch. A
  // datalist keeps the input free text, so a path that is not a checkout — or a
  // branch that does not exist yet — is never a dead end.
  let branches = $state<string[]>([]);

  /**
   * Read the checkout behind `path`. `fill` is what separates the two callers:
   * a folder the user just PICKED (or left the field on) fills the derived
   * values, while the pass on open only learns what the path is — filling an
   * untouched form's fields behind the user's back would both surprise them and
   * mark the form dirty. `snap` additionally adopts the repository ROOT, so
   * picking a subdirectory still configures the repo.
   */
  async function inspect(path: string, { fill = false, snap = false } = {}) {
    const p = path.trim();
    if (!p) return;
    if (!fill && p === inspectedFor) return;
    inspectedFor = p;
    let info: PathInfoDTO;
    try {
      info = await ConfigService.InspectPath(p);
    } catch {
      return; // best-effort: an unreadable path just leaves the fields alone
    }
    // Re-check on return: the user may have changed the path while this was in
    // flight, and answering about the old checkout would fill in the wrong facts.
    if (!f || f.path.trim() !== p) return;
    pathInfo = info;
    branches = info.branches ?? [];
    if (!fill) return;
    if (snap && info.path) {
      f.path = info.path;
      inspectedFor = info.path;
    }
    if (info.repo && !f.repo.trim()) {
      f.repo = info.repo;
      repoAuto = true;
    }
    if (info.defaultBranch && (branchAuto || !f.defaultBranch.trim())) f.defaultBranch = info.defaultBranch;
    // Identity is suggested for a NEW project only: filling an existing
    // project's empty label would write a label key nobody asked for.
    if (!f.isNew) return;
    if (!f.label.trim() && info.suggestedLabel) f.label = info.suggestedLabel;
    if (idAuto) f.name = slug(f.label);
  }

  /** The native directory chooser. An empty return is a cancel, not an error. */
  async function pickFolder() {
    if (!f || picking) return;
    picking = true;
    try {
      const chosen = await ConfigService.PickFolder(f.path.trim());
      if (chosen && f) {
        f.path = chosen;
        await inspect(chosen, { fill: true, snap: true });
      }
    } catch (e) {
      store.setFlash(String(e), "bad");
    } finally {
      picking = false;
    }
  }

  // Linear metadata drives the cascading pickers. When it can't load (no key,
  // API error) the ID fields fall back to raw UUID entry so the form still
  // works — options=null means "render a text input".
  let teams = $state<LinearTeam[]>([]);
  let teamsErr = $state("");
  let meta = $state<LinearTeamMeta | null>(null);
  let metaLoading = $state(false);
  let metaErr = $state("");

  const agents: LinearOption[] = [
    { id: "", label: "inherit" },
    { id: "claude", label: "claude" },
    { id: "codex", label: "codex" },
    { id: "opencode", label: "opencode" },
  ];

  const title = $derived(
    f ? (f.isNew ? "add project" : `project: ${displayName(f)}`) : nav.overlayProject === "" ? "add project" : `project: ${nav.overlayProject}`,
  );
  // Saving needs a usable ID, not merely non-empty text: an all-non-ASCII label
  // slugs to "" and there is no id to write.
  const canSave = $derived(!!f && !saving && slug(f.name) !== "");
  const renaming = $derived(!!f && !f.isNew && slug(f.name) !== origName);

  function toggleId(arr: string[] | null, id: string): string[] {
    const a = arr ?? [];
    return a.includes(id) ? a.filter((x) => x !== id) : [...a, id];
  }

  /** Fill in every bit so a DTO from an older backend can't leave one undefined. */
  function inheritsOf(src: Partial<InheritsDTO> | undefined): InheritsDTO {
    const out = {} as InheritsDTO;
    for (const k of INHERIT_KEYS) out[k] = !!src?.[k];
    return out;
  }

  function inherited(k: InheritKey): boolean {
    return !!f?.inherits[k];
  }
  /** Any edit of an inherited field promotes it to a project-level override. */
  function promote(k: InheritKey) {
    if (f && f.inherits[k]) f.inherits[k] = false;
  }
  /**
   * Hand the key back to [defaults] AND refill the control from them, so the
   * ghosted value is the one that will actually apply.
   */
  function revert(k: InheritKey) {
    if (!f) return;
    f.inherits[k] = true;
    const d = defaults;
    if (!d) return; // settings unreadable — keep the current value as the ghost
    switch (k) {
      case "symlinks":
        f.symlinks = [...(d.symlinks ?? [])];
        break;
      case "postCreate":
        f.postCreate = [...(d.postCreate ?? [])];
        break;
      case "env":
        f.env = [...(d.env ?? [])];
        break;
      case "matchLabels":
        f.matchLabels = [...(d.matchLabels ?? [])];
        break;
      case "matchMode":
        f.matchMode = d.matchMode;
        break;
      case "onSentSetLabel":
        f.onSentSetLabel = d.onSentSetLabel;
        break;
      case "blockedLabelId":
        f.blockedLabelId = d.blockedLabelId;
        break;
      case "dedupMode":
        f.dedupMode = d.dedupMode;
        break;
      case "prioritySort":
        break; // not surfaced by this form; the bit is passed through on save
    }
  }
  function toggleInherit(k: InheritKey) {
    if (inherited(k)) promote(k);
    else revert(k);
  }
  function ghost(k: InheritKey | null): string {
    return k && inherited(k) ? "opacity-55" : "";
  }

  // The [[group]] folders this project can be filed under. Set from config on
  // mount; empty simply means the Group field offers only "top level".
  let groups = $state<GroupDTO[]>([]);

  async function loadMeta(teamId: string) {
    meta = null;
    metaErr = "";
    if (!teamId) return;
    metaLoading = true;
    try {
      meta = await LinearService.TeamMeta(teamId, false);
    } catch (e) {
      metaErr = String(e);
    } finally {
      metaLoading = false;
    }
  }

  onMount(async () => {
    try {
      const d = await ConfigService.GetProject(nav.overlayProject);
      // label is normalized like inherits: a DTO from an older backend must not
      // leave it undefined and make every .trim() on it throw.
      f = { ...d, label: d.label ?? "", inherits: inheritsOf(d.inherits) };
      // The baseline the dirty check compares against — the normalized DTO, not
      // the raw one, and before any user edit.
      loaded = $state.snapshot(f) as ProjectFormDTO;
      origName = d.name;
      // Only a NEW project derives its id from the label. An existing id is
      // load-bearing and must not drift when someone edits the label.
      idAuto = d.isNew;
      // The seeded "main" is a placeholder, not a choice — the checkout's own
      // default branch replaces it. An existing project's branch is a decision.
      branchAuto = d.isNew;
      if (d.isNew) {
        // Adding a project STARTS at the folder: nothing else on this tab can be
        // derived without it. Cancelling the chooser leaves an empty form, so
        // nothing is forced.
        void pickFolder();
      } else if (d.path.trim()) {
        // Learn what the path is (branch list, checkout status) without filling
        // anything: an untouched form must not come up dirty.
        void inspect(d.path);
      }
    } catch (e) {
      loadErr = String(e);
      store.setFlash(String(e), "bad");
      return;
    }
    try {
      // From config, not the store's push: the group list is config's own and
      // must still be offered when no daemon is running.
      groups = (await ConfigService.Groups()) ?? [];
    } catch {
      // Non-fatal: the field falls back to whatever the project already has.
    }
    try {
      defaults = { ...(await ConfigService.GetSettings()) };
    } catch {
      // Non-fatal: reverting a key still works, it just keeps the shown value.
    }
    try {
      teams = (await LinearService.Teams()) ?? [];
    } catch (e) {
      teamsErr = String(e); // key missing / API down → raw team-id input
    }
    if (f.teamId) void loadMeta(f.teamId);
  });

  /**
   * Team-scoped UUIDs from the old team match nothing, so switching teams
   * clears every dependent ID — the same thing the TUI's applyPick does. The
   * three inheritable label keys are only cleared when this project overrides
   * them; an inherited value belongs to [defaults], not here.
   */
  function onTeam(v: string) {
    if (!f || v === f.teamId) return;
    f.teamId = v;
    f.projectId = "";
    f.cycleId = "";
    f.stateIds = [];
    f.assigneeUserId = "";
    f.onSpawnStateId = "";
    f.onPrStateId = "";
    f.onMergedStateId = "";
    if (!f.inherits.matchLabels) f.matchLabels = [];
    if (!f.inherits.onSentSetLabel) f.onSentSetLabel = "";
    if (!f.inherits.blockedLabelId) f.blockedLabelId = "";
    void loadMeta(v);
  }

  /**
   * Ask the daemon to change the project's id, migrating the runtime state keyed
   * by it (worktree dir, seen file, tmux/session names). Returns true when the
   * save may proceed.
   *
   * A refusal aborts the whole save: the fields below are about to be written
   * against the NEW id, and writing them against the old one instead would be a
   * silently half-applied edit.
   */
  async function renameFirst(to: string): Promise<boolean> {
    try {
      await DaemonService.RenameProject(origName, to);
      origName = to;
      return true;
    } catch (e) {
      // The daemon names the live sessions in its message; surface it verbatim
      // so the human knows what to finish rather than just that it failed.
      saveErr = String(e);
      store.setFlash(String(e), "bad");
      saving = false;
      return false;
    }
  }

  async function save() {
    if (!f || !canSave) return;
    saving = true;
    saveErr = "";
    const id = slug(f.name);
    if (!f.isNew && id !== origName && !(await renameFirst(id))) return;
    const label = f.label.trim();
    const dto: ProjectFormDTO = {
      ...f,
      name: id,
      // A label identical to the id carries nothing; Go drops it too, but doing
      // it here keeps the flash message and the saved file in agreement.
      label: label === id ? "" : label,
      path: f.path.trim(),
      repo: f.repo.trim(),
      defaultBranch: f.defaultBranch.trim(),
      branchPrefix: f.branchPrefix.trim(),
      symlinks: cleanLines(f.symlinks),
      postCreate: cleanLines(f.postCreate),
      devCommands: cleanLines(f.devCommands),
      env: cleanLines(f.env),
      stateIds: cleanLines(f.stateIds),
      matchLabels: cleanLines(f.matchLabels),
      concurrencyCap: Number(f.concurrencyCap) || 0,
      inherits: { ...f.inherits },
    };
    try {
      await ConfigService.SaveProject(dto);
      store.setFlash(
        f.isNew ? `added ${displayName(dto)}` : `saved ${displayName(dto)}`,
        "good",
      );
      nav.closeOverlay();
    } catch (e) {
      saveErr = String(e);
      store.setFlash(String(e), "bad");
      saving = false;
    }
  }

  /**
   * The single close path for the ✕, the backdrop, Escape and the cancel button
   * (see overlayClose): a stray one of those after editing several tabs would
   * drop every edit, so a dirty form routes the close through the confirm dialog
   * instead of closing outright. Save and remove close directly and never reach
   * here, so neither trips the prompt.
   */
  function requestClose() {
    if (!dirty) {
      nav.closeOverlay();
      return;
    }
    confirm.ask({
      title: "Discard changes?",
      body: `Discard your unsaved changes to ${f ? displayName(f) : "this project"}?`,
      confirmLabel: "Discard",
      onConfirm: () => nav.closeOverlay(),
    });
  }

  // Escape closes from App.svelte's global handler; register so it asks this
  // form (running the dirty guard) rather than closing the overlay blindly.
  onMount(() => overlayClose.register(requestClose));
  onDestroy(() => overlayClose.unregister(requestClose));

  async function remove() {
    if (!f) return;
    try {
      // Remove targets the id on disk (origName), not whatever the id field
      // currently holds — a half-typed rename must not delete the wrong project.
      await ConfigService.RemoveProject(origName);
      store.setFlash(`removed ${displayName(f)}`, "warn");
      nav.closeOverlay();
    } catch (e) {
      store.setFlash(String(e), "bad");
      confirmRemove = false;
    }
  }

  const rowCls = "grid grid-cols-[170px_1fr] items-center gap-3";
  const rowTopCls = "grid grid-cols-[170px_1fr] items-start gap-3";
  const labelCls = "label flex items-center gap-1.5 text-faint";
  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder";
  const hintCls = "mt-1 block text-sm text-faint";
</script>

<!--
  A caption plus, for an inheritable key, the chip that flips between
  "inherited from [defaults]" and "overridden here".
-->
{#snippet cap(caption: string, k: InheritKey | null = null)}
  <span class={labelCls}>
    <span>{caption}</span>
    {#if k}
      {@const on = inherited(k)}
      <!-- A status chip you can click, not a full-height control: it sits inside a
           field caption, so it keeps its own smaller geometry rather than
           borrowing a <Button> size that would out-measure the label beside it. -->
      <button
        type="button"
        class="label rounded border px-1 py-px font-normal normal-case transition-colors {on
          ? 'border-edge text-faint hover:border-accent hover:text-accent-ink'
          : 'border-accent/40 text-accent-ink hover:border-accent hover:text-accent-ink'}"
        title={on
          ? "inherited from [defaults] — click to override it for this project"
          : "overridden for this project — click to go back to [defaults]"}
        onclick={() => toggleInherit(k)}>{on ? "Inherited" : "Override"}</button
      >
    {/if}
  </span>
{/snippet}

{#snippet textRow(
  caption: string,
  value: string,
  onChange: (v: string) => void,
  placeholder = "",
  k: InheritKey | null = null,
  readonly = false,
  hint = "",
  onBlur: (() => void) | null = null,
)}
  <div class={rowCls}>
    {@render cap(caption, k)}
    <span>
      <input
        class="{inputCls} font-mono {ghost(k)} {readonly ? 'cursor-not-allowed text-faint' : ''}"
        aria-label={caption}
        {placeholder}
        {readonly}
        {value}
        oninput={(e) => {
          if (k) promote(k);
          onChange(e.currentTarget.value);
        }}
        onblur={() => onBlur?.()}
      />
      {#if hint}<span class={hintCls}>{hint}</span>{/if}
    </span>
  </div>
{/snippet}

{#snippet areaRow(
  caption: string,
  value: string[] | null,
  onChange: (v: string[]) => void,
  placeholder = "",
  hint = "",
  k: InheritKey | null = null,
)}
  <div class={rowTopCls}>
    {@render cap(caption, k)}
    <span>
      <textarea
        class="{inputCls} resize-y font-mono {ghost(k)}"
        aria-label={caption}
        rows="3"
        spellcheck="false"
        {placeholder}
        value={linesToText(value)}
        oninput={(e) => {
          if (k) promote(k);
          onChange(splitLines(e.currentTarget.value));
        }}
      ></textarea>
      {#if hint}<span class={hintCls}>{hint}</span>{/if}
    </span>
  </div>
{/snippet}

<!-- A single-select row. `options` null → raw UUID entry (the fallback). -->
{#snippet idRow(
  caption: string,
  current: string,
  options: LinearOption[] | null,
  onChange: (v: string) => void,
  anyLabel = "",
  k: InheritKey | null = null,
)}
  <div class={rowCls}>
    {@render cap(caption, k)}
    {#if options}
      <Select
        class={ghost(k)}
        aria-label={caption}
        value={current}
        onchange={(e) => {
          if (k) promote(k);
          onChange(e.currentTarget.value);
        }}
      >
        {#if anyLabel}<option value="">{anyLabel}</option>{/if}
        {#each options as o (o.id)}<option value={o.id}>{o.label}</option>{/each}
      </Select>
    {:else}
      <input
        class="{inputCls} font-mono {ghost(k)}"
        aria-label={caption}
        value={current}
        placeholder="UUID"
        oninput={(e) => {
          if (k) promote(k);
          onChange(e.currentTarget.value);
        }}
      />
    {/if}
  </div>
{/snippet}

<!-- A multi-select. `options` null → a newline-per-UUID textarea (fallback). -->
{#snippet multiRow(
  caption: string,
  selected: string[] | null,
  options: LinearOption[] | null,
  onChange: (v: string[]) => void,
  k: InheritKey | null = null,
)}
  {#if options}
    <div class={rowTopCls}>
      {@render cap(caption, k)}
      <div class="max-h-36 space-y-1 overflow-auto rounded border border-edge p-2 {ghost(k)}">
        {#each options as o (o.id)}
          <label class="flex items-center gap-2 text-ink">
            <Checkbox
              checked={(selected ?? []).includes(o.id)}
              onchange={() => {
                if (k) promote(k);
                onChange(toggleId(selected, o.id));
              }}
            />
            <span class="truncate">{o.label}</span>
          </label>
        {/each}
        {#if options.length === 0}<span class="text-sm text-faint">none</span>{/if}
      </div>
    </div>
  {:else}
    {@render areaRow(caption, selected, onChange, "one UUID per line", "", k)}
  {/if}
{/snippet}

{#snippet boolRow(caption: string, checked: boolean, onToggle: () => void, hint = "")}
  <div class={rowCls}>
    <span class={labelCls}>{caption}</span>
    <label class="flex items-center gap-2 text-ink">
      <Checkbox {checked} onchange={onToggle} aria-label={caption} />
      {#if hint}<span class="text-faint">{hint}</span>{/if}
    </label>
  </div>
{/snippet}

<Modal {title} onClose={requestClose} width="660px">
  {#if loadErr}
    <div class="rounded border border-bad/40 bg-bad/10 px-3 py-2 text-sm text-bad">{loadErr}</div>
  {:else if !f}
    <div class="px-3 py-8 text-center text-sm text-faint">loading project…</div>
  {:else}
    {@const d = f}
    <Tabs tabs={TABS} active={tab} onSelect={(id) => (tab = id)} />

    {#if tab === "repo"}
      <div class="space-y-2">
        {@render textRow(
          "Label",
          d.label,
          (v) => {
            d.label = v;
            // While the id still tracks the label, every keystroke re-derives it,
            // so one typed name yields a valid identity for free.
            if (idAuto) d.name = slug(v);
          },
          "Nori App",
          null,
          false,
          "shown everywhere in the app; rename it any time",
        )}
        {@render textRow(
          "ID",
          d.name,
          (v) => {
            // slugTyping, not slug: trimming mid-typing would eat the hyphen the
            // moment it is typed, making "nori-app" impossible to enter.
            d.name = slugTyping(v);
            idAuto = false;
          },
          "nori-app",
          null,
          false,
          renaming
            ? `rename ${origName} → ${slug(d.name)} · needs no live sessions`
            : "path segment + tmux name prefix; changing it is a rename",
        )}
        <!-- Which sidebar folder this project sits in. The sidebar's own way to
             set it is dragging the row; this is the same fact reachable without
             a pointer, and the only way to see it spelled out. -->
        <div class={rowCls}>
          {@render cap("Group", null)}
          <span>
            <Select
              aria-label="Group"
              value={d.group ?? ""}
              onchange={(e) => {
                d.group = e.currentTarget.value;
              }}
            >
              <option value="">No group</option>
              {#each groups as g (g.name)}<option value={g.name}>{g.label || g.name}</option>{/each}
              <!-- A group the list does not carry (config unreadable, or the
                   list is a beat behind a rename) must still be shown, or saving
                   an untouched form would silently move the project out of it. -->
              {#if d.group && !groups.some((g) => g.name === d.group)}
                <option value={d.group}>{d.group}</option>
              {/if}
            </Select>
            <span class={hintCls}>groups are created in the sidebar's + menu</span>
          </span>
        </div>
        <!-- The folder is the FIRST decision and everything below is derived
             from it, so the chooser sits on the row rather than in a menu. The
             field stays typable: a path you already know is never a dead end. -->
        <div class={rowCls}>
          {@render cap("Path", null)}
          <span>
            <span class="flex items-center gap-2">
              <input
                class="{inputCls} min-w-0 flex-1 font-mono"
                aria-label="Path"
                placeholder="/Users/you/code/my-project"
                value={d.path}
                oninput={(e) => { d.path = e.currentTarget.value; }}
                onblur={() => void inspect(d.path, { fill: true })}
              />
              <Button size="sm" variant="secondary" onclick={pickFolder} disabled={picking}>
                {picking ? "Choosing…" : "Choose folder…"}
              </Button>
            </span>
            <span class={hintCls}>
              {#if pathInfo && pathInfo.path === d.path.trim() && !pathInfo.isRepo}
                <span class="text-warn">not a git checkout — worktrees fork from this repository</span>
              {:else if pathInfo?.isRepo}
                the label, id, repo and default branch below came from this checkout
              {:else}
                the repository this project's worktrees fork from
              {/if}
            </span>
          </span>
        </div>
        {@render textRow(
          "Repo",
          d.repo,
          (v) => { d.repo = v; repoAuto = false; },
          "owner/name",
          null,
          false,
          repoAuto
            ? "detected from the checkout — verify it if this is a fork"
            : "for PR checks; empty disables them",
        )}
        <!-- default branch: suggestions from the checkout, still free text -->
        <div class={rowCls}>
          {@render cap("Default branch", null)}
          <span>
            <input
              class="{inputCls} font-mono"
              aria-label="Default branch"
              list="lola-branches"
              placeholder="main"
              value={d.defaultBranch}
              oninput={(e) => { d.defaultBranch = e.currentTarget.value; branchAuto = false; }}
              onfocus={() => void inspect(d.path)}
            />
            <datalist id="lola-branches">
              {#each branches as b (b)}<option value={b}></option>{/each}
            </datalist>
            <span class={hintCls}>
              {branches.length
                ? "branches from the checkout — or type one"
                : "worktrees fork from this branch"}
            </span>
          </span>
        </div>
        {@render textRow("Branch prefix", d.branchPrefix, (v) => { d.branchPrefix = v; }, "lola/", null, false, "empty inherits the [defaults] prefix")}

        <!-- agent: "" already means inherit, so no bitmap entry -->
        <div class={rowCls}>
          <span class={labelCls}>Agent</span>
          <span class="flex w-fit items-center gap-0.5 rounded-md border border-edge p-0.5">
            {#each agents as a (a.id)}
              <Button size="xs" selected={d.agent === a.id} onclick={() => { d.agent = a.id; }}>{a.label}</Button>
            {/each}
          </span>
        </div>

        {@render areaRow(
          "Symlinks",
          d.symlinks,
          (v) => { d.symlinks = v; },
          ".env\nnode_modules",
          "one path per line — linked into each worktree",
          "symlinks",
        )}
        {@render areaRow(
          "Post-create",
          d.postCreate,
          (v) => { d.postCreate = v; },
          "npm install\nmake build",
          "one command per line — run after the worktree is created",
          "postCreate",
        )}
        {@render areaRow(
          "Dev commands",
          d.devCommands,
          (v) => { d.devCommands = v; },
          "composer dev\nnpm run dev",
          "one long-running command per line — only the ACTIVE session runs them (one per project), each in its own terminal tab",
        )}
        {@render areaRow("Env", d.env, (v) => { d.env = v; }, "KEY=value\nAPI_URL=http://localhost", "one KEY=value per line", "env")}
      </div>
    {:else if tab === "filter"}
      <div class="space-y-2">
        {#if teamsErr}
          <p class="mb-3 rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">
            Linear metadata unavailable ({teamsErr}) — paste UUIDs directly below.
          </p>
        {/if}

        {@render boolRow("Enabled", d.enabled, () => { d.enabled = !d.enabled; }, "poll Linear for matching issues")}

        <!-- Team drives every dependent picker. -->
        <div class={rowCls}>
          <span class={labelCls}>Team</span>
          {#if teams.length > 0}
            <Select aria-label="Team" value={d.teamId} onchange={(e) => onTeam(e.currentTarget.value)}>
              <option value="">(pick a team)</option>
              {#each teams as t (t.id)}<option value={t.id}>{t.key} — {t.name}</option>{/each}
            </Select>
          {:else}
            <!-- onchange, not oninput: switching teams clears the dependent IDs,
                 which must not happen on every keystroke of a pasted UUID. -->
            <input
              class="{inputCls} font-mono"
              aria-label="Team"
              value={d.teamId}
              placeholder="team UUID"
              onchange={(e) => onTeam(e.currentTarget.value)}
            />
          {/if}
        </div>

        {#if metaLoading}
          <p class="text-sm text-faint">loading Linear metadata…</p>
        {:else if metaErr}
          <p class="rounded border border-warn/40 bg-warn/10 px-3 py-1.5 text-sm text-warn">
            couldn't load team metadata ({metaErr}) — using raw UUID inputs
          </p>
        {/if}

        {@render idRow("Project", d.projectId, meta?.projects ?? null, (v) => { d.projectId = v; }, "(any project)")}
        {@render idRow(
          "Cycle mode",
          d.cycleMode,
          [
            { id: "none", label: "none" },
            { id: "active", label: "active" },
            { id: "pinned", label: "pinned" },
          ],
          (v) => { d.cycleMode = v; },
        )}
        {#if d.cycleMode === "pinned"}
          {@render idRow("Cycle", d.cycleId, meta?.cycles ?? null, (v) => { d.cycleId = v; }, "(pick a cycle)")}
        {/if}

        {@render multiRow("Workflow states", d.stateIds, meta?.states ?? null, (v) => { d.stateIds = v; })}

        {@render idRow(
          "Assignee",
          d.assigneeMode,
          [
            { id: "anyone", label: "anyone" },
            { id: "me", label: "me" },
            { id: "user", label: "specific user" },
          ],
          (v) => { d.assigneeMode = v; },
        )}
        {#if d.assigneeMode === "user"}
          {@render idRow("Assignee user", d.assigneeUserId, meta?.members ?? null, (v) => { d.assigneeUserId = v; }, "(pick a user)")}
        {/if}

        <div class={rowCls}>
          <span class={labelCls}>Concurrency cap</span>
          <span>
            <input type="number" min="0" class="{inputCls} num w-24" aria-label="Concurrency cap" bind:value={d.concurrencyCap} />
            <span class={hintCls}>0 uses the [defaults] cap</span>
          </span>
        </div>
      </div>
    {:else if tab === "labels"}
      <div class="space-y-2">
        {@render multiRow("Match labels", d.matchLabels, meta?.labels ?? null, (v) => { d.matchLabels = v; }, "matchLabels")}
        {@render idRow(
          "Match mode",
          d.matchMode,
          [
            { id: "any", label: "any label" },
            { id: "all", label: "all labels" },
          ],
          (v) => { d.matchMode = v; },
          "",
          "matchMode",
        )}
        {@render idRow(
          "Dedup mode",
          d.dedupMode,
          [
            { id: "label", label: "label (flip a label on send)" },
            { id: "seen", label: "seen (remember dispatched)" },
            { id: "state", label: "state (Linear workflow state)" },
          ],
          (v) => { d.dedupMode = v; },
          "",
          "dedupMode",
        )}
        {@render idRow(
          "On-sent set label",
          d.onSentSetLabel,
          meta?.labels ?? null,
          (v) => { d.onSentSetLabel = v; },
          "(none)",
          "onSentSetLabel",
        )}
        <p class="copy pt-1 text-sm text-faint">
          Label UUIDs are team-scoped — changing the team on the Filter tab clears the ones this project overrides.
        </p>
      </div>
    {:else}
      <div class="space-y-2">
        {@render idRow("On-spawn state", d.onSpawnStateId, meta?.states ?? null, (v) => { d.onSpawnStateId = v; }, "(none)")}
        {@render boolRow("Comment on spawn", d.commentOnSpawn, () => { d.commentOnSpawn = !d.commentOnSpawn; })}
        {@render idRow("On-PR state", d.onPrStateId, meta?.states ?? null, (v) => { d.onPrStateId = v; }, "(none)")}
        {@render boolRow("PR requires checks", d.prRequiresChecks, () => { d.prRequiresChecks = !d.prRequiresChecks; }, "only after CI passes")}
        {@render boolRow("Comment on PR", d.commentOnPr, () => { d.commentOnPr = !d.commentOnPr; })}
        {@render idRow("On-merged state", d.onMergedStateId, meta?.states ?? null, (v) => { d.onMergedStateId = v; }, "(none)")}
        {@render boolRow("Comment on merged", d.commentOnMerged, () => { d.commentOnMerged = !d.commentOnMerged; })}
        {@render idRow("Blocked label", d.blockedLabelId, meta?.labels ?? null, (v) => { d.blockedLabelId = v; }, "(none)", "blockedLabelId")}
        {@render boolRow("Comment on blocked", d.commentOnBlocked, () => { d.commentOnBlocked = !d.commentOnBlocked; })}
      </div>
    {/if}
  {/if}

  <!-- The save error, inline and above the footer where it can't hide behind the
       backdrop. A Go error can be long and multi-line, so it wraps rather than
       truncating and stays selectable; dismissable, and cleared on the next save. -->
  {#if saveErr}
    <div class="mt-3 flex items-start gap-2 rounded border border-bad/40 bg-bad/10 px-3 py-2 text-sm text-bad">
      <span class="min-w-0 flex-1 font-mono break-words whitespace-pre-wrap select-text">{saveErr}</span>
      <Button variant="danger" size="xs" icon aria-label="dismiss error" onclick={() => (saveErr = "")}>✕</Button>
    </div>
  {/if}

  {#snippet footer()}
    <div class="flex items-center gap-2">
      {#if f && !f.isNew}
        {#if confirmRemove}
          <Button variant="danger-solid" size="md" onclick={remove}>Remove project</Button>
          <Button size="md" onclick={() => (confirmRemove = false)}>Cancel</Button>
        {:else}
          <Button variant="danger" size="md" onclick={() => (confirmRemove = true)}>Remove</Button>
        {/if}
      {/if}
      <Button size="md" class="ml-auto" onclick={requestClose}>Cancel</Button>
      <Button variant="primary" size="md" disabled={!canSave} onclick={save}>{saving ? "Saving…" : "Save"}</Button>
    </div>
  {/snippet}
</Modal>
