<script lang="ts">
  import { onMount } from "svelte";
  import { ConfigService, LinearService } from "@bindings/desktop";
  import type { PollFormDTO, LinearTeam, LinearTeamMeta, LinearOption } from "@bindings/desktop";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import Modal from "$lib/components/Modal.svelte";

  let dto = $state<PollFormDTO | null>(null);
  let loadErr = $state("");
  let saving = $state(false);

  // Linear metadata drives the cascading pickers. When it can't load (no key,
  // API error), the ID fields fall back to raw UUID inputs so the form still
  // works — options=null means "render a text input".
  let teams = $state<LinearTeam[]>([]);
  let teamsErr = $state("");
  let meta = $state<LinearTeamMeta | null>(null);
  let metaLoading = $state(false);
  let metaErr = $state("");

  const rowCls = "grid grid-cols-[168px_1fr] items-center gap-3";
  const labelCls = "text-[11px] text-faint";
  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent";
  const cbCls = "h-3.5 w-3.5 accent-[var(--color-accent)]";

  // Go nil slices arrive as null over the bridge, so these all tolerate null.
  function splitLines(v: string): string[] {
    return v.split("\n");
  }
  function joinLines(a: string[] | null): string {
    return (a ?? []).join("\n");
  }
  function cleanArr(a: string[] | null): string[] {
    return (a ?? []).map((s) => s.trim()).filter(Boolean);
  }
  function toggle(arr: string[] | null, id: string): string[] {
    const a = arr ?? [];
    return a.includes(id) ? a.filter((x) => x !== id) : [...a, id];
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
      dto = await ConfigService.GetPoll(nav.overlayProject);
    } catch (e) {
      loadErr = String(e);
      store.setFlash(String(e), "bad");
      return;
    }
    try {
      teams = (await LinearService.Teams()) ?? [];
    } catch (e) {
      teamsErr = String(e); // key missing / API down → raw team-id input
    }
    if (dto.teamId) void loadMeta(dto.teamId);
  });

  function onTeam(v: string) {
    if (!dto) return;
    dto.teamId = v;
    void loadMeta(v);
  }

  async function save() {
    if (!dto) return;
    saving = true;
    try {
      await ConfigService.SavePoll({
        ...dto,
        stateIds: cleanArr(dto.stateIds),
        matchLabels: cleanArr(dto.matchLabels),
        concurrencyCap: Number(dto.concurrencyCap) || 0,
      });
      store.setFlash(`saved poll: ${dto.project}`, "good");
      nav.closeOverlay();
    } catch (e) {
      store.setFlash(String(e), "bad");
    } finally {
      saving = false;
    }
  }
</script>

{#snippet sectionHead(t: string)}
  <h3 class="mt-1 mb-2 border-b border-edge/40 pb-1 text-[10px] font-semibold tracking-wider text-faint uppercase">{t}</h3>
{/snippet}

<!-- A single-select row. `options` null → a raw UUID text input (fallback). -->
{#snippet idRow(caption: string, current: string, options: LinearOption[] | null, onChange: (v: string) => void, anyLabel = "")}
  <label class={rowCls}>
    <span class={labelCls}>{caption}</span>
    {#if options}
      <select class={inputCls} value={current} onchange={(e) => onChange(e.currentTarget.value)}>
        {#if anyLabel}<option value="">{anyLabel}</option>{/if}
        {#each options as o (o.id)}<option value={o.id}>{o.label}</option>{/each}
      </select>
    {:else}
      <input class={inputCls} value={current} oninput={(e) => onChange(e.currentTarget.value)} placeholder="UUID" />
    {/if}
  </label>
{/snippet}

<!-- A multi-select. `options` null → a newline-per-UUID textarea (fallback). -->
{#snippet multiRow(caption: string, selected: string[] | null, options: LinearOption[] | null, onToggle: (id: string) => void, onRaw: (v: string) => void)}
  <div class="grid grid-cols-[168px_1fr] items-start gap-3">
    <span class="{labelCls} pt-1.5">{caption}</span>
    {#if options}
      <div class="max-h-36 space-y-1 overflow-auto rounded border border-edge p-2">
        {#each options as o (o.id)}
          <label class="flex items-center gap-2 text-xs text-ink">
            <input type="checkbox" class={cbCls} checked={(selected ?? []).includes(o.id)} onchange={() => onToggle(o.id)} />
            <span class="truncate">{o.label}</span>
          </label>
        {/each}
        {#if options.length === 0}<span class="text-[11px] text-faint">none</span>{/if}
      </div>
    {:else}
      <textarea
        class="{inputCls} font-mono text-[11px]"
        rows="3"
        placeholder="one UUID per line"
        value={joinLines(selected)}
        oninput={(e) => onRaw(e.currentTarget.value)}
      ></textarea>
    {/if}
  </div>
{/snippet}

{#snippet boolRow(caption: string, checked: boolean, onToggle: () => void, hint = "")}
  <div class={rowCls}>
    <span class={labelCls}>{caption}</span>
    <label class="flex items-center gap-2 text-xs text-ink">
      <input type="checkbox" class={cbCls} {checked} onchange={onToggle} />
      {#if hint}<span class="text-faint">{hint}</span>{/if}
    </label>
  </div>
{/snippet}

<Modal title={"polls: " + nav.overlayProject} onClose={() => nav.closeOverlay()} width="640px">
  {#if loadErr}
    <div class="rounded border border-bad/40 bg-bad/10 px-3 py-2 text-xs text-bad">{loadErr}</div>
  {:else if !dto}
    <div class="px-3 py-8 text-center text-faint">loading…</div>
  {:else}
    {@const d = dto}
    {#if teamsErr}
      <p class="mb-3 rounded border border-warn/40 bg-warn/10 px-3 py-2 text-[11px] text-warn">
        Linear metadata unavailable ({teamsErr}) — paste UUIDs directly below.
      </p>
    {/if}

    <div class="space-y-4">
      <section class="space-y-2">
        {@render sectionHead("Filter")}

        {@render boolRow("Enabled", d.enabled, () => (d.enabled = !d.enabled), "poll actively for matching issues")}

        <!-- Team drives every dependent picker. -->
        <label class={rowCls}>
          <span class={labelCls}>Team</span>
          {#if teams.length > 0}
            <select class={inputCls} value={d.teamId} onchange={(e) => onTeam(e.currentTarget.value)}>
              <option value="">(pick a team)</option>
              {#each teams as t (t.id)}<option value={t.id}>{t.key} — {t.name}</option>{/each}
            </select>
          {:else}
            <input class={inputCls} value={d.teamId} oninput={(e) => onTeam(e.currentTarget.value)} placeholder="team UUID" />
          {/if}
        </label>

        {#if metaLoading}
          <p class="text-[11px] text-faint">loading Linear metadata…</p>
        {:else if metaErr}
          <p class="rounded border border-warn/40 bg-warn/10 px-3 py-1.5 text-[11px] text-warn">
            couldn't load team metadata ({metaErr}) — using raw UUID inputs
          </p>
        {/if}

        {@render idRow("Project", d.projectId, meta?.projects ?? null, (v) => (d.projectId = v), "(any project)")}

        {@render idRow("Cycle mode", d.cycleMode, [
          { id: "none", label: "none" },
          { id: "active", label: "active" },
          { id: "pinned", label: "pinned" },
        ], (v) => (d.cycleMode = v))}
        {#if d.cycleMode === "pinned"}
          {@render idRow("Cycle", d.cycleId, meta?.cycles ?? null, (v) => (d.cycleId = v), "(pick a cycle)")}
        {/if}

        {@render multiRow("Workflow states", d.stateIds, meta?.states ?? null, (id) => { d.stateIds = toggle(d.stateIds, id); }, (v) => { d.stateIds = splitLines(v); })}
        {@render multiRow("Match labels", d.matchLabels, meta?.labels ?? null, (id) => { d.matchLabels = toggle(d.matchLabels, id); }, (v) => { d.matchLabels = splitLines(v); })}

        {@render idRow("Match mode", d.matchMode, [
          { id: "any", label: "any label" },
          { id: "all", label: "all labels" },
        ], (v) => (d.matchMode = v))}

        {@render idRow("Assignee", d.assigneeMode, [
          { id: "anyone", label: "anyone" },
          { id: "me", label: "me" },
          { id: "user", label: "specific user" },
        ], (v) => (d.assigneeMode = v))}
        {#if d.assigneeMode === "user"}
          {@render idRow("Assignee user", d.assigneeUserId, meta?.members ?? null, (v) => (d.assigneeUserId = v), "(pick a user)")}
        {/if}

        <label class={rowCls}>
          <span class={labelCls}>Concurrency cap</span>
          <input type="number" min="0" class="{inputCls} w-24 tabular-nums" bind:value={d.concurrencyCap} />
        </label>

        {@render idRow("Dedup mode", d.dedupMode, [
          { id: "label", label: "label (flip a label on send)" },
          { id: "seen", label: "seen (remember dispatched)" },
          { id: "state", label: "state (Linear workflow state)" },
        ], (v) => (d.dedupMode = v))}
        {#if d.dedupMode === "label"}
          {@render idRow("On-sent set label", d.onSentSetLabel, meta?.labels ?? null, (v) => (d.onSentSetLabel = v), "(pick a label)")}
        {/if}
      </section>

      <section class="space-y-2">
        {@render sectionHead("Write-back (optional)")}
        {@render idRow("On-spawn state", d.onSpawnStateId, meta?.states ?? null, (v) => (d.onSpawnStateId = v), "(none)")}
        {@render boolRow("Comment on spawn", d.commentOnSpawn, () => (d.commentOnSpawn = !d.commentOnSpawn))}
        {@render idRow("On-PR state", d.onPrStateId, meta?.states ?? null, (v) => (d.onPrStateId = v), "(none)")}
        {@render boolRow("PR requires checks", d.prRequiresChecks, () => (d.prRequiresChecks = !d.prRequiresChecks), "only after CI passes")}
        {@render boolRow("Comment on PR", d.commentOnPr, () => (d.commentOnPr = !d.commentOnPr))}
        {@render idRow("On-merged state", d.onMergedStateId, meta?.states ?? null, (v) => (d.onMergedStateId = v), "(none)")}
        {@render boolRow("Comment on merged", d.commentOnMerged, () => (d.commentOnMerged = !d.commentOnMerged))}
        {@render idRow("Blocked label", d.blockedLabelId, meta?.labels ?? null, (v) => (d.blockedLabelId = v), "(none)")}
        {@render boolRow("Comment on blocked", d.commentOnBlocked, () => (d.commentOnBlocked = !d.commentOnBlocked))}
      </section>
    </div>
  {/if}

  {#snippet footer()}
    <div class="flex items-center justify-end gap-2">
      <button class="rounded border border-edge px-3 py-1 text-xs text-faint hover:border-accent hover:text-ink" onclick={() => nav.closeOverlay()}>cancel</button>
      <button
        class="rounded bg-accent/20 px-3 py-1 text-xs text-accent hover:bg-accent/30 disabled:opacity-40"
        disabled={!dto || saving}
        onclick={save}>{saving ? "saving…" : "save"}</button
      >
    </div>
  {/snippet}
</Modal>
