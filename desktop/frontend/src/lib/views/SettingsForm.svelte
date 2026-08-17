<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import Modal from "$lib/components/Modal.svelte";
  import Tabs from "$lib/components/Tabs.svelte";
  import Button from "$lib/components/Button.svelte";
  import Checkbox from "$lib/components/Checkbox.svelte";
  import Select from "$lib/components/Select.svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { overlayClose } from "$lib/overlayClose";
  import { deepEqual } from "$lib/deepEqual";
  import { ConfigService, LinearService } from "@bindings/desktop";
  import type { SettingsDTO, LinearOption, LinearKeyStatusDTO } from "@bindings/desktop/models";
  import { linesToText, splitLines, cleanLines } from "$lib/lines";
  import { appearance, FLAVORS, THEME_IDS, type ThemeId } from "$lib/theme-runtime.svelte";

  // The bindings hand back a class instance, which $state does NOT deep-proxy —
  // copy it into a plain object so the segmented controls and pickers below
  // actually re-render when they are clicked.
  let dto = $state<SettingsDTO | null>(null);
  let loading = $state(true);
  let loadError = $state("");
  let saving = $state(false);
  // A rejected SaveSettings used to surface only as a footer flash — behind this
  // backdrop, truncated, gone in 4s — so it read like a save that just didn't
  // close. Held here and shown inline instead; cleared on the next attempt.
  let saveErr = $state("");

  // The DTO exactly as it was loaded, so a close can tell real edits apart from an
  // untouched form and only prompt to discard when something changed. The theme
  // is deliberately NOT part of this: [ui] is not a DTO field and already reverts
  // on every close path (see previewTheme / revertTheme), so a theme-only preview
  // is not "unsaved changes" to guard.
  let loaded = $state<SettingsDTO | null>(null);
  const dirty = $derived.by(() => (dto && loaded ? !deepEqual($state.snapshot(dto), $state.snapshot(loaded)) : false));

  /** Reset the dirty baseline to the current DTO — after load and after a migrate. */
  function markPristine() {
    loaded = dto ? ($state.snapshot(dto) as SettingsDTO) : null;
  }

  const TABS = [
    { id: "defaults", label: "Defaults" },
    { id: "linear", label: "Linear" },
    { id: "project", label: "Project defaults" },
    { id: "notify", label: "Notify" },
    { id: "brain", label: "Brain" },
    { id: "interpreter", label: "Interpreter" },
    // "Review", not "CodeRabbit": the body is the whole [[review.provider]]
    // catalog — coderabbit-cli, coderabbit-watch AND claude-session — so naming
    // the tab after one provider hid the other two.
    { id: "review", label: "Review" },
    { id: "appearance", label: "Appearance" },
  ];

  // Every tab body is an explicit `{:else if tab === …}` branch with no catch-all,
  // so an unknown deep-link id would render a blank pane. Clamp it to a real tab.
  let tab = $state(TABS.some((t) => t.id === nav.overlayTab) ? nav.overlayTab : "defaults");

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

  // --- appearance ([ui].theme) ----------------------------------------------
  //
  // The theme is the one setting with a LIVE PREVIEW: clicking a flavor repaints
  // the whole app — chrome, live terminals and snapshot tiles — so the choice can
  // be judged before it is committed. The preview is paint-only; nothing reaches
  // config.toml until `save`, and every way out of this overlay (cancel, ✕,
  // Escape, backdrop) puts the persisted flavor back.
  //
  // It is deliberately NOT a SettingsDTO field: [ui] is presentation, not a
  // [defaults] key, and ConfigService.SetTheme is its sole writer. `save`
  // therefore issues a second, single-key write alongside SaveSettings.

  // The flavor that is actually in config.toml. `appearance.id` is the LIVE one,
  // which the preview moves around; this is the baseline it snaps back to.
  let savedTheme: ThemeId = appearance.id;
  let themeId = $state<ThemeId>(appearance.id);

  // Which ids to offer. MEMBERSHIP is the Go side's call — config.Validate
  // rejects anything outside config.UIThemes, so offering more would just build
  // a picker that fails on save. ORDER is ours: config.UIThemes runs light→dark
  // and THEME_IDS runs dark→light, so adopting the bridge's sequence would
  // reflow the grid the moment the call landed. Hence filter, don't replace.
  //
  // Anything the bridge names that we have no palette for is dropped — a swatch
  // needs colours only the frontend carries. An empty or failed answer keeps the
  // local list, so an old desktop binary predating Themes() still picks a theme
  // rather than showing an empty grid.
  let themeIds = $state<ThemeId[]>(THEME_IDS);
  let themesRequested = false;

  async function loadThemes() {
    if (themesRequested) return;
    themesRequested = true;
    try {
      const allowed = new Set((await ConfigService.Themes()) ?? []);
      const known = THEME_IDS.filter((id) => allowed.has(id));
      if (known.length) themeIds = known;
    } catch {
      // keep THEME_IDS
    }
  }

  /**
   * Paint a flavor without persisting it. Driving `appearance.id` + `paint()` is
   * the same pair `appearance.init()` uses, and it is what makes the preview
   * COMPLETE: `applyFlavor()` alone repaints the chrome but leaves
   * `appearance.term` / `appearance.ansi` on the old flavor, so terminals would
   * keep their old colors. Nothing is persisted here — `appearance.commit()`
   * in `save` is the only writer, which is what keeps a preview the user backs
   * out of from reaching config.toml or the boot cache.
   */
  function previewTheme(id: ThemeId) {
    themeId = id;
    appearance.id = id;
    appearance.paint();
  }

  /** Undo an uncommitted preview. A no-op once save has moved the baseline. */
  function revertTheme() {
    if (appearance.id === savedTheme) return;
    appearance.id = savedTheme;
    appearance.paint();
  }

  function cancel() {
    revertTheme();
    nav.closeOverlay();
  }

  /**
   * The single close path for the ✕, the backdrop, Escape and the cancel button
   * (see overlayClose): a stray one of those after editing several tabs would drop
   * every edit, so a dirty form routes the close through the confirm dialog first.
   * Both the plain and the confirmed path go through cancel(), which also reverts
   * an uncommitted theme preview. Save closes directly and never reaches here.
   */
  function requestClose() {
    if (!dirty) {
      cancel();
      return;
    }
    confirm.ask({
      title: "Discard changes?",
      body: "Discard your unsaved changes to settings?",
      confirmLabel: "Discard",
      onConfirm: cancel,
    });
  }

  // Escape closes from App.svelte's global handler; register so it asks this form
  // (running the dirty guard) rather than closing the overlay blindly.
  onMount(() => overlayClose.register(requestClose));
  onDestroy(() => overlayClose.unregister(requestClose));

  // Hung off the lifecycle, not just the cancel button: Escape, the backdrop and
  // the ✕ all close the overlay too, and so does the overlay being swapped out
  // from under us. A preview must never outlive the form that started it.
  onDestroy(revertTheme);

  // Per-tab lazy loads, shared by the tab strip and the deep-link on mount so
  // the two can't drift.
  function lazyLoadFor(id: string) {
    if (id === "project") {
      void loadWorkspaceLabels();
      void loadSortKeys();
    } else if (id === "appearance") {
      void loadThemes();
    } else if (id === "linear") {
      void loadKeyStatus();
    }
  }

  function selectTab(id: string) {
    tab = id;
    lazyLoadFor(id);
  }

  const AGENTS = ["claude", "codex", "opencode"];

  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder";
  const rowCls = "grid grid-cols-[11rem_1fr] items-center gap-3";
  const rowTopCls = "grid grid-cols-[11rem_1fr] items-start gap-3";
  const hintCls = "mt-1 block text-sm text-faint";

  function toggleId(arr: string[] | null, id: string): string[] {
    const a = arr ?? [];
    return a.includes(id) ? a.filter((x) => x !== id) : [...a, id];
  }

  onMount(async () => {
    try {
      dto = { ...(await ConfigService.GetSettings()) };
      markPristine(); // baseline for the dirty check, before any edit
    } catch (err) {
      loadError = String(err);
      store.setFlash(String(err), "bad");
      return;
    } finally {
      loading = false;
    }
    // Deep-linked straight to the tab that needs them.
    lazyLoadFor(tab);
  });

  async function save() {
    if (!dto) return;
    saving = true;
    saveErr = "";
    try {
      await ConfigService.SaveSettings({
        ...dto,
        symlinks: cleanLines(dto.symlinks),
        postCreate: cleanLines(dto.postCreate),
        env: cleanLines(dto.env),
        matchLabels: cleanLines(dto.matchLabels),
        prioritySort: cleanLines(dto.prioritySort),
      });
      // [ui].theme is a single-key write on its own path, not a DTO field.
      // Sequenced AFTER the settings write so a payload the daemon rejects can
      // never leave a theme change behind on disk.
      //
      // Through appearance.commit, not ConfigService.SetTheme directly: the
      // localStorage cache that lets the next launch paint this flavor on the
      // first frame is written there. Calling the binding straight left the
      // cache on the OLD flavor, so the very next launch — the one right after
      // the user changed the theme — flashed the previous colours.
      if (themeId !== savedTheme) {
        await appearance.commit(themeId);
        savedTheme = themeId; // the preview is now the persisted value
      }
      store.setFlash("settings saved", "good");
      nav.closeOverlay();
    } catch (err) {
      saveErr = String(err);
      store.setFlash(String(err), "bad");
    } finally {
      saving = false;
    }
  }

  // --- review provider catalog ----------------------------------------------
  //
  // The Review tab edits d.reviewProviders (the [[review.provider]] catalog).
  // A legacy [review]/[coderabbit] config is shown read-only until migrated:
  // editing it alongside a catalog is a hard validation error, so MigrateReview
  // is the one-way path off it.
  const ALL_KINDS = ["coderabbit-cli", "coderabbit-watch", "claude-session"];
  const KIND_LABELS: Record<string, string> = {
    "coderabbit-cli": "coderabbit-cli — execs `coderabbit review` on PR-open",
    "coderabbit-watch": "coderabbit-watch — polls the PR for the app's comments",
    "claude-session": "claude-session — headless `claude -p` review on PR-open",
  };
  const TRANSPORTS = ["lola", "github", "linear"];
  const isWatch = (kind: string) => kind === "coderabbit-watch";
  // Transports offered per kind: the watch forbids github (its feedback is
  // already on the PR).
  const transportsFor = (kind: string) => (isWatch(kind) ? ["lola", "linear"] : TRANSPORTS);
  // A provider may fall through to the OTHER pass kind only (never itself / the watch).
  const fallbackFor = (kind: string) => ALL_KINDS.filter((k) => k !== kind && !isWatch(k));

  const providers = () => (dto?.reviewProviders ?? []) as any[];
  const providerOf = (kind: string) => providers().find((p) => p.provider === kind);
  const missingKinds = () => ALL_KINDS.filter((k) => !providerOf(k));

  function addProvider(kind: string) {
    if (!dto || providerOf(kind)) return;
    dto.reviewProviders = [
      ...providers(),
      {
        provider: kind,
        enabled: true,
        onPrOpen: !isWatch(kind),
        command: "",
        timeoutSeconds: 300,
        model: "",
        author: isWatch(kind) ? "coderabbitai" : "",
        transports: ["lola"],
        // The github transport posts anchored, resolvable threads by default;
        // it degrades to one flat comment by itself when nothing can be anchored.
        githubInline: true,
        notify: true,
        sendToAgent: true,
        // A pass runs in a watchable "<session>-review" tmux session by default;
        // a watch has no exec to watch.
        visible: !isWatch(kind),
        fallback: [],
      },
    ];
  }

  function removeProvider(kind: string) {
    if (!dto) return;
    dto.reviewProviders = providers().filter((p) => p.provider !== kind);
  }

  function toggleTransport(p: any, t: string, on: boolean) {
    const set = new Set<string>(p.transports ?? []);
    if (on) set.add(t);
    else set.delete(t);
    set.add("lola"); // lola is always present
    p.transports = TRANSPORTS.filter((x) => set.has(x));
  }

  function toggleFallback(p: any, k: string, on: boolean) {
    const set = new Set<string>(p.fallback ?? []);
    if (on) set.add(k);
    else set.delete(k);
    p.fallback = fallbackFor(p.provider).filter((x) => set.has(x));
  }

  // --- Linear API key ([linear]) --------------------------------------------
  //
  // The key is NOT a SettingsDTO field, for the reasons the theme is not one
  // plus one of its own: a whole-form commit would carry a secret through every
  // unrelated save, and a validation failure on some other tab would silently
  // drop the key just typed. So this section has its own read (LinearKeyStatus,
  // which reports the SOURCE and whether it resolves — never the value) and its
  // own write (SetLinearKey), and neither touches `dto` or the dirty baseline.
  //
  // It exists because the key was settable only in the first-run wizard. A
  // config written by hand, or a rotated key, had no path at all — and a daemon
  // without a key fails every poll, the exact silent failure this app is for.
  let keyStatus = $state<LinearKeyStatusDTO | null>(null);
  let keyInput = $state("");
  let keyBusy = $state<"" | "validating" | "saving">("");
  let keyMsg = $state("");
  let keyMsgKind = $state<"good" | "bad">("good");

  async function loadKeyStatus() {
    try {
      keyStatus = await ConfigService.LinearKeyStatus();
    } catch (e) {
      keyStatus = null;
      keyMsg = String(e);
      keyMsgKind = "bad";
    }
  }

  async function validateKey() {
    if (!keyInput.trim()) return;
    keyBusy = "validating";
    keyMsg = "";
    try {
      await ConfigService.ValidateLinearKey(keyInput);
      keyMsg = "Key is valid.";
      keyMsgKind = "good";
    } catch (e) {
      keyMsg = String(e);
      keyMsgKind = "bad";
    } finally {
      keyBusy = "";
    }
  }

  async function saveKey() {
    if (!keyInput.trim()) return;
    keyBusy = "saving";
    keyMsg = "";
    try {
      const msg = await ConfigService.SetLinearKey(keyInput);
      // Clear the field on success: the key is stored, and leaving it in a DOM
      // input keeps a live secret on screen for as long as the overlay is open.
      keyInput = "";
      keyMsg = msg;
      keyMsgKind = "good";
      await loadKeyStatus();
      // The daemon reads the key at startup and on reload, so a key saved into a
      // running daemon means nothing until it re-reads config.
      await store.reload();
    } catch (e) {
      keyMsg = String(e);
      keyMsgKind = "bad";
    } finally {
      keyBusy = "";
    }
  }

  async function migrateReview() {
    try {
      await ConfigService.MigrateReview();
      dto = { ...(await ConfigService.GetSettings()) };
      // The migrate already wrote config, so the reloaded DTO is the new baseline
      // — not an unsaved edit the discard prompt should fire on.
      markPristine();
      store.setFlash("migrated to review providers", "good");
    } catch (err) {
      store.setFlash(String(err), "bad");
    }
  }
</script>

{#snippet head(label: string)}
  <!-- text-lg, not `label`: a section head rendered as an 12px uppercase faint
       micro-label is smaller AND quieter than the 13px rows it heads, which is
       hierarchy upside down. This is the one place in the settings overlay that
       must out-rank the content under it. `label` stays for per-field captions. -->
  <h3 class="mb-2 text-lg text-ink">{label}</h3>
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
      <Select aria-label={caption} value={current} onchange={(e) => onChange(e.currentTarget.value)}>
        {#if anyLabel}<option value="">{anyLabel}</option>{/if}
        {#each options as o (o.id)}<option value={o.id}>{o.label}</option>{/each}
      </Select>
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

<Modal title="settings" onClose={requestClose} width="640px">
  {#if loading}
    <div class="py-10 text-center text-faint">loading settings…</div>
  {:else if loadError}
    <div class="py-10 text-center text-bad">{loadError}</div>
  {:else if dto}
    {@const d = dto}
    <Tabs tabs={TABS} active={tab} onSelect={selectTab} />

    <!-- No size class: every field caption, control and option inherits the
         13px base. Only the faint explanations and micro-labels step down. -->
    <div>
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
              <!-- Segmented control: a hairline rail with a 2px gutter, so the
                   selected chip is a rounded button INSIDE it rather than a
                   flush-cut segment. Same shape as every other segmented control
                   in the app (lens picker, scope picker, agent/shell). -->
              <div class="inline-flex w-fit items-center gap-0.5 rounded-md border border-edge p-0.5">
                {#each AGENTS as a (a)}
                  <Button selected={d.agent === a} onclick={() => { d.agent = a; }}>{a}</Button>
                {/each}
              </div>
            </div>
          </div>
        </section>
      {:else if tab === "linear"}
        <section>
          {@render head("Linear")}
          <p class="copy mb-3 text-sm text-faint">
            lola reads every issue through this key. It is stored in the macOS Keychain and never written to
            <span class="font-mono text-sm">config.toml</span> — only the name of its source is.
          </p>

          <div class="mb-4 rounded-lg border border-edge bg-canvas px-3 py-2.5">
            {#if !keyStatus}
              <span class="text-faint">Checking…</span>
            {:else if keyStatus.resolvable}
              <span class="text-good">✓ Key configured</span>
              <span class="mt-1 block text-sm text-faint">Read from {keyStatus.source}.</span>
            {:else if keyStatus.configured}
              <span class="text-bad">✗ Key configured but unreadable</span>
              <span class="mt-1 block text-sm text-faint">{keyStatus.source} — {keyStatus.detail}</span>
            {:else}
              <span class="text-warn">▲ No key configured</span>
              <span class="mt-1 block text-sm text-faint">Every poll will fail until one is set.</span>
            {/if}
          </div>

          <div class="space-y-2">
            <div class={rowTopCls}>
              <span class="text-faint">{keyStatus?.configured ? "Replace key" : "API key"}</span>
              <span>
                <input
                  class="{inputCls} font-mono"
                  type="password"
                  autocomplete="off"
                  aria-label="Linear API key"
                  placeholder="lin_api_…"
                  bind:value={keyInput}
                  oninput={() => (keyMsg = "")}
                />
                <span class={hintCls}>Personal API key from Linear → Settings → Security &amp; access → API keys.</span>
              </span>
            </div>
            <div class={rowCls}>
              <span></span>
              <span class="flex items-center gap-2">
                <Button
                  variant="secondary"
                  disabled={!keyInput.trim() || keyBusy !== ""}
                  loading={keyBusy === "validating"}
                  onclick={validateKey}>Validate</Button
                >
                <Button
                  variant="primary"
                  disabled={!keyInput.trim() || keyBusy !== ""}
                  loading={keyBusy === "saving"}
                  onclick={saveKey}>Save key</Button
                >
              </span>
            </div>
            {#if keyMsg}
              <div class={rowTopCls}>
                <span></span>
                <p class="text-sm {keyMsgKind === 'good' ? 'text-good' : 'text-bad'}">{keyMsg}</p>
              </div>
            {/if}
          </div>

          <!-- Saved on its own, not by the overlay's Save: the key is a secret and
               must not ride along on an unrelated form commit (see saveKey). -->
          <p class="copy mt-4 text-sm text-faint">
            The key saves immediately — the overlay's Save button does not carry it.
          </p>
        </section>
      {:else if tab === "project"}
        <section>
          {@render head("Project defaults")}
          <p class="copy mb-3 text-sm text-faint">
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
              <p class="text-sm text-faint">loading workspace labels…</p>
            {:else if wsErr}
              <p class="rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">
                couldn't load workspace labels ({wsErr}) — enter the UUIDs by hand below
              </p>
            {:else if wsLabels && wsLabels.length === 0}
              <p class="rounded border border-edge bg-canvas px-3 py-2 text-sm text-faint">
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
                      <label class="flex items-center gap-2 text-ink">
                        <Checkbox
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
                      <Button block onclick={() => toggleSortKey(k)}>
                        <span
                          class="w-4 shrink-0 text-center font-mono {rank >= 0 ? 'text-accent-ink' : 'text-faint/40'}"
                        >{rank >= 0 ? rank + 1 : "·"}</span>
                        <span>{k}</span>
                        <span class="text-faint">{SORT_KEY_HELP[k] ?? ""}</span>
                      </Button>
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
              <Checkbox bind:checked={d.notifyDesktop} />
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
              <Checkbox bind:checked={d.brainEnabled} />
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
                <Checkbox bind:checked={d.brainSummarizeEscalation} />
                <span>Summarize on escalation</span>
              </label>
              <label class="flex cursor-pointer items-center gap-2">
                <Checkbox bind:checked={d.brainSummarizeApproved} />
                <span>Summarize on approved</span>
              </label>
            </div>
          </div>
        </section>
      {:else if tab === "interpreter"}
        <section>
          {@render head("Interpreter")}
          <p class="copy mb-3 text-sm text-faint">
            The <span class="font-mono text-ink">[statusagent]</span> status interpreter: a small bounded claude pass that judges what
            each agent is <span class="text-ink">actually</span> doing (an "≈" overlay + one-line headline in the session list).
            Display only — it can never change the real status or type into an agent. Each interpretation spends tokens.
          </p>
          <div class="space-y-2">
            <label class="flex cursor-pointer items-center gap-2">
              <Checkbox bind:checked={d.statusAgentEnabled} />
              <span>Enabled</span>
            </label>
            <label class={rowCls}>
              <span class="text-faint">Binary</span>
              <input class={inputCls} type="text" placeholder="claude (via PATH)" bind:value={d.statusAgentBin} />
            </label>
            <label class={rowCls}>
              <span class="text-faint">Model</span>
              <input class={inputCls} type="text" placeholder="sonnet" bind:value={d.statusAgentModel} />
            </label>
            <label class={rowCls}>
              <span class="text-faint">Timeout (s)</span>
              <input class={inputCls} type="number" min="0" bind:value={d.statusAgentTimeout} />
            </label>
            <label class={rowCls}>
              <span class="text-faint">Min interval (s)</span>
              <input class={inputCls} type="number" min="0" bind:value={d.statusAgentMinInterval} />
            </label>
            <label class={rowCls}>
              <span class="text-faint">Max per cycle</span>
              <input class={inputCls} type="number" min="0" bind:value={d.statusAgentMaxPerCycle} />
            </label>
            <label class={rowCls}>
              <span class="text-faint">Min confidence</span>
              <input class={inputCls} type="number" min="0" max="1" step="0.05" bind:value={d.statusAgentMinConfidence} />
            </label>
            <label class="flex cursor-pointer items-center gap-2">
              <Checkbox bind:checked={d.statusAgentIncludeTranscript} />
              <span>Include transcript tail</span>
            </label>
          </div>
        </section>
      {:else if tab === "appearance"}
        <section>
          {@render head("Appearance")}
          <p class="copy mb-3 text-sm text-faint">
            Sets <span class="font-mono text-ink">[ui].theme</span>, which colours the app chrome and every terminal from one palette.
            Picking a flavor <span class="text-ink">previews it immediately</span>; save writes it to config.toml, cancel puts the old one
            back.
          </p>
          <!-- Grid, not flex: WKWebView does not stretch a flex child inside a
               flex column, so a flex layout that fills correctly in Chrome
               collapses to content width in the packaged .app. -->
          <div class="grid grid-cols-2 gap-2">
            {#each themeIds as id (id)}
              {@const f = FLAVORS[id]}
              {@const on = themeId === id}
              <!-- Each option is drawn in its OWN colours, so the choice is
                   legible before it is applied. Catppuccin guarantees the
                   base/text pair's contrast, so this stays readable under
                   whichever flavor is currently live. -->
              <button
                type="button"
                aria-pressed={on}
                class="grid gap-2 rounded-lg border p-2.5 text-left {on ? 'ring-2 ring-accent' : ''}"
                style="background:{f.base};border-color:{on ? f.sky : f.surface1}"
                onclick={() => previewTheme(id)}
              >
                <span class="flex items-baseline gap-2">
                  <span class="font-medium" style="color:{f.text}">{f.label}</span>
                  <span class="label ml-auto" style="color:{on ? f.sky : f.overlay1}">
                    {on ? "selected" : f.dark ? "dark" : "light"}
                  </span>
                </span>
                <!-- The surface ramp first, then the accents lola maps onto
                     status — the parts of the palette the UI actually spends. -->
                <span class="grid auto-cols-max grid-flow-col gap-1">
                  {#each [f.surface0, f.surface2, f.subtext0, f.sky, f.green, f.yellow, f.peach, f.red, f.mauve] as c, i (i)}
                    <span class="h-3 w-3 rounded-sm" style="background:{c}"></span>
                  {/each}
                </span>
              </button>
            {/each}
          </div>
        </section>
      {:else if tab === "review"}
        <div class="space-y-5">
          {#if d.reviewLegacy}
            <div class="rounded border border-warn/40 bg-warn/10 px-3 py-2 text-ink">
              <p>
                This config still uses the legacy <code>[review]</code>/<code>[coderabbit]</code> tables. They are
                <span class="text-faint">read-only</span> here — migrate them into the editable provider catalog to continue.
              </p>
              <Button variant="secondary" size="md" class="mt-2" onclick={migrateReview}>Migrate to providers</Button>
            </div>
          {/if}

          {#each providers() as p (p.provider)}
            <section class="border-t border-edge/40 pt-4 first:border-t-0 first:pt-0" class:opacity-60={d.reviewLegacy}>
              {@render head(KIND_LABELS[p.provider] ?? p.provider)}
              <div class="space-y-2">
                <div class="flex items-center justify-between">
                  <label class="flex cursor-pointer items-center gap-2">
                    <Checkbox disabled={d.reviewLegacy} bind:checked={p.enabled} />
                    <span>Enabled</span>
                  </label>
                  {#if !d.reviewLegacy}
                    <Button variant="danger" onclick={() => removeProvider(p.provider)}>Remove</Button>
                  {/if}
                </div>

                {#if p.provider === "coderabbit-cli"}
                  <label class={rowCls}>
                    <span class="text-faint">Command</span>
                    <input class={inputCls} type="text" placeholder="coderabbit review" disabled={d.reviewLegacy} bind:value={p.command} />
                  </label>
                {/if}
                {#if p.provider === "claude-session"}
                  <label class={rowCls}>
                    <span class="text-faint">Model</span>
                    <input class={inputCls} type="text" placeholder="(claude default)" disabled={d.reviewLegacy} bind:value={p.model} />
                  </label>
                {/if}
                {#if isWatch(p.provider)}
                  <label class={rowCls}>
                    <span class="text-faint">Author</span>
                    <input class={inputCls} type="text" placeholder="coderabbitai" disabled={d.reviewLegacy} bind:value={p.author} />
                  </label>
                {:else}
                  <label class={rowCls}>
                    <span class="text-faint">Timeout (s)</span>
                    <input class={inputCls} type="number" min="0" disabled={d.reviewLegacy} bind:value={p.timeoutSeconds} />
                  </label>
                {/if}

                <div class="flex flex-wrap gap-x-6 gap-y-2 pt-1">
                  {#if !isWatch(p.provider)}
                    <label class="flex cursor-pointer items-center gap-2">
                      <Checkbox disabled={d.reviewLegacy} bind:checked={p.onPrOpen} />
                      <span>On PR open</span>
                    </label>
                  {/if}
                  <label class="flex cursor-pointer items-center gap-2">
                    <Checkbox disabled={d.reviewLegacy} bind:checked={p.notify} />
                    <span>Notify</span>
                  </label>
                  <label class="flex cursor-pointer items-center gap-2">
                    <Checkbox disabled={d.reviewLegacy} bind:checked={p.sendToAgent} />
                    <span>Send to agent</span>
                  </label>
                  {#if !isWatch(p.provider)}
                    <!-- The pass runs in its own "<session>-review" tmux session,
                         so it shows up as a Review tab beside the shells. -->
                    <label class="flex cursor-pointer items-center gap-2">
                      <Checkbox disabled={d.reviewLegacy} bind:checked={p.visible} />
                      <span>Watch it run</span>
                    </label>
                  {/if}
                </div>

                <div class={rowTopCls}>
                  <span class="text-faint">Transports</span>
                  <div class="flex flex-wrap gap-x-6 gap-y-2">
                    {#each transportsFor(p.provider) as t}
                      <label class="flex cursor-pointer items-center gap-2">
                        <Checkbox
                          disabled={d.reviewLegacy || t === "lola"}
                          checked={(p.transports ?? []).includes(t)}
                          onchange={(e) => toggleTransport(p, t, (e.currentTarget as HTMLInputElement).checked)} />
                        <span>{t}{t === "lola" ? " (always)" : ""}</span>
                      </label>
                    {/each}
                  </div>
                </div>

                <!-- Only the github transport has two shapes, so the choice
                     appears only once it is selected. Inline posts one anchored,
                     resolvable thread per finding (and asks the worker to close
                     the ones it fixes); off posts a single flat comment. -->
                {#if !isWatch(p.provider) && (p.transports ?? []).includes("github")}
                  <div class={rowTopCls}>
                    <span class="text-faint">GitHub shape</span>
                    <label class="flex cursor-pointer items-center gap-2">
                      <Checkbox disabled={d.reviewLegacy} bind:checked={p.githubInline} />
                      <span>Inline PR threads (resolvable)</span>
                    </label>
                  </div>
                {/if}

                {#if !isWatch(p.provider)}
                  <div class={rowTopCls}>
                    <span class="text-faint">Fallback</span>
                    <div class="flex flex-wrap gap-x-6 gap-y-2">
                      {#each fallbackFor(p.provider) as k}
                        <label class="flex cursor-pointer items-center gap-2">
                          <Checkbox
                            disabled={d.reviewLegacy}
                            checked={(p.fallback ?? []).includes(k)}
                            onchange={(e) => toggleFallback(p, k, (e.currentTarget as HTMLInputElement).checked)} />
                          <span>{k}</span>
                        </label>
                      {/each}
                    </div>
                  </div>
                {/if}
              </div>
            </section>
          {/each}

          <!-- Empty state. Without it a fresh config showed the Review tab as three
               bare buttons with raw kind ids and no clue what a "provider" is. -->
          {#if !d.reviewLegacy && providers().length === 0}
            <div class="rounded border border-edge/60 px-3 py-3">
              <p class="text-ink">No review pass configured.</p>
              <p class="copy mt-1 text-sm text-faint">
                A provider runs a QA pass over each pull request and routes its findings back — to the worker agent, the PR, or
                the Linear issue. Add one below to turn reviews on.
              </p>
            </div>
          {/if}

          {#if !d.reviewLegacy && missingKinds().length}
            <div class="flex flex-wrap items-center gap-2 border-t border-edge/40 pt-4">
              <span class="text-sm text-faint">Add provider:</span>
              {#each missingKinds() as k}
                <Button variant="secondary" title={KIND_LABELS[k] ?? k} onclick={() => addProvider(k)}>{k}</Button>
              {/each}
            </div>
          {/if}
        </div>
      {/if}
    </div>
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
    <div class="flex items-center justify-end gap-2">
      <Button size="md" onclick={requestClose}>Cancel</Button>
      <Button variant="primary" size="md" onclick={save} disabled={saving || loading || !dto}>
        {saving ? "Saving…" : "Save"}
      </Button>
    </div>
  {/snippet}
</Modal>
