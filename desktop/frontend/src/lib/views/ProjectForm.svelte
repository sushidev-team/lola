<script lang="ts">
  import { onMount } from "svelte";
  import { linesToText, splitLines, cleanLines } from "$lib/lines";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import Modal from "$lib/components/Modal.svelte";
  import Tabs from "$lib/components/Tabs.svelte";
  import { ConfigService, LinearService } from "@bindings/desktop";
  import type {
    ProjectFormDTO,
    InheritsDTO,
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
  let confirmRemove = $state(false);
  let tab = $state(nav.overlayTab || "repo");

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
    f ? (f.isNew ? "add project" : `project: ${f.name}`) : nav.overlayProject === "" ? "add project" : `project: ${nav.overlayProject}`,
  );
  const canSave = $derived(!!f && !saving && f.name.trim().length > 0);

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
      f = { ...d, inherits: inheritsOf(d.inherits) };
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

  async function save() {
    if (!f || !canSave) return;
    saving = true;
    const dto: ProjectFormDTO = {
      ...f,
      name: f.name.trim(),
      path: f.path.trim(),
      repo: f.repo.trim(),
      defaultBranch: f.defaultBranch.trim(),
      branchPrefix: f.branchPrefix.trim(),
      symlinks: cleanLines(f.symlinks),
      postCreate: cleanLines(f.postCreate),
      env: cleanLines(f.env),
      stateIds: cleanLines(f.stateIds),
      matchLabels: cleanLines(f.matchLabels),
      concurrencyCap: Number(f.concurrencyCap) || 0,
      inherits: { ...f.inherits },
    };
    try {
      await ConfigService.SaveProject(dto);
      store.setFlash(f.isNew ? `added ${dto.name}` : `saved ${dto.name}`, "good");
      nav.closeOverlay();
    } catch (e) {
      store.setFlash(String(e), "bad");
      saving = false;
    }
  }

  async function remove() {
    if (!f) return;
    try {
      await ConfigService.RemoveProject(f.name);
      store.setFlash(`removed ${f.name}`, "warn");
      nav.closeOverlay();
    } catch (e) {
      store.setFlash(String(e), "bad");
      confirmRemove = false;
    }
  }

  const rowCls = "grid grid-cols-[170px_1fr] items-center gap-3";
  const rowTopCls = "grid grid-cols-[170px_1fr] items-start gap-3";
  const labelCls = "flex items-center gap-1.5 text-[11px] tracking-wide text-faint uppercase";
  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent placeholder:text-faint/50";
  const cbCls = "h-3.5 w-3.5 accent-[var(--color-accent)]";
  const hintCls = "mt-1 block text-[10px] text-faint";
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
      <button
        type="button"
        class="rounded border px-1 py-px text-[9px] tracking-wide normal-case {on
          ? 'border-edge text-faint hover:border-accent hover:text-accent'
          : 'border-accent/40 text-accent/80 hover:border-accent hover:text-accent'}"
        title={on
          ? "inherited from [defaults] — click to override it for this project"
          : "overridden for this project — click to go back to [defaults]"}
        onclick={() => toggleInherit(k)}>{on ? "inherited" : "override"}</button
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
      <select
        class="{inputCls} {ghost(k)}"
        aria-label={caption}
        value={current}
        onchange={(e) => {
          if (k) promote(k);
          onChange(e.currentTarget.value);
        }}
      >
        {#if anyLabel}<option value="">{anyLabel}</option>{/if}
        {#each options as o (o.id)}<option value={o.id}>{o.label}</option>{/each}
      </select>
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
          <label class="flex items-center gap-2 text-xs text-ink">
            <input
              type="checkbox"
              class={cbCls}
              checked={(selected ?? []).includes(o.id)}
              onchange={() => {
                if (k) promote(k);
                onChange(toggleId(selected, o.id));
              }}
            />
            <span class="truncate">{o.label}</span>
          </label>
        {/each}
        {#if options.length === 0}<span class="text-[11px] text-faint">none</span>{/if}
      </div>
    </div>
  {:else}
    {@render areaRow(caption, selected, onChange, "one UUID per line", "", k)}
  {/if}
{/snippet}

{#snippet boolRow(caption: string, checked: boolean, onToggle: () => void, hint = "")}
  <div class={rowCls}>
    <span class={labelCls}>{caption}</span>
    <label class="flex items-center gap-2 text-xs text-ink">
      <input type="checkbox" class={cbCls} {checked} onchange={onToggle} aria-label={caption} />
      {#if hint}<span class="text-faint">{hint}</span>{/if}
    </label>
  </div>
{/snippet}

<Modal {title} onClose={() => nav.closeOverlay()} width="660px">
  {#if loadErr}
    <div class="rounded border border-bad/40 bg-bad/10 px-3 py-2 text-xs text-bad">{loadErr}</div>
  {:else if !f}
    <div class="px-3 py-8 text-center text-xs text-faint">loading project…</div>
  {:else}
    {@const d = f}
    <Tabs tabs={TABS} active={tab} onSelect={(id) => (tab = id)} />

    {#if tab === "repo"}
      <div class="space-y-2">
        {@render textRow(
          "Name",
          d.name,
          (v) => { d.name = v; },
          "my-project",
          null,
          !d.isNew,
          d.isNew ? "" : "the project name is the config key and can't be renamed here",
        )}
        {@render textRow("Path", d.path, (v) => { d.path = v; }, "/Users/you/code/my-project")}
        {@render textRow("Repo", d.repo, (v) => { d.repo = v; }, "owner/name")}
        {@render textRow("Default branch", d.defaultBranch, (v) => { d.defaultBranch = v; }, "main")}
        {@render textRow("Branch prefix", d.branchPrefix, (v) => { d.branchPrefix = v; }, "lola/", null, false, "empty inherits the [defaults] prefix")}

        <!-- agent: "" already means inherit, so no bitmap entry -->
        <div class={rowCls}>
          <span class={labelCls}>Agent</span>
          <span class="flex w-fit items-center gap-0.5 rounded border border-edge p-0.5">
            {#each agents as a (a.id)}
              <button
                type="button"
                class="rounded px-2 py-[2px] text-[11px]"
                class:bg-accent={d.agent === a.id}
                class:text-canvas={d.agent === a.id}
                class:text-faint={d.agent !== a.id}
                onclick={() => { d.agent = a.id; }}>{a.label}</button
              >
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
        {@render areaRow("Env", d.env, (v) => { d.env = v; }, "KEY=value\nAPI_URL=http://localhost", "one KEY=value per line", "env")}
      </div>
    {:else if tab === "filter"}
      <div class="space-y-2">
        {#if teamsErr}
          <p class="mb-3 rounded border border-warn/40 bg-warn/10 px-3 py-2 text-[11px] text-warn">
            Linear metadata unavailable ({teamsErr}) — paste UUIDs directly below.
          </p>
        {/if}

        {@render boolRow("Enabled", d.enabled, () => { d.enabled = !d.enabled; }, "poll Linear for matching issues")}

        <!-- Team drives every dependent picker. -->
        <div class={rowCls}>
          <span class={labelCls}>Team</span>
          {#if teams.length > 0}
            <select class={inputCls} aria-label="Team" value={d.teamId} onchange={(e) => onTeam(e.currentTarget.value)}>
              <option value="">(pick a team)</option>
              {#each teams as t (t.id)}<option value={t.id}>{t.key} — {t.name}</option>{/each}
            </select>
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
          <p class="text-[11px] text-faint">loading Linear metadata…</p>
        {:else if metaErr}
          <p class="rounded border border-warn/40 bg-warn/10 px-3 py-1.5 text-[11px] text-warn">
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
            <input type="number" min="0" class="{inputCls} w-24 tabular-nums" aria-label="Concurrency cap" bind:value={d.concurrencyCap} />
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
        <p class="pt-1 text-[10px] text-faint">
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

  {#snippet footer()}
    <div class="flex items-center gap-2">
      {#if f && !f.isNew}
        {#if confirmRemove}
          <button class="rounded bg-bad/20 px-3 py-1 text-xs text-bad hover:bg-bad/30" onclick={remove}>confirm remove</button>
          <button class="px-2 py-1 text-xs text-faint hover:text-ink" onclick={() => (confirmRemove = false)}>cancel</button>
        {:else}
          <button class="px-3 py-1 text-xs text-bad/80 hover:text-bad" onclick={() => (confirmRemove = true)}>remove</button>
        {/if}
      {/if}
      <button class="ml-auto px-3 py-1 text-xs text-faint hover:text-ink" onclick={() => nav.closeOverlay()}>cancel</button>
      <button
        class="rounded bg-accent/20 px-3 py-1 text-xs text-accent hover:bg-accent/30 disabled:opacity-40"
        disabled={!canSave}
        onclick={save}>{saving ? "saving…" : "save"}</button
      >
    </div>
  {/snippet}
</Modal>
