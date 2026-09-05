<script lang="ts">
  import { onMount, onDestroy, type Snippet } from "svelte";
  import { linesToText, splitLines, cleanLines } from "$lib/lines";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { overlayClose } from "$lib/overlayClose";
  import { deepEqual } from "$lib/deepEqual";
  import Modal from "$lib/components/Modal.svelte";
  import Tabs from "$lib/components/Tabs.svelte";
  import HelpText from "$lib/components/HelpText.svelte";
  import Button from "$lib/components/Button.svelte";
  import CheckboxOptions from "$lib/components/CheckboxOptions.svelte";
  import { PICKUP_FIELDS, LABEL_MATCHING, REPEAT_PICKUP, ENTRY_FORMAT } from "$lib/settingsFields";
  import Checkbox from "$lib/components/Checkbox.svelte";
  import AgentModelFields from "$lib/components/AgentModelFields.svelte";
  import PresetInput from "$lib/components/PresetInput.svelte";
  import { BRANCH_PREFIXES } from "$lib/settingPresets";
  import Select from "$lib/components/Select.svelte";
  import { ConfigService, DaemonService, LinearService } from "@bindings/desktop";
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
    { id: "repo", label: "General" },
    { id: "worktree", label: "Worktree setup" },
    { id: "filter", label: "Issue pickup" },
    { id: "writeback", label: "Linear updates" },
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
  let customCap = $state(false);
  // A save that the daemon rejects used to surface only as a footer flash — behind
  // this backdrop, truncated, gone in 4s — so it read like a save that just didn't
  // close. Held here and shown inline instead; cleared on the next attempt.
  let saveErr = $state("");
  let confirmRemove = $state(false);
  let tab = $state(nav.overlayTab === "labels" ? "filter" : TABS.some((t) => t.id === nav.overlayTab) ? nav.overlayTab : "repo");

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
  // custom option keeps the input free text, so a path that is not a checkout — or a
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


  const title = $derived(
    f ? (f.isNew ? "add project" : `project: ${displayName(f)}`) : nav.overlayProject === "" ? "add project" : `project: ${nav.overlayProject}`,
  );
  // Saving needs a usable ID, not merely non-empty text: an all-non-ASCII label
  // slugs to "" and there is no id to write.
  const canSave = $derived(!!f && !saving && slug(f.name) !== ""
    && (!customCap || (Number.isInteger(f.concurrencyCap) && f.concurrencyCap > 0)));
  const renaming = $derived(!!f && !f.isNew && slug(f.name) !== origName);



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
  // Drafts are presentation state, never persisted. Comparing with defaults
  // must not erase commands or environment variables the user just entered.
  const overrideDrafts = new Map<InheritKey, string | string[] | null>();
  function promote(k: InheritKey, restoreDraft = false) {
    if (!f || !f.inherits[k]) return;
    if (restoreDraft && overrideDrafts.has(k)) {
      const draft = overrideDrafts.get(k)!;
      Object.assign(f, { [k]: Array.isArray(draft) ? [...draft] : draft });
    }
    f.inherits[k] = false;
  }
  /**
   * Hand the key back to [defaults] AND refill the control from them, so the
   * displayed value is the one that will actually apply.
   */
  function revert(k: InheritKey) {
    if (!f) return;
    if (k !== "prioritySort" && !f.inherits[k]) {
      const value = f[k];
      overrideDrafts.set(k, Array.isArray(value) ? [...value] : value);
    }
    f.inherits[k] = true;
    const d = defaults;
    if (!d) return; // settings unreadable — keep the resolved value visible
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
    if (inherited(k)) promote(k, true);
    else revert(k);
  }
  function inheritedStyle(k: InheritKey | null): string {
    return k && inherited(k) ? "border-dashed" : "";
  }

  let metaRequest = 0;
  let teamsLoading = $state(false);
  async function loadTeams() {
    if (teamsLoading) return;
    teamsLoading = true;
    teamsErr = "";
    try {
      teams = (await LinearService.Teams()) ?? [];
    } catch (e) {
      teamsErr = String(e);
    } finally {
      teamsLoading = false;
    }
  }

  async function loadMeta(teamId: string) {
    const request = ++metaRequest;
    meta = null;
    metaErr = "";
    metaLoading = !!teamId;
    if (!teamId) return;
    try {
      const result = await LinearService.TeamMeta(teamId, false);
      if (request === metaRequest && f?.teamId === teamId) meta = result;
    } catch (e) {
      if (request === metaRequest && f?.teamId === teamId) metaErr = String(e);
    } finally {
      if (request === metaRequest) metaLoading = false;
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
      customCap = d.concurrencyCap > 0;
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
      defaults = { ...(await ConfigService.GetSettings()) };
    } catch {
      // Non-fatal: reverting a key still works, it just keeps the shown value.
    }
    void loadTeams();
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
    // Team-specific drafts cannot be restored into another team's picker.
    for (const key of ["matchLabels", "onSentSetLabel", "blockedLabelId"] as const) overrideDrafts.delete(key);
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

  const formId = $props.id();
  const rowCls = "project-field grid min-w-0 grid-cols-[170px_minmax(0,1fr)] items-baseline gap-3";
  const rowTopCls = "project-field grid min-w-0 grid-cols-[170px_minmax(0,1fr)] items-start gap-3";
  const labelCls = "label flex items-center gap-1.5 text-faint";
  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder";
  const hintCls = "mt-1 block text-sm text-faint";
</script>

{#snippet section(title: string, children: Snippet)}
  <section aria-label={title} class="space-y-3 border-b border-edge pb-5 last:border-b-0 last:pb-0">
    <h3 class="text-lg text-ink">{title}</h3>
    {@render children()}
  </section>
{/snippet}

<!--
  Keep help with the caption and inheritance below it. State and action
  have distinct labels, so reading a badge never implies a reset.
-->
{#snippet cap(caption: string, k: InheritKey | null = null, hint = "", controlId: string | null = null)}
  <span class="flex min-w-0 flex-col items-start gap-1.5">
    <span class={labelCls}>
      {#if controlId}<label for={controlId}>{caption}</label>{:else}<span>{caption}</span>{/if}
      {#if hint}<HelpText label={caption} detail={hint} />{/if}
    </span>
    {#if k}
      {@const on = inherited(k)}
      <span class="flex flex-wrap items-center gap-x-1.5 gap-y-1 text-sm">
        <span class="rounded border border-edge px-1 py-px text-faint">{on ? "Default" : "Project override"}</span>
        <Button size="xs" aria-label={`${on ? "Customize" : "Use default for"} ${caption}`}
          onclick={() => toggleInherit(k)}>{on ? "Customize" : "Use default"}</Button>
      </span>
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
    {@render cap(caption, k, hint, `${formId}-${caption}`)}
    <span>
      <input
        id={`${formId}-${caption}`}
        spellcheck="false"
        class="{inputCls} font-mono {inheritedStyle(k)} {readonly ? 'cursor-not-allowed text-faint' : ''}"
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
    {@render cap(caption, k, hint, `${formId}-${caption}`)}
    <span>
      <textarea
        id={`${formId}-${caption}`}
        class="{inputCls} block resize-y font-mono {inheritedStyle(k)}"
        aria-label={caption}
        aria-describedby={ENTRY_FORMAT[caption] ? `${formId}-${caption}-format` : undefined}
        rows="3"
        spellcheck="false"
        {placeholder}
        value={linesToText(value)}
        oninput={(e) => {
          if (k) promote(k);
          onChange(splitLines(e.currentTarget.value));
        }}
      ></textarea>
      {#if ENTRY_FORMAT[caption]}<span id={`${formId}-${caption}-format`} class={hintCls}>{ENTRY_FORMAT[caption]}</span>{/if}
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
        class={inheritedStyle(k)}
        aria-label={caption}
        value={current}
        onchange={(e) => {
          if (k) promote(k);
          onChange(e.currentTarget.value);
        }}
      >
        {#if anyLabel}<option value="">{anyLabel}</option>{/if}
        {#if current && !options.some((o) => o.id === current)}<option value={current}>{current} (current value)</option>{/if}
        {#each options as o (o.id)}<option value={o.id}>{o.label}</option>{/each}
      </Select>
    {:else}
      <input
        class="{inputCls} font-mono {inheritedStyle(k)}"
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
      <CheckboxOptions label={caption} {options} {selected} onChange={(value) => {
        if (k) promote(k);
        onChange(value);
      }} />
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
      <div class="space-y-5">
        {#snippet repository()}
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
                  Detected from checkout.
                {:else}
                  Source checkout.
                {/if}
              </span>
            </span>
          </div>
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
          <!-- Branch options come from the checkout; custom values are preserved. -->
          <div class={rowCls}>
            {@render cap("Default branch", null)}
            <span>
              <PresetInput label="Default branch" value={d.defaultBranch}
                options={branches.map((value) => ({ value, label: value }))}
                onChange={(v) => { d.defaultBranch = v; branchAuto = false; }}
                onFocus={() => void inspect(d.path)} placeholder="main" />
              <span class={hintCls}>
                {branches.length
                  ? "Choose a branch or Custom."
                  : "Base for new worktrees."}
              </span>
            </span>
          </div>
          <AgentModelFields provider={d.agent} providerLabel="Agent" defaultProvider={defaults?.agent ?? "claude"}
            rowClass={rowCls} labelClass={labelCls} onProviderChange={(value) => { d.agent = value; }} />

        {/snippet}
        {@render section("Repository and agent", repository)}
        <details class="pt-1">
          <summary class="cursor-pointer text-faint">Advanced identity</summary>
          <div class="mt-3">
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
          </div>
        </details>
      </div>
    {:else if tab === "worktree"}
      <div class="space-y-5">
        {#snippet preparation()}
          <div class={rowCls}>
            <span class={labelCls}>Branch prefix</span>
            <PresetInput label="Branch prefix" value={d.branchPrefix} options={BRANCH_PREFIXES}
              onChange={(v) => { d.branchPrefix = v; }} placeholder="lola/" />
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
          {@render areaRow("Env", d.env, (v) => { d.env = v; }, "KEY=value\nAPI_URL=http://localhost", "one KEY=value per line", "env")}
        {/snippet}
        {@render section("Worktree preparation", preparation)}
        {#snippet testing()}
          {@render areaRow(
            "Dev commands",
            d.devCommands,
            (v) => { d.devCommands = v; },
            "composer dev\nnpm run dev",
            "one long-running command per line — only the ACTIVE session runs them (one per project), each in its own terminal tab",
          )}
        {/snippet}
        {@render section("Local testing", testing)}
      </div>
    {:else if tab === "filter"}
      <div class="space-y-5">
        {#if teamsErr}
          <div role="alert" class="rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">
            <p>Couldn’t load Linear teams. Retry or enter team IDs manually below.</p>
            <HelpText label="team loading error" detail={teamsErr} />
            <Button size="sm" loading={teamsLoading} onclick={loadTeams}>Retry teams</Button>
          </div>
        {/if}

        {#snippet pickup()}
          {@render boolRow("Enabled", d.enabled, () => { d.enabled = !d.enabled; }, "poll Linear for matching issues")}

          <div class={rowCls}>
            <span class={labelCls}>Concurrency cap</span>
            <div class="space-y-2">
              <Select aria-label="Concurrency" value={customCap ? "custom" : "default"}
                onchange={(e) => {
                  customCap = e.currentTarget.value === "custom";
                  d.concurrencyCap = customCap ? Math.max(1, defaults?.concurrencyCap ?? 1) : 0;
                }}>
                <option value="default">Use default{defaults?.concurrencyCap ? ` (${defaults.concurrencyCap})` : ""}</option>
                <option value="custom">Set project limit</option>
              </Select>
              {#if customCap}
                <input type="number" min="1" step="1" class="{inputCls} num" aria-label="Concurrency cap" aria-invalid={!Number.isInteger(d.concurrencyCap) || !(d.concurrencyCap > 0)} aria-describedby={!Number.isInteger(d.concurrencyCap) || !(d.concurrencyCap > 0) ? `${formId}-cap-error` : undefined} bind:value={d.concurrencyCap} />
                {#if !Number.isInteger(d.concurrencyCap) || !(d.concurrencyCap > 0)}
                  <span id={`${formId}-cap-error`} role="alert" class="text-sm text-warn">Enter a whole number of at least 1.</span>
                {/if}
              {/if}
            </div>
          </div>
        {/snippet}
        {@render section("Automatic pickup", pickup)}
        {#snippet filters()}
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
            <p role="status" class="text-sm text-faint">Loading team options…</p>
          {:else if metaErr}
            <div role="alert" class="rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">
              <p>Couldn’t load team options. Retry or enter IDs manually below.</p>
              <HelpText label="team options error" detail={metaErr} />
              <Button size="sm" onclick={() => void loadMeta(d.teamId)}>Retry team options</Button>
            </div>
          {/if}

          {@render idRow("Project", d.projectId, meta?.projects ?? null, (v) => { d.projectId = v; }, "(any project)")}
          {@render idRow(
            "Cycle mode",
            d.cycleMode,
            [
              { id: "none", label: "All cycles" },
              { id: "active", label: "Active cycle" },
              { id: "pinned", label: "Choose a cycle" },
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
              { id: "anyone", label: "Anyone" },
              { id: "me", label: "Me" },
              { id: "user", label: "Choose a person" },
            ],
            (v) => { d.assigneeMode = v; },
          )}
          {#if d.assigneeMode === "user"}
            {@render idRow("Assignee user", d.assigneeUserId, meta?.members ?? null, (v) => { d.assigneeUserId = v; }, "(pick a user)")}
          {/if}

        {/snippet}
        {@render section("Issue filters", filters)}
        {#snippet labels()}
          {@render multiRow("Match labels", d.matchLabels, meta?.labels ?? null, (v) => { d.matchLabels = v; }, "matchLabels")}
          {@render idRow(
            PICKUP_FIELDS.matchMode,
            d.matchMode,
            LABEL_MATCHING,
            (v) => { d.matchMode = v; },
            "",
            "matchMode",
          )}
          {@render idRow(
            PICKUP_FIELDS.dedupMode,
            d.dedupMode,
            REPEAT_PICKUP,
            (v) => { d.dedupMode = v; },
            "",
            "dedupMode",
          )}
          {#if d.dedupMode === "label"}
          {@render idRow(
            PICKUP_FIELDS.onSentSetLabel,
            d.onSentSetLabel,
            meta?.labels ?? null,
            (v) => { d.onSentSetLabel = v; },
            "(none)",
            "onSentSetLabel",
          )}
          {/if}
          <HelpText label="team labels" summary="Labels follow the team." detail="Changing the team clears this project’s label overrides. Inherited workspace labels stay unchanged." />
        {/snippet}
        {@render section("Labels and repeat pickup", labels)}
      </div>
    {:else if tab === "writeback"}
      <p class="copy mb-4 text-sm text-faint">Optional updates. Empty leaves states unchanged.</p>
      <div class="space-y-5">
        {#snippet spawned()}
          {@render idRow("On-spawn state", d.onSpawnStateId, meta?.states ?? null, (v) => { d.onSpawnStateId = v; }, "(none)")}
          {@render boolRow("Comment on spawn", d.commentOnSpawn, () => { d.commentOnSpawn = !d.commentOnSpawn; })}
        {/snippet}
        {@render section("Session starts", spawned)}
        {#snippet pullRequest()}
          {@render idRow("On-PR state", d.onPrStateId, meta?.states ?? null, (v) => { d.onPrStateId = v; }, "(none)")}
          {@render boolRow("PR requires checks", d.prRequiresChecks, () => { d.prRequiresChecks = !d.prRequiresChecks; }, "only after CI passes")}
          {@render boolRow("Comment on PR", d.commentOnPr, () => { d.commentOnPr = !d.commentOnPr; })}
        {/snippet}
        {@render section("Pull request opens", pullRequest)}
        {#snippet merged()}
          {@render idRow("On-merged state", d.onMergedStateId, meta?.states ?? null, (v) => { d.onMergedStateId = v; }, "(none)")}
          {@render boolRow("Comment on merged", d.commentOnMerged, () => { d.commentOnMerged = !d.commentOnMerged; })}
        {/snippet}
        {@render section("Pull request merges", merged)}
        {#snippet blocked()}
          {@render idRow("Blocked label", d.blockedLabelId, meta?.labels ?? null, (v) => { d.blockedLabelId = v; }, "(none)", "blockedLabelId")}
          {@render boolRow("Comment on blocked", d.commentOnBlocked, () => { d.commentOnBlocked = !d.commentOnBlocked; })}
        {/snippet}
        {@render section("Session is blocked", blocked)}
      </div>
    {/if}
  {/if}

  <!-- The save error, inline and above the footer where it can't hide behind the
       backdrop. A Go error can be long and multi-line, so it wraps rather than
       truncating and stays selectable; dismissable, and cleared on the next save. -->
  {#if saveErr}
    <div role="alert" class="mt-3 flex items-start gap-2 rounded border border-bad/40 bg-bad/10 px-3 py-2 text-sm text-bad">
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

<style>
  @media (max-width: 540px) {
    :global(.project-field) { grid-template-columns: minmax(0, 1fr); gap: 0.375rem; }
  }
</style>
