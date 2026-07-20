<script lang="ts">
  import { onMount } from "svelte";
  import Modal from "$lib/components/Modal.svelte";
  import Tabs from "$lib/components/Tabs.svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { ConfigService, LinearService } from "@bindings/desktop";
  import type { SettingsDTO, LinearOption } from "@bindings/desktop/models";
  import { linesToText, splitLines, cleanLines } from "$lib/lines";

  // The bindings hand back a class instance, which $state does NOT deep-proxy —
  // copy it into a plain object so the segmented controls and pickers below
  // actually re-render when they are clicked.
  let dto = $state<SettingsDTO | null>(null);
  let loading = $state(true);
  let loadError = $state("");
  let saving = $state(false);
  let tab = $state(nav.overlayTab || "defaults");

  // The [defaults] label keys offer WORKSPACE (organisation-level) labels, not
  // team labels: a shared default is inherited by projects on any team, and a
  // team-scoped label cannot match issues outside its own team. ProjectForm's
  // per-project pickers keep using TeamMeta, where a team label is correct.
  //
  // Loaded lazily on first visit to the Project-defaults tab so the rest of the
  // settings form never waits on a Linear round-trip.
  let wsLabels = $state<LinearOption[] | null>(null);
  let wsLoading = $state(false);
  let wsErr = $state("");
  let wsRequested = false;

  // A picker is only usable with something in it; an empty workspace falls back
  // to manual entry like a failed call does.
  const wsReady = $derived(!!wsLabels && wsLabels.length > 0);

  // priority_sort is a tie-break CHAIN over lola's own sort keys — not Linear
  // priorities, and nothing is fetched from the API. Selection ORDER is the
  // value: "priority then createdAt" and the reverse are different sorts, so
  // clicking a key appends it and the rank is shown rather than a tick.
  let sortKeys = $state<string[]>([]);

  const SORT_KEY_HELP: Record<string, string> = {
    priority: "highest first (no priority last)",
    createdAt: "oldest first",
  };

  async function loadSortKeys() {
    try {
      sortKeys = (await ConfigService.PrioritySortKeys()) ?? [];
    } catch {
      sortKeys = []; // falls back to the textarea below
    }
  }

  function toggleSortKey(k: string) {
    if (!dto) return; // `d` in the markup is a template-local {@const}, not this scope
    const cur = dto.prioritySort ?? [];
    dto.prioritySort = cur.includes(k) ? cur.filter((x) => x !== k) : [...cur, k];
  }

  async function loadWorkspaceLabels() {
    if (wsRequested) return;
    wsRequested = true;
    wsLoading = true;
    try {
      wsLabels = (await LinearService.WorkspaceLabels()) ?? [];
    } catch (e) {
      wsErr = String(e); // no key / offline → raw UUID entry, never a dead end
    } finally {
      wsLoading = false;
    }
  }

  function selectTab(id: string) {
    tab = id;
    if (id === "project") {
      void loadWorkspaceLabels();
      void loadSortKeys();
    }
  }

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
    // Deep-linked straight to the tab that needs them.
    if (tab === "project") {
      void loadWorkspaceLabels();
      void loadSortKeys();
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

{#snippet selectRow(caption: string, current: string, options: LinearOption[], onChange: (v: string) => void, anyLabel = "", hint = "")}
  <div class={hint ? rowTopCls : rowCls}>
    <span class="text-faint">{caption}</span>
    <span>
      <select class={inputCls} aria-label={caption} value={current} onchange={(e) => onChange(e.currentTarget.value)}>
        {#if anyLabel}<option value="">{anyLabel}</option>{/if}
        {#each options as o (o.id)}<option value={o.id}>{o.label}</option>{/each}
      </select>
      {#if hint}<span class={hintCls}>{hint}</span>{/if}
    </span>
  </div>
{/snippet}

<!-- One [defaults] label key: a workspace-label picker, or manual UUID entry
     when the workspace labels couldn't be loaded or there are none. -->
{#snippet labelRow(caption: string, current: string, onChange: (v: string) => void)}
  {#if wsReady}
    {@render selectRow(caption, current, wsLabels ?? [], onChange, "(none)", "workspace label — valid across every team")}
  {:else}
    <div class={rowTopCls}>
      <span class="text-faint">{caption}</span>
      <span>
        <input
          class="{inputCls} font-mono"
          aria-label={caption}
          value={current}
          placeholder="workspace label UUID"
          oninput={(e) => onChange(e.currentTarget.value)}
        />
        <span class={hintCls}>workspace label — valid across every team</span>
      </span>
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
    <Tabs tabs={TABS} active={tab} onSelect={selectTab} />

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
            Every [[project]] that omits one of these keys inherits it. The project editor shows an inherited value ghosted. The label fields
            offer <span class="text-ink">workspace</span> labels, which apply across every team — a project's own pickers offer that project's
            team labels instead.
          </p>
          <div class="space-y-2">
            <label class={rowCls}>
              <span class="text-faint">Branch prefix</span>
              <input class="{inputCls} font-mono" type="text" placeholder="lola/" bind:value={d.branchPrefix} />
            </label>
            {@render areaRow("Symlinks", d.symlinks, (v) => { d.symlinks = v; }, ".env\nnode_modules", "one path per line")}
            {@render areaRow("Post-create", d.postCreate, (v) => { d.postCreate = v; }, "npm install", "one command per line")}
            {@render areaRow("Env", d.env, (v) => { d.env = v; }, "KEY=value", "one KEY=value per line")}

            {#if wsLoading}
              <p class="text-[10px] text-faint">loading workspace labels…</p>
            {:else if wsErr}
              <p class="rounded border border-warn/40 bg-warn/10 px-3 py-2 text-[10px] text-warn">
                couldn't load workspace labels ({wsErr}) — enter the UUIDs by hand below
              </p>
            {:else if wsLabels && wsLabels.length === 0}
              <p class="rounded border border-edge bg-canvas px-3 py-2 text-[10px] text-faint">
                This workspace has no organisation-level labels. A shared default is inherited by projects on any team, so it should be one —
                create it in Linear, or paste a UUID below.
              </p>
            {/if}

            {#if wsReady}
              <div class={rowTopCls}>
                <span class="text-faint">Match labels</span>
                <span>
                  <div class="max-h-36 space-y-1 overflow-auto rounded border border-edge p-2">
                    {#each wsLabels ?? [] as o (o.id)}
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
                  </div>
                  <span class={hintCls}>workspace labels — valid across every team</span>
                </span>
              </div>
            {:else}
              {@render areaRow(
                "Match labels",
                d.matchLabels,
                (v) => { d.matchLabels = v; },
                "one UUID per line",
                "workspace labels — valid across every team",
              )}
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
            {#if sortKeys.length}
              <div class={rowTopCls}>
                <span class="text-faint">Priority sort</span>
                <span>
                  <div class="space-y-1 rounded border border-edge p-2">
                    {#each sortKeys as k (k)}
                      {@const rank = (d.prioritySort ?? []).indexOf(k)}
                      <button
                        type="button"
                        class="flex w-full items-center gap-2 rounded px-1 py-0.5 text-left text-xs hover:bg-edge/40"
                        onclick={() => toggleSortKey(k)}
                      >
                        <span
                          class="w-4 shrink-0 text-center font-mono {rank >= 0 ? 'text-accent' : 'text-faint/40'}"
                        >{rank >= 0 ? rank + 1 : "·"}</span>
                        <span class="text-ink">{k}</span>
                        <span class="text-faint">{SORT_KEY_HELP[k] ?? ""}</span>
                      </button>
                    {/each}
                  </div>
                  <span class={hintCls}>
                    the number is the tie-break order — click to add or remove; empty means priority, then createdAt
                  </span>
                </span>
              </div>
            {:else}
              {@render areaRow(
                "Priority sort",
                d.prioritySort,
                (v) => { d.prioritySort = v; },
                "priority\ncreatedAt",
                "one key per line — empty means priority, createdAt",
              )}
            {/if}
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
