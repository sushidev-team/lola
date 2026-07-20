<script lang="ts">
  import { onMount } from "svelte";
  import Modal from "$lib/components/Modal.svelte";
  import Tabs from "$lib/components/Tabs.svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { ConfigService, LinearService } from "@bindings/desktop";
  import type { SettingsDTO, LinearTeamMeta, LinearOption } from "@bindings/desktop/models";
  import { linesToText, splitLines, cleanLines } from "$lib/lines";

  // The bindings hand back a class instance, which $state does NOT deep-proxy —
  // copy it into a plain object so the segmented controls and pickers below
  // actually re-render when they are clicked.
  let dto = $state<SettingsDTO | null>(null);
  let loading = $state(true);
  let loadError = $state("");
  let saving = $state(false);
  let tab = $state(nav.overlayTab || "defaults");

  // Only offerable when every polling project shares one team: label UUIDs are
  // team-scoped, so a [defaults] label has no meaning across several teams.
  let meta = $state<LinearTeamMeta | null>(null);
  let metaErr = $state("");

  const TABS = [
    { id: "defaults", label: "Defaults" },
    { id: "project", label: "Project defaults" },
    { id: "notify", label: "Notify" },
    { id: "brain", label: "Brain" },
    { id: "coderabbit", label: "CodeRabbit" },
  ];

  const AGENTS = ["claude", "codex", "opencode"];

  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent placeholder:text-faint/50";
  const rowCls = "grid grid-cols-[11rem_1fr] items-center gap-3";
  const rowTopCls = "grid grid-cols-[11rem_1fr] items-start gap-3";
  const cbCls = "h-3.5 w-3.5 accent-[var(--color-accent)]";
  const hintCls = "mt-1 block text-[10px] text-faint";

  function toggleId(arr: string[] | null, id: string): string[] {
    const a = arr ?? [];
    return a.includes(id) ? a.filter((x) => x !== id) : [...a, id];
  }

  onMount(async () => {
    try {
      dto = { ...(await ConfigService.GetSettings()) };
    } catch (err) {
      loadError = String(err);
      store.setFlash(String(err), "bad");
      return;
    } finally {
      loading = false;
    }
    if (dto.defaultsTeamId) {
      try {
        meta = await LinearService.TeamMeta(dto.defaultsTeamId, false);
      } catch (e) {
        metaErr = String(e); // fall back to raw UUID entry
      }
    }
  });

  async function save() {
    if (!dto) return;
    saving = true;
    try {
      await ConfigService.SaveSettings({
        ...dto,
        symlinks: cleanLines(dto.symlinks),
        postCreate: cleanLines(dto.postCreate),
        env: cleanLines(dto.env),
        matchLabels: cleanLines(dto.matchLabels),
        prioritySort: cleanLines(dto.prioritySort),
      });
      store.setFlash("settings saved", "good");
      nav.closeOverlay();
    } catch (err) {
      store.setFlash(String(err), "bad");
    } finally {
      saving = false;
    }
  }
</script>

{#snippet head(label: string)}
  <h3 class="mb-2 text-[10px] font-semibold tracking-wider text-faint uppercase">{label}</h3>
{/snippet}

{#snippet areaRow(caption: string, value: string[] | null, onChange: (v: string[]) => void, placeholder = "", hint = "")}
  <div class={rowTopCls}>
    <span class="text-faint">{caption}</span>
    <span>
      <textarea
        class="{inputCls} resize-y font-mono"
        aria-label={caption}
        rows="3"
        spellcheck="false"
        {placeholder}
        value={linesToText(value)}
        oninput={(e) => onChange(splitLines(e.currentTarget.value))}
      ></textarea>
      {#if hint}<span class={hintCls}>{hint}</span>{/if}
    </span>
  </div>
{/snippet}

{#snippet selectRow(caption: string, current: string, options: LinearOption[], onChange: (v: string) => void, anyLabel = "")}
  <div class={rowCls}>
    <span class="text-faint">{caption}</span>
    <select class={inputCls} aria-label={caption} value={current} onchange={(e) => onChange(e.currentTarget.value)}>
      {#if anyLabel}<option value="">{anyLabel}</option>{/if}
      {#each options as o (o.id)}<option value={o.id}>{o.label}</option>{/each}
    </select>
  </div>
{/snippet}

<!-- A [defaults] label field: a real picker when one team owns every poll,
     otherwise raw UUID entry (see SettingsDTO.defaultsTeamId). -->
{#snippet labelRow(caption: string, current: string, onChange: (v: string) => void)}
  {#if meta}
    {@render selectRow(caption, current, meta.labels ?? [], onChange, "(none)")}
  {:else}
    <div class={rowCls}>
      <span class="text-faint">{caption}</span>
      <input
        class="{inputCls} font-mono"
        aria-label={caption}
        value={current}
        placeholder="label UUID"
        oninput={(e) => onChange(e.currentTarget.value)}
      />
    </div>
  {/if}
{/snippet}

<Modal title="settings" onClose={() => nav.closeOverlay()} width="640px">
  {#if loading}
    <div class="py-10 text-center text-xs text-faint">loading settings…</div>
  {:else if loadError}
    <div class="py-10 text-center text-xs text-bad">{loadError}</div>
  {:else if dto}
    {@const d = dto}
    <Tabs tabs={TABS} active={tab} onSelect={(id) => (tab = id)} />

    <div class="text-xs">
      {#if tab === "defaults"}
        <section>
          {@render head("Defaults")}
          <div class="space-y-2">
            <label class={rowCls}>
              <span class="text-faint">Global cap</span>
              <input class={inputCls} type="number" min="0" bind:value={d.globalCap} />
            </label>
            <label class={rowCls}>
              <span class="text-faint">Concurrency cap</span>
              <input class={inputCls} type="number" min="0" bind:value={d.concurrencyCap} />
            </label>
            <label class={rowCls}>
              <span class="text-faint">Poll interval</span>
              <input class={inputCls} type="text" placeholder="60s" bind:value={d.pollInterval} />
            </label>
            <div class={rowCls}>
              <span class="text-faint">Agent</span>
              <div class="inline-flex w-fit divide-x divide-edge overflow-hidden rounded border border-edge">
                {#each AGENTS as a (a)}
                  <button
                    type="button"
                    class="px-3 py-1 {d.agent === a ? 'bg-accent/20 text-accent' : 'text-faint hover:text-ink'}"
                    onclick={() => { d.agent = a; }}>{a}</button
                  >
                {/each}
              </div>
            </div>
          </div>
        </section>
      {:else if tab === "project"}
        <section>
          {@render head("Project defaults")}
          <p class="mb-3 text-[10px] text-faint">
            Every [[project]] that omits one of these keys inherits it. The project editor shows an inherited value ghosted.
          </p>
          <div class="space-y-2">
            <label class={rowCls}>
              <span class="text-faint">Branch prefix</span>
              <input class="{inputCls} font-mono" type="text" placeholder="lola/" bind:value={d.branchPrefix} />
            </label>
            {@render areaRow("Symlinks", d.symlinks, (v) => { d.symlinks = v; }, ".env\nnode_modules", "one path per line")}
            {@render areaRow("Post-create", d.postCreate, (v) => { d.postCreate = v; }, "npm install", "one command per line")}
            {@render areaRow("Env", d.env, (v) => { d.env = v; }, "KEY=value", "one KEY=value per line")}

            {#if !d.defaultsTeamId}
              <p class="rounded border border-edge bg-canvas px-3 py-2 text-[10px] text-faint">
                Your polling projects span more than one team (or none polls yet). Label UUIDs are team-scoped, so no shared picker can be
                offered — paste the UUIDs below.
              </p>
            {:else if metaErr}
              <p class="rounded border border-warn/40 bg-warn/10 px-3 py-2 text-[10px] text-warn">
                couldn't load team labels ({metaErr}) — using raw UUID entry
              </p>
            {/if}

            {#if meta}
              <div class={rowTopCls}>
                <span class="text-faint">Match labels</span>
                <div class="max-h-36 space-y-1 overflow-auto rounded border border-edge p-2">
                  {#each meta.labels ?? [] as o (o.id)}
                    <label class="flex items-center gap-2 text-xs text-ink">
                      <input
                        type="checkbox"
                        class={cbCls}
                        checked={(d.matchLabels ?? []).includes(o.id)}
                        onchange={() => { d.matchLabels = toggleId(d.matchLabels, o.id); }}
                      />
                      <span class="truncate">{o.label}</span>
                    </label>
                  {/each}
                  {#if (meta.labels ?? []).length === 0}<span class="text-[11px] text-faint">none</span>{/if}
                </div>
              </div>
            {:else}
              {@render areaRow("Match labels", d.matchLabels, (v) => { d.matchLabels = v; }, "one UUID per line")}
            {/if}

            {@render selectRow(
              "Match mode",
              d.matchMode,
              [
                { id: "any", label: "any label" },
                { id: "all", label: "all labels" },
              ],
              (v) => { d.matchMode = v; },
            )}
            {@render selectRow(
              "Dedup mode",
              d.dedupMode,
              [
                { id: "label", label: "label (flip a label on send)" },
                { id: "seen", label: "seen (remember dispatched)" },
                { id: "state", label: "state (Linear workflow state)" },
              ],
              (v) => { d.dedupMode = v; },
            )}
            {@render labelRow("On-sent set label", d.onSentSetLabel, (v) => { d.onSentSetLabel = v; })}
            {@render labelRow("Blocked label", d.blockedLabelId, (v) => { d.blockedLabelId = v; })}
            {@render areaRow(
              "Priority sort",
              d.prioritySort,
              (v) => { d.prioritySort = v; },
              "priority\ncreatedAt",
              "one key per line — empty means priority, createdAt",
            )}
          </div>
        </section>
      {:else if tab === "notify"}
        <section>
          {@render head("Notify")}
          <div class="space-y-2">
            <label class="flex cursor-pointer items-center gap-2">
              <input type="checkbox" class="accent-accent" bind:checked={d.notifyDesktop} />
              <span>Desktop notifications</span>
            </label>
            <label class={rowTopCls}>
              <span class="text-faint">Slack webhook env</span>
              <div>
                <input class={inputCls} type="text" placeholder="LOLA_SLACK_WEBHOOK" bind:value={d.slackWebhookEnv} />
                <span class={hintCls}>Slack webhook env VAR NAME — never the URL.</span>
              </div>
            </label>
          </div>
        </section>
      {:else if tab === "brain"}
        <section>
          {@render head("Brain")}
          <div class="space-y-2">
            <label class="flex cursor-pointer items-center gap-2">
              <input type="checkbox" class="accent-accent" bind:checked={d.brainEnabled} />
              <span>Enabled</span>
            </label>
            <label class={rowCls}>
              <span class="text-faint">Model</span>
              <input class={inputCls} type="text" placeholder="claude-…" bind:value={d.brainModel} />
            </label>
            <label class={rowCls}>
              <span class="text-faint">Timeout (s)</span>
              <input class={inputCls} type="number" min="0" bind:value={d.brainTimeout} />
            </label>
            <div class="flex flex-wrap gap-x-6 gap-y-2 pt-1">
              <label class="flex cursor-pointer items-center gap-2">
                <input type="checkbox" class="accent-accent" bind:checked={d.brainSummarizeEscalation} />
                <span>Summarize on escalation</span>
              </label>
              <label class="flex cursor-pointer items-center gap-2">
                <input type="checkbox" class="accent-accent" bind:checked={d.brainSummarizeApproved} />
                <span>Summarize on approved</span>
              </label>
            </div>
          </div>
        </section>
      {:else}
        <div class="space-y-5">
          <section>
            {@render head("CodeRabbit review")}
            <div class="space-y-2">
              <label class="flex cursor-pointer items-center gap-2">
                <input type="checkbox" class="accent-accent" bind:checked={d.reviewEnabled} />
                <span>Enabled</span>
              </label>
              <label class={rowCls}>
                <span class="text-faint">Command</span>
                <input class={inputCls} type="text" placeholder="coderabbit review" bind:value={d.reviewCommand} />
              </label>
              <label class={rowCls}>
                <span class="text-faint">Timeout (s)</span>
                <input class={inputCls} type="number" min="0" bind:value={d.reviewTimeout} />
              </label>
              <div class="flex flex-wrap gap-x-6 gap-y-2 pt-1">
                <label class="flex cursor-pointer items-center gap-2">
                  <input type="checkbox" class="accent-accent" bind:checked={d.reviewOnPrOpen} />
                  <span>On PR open</span>
                </label>
                <label class="flex cursor-pointer items-center gap-2">
                  <input type="checkbox" class="accent-accent" bind:checked={d.reviewSendToAgent} />
                  <span>Send to agent</span>
                </label>
                <label class="flex cursor-pointer items-center gap-2">
                  <input type="checkbox" class="accent-accent" bind:checked={d.reviewCommentOnLinear} />
                  <span>Comment on Linear</span>
                </label>
              </div>
            </div>
          </section>

          <section class="border-t border-edge/40 pt-4">
            {@render head("CodeRabbit watch")}
            <div class="space-y-2">
              <label class="flex cursor-pointer items-center gap-2">
                <input type="checkbox" class="accent-accent" bind:checked={d.crEnabled} />
                <span>Enabled</span>
              </label>
              <label class={rowCls}>
                <span class="text-faint">Author</span>
                <input class={inputCls} type="text" placeholder="coderabbitai[bot]" bind:value={d.crAuthor} />
              </label>
              <div class="flex flex-wrap gap-x-6 gap-y-2 pt-1">
                <label class="flex cursor-pointer items-center gap-2">
                  <input type="checkbox" class="accent-accent" bind:checked={d.crNotify} />
                  <span>Notify</span>
                </label>
                <label class="flex cursor-pointer items-center gap-2">
                  <input type="checkbox" class="accent-accent" bind:checked={d.crSendToAgent} />
                  <span>Send to agent</span>
                </label>
                <label class="flex cursor-pointer items-center gap-2">
                  <input type="checkbox" class="accent-accent" bind:checked={d.crCommentOnLinear} />
                  <span>Comment on Linear</span>
                </label>
              </div>
            </div>
          </section>
        </div>
      {/if}
    </div>
  {/if}

  {#snippet footer()}
    <div class="flex items-center justify-end gap-2">
      <button class="rounded px-3 py-1 text-xs text-faint hover:text-ink" onclick={() => nav.closeOverlay()}>cancel</button>
      <button
        class="rounded bg-accent/20 px-3 py-1 text-xs text-accent hover:bg-accent/30 disabled:opacity-40"
        onclick={save}
        disabled={saving || loading || !dto}>{saving ? "saving…" : "save"}</button
      >
    </div>
  {/snippet}
</Modal>
