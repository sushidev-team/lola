<script lang="ts">
  import { onMount } from "svelte";
  import { ConfigService } from "@bindings/desktop";
  import type { PollFormDTO } from "@bindings/desktop/models";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import Modal from "$lib/components/Modal.svelte";

  let dto = $state<PollFormDTO | null>(null);
  let loadErr = $state("");
  let saving = $state(false);
  // Array fields edit as newline-joined text, bound to a real string so the
  // textarea is a plain controlled input (reliable in jsdom, tidy binding).
  let statesText = $state("");
  let labelsText = $state("");

  // Shared class idioms (mirror Home.svelte inputs).
  const rowCls = "grid grid-cols-[172px_1fr] items-center gap-3";
  const labelCls = "text-[11px] text-faint";
  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent";
  const areaCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1 font-mono text-[11px] leading-snug text-ink outline-none focus:border-accent";
  const cbCls = "h-3.5 w-3.5 accent-[var(--color-accent)]";

  function joinLines(a: string[]): string {
    return (a ?? []).join("\n");
  }
  function splitLines(v: string): string[] {
    return v.split("\n");
  }
  function cleanArr(a: string[]): string[] {
    return (a ?? []).map((s) => s.trim()).filter(Boolean);
  }

  async function load() {
    try {
      const d = await ConfigService.GetPoll(nav.overlayProject);
      statesText = joinLines(d.stateIds);
      labelsText = joinLines(d.matchLabels);
      dto = d;
    } catch (err) {
      loadErr = String(err);
      store.setFlash(String(err), "bad");
    }
  }
  onMount(() => {
    void load();
  });

  async function save() {
    if (!dto) return;
    saving = true;
    try {
      await ConfigService.SavePoll({
        ...dto,
        stateIds: cleanArr(splitLines(statesText)),
        matchLabels: cleanArr(splitLines(labelsText)),
        concurrencyCap: Number(dto.concurrencyCap) || 0,
      });
      store.setFlash(`saved poll: ${dto.project}`, "good");
      nav.closeOverlay();
    } catch (err) {
      store.setFlash(String(err), "bad");
    } finally {
      saving = false;
    }
  }
</script>

{#snippet sectionHead(t: string)}
  <h3 class="mt-1 mb-2 border-b border-edge/40 pb-1 text-[10px] font-semibold tracking-wider text-faint uppercase">
    {t}
  </h3>
{/snippet}

<Modal title={"polls: " + nav.overlayProject} onClose={() => nav.closeOverlay()} width="640px">
  <p class="mb-3 text-[11px] text-faint">
    Linear IDs are UUIDs — paste them from Linear. (Live pickers coming later.)
  </p>

  {#if loadErr}
    <div class="rounded border border-bad/40 bg-bad/10 px-3 py-2 text-xs text-bad">{loadErr}</div>
  {:else if !dto}
    <div class="px-3 py-8 text-center text-faint">loading…</div>
  {:else}
    {@const d = dto}
    <div class="space-y-4">
      <!-- ── Filter ─────────────────────────────────────────── -->
      <section class="space-y-2">
        {@render sectionHead("Filter")}

        <div class={rowCls}>
          <span class={labelCls}>Enabled</span>
          <label class="flex items-center gap-2 text-xs text-ink">
            <input type="checkbox" class={cbCls} bind:checked={d.enabled} />
            <span class="text-faint">poll actively for matching issues</span>
          </label>
        </div>

        <div class={rowCls}>
          <label class={labelCls}>Team ID</label>
          <input class={inputCls} placeholder="team UUID" bind:value={d.teamId} />
        </div>

        <div class={rowCls}>
          <label class={labelCls}>Project ID</label>
          <input class={inputCls} placeholder="project UUID" bind:value={d.projectId} />
        </div>

        <div class={rowCls}>
          <label class={labelCls}>Cycle mode</label>
          <select class={inputCls} bind:value={d.cycleMode}>
            <option value="none">none</option>
            <option value="active">active</option>
            <option value="pinned">pinned</option>
          </select>
        </div>

        <div class={rowCls}>
          <label class={labelCls}>Cycle ID</label>
          <input class={inputCls} placeholder="cycle UUID (pinned mode)" bind:value={d.cycleId} />
        </div>

        <div class="grid grid-cols-[172px_1fr] items-start gap-3">
          <label class="{labelCls} pt-1.5">State IDs</label>
          <div class="space-y-1">
            <textarea
              class={areaCls}
              rows="3"
              placeholder="one workflow-state UUID per line"
              bind:value={statesText}
            ></textarea>
            <p class="text-[10px] text-faint">one UUID per line</p>
          </div>
        </div>

        <div class="grid grid-cols-[172px_1fr] items-start gap-3">
          <label class="{labelCls} pt-1.5">Match labels</label>
          <div class="space-y-1">
            <textarea
              class={areaCls}
              rows="3"
              placeholder="one label per line"
              bind:value={labelsText}
            ></textarea>
            <p class="text-[10px] text-faint">one label per line</p>
          </div>
        </div>

        <div class={rowCls}>
          <label class={labelCls}>Match mode</label>
          <select class={inputCls} bind:value={d.matchMode}>
            <option value="any">any</option>
            <option value="all">all</option>
          </select>
        </div>

        <div class={rowCls}>
          <label class={labelCls}>Assignee mode</label>
          <select class={inputCls} bind:value={d.assigneeMode}>
            <option value="anyone">anyone</option>
            <option value="me">me</option>
            <option value="user">user</option>
          </select>
        </div>

        <div class={rowCls}>
          <label class={labelCls}>Assignee user ID</label>
          <input class={inputCls} placeholder="user UUID (user mode)" bind:value={d.assigneeUserId} />
        </div>

        <div class={rowCls}>
          <label class={labelCls}>Concurrency cap</label>
          <input type="number" min="0" class="{inputCls} w-24 tabular-nums" bind:value={d.concurrencyCap} />
        </div>

        <div class={rowCls}>
          <label class={labelCls}>Dedup mode</label>
          <select class={inputCls} bind:value={d.dedupMode}>
            <option value="label">label</option>
            <option value="seen">seen</option>
            <option value="state">state</option>
          </select>
        </div>

        <div class={rowCls}>
          <label class={labelCls}>On-sent set label</label>
          <input class={inputCls} placeholder="label applied after dispatch" bind:value={d.onSentSetLabel} />
        </div>
      </section>

      <!-- ── Write-back ─────────────────────────────────────── -->
      <section class="space-y-2">
        {@render sectionHead("Write-back")}

        <div class={rowCls}>
          <label class={labelCls}>On-spawn state ID</label>
          <input class={inputCls} placeholder="state UUID" bind:value={d.onSpawnStateId} />
        </div>

        <div class={rowCls}>
          <label class={labelCls}>On-PR state ID</label>
          <input class={inputCls} placeholder="state UUID" bind:value={d.onPrStateId} />
        </div>

        <div class={rowCls}>
          <label class={labelCls}>On-merged state ID</label>
          <input class={inputCls} placeholder="state UUID" bind:value={d.onMergedStateId} />
        </div>

        <div class={rowCls}>
          <label class={labelCls}>Blocked label ID</label>
          <input class={inputCls} placeholder="label UUID" bind:value={d.blockedLabelId} />
        </div>

        <div class={rowCls}>
          <span class={labelCls}>Comment on spawn</span>
          <label class="flex items-center gap-2 text-xs text-ink">
            <input type="checkbox" class={cbCls} bind:checked={d.commentOnSpawn} />
          </label>
        </div>

        <div class={rowCls}>
          <span class={labelCls}>Comment on PR</span>
          <label class="flex items-center gap-2 text-xs text-ink">
            <input type="checkbox" class={cbCls} bind:checked={d.commentOnPr} />
          </label>
        </div>

        <div class={rowCls}>
          <span class={labelCls}>Comment on merged</span>
          <label class="flex items-center gap-2 text-xs text-ink">
            <input type="checkbox" class={cbCls} bind:checked={d.commentOnMerged} />
          </label>
        </div>

        <div class={rowCls}>
          <span class={labelCls}>Comment on blocked</span>
          <label class="flex items-center gap-2 text-xs text-ink">
            <input type="checkbox" class={cbCls} bind:checked={d.commentOnBlocked} />
          </label>
        </div>

        <div class={rowCls}>
          <span class={labelCls}>PR requires checks</span>
          <label class="flex items-center gap-2 text-xs text-ink">
            <input type="checkbox" class={cbCls} bind:checked={d.prRequiresChecks} />
            <span class="text-faint">only write back after CI passes</span>
          </label>
        </div>
      </section>
    </div>
  {/if}

  {#snippet footer()}
    <div class="flex items-center justify-end gap-2">
      <button
        class="rounded border border-edge px-3 py-1 text-xs text-faint hover:border-accent hover:text-ink"
        onclick={() => nav.closeOverlay()}>cancel</button
      >
      <button
        class="rounded bg-accent/20 px-3 py-1 text-xs text-accent hover:bg-accent/30 disabled:opacity-40"
        disabled={!dto || saving}
        onclick={save}>{saving ? "saving…" : "save"}</button
      >
    </div>
  {/snippet}
</Modal>
