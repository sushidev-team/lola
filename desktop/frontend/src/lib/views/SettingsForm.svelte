<script lang="ts">
  import ThemeSwatch from "$lib/components/ThemeSwatch.svelte";
  import SystemThemePicker from "$lib/components/SystemThemePicker.svelte";
  import type { AppearancePreference } from "$lib/appearance";
  import Icon from "$lib/components/Icon.svelte";
  import { onMount, onDestroy, tick } from "svelte";
  import Modal from "$lib/components/Modal.svelte";
  import Tabs from "$lib/components/Tabs.svelte";
  import HelpText from "$lib/components/HelpText.svelte";
  import Disclosure from "$lib/components/Disclosure.svelte";
  import Button from "$lib/components/Button.svelte";
  import SettingsGroup from "$lib/components/SettingsGroup.svelte";
  import Chevron from "$lib/components/Chevron.svelte";
  import CheckboxOptions from "$lib/components/CheckboxOptions.svelte";
  import { PICKUP_FIELDS, LABEL_MATCHING, REPEAT_PICKUP, ENTRY_FORMAT } from "$lib/settingsFields";
  import Checkbox from "$lib/components/Checkbox.svelte";
  import AgentModelFields from "$lib/components/AgentModelFields.svelte";
  import PresetInput from "$lib/components/PresetInput.svelte";
  import PathInput from "$lib/components/PathInput.svelte";
  import { POLL_INTERVALS, BRANCH_PREFIXES, BASE_FLAGS } from "$lib/settingPresets";
  import Select from "$lib/components/Select.svelte";
  import ReviewProfiles from "$lib/components/ReviewProfiles.svelte";
  import SSHHostSettings from "$lib/components/ssh/SSHHostSettings.svelte";
  import SandboxSettings from "$lib/components/SandboxSettings.svelte";
  import HubSettings, { CLAIMS_LABEL, POLL_LABEL, hubClaimsError, hubPollError } from "$lib/components/hub/HubSettings.svelte";
  import TeamLock from "$lib/components/TeamLock.svelte";
  import QRCode from "$lib/components/QRCode.svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { overlayClose } from "$lib/overlayClose";
  import { deepEqual } from "$lib/deepEqual";
  import { ConfigService, LinearService } from "@bindings/desktop";
  import type { ReviewKindDTO } from "@bindings/desktop";
  import type { SettingsDTO, LinearOption, LinearKeyStatusDTO, ConnectCodeDTO, TeamPolicyDTO } from "@bindings/desktop/models";
  import { linesToText, splitLines, cleanLines } from "$lib/lines";
  import { appearance, THEME_IDS, type ThemeId } from "$lib/theme-runtime.svelte";
  import { terms } from "$lib/terms.svelte";

  // The bindings hand back a class instance, which $state does NOT deep-proxy —
  // copy it into a plain object so the segmented controls and pickers below
  // actually re-render when they are clicked.
  let dto = $state<SettingsDTO | null>(null);
  let loading = $state(true);
  let loadError = $state("");
  let saving = $state(false);
  const globalCapError = $derived(dto && (!Number.isInteger(dto.globalCap) || dto.globalCap < 1) ? "Enter a whole number of at least 1." : "");
  const projectCapError = $derived(dto && (!Number.isInteger(dto.concurrencyCap) || dto.concurrencyCap < 0) ? "Enter a whole number of 0 or more." : "");
  // The Team hub tab's number fields: a cleared one is NaN, which JSON sends as
  // null and the daemon would skip, so the save reports success with the old
  // value. The same gate as the caps blocks the save instead.
  const hubClaimsErr = $derived(dto ? hubClaimsError(dto.hubMaxClaimsPerDay) : "");
  const hubPollErr = $derived(dto ? hubPollError(dto.hubPollSeconds) : "");
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
  // The SSH host form saves on its own, with its own button, so it reports its own
  // unsaved edits: a close must not silently drop them either.
  let sshHostDirty = $state(false);
  // The Team hub tab saves at once, but a trusted key being typed or a number
  // that is not valid yet is a DRAFT there: it reports itself here so a close
  // asks first and Save stops at it instead of dropping it.
  let hubDirty = $state(false);
  const dirty = $derived.by(() => sshHostDirty || hubDirty || (dto && loaded ? !deepEqual($state.snapshot(dto), $state.snapshot(loaded)) : false));

  /** Reset the dirty baseline to the current DTO — after load and after a migrate. */
  function markPristine() {
    loaded = dto ? ($state.snapshot(dto) as SettingsDTO) : null;
  }

  const TABS = [
    { id: "defaults", label: "General", group: "Workspace" },
    { id: "project", label: "Project defaults", group: "Workspace" },
    { id: "appearance", label: "Appearance", group: "Workspace" },
    { id: "sandbox", label: "Sandbox", group: "Workspace" },
    { id: "linear", label: "Linear", group: "Connections" },
    { id: "notify", label: "Notifications", group: "Connections" },
    { id: "sshhosts", label: "SSH hosts", group: "Connections" },
    { id: "remote", label: "Phone access", group: "Connections" },
    { id: "hub", label: "Team hub", group: "Connections" },
    { id: "review", label: "Review", group: "Automation" },
    { id: "assistants", label: "Assistants", group: "Automation" },
  ];
  // "Summaries" and "Status interpretation" were two tabs; old deep links land
  // on the one tab that holds both.
  const TAB_ALIASES: Record<string, string> = { brain: "assistants", interpreter: "assistants" };

  // Every tab body is an explicit `{:else if tab === …}` branch with no catch-all,
  // so an unknown deep-link id would render a blank pane. Clamp it to a real tab.
  const linked = TAB_ALIASES[nav.overlayTab] ?? nav.overlayTab;
  let tab = $state(TABS.some((t) => t.id === linked) ? linked : "defaults");

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

  // --- remote ([remote], the phone listener) --------------------------------
  //
  // bind is either one of config.RemoteBinds or an IP LITERAL, so the picker
  // alone cannot express every valid value. That is not a nicety: a form that
  // offered only the keywords would rewrite a configured literal to a keyword on
  // the next save of any unrelated tab, silently rebinding the daemon to a
  // different set of interfaces. So a value the daemon accepts but the picker
  // cannot offer switches the row to a text input instead of being coerced.
  let remoteBinds = $state<string[]>([]);

  async function loadRemoteBinds() {
    if (remoteBinds.length) return;
    try {
      remoteBinds = (await ConfigService.RemoteBinds()) ?? [];
    } catch {
      remoteBinds = []; // the row falls back to plain text entry, never a dead end
    }
  }

  // The sentinel is not a bind value; it only tells the Select to hand the row
  // over to the text input.
  const BIND_LITERAL = "__literal";

  const BIND_HELP: Record<string, string> = {
    off: "No listener",
    localhost: "This Mac only",
    lan: "Private network",
    all: "All interfaces",
  };

  // True when the persisted value is a literal the picker cannot show. Guarded
  // on remoteBinds being loaded, so the row does not flip to text for a split
  // second before the keywords arrive.
  const bindIsLiteral = $derived(!!dto && remoteBinds.length > 0 && !remoteBinds.includes(dto.remoteBind));

  // Set when the user picks "IP literal" for a value that is still a keyword —
  // otherwise choosing it would immediately re-derive back to the picker.
  let bindLiteralPinned = $state(false);
  const bindShowsLiteral = $derived(bindIsLiteral || bindLiteralPinned);

  function onBindChange(v: string) {
    if (!dto) return;
    if (v === BIND_LITERAL) {
      bindLiteralPinned = true;
      // Clear a keyword so the field is ready to type into; an existing literal
      // is kept, since re-picking the same mode must not discard it.
      if (remoteBinds.includes(dto.remoteBind)) dto.remoteBind = "";
      return;
    }
    bindLiteralPinned = false;
    dto.remoteBind = v;
  }

  async function loadWorkspaceLabels(retry = false) {
    if (wsLoading || (wsRequested && !retry)) return;
    wsErr = "";
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
  // [defaults] key. `save` persists the appearance preference through its own
  // service method after SaveSettings succeeds.

  // The flavor that is actually in config.toml. `appearance.id` is the LIVE one,
  // which the preview moves around; this is the baseline it snaps back to.
  let savedAppearance = appearance.snapshot();
  let themePreference = $state<AppearancePreference>(appearance.snapshot());
  const themeId = $derived(themePreference.theme);

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
    previewAppearance({ ...themePreference, theme: id, followSystem: false });
  }

  function previewAppearance(value: AppearancePreference) {
    themePreference = value;
    appearance.preview(value);
  }

  /** Undo an uncommitted preview. A no-op once save has moved the baseline. */
  function revertTheme() {
    appearance.preview(savedAppearance);
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
    } else if (id === "review") {
      void loadReviewKinds();
    } else if (id === "remote") {
      void loadRemoteBinds();
    } else if (id === "defaults") {
      void loadTeamLimits();
    }
  }

  function selectTab(id: string) {
    tab = id;
    lazyLoadFor(id);
  }
  let hubVisited = $state(false);
  $effect(() => {
    if (tab === "hub") hubVisited = true;
  });

  const statusBinaries = new Map<string, string>();

  // --- team policy ----------------------------------------------------------
  //
  // Which settings the daemon's verified team policy LOCKS (ConfigService.
  // TeamPolicy, one hubStatus round-trip). A locked control is drawn through
  // <TeamLock>: disabled, with the team's value. The form still loads and saves
  // config.toml as before; the overlay never reaches the file. A down or older
  // daemon answers "nothing locked", and a failure here never blocks the form.
  let teamPolicy = $state<TeamPolicyDTO | null>(null);
  async function loadTeamPolicy() {
    try {
      teamPolicy = await ConfigService.TeamPolicy();
    } catch {
      teamPolicy = null;
    }
  }
  const lockFor = (field: string) =>
    teamPolicy?.applied ? (teamPolicy.locked ?? []).find((l) => l.field === field) ?? null : null;
  // max_concurrency is a CEILING, not a lock: the daemon runs min(local, team),
  // so a lower local value still counts. The field stays editable; the team's
  // value is a hint.
  // Read as ceilings whatever an older desktop build's DTO says.
  const ceilingFor = (field: string) => {
    const l = lockFor(field);
    return l ? { ...l, ceiling: true } : null;
  };
  const capCeiling = $derived(ceilingFor("globalCap"));
  const budgetCeiling = $derived(ceilingFor("budgetDaily"));
  const mergeQueueLock = $derived(lockFor("mergeQueue"));

  // --- team-governed limits ([budget].daily_tokens, [merge_queue].enabled) ---
  //
  // Not SettingsDTO fields: they save AT ONCE (ConfigService.SaveTeamLimits),
  // so the team policy's lock and ceiling sit over a real control and the
  // overlay's Save never carries a stale copy of them.
  let limits = $state<{ budgetDaily: number; mergeQueue: boolean } | null>(null);
  let budgetDraft = $state<number | null>(null);
  let limitsBusy = $state(false);
  let limitsMsg = $state("");
  let limitsErr = $state("");
  const budgetValue = $derived(budgetDraft ?? limits?.budgetDaily ?? 0);
  const budgetError = $derived(!Number.isInteger(budgetValue) || budgetValue < 0 ? "Enter a whole number of 0 or more." : "");

  async function loadTeamLimits() {
    try {
      limits = { ...(await ConfigService.TeamLimits()) };
      limitsErr = "";
    } catch {
      limits = null; // an older daemon build: the row explains itself
    }
  }

  async function saveTeamLimits(patch: Partial<{ budgetDaily: number; mergeQueue: boolean }>) {
    if (!limits || limitsBusy) return;
    const next = { ...limits, ...patch };
    limitsBusy = true;
    limitsMsg = "";
    limitsErr = "";
    try {
      await ConfigService.SaveTeamLimits(next);
      limits = next;
      budgetDraft = null;
      limitsMsg = "Saved";
    } catch (err) {
      limitsErr = String(err).replace(/^Error:\s*/, "");
    } finally {
      limitsBusy = false;
    }
  }

  function commitBudget() {
    if (budgetDraft === null || budgetError || budgetDraft === limits?.budgetDaily) return;
    void saveTeamLimits({ budgetDaily: budgetDraft });
  }
  // The review_providers lock sets only WHICH kinds are enabled; each kind still
  // runs with its local command, author, model and publishing settings (a
  // locked-on kind with no local entry runs on defaults, and a bot-watch with
  // no author stays off). So only the Enabled toggles are locked: details stay
  // editable and drawn for every provider, and Add provider stays, because
  // adding the local entry is how a member fills a missing author or command.
  const reviewLock = $derived(lockFor("reviewProviders"));

  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder";
  const formId = $props.id();
  let pollingOpen = $state(false);
  const rowCls = "settings-field grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] items-baseline gap-x-6 gap-y-1.5";
  const rowTopCls = "settings-field grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] items-start gap-x-6 gap-y-1.5";
  const hintCls = "mt-1 block text-sm text-faint";



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
    void loadTeamPolicy();
  });

  async function save() {
    if (!dto || saving) return;
    if (globalCapError || projectCapError) {
      tab = "defaults";
      await tick();
      document.getElementById(`${formId}-${globalCapError ? "global" : "project"}-cap`)?.focus();
      return;
    }
    if (hubDirty) {
      // A hub draft (a key being typed, a number not valid yet) never goes out
      // with this Save and is never dropped by it: show it instead.
      tab = "hub";
      await tick();
      document.querySelector<HTMLElement>("[data-hub-unsaved]")?.focus();
      return;
    }
    if (hubClaimsErr || hubPollErr) {
      tab = "hub";
      await tick();
      document.querySelector<HTMLElement>(`input[aria-label="${hubClaimsErr ? CLAIMS_LABEL : POLL_LABEL}"]`)?.focus();
      return;
    }
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
      // [ui] is written on its own path, not as a SettingsDTO field.
      // Sequenced AFTER the settings write so a payload the daemon rejects can
      // never leave a theme change behind on disk.
      //
      // Through appearance.commit, not ConfigService.SetTheme directly: the
      // localStorage cache that lets the next launch paint this flavor on the
      // first frame is written there. Calling the binding straight left the
      // cache on the OLD flavor, so the very next launch — the one right after
      // the user changed the theme — flashed the previous colours.
      if (!deepEqual(themePreference, savedAppearance)) {
        const onlyFixedTheme = !themePreference.followSystem && !savedAppearance.followSystem &&
          themePreference.lightTheme === savedAppearance.lightTheme && themePreference.darkTheme === savedAppearance.darkTheme;
        await appearance.commit(onlyFixedTheme ? themeId : $state.snapshot(themePreference));
        savedAppearance = { ...themePreference };
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
  // The kind catalog comes from the BACKEND (ConfigService.ReviewKinds), never a
  // hardcoded array here: which kinds exist, what each is called, and which
  // fields it has are config-side facts, and a second copy in TypeScript is a
  // copy that drifts the first time a review agent is added.
  //
  // Until it lands the tab shows a loading line rather than the providers,
  // because EVERY per-kind predicate below is false without it: a watch would be
  // drawn with a Timeout and a github transport (which validation then rejects,
  // with the save failing for no visible reason) and without the Author its
  // whole configuration is. Half a form is worse than a moment of nothing.
  let reviewKinds = $state<ReviewKindDTO[]>([]);
  let reviewKindsError = $state("");

  async function loadReviewKinds() {
    try {
      reviewKinds = (await ConfigService.ReviewKinds()) ?? [];
      reviewKindsError = reviewKinds.length ? "" : "the review provider catalog came back empty";
    } catch (err) {
      reviewKinds = [];
      reviewKindsError = String(err);
    }
  }

  const kindIds = () => reviewKinds.map((k) => k.kind);
  const kindMeta = (kind: string) => reviewKinds.find((k) => k.kind === kind);
  const kindLabel = (kind: string) => kindMeta(kind)?.label ?? kind;
  // Presentation names only; the backend still owns kind membership and fields.
  const PROVIDER_NAMES: Record<string, string> = {
    "claude-session": "Claude", "codex-session": "Codex", "opencode-session": "OpenCode",
    "coderabbit-cli": "CodeRabbit CLI", "custom-cli": "Custom CLI",
    "coderabbit-watch": "CodeRabbit bot", "bot-watch": "Review bot",
  };
  const providerName = (kind: string) => PROVIDER_NAMES[kind] ?? kindLabel(kind).split(" — ")[0];
  const TRANSPORTS = ["lola", "github", "linear"];
  // Copies of internal/config's resolve-time defaults, used ONLY to seed a
  // newly-added provider (the backend re-applies them on load either way).
  // Pinned against the Go source by a test, like the theme ids and the kind list
  // — an unpinned copy is one that drifts the first time a default changes.
  const DEFAULT_BASE_FLAG = "--base";
  const DEFAULT_PASS_TIMEOUT = 300;
  const DEFAULT_AGENT_TIMEOUT = 900;
  const isWatch = (kind: string) => kindMeta(kind)?.watch ?? false;
  const isCLI = (kind: string) => kindMeta(kind)?.cli ?? false;
  const agentOf = (kind: string) => kindMeta(kind)?.agent ?? "";
  const needsCommand = (kind: string) => kindMeta(kind)?.requiresCommand ?? false;
  const needsAuthor = (kind: string) => kindMeta(kind)?.requiresAuthor ?? false;
  // Transports offered per kind: a watch forbids github (its feedback is
  // already on the PR).
  const transportsFor = (kind: string) => (isWatch(kind) ? ["lola", "linear"] : TRANSPORTS);
  // A provider may fall through to any OTHER pass kind (never itself / a watch).
  const fallbackFor = (kind: string) => kindIds().filter((k) => k !== kind && !isWatch(k));

  const providers = () => (dto?.reviewProviders ?? []) as any[];
  const providerOf = (kind: string) => providers().find((p) => p.provider === kind);
  const missingKinds = () => kindIds().filter((k) => !providerOf(k));

  function addProvider(kind: string) {
    if (!dto || providerOf(kind)) return;
    dto.reviewProviders = [
      ...providers(),
      {
        provider: kind,
        enabled: true,
        onPrOpen: !isWatch(kind),
        command: "",
        // Empty appends no base at all, so a cli kind starts with the default
        // flag every review CLI takes.
        baseFlag: isCLI(kind) ? DEFAULT_BASE_FLAG : "",
        // An agent pass reads the PR's files before it reports, so it needs
        // minutes where a CLI pass needs seconds.
        timeoutSeconds: agentOf(kind) ? DEFAULT_AGENT_TIMEOUT : DEFAULT_PASS_TIMEOUT,
        model: "",
        // Only the coderabbit watch has a bot to default to; the generic one
        // exists precisely to name a different one, and validation rejects it
        // empty while enabled.
        author: kind === "coderabbit-watch" ? "coderabbitai" : "",
        transports: ["lola"],
        // The github transport posts anchored, resolvable threads by default;
        // it degrades to one flat comment by itself when nothing can be anchored.
        githubInline: true,
        // Opt-in: a required lola/review status blocks merges until a review
        // has passed the PR's current head.
        githubStatus: false,
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
  let keyEditing = $state(false);
  let keyBusy = $state<"" | "validating" | "saving">("");
  let keyMsg = $state("");
  let keyMsgKind = $state<"good" | "bad">("good");

  async function editKey(editing: boolean) {
    keyInput = "";
    keyMsg = "";
    keyEditing = editing;
    await tick();
    document.getElementById(`${formId}-${editing ? "linear-key" : "replace-key"}`)?.focus();
  }

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
      keyEditing = false;
      await tick();
      document.getElementById(`${formId}-replace-key`)?.focus();
    } catch (e) {
      keyMsg = String(e);
      keyMsgKind = "bad";
    } finally {
      keyBusy = "";
    }
  }

  // --- connecting a phone (cmd=pairBegin) -----------------------------------
  //
  // The four values this hands over used to be copied by hand out of a log line
  // and a key file, which is four chances to transpose a character into a
  // failure that looks exactly like a wrong host. The daemon knows all four
  // about the listener it is actually running, so the app asks it and draws a
  // code.
  //
  // The whole thing is treated as a secret, because it is one: the code
  // CONTAINS the bearer key. So it is fetched only when a human presses the
  // button (never on tab open), it is held in memory and never persisted, the
  // key's characters need a second explicit press on top of that, and it is
  // dropped on hide, on leaving the tab and on closing the overlay. What that
  // buys is the thing an app can actually control — a code left on a screen, in
  // a screen share, or in a window someone walked past.
  let connect = $state<ConnectCodeDTO | null>(null);
  let connectBusy = $state(false);
  let connectErr = $state("");
  let connectKeyShown = $state(false);
  let copied = $state("");

  /**
   * How long a revealed code stays on screen.
   *
   * IT IS A CLOCK BECAUSE THE THING ON SCREEN IS A BEARER CREDENTIAL. Every
   * other control here is right — nothing is fetched on tab open, the reveal
   * takes a press, the key's characters take a second one, and it is dropped on
   * hide, on leaving the tab and on closing the overlay — but all of that
   * bounded the exposure to "until a human remembers". M2 bounds the equivalent
   * window to 90 seconds precisely because a displayed credential is a bearer
   * credential, and M1's is strictly longer-lived: no TTL, not single-use,
   * never zeroed. So the one thing this app can actually control — a code left
   * up in a share, a recording, or in front of someone walking past — gets the
   * same 90 seconds.
   *
   * Re-revealing costs one socket round trip: cmd=pairBegin reads in-memory
   * state, execs nothing and is idempotent.
   */
  const CONNECT_REVEAL_MS = 90_000;

  let connectUntil = $state(0);
  let connectNow = $state(0);
  const connectLeft = $derived(
    connectUntil > 0 ? Math.max(0, Math.ceil((connectUntil - connectNow) / 1000)) : 0,
  );
  let connectTimer: ReturnType<typeof setInterval> | undefined;

  // --- regenerating the key -------------------------------------------------
  //
  // Rolling the bearer key is milestone 1's ONLY revocation, and it is blunt:
  // every paired phone loses access at once, because every paired phone holds
  // the same key. The dialog says that plainly rather than offering it as
  // routine maintenance — M2's per-device revocation is the precise version.
  //
  // It goes through the shared `confirm` store like every other irreversible
  // action in this app, so a shortcut and a button ask the same way.
  let regenBusy = $state(false);
  let regenDone = $state("");

  function askRegenerate() {
    confirm.ask({
      title: "Regenerate the phone key?",
      body: "Every phone paired with this daemon loses access immediately.",
      detail:
        "Milestone 1 authenticates every phone with one shared key, so there is no way to revoke a single device. Each phone has to scan a new code afterwards.",
      confirmLabel: "Regenerate",
      onConfirm: () => void regenerateKey(),
    });
  }

  async function regenerateKey() {
    regenBusy = true;
    connectErr = "";
    regenDone = "";
    try {
      await ConfigService.RegenerateRemoteKey();
      // Whatever is on screen describes the OLD key, so it is dropped rather
      // than left looking valid — a stale code scans cleanly and is then
      // refused, which from the phone is indistinguishable from a bad read.
      hideConnect();
      regenDone = "New key in place. The listener was restarted; press Show code for the new one.";
    } catch (e) {
      connectErr = String(e);
    } finally {
      regenBusy = false;
    }
  }

  function stopConnectTimer() {
    if (connectTimer !== undefined) clearInterval(connectTimer);
    connectTimer = undefined;
  }

  async function revealConnect() {
    connectBusy = true;
    connectErr = "";
    try {
      connect = await ConfigService.ConnectCode();
      // Only a code that is actually on screen runs a clock. A `problem` answer
      // carries no key, and an error carries nothing at all.
      if (connect?.code) {
        connectNow = Date.now();
        connectUntil = connectNow + CONNECT_REVEAL_MS;
        stopConnectTimer();
        connectTimer = setInterval(() => {
          connectNow = Date.now();
          if (connectNow >= connectUntil) hideConnect();
        }, 1000);
      }
    } catch (e) {
      connect = null;
      connectErr = String(e);
    } finally {
      connectBusy = false;
    }
  }

  function hideConnect() {
    stopConnectTimer();
    connectUntil = 0;
    connect = null;
    connectErr = "";
    connectKeyShown = false;
    copied = "";
  }

  async function copyConnect(what: string, value: string) {
    try {
      await navigator.clipboard.writeText(value);
      copied = what;
      // A one-shot confirmation, not a lasting state: the point is to say the
      // press registered, and a "copied" that stays on screen stops meaning
      // anything after the second field.
      setTimeout(() => {
        if (copied === what) copied = "";
      }, 1500);
    } catch {
      copied = "";
    }
  }

  /** The first host is the one the code tells a phone to dial. */
  const connectHost = $derived(connect?.hosts?.[0] ?? "");

  // Leaving the tab drops it. Without this the code survives behind whatever
  // tab is opened next and comes back on screen when Remote is opened again —
  // a reveal nobody performed.
  $effect(() => {
    if (tab !== "remote" && connect) hideConnect();
  });

  // The overlay is mounted inside an {#if} in App.svelte, so closing it really
  // does destroy this component — but an interval outlives a component that
  // forgot to clear it, and this one calls hideConnect() on a $state.
  $effect(() => stopConnectTimer);

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
  <h3 class="settings-heading">{label}</h3>
{/snippet}

{#snippet connectRow(caption: string, shown: string, value: string)}
  <!-- `shown` and `value` are separate so the key can be masked on screen and
       still copy in full: a row that copies its own dots is worse than no copy
       button, and one that reveals to copy defeats the mask. -->
  <div class="grid grid-cols-[7rem_1fr_auto] items-center gap-2">
    <span class="text-sm text-faint">{caption}</span>
    <span class="truncate font-mono text-sm text-ink" title={caption}>{shown}</span>
    <Button size="xs" onclick={() => copyConnect(caption, value)}>
      {copied === caption ? "Copied" : "Copy"}
    </Button>
  </div>
{/snippet}

{#snippet areaRow(caption: string, value: string[] | null, onChange: (v: string[]) => void, placeholder = "", hint = "")}
  {@const fieldId = `${formId}-${caption.toLowerCase().replace(/[^a-z0-9]+/g, "-")}`}
  <div class={rowTopCls}>
    <span class="flex items-center gap-1.5 text-faint">
      <label for={fieldId}>{caption}</label>
      {#if hint && !hint.startsWith("one ")}<HelpText label={caption} detail={hint} />{/if}
    </span>
    <span>
      <textarea
        id={fieldId}
        class="{inputCls} block resize-y font-mono"
        aria-label={caption}
        aria-describedby={ENTRY_FORMAT[caption] ? `${fieldId}-format` : undefined}
        rows="3"
        spellcheck="false"
        {placeholder}
        value={linesToText(value)}
        oninput={(e) => onChange(splitLines(e.currentTarget.value))}
      ></textarea>
      {#if ENTRY_FORMAT[caption]}<span id={`${fieldId}-format`} class={hintCls}>{ENTRY_FORMAT[caption]}</span>{/if}
    </span>
  </div>
{/snippet}

{#snippet selectRow(caption: string, current: string, options: LinearOption[], onChange: (v: string) => void, anyLabel = "", hint = "")}
  <div class={rowCls}>
    <span class="text-faint">{caption}</span>
    <span>
      <Select aria-label={caption} value={current} onchange={(e) => onChange(e.currentTarget.value)}>
        {#if anyLabel}<option value="">{anyLabel}</option>{/if}
        {#if current && !options.some((o) => o.id === current)}<option value={current}>{current} (current value)</option>{/if}
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
    {@render selectRow(caption, current, wsLabels ?? [], onChange, "(none)", "Workspace label only.")}
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
        <span class={hintCls}>Workspace label only.</span>
      </span>
    </div>
  {/if}
{/snippet}

<Modal title="Settings" onClose={requestClose} width="880px" bodyClass="grid h-[65vh] grid-rows-[minmax(0,1fr)] overflow-hidden">
  {#if loading}
    <div class="py-10 text-center text-faint">loading settings…</div>
  {:else if loadError}
    <div class="py-10 text-center text-bad">{loadError}</div>
  {:else if dto}
    {@const d = dto}
    <div class="settings-layout grid min-h-0 grid-cols-[11.5rem_minmax(0,1fr)] overflow-hidden">
    <nav aria-label="Settings sections" class="settings-nav min-h-0 overflow-y-auto overscroll-contain border-r border-edge p-4">
      <Tabs tabs={TABS} active={tab} onSelect={selectTab} vertical />
    </nav>

    <!-- No size class: every field caption, control and option inherits the
         13px base. Only the faint explanations and micro-labels step down. -->
    <div role="region" aria-label="Settings content" class="settings-content min-h-0 min-w-0 overflow-y-auto overscroll-contain p-6">
      {#if tab === "defaults"}
        <section>
          {@render head("General")}
          <SettingsGroup title="Agent sessions">
            <AgentModelFields provider={d.agent || "claude"} providerLabel="Default agent" rowClass={rowCls}
              onProviderChange={(value) => { d.agent = value; }} />
            <label class={rowCls}>
              <span class="text-faint">Total running agents</span>
              <span>
                <TeamLock lock={capCeiling} version={teamPolicy?.version ?? 0}>
                  <input id={`${formId}-global-cap`} aria-label="Total running agents" class={inputCls} type="number" min="1" step="1" bind:value={d.globalCap}
                    aria-invalid={!!globalCapError} aria-describedby={`${formId}-global-cap-help`} />
                </TeamLock>
                <span id={`${formId}-global-cap-help`} class={hintCls} aria-live="polite">{globalCapError || "Maximum across all projects."}</span>
              </span>
            </label>
            <label class={rowCls}>
              <span class="text-faint">Agents per project</span>
              <span>
                <input id={`${formId}-project-cap`} aria-label="Agents per project" class={inputCls} type="number" min="0" step="1" bind:value={d.concurrencyCap}
                  aria-invalid={!!projectCapError} aria-describedby={`${formId}-project-cap-help`} />
                <span id={`${formId}-project-cap-help`} class={hintCls} aria-live="polite">{projectCapError || (d.concurrencyCap === 0 ? "Set a limit in each project." : "Default limit. Projects can override it.")}</span>
              </span>
            </label>
          </SettingsGroup>
          <SettingsGroup title="Limits" help="These two save at once. A team policy can cap the budget or set the merge queue.">
            {#snippet aside()}
              <span role="status" class="text-sm text-faint">{limitsBusy ? "Saving…" : limitsMsg}</span>
            {/snippet}
            {#if !limits}
              <p class="text-sm text-faint">Loading limits…</p>
            {:else}
              <div class={rowCls}>
                <label for={`${formId}-budget`} class="text-faint">Daily token budget</label>
                <span>
                  <TeamLock lock={budgetCeiling} version={teamPolicy?.version ?? 0}>
                    <input id={`${formId}-budget`} aria-label="Daily token budget" class={inputCls} type="number" min="0" step="1000" value={budgetValue}
                      aria-invalid={!!budgetError}
                      oninput={(e) => (budgetDraft = e.currentTarget.valueAsNumber)}
                      onchange={commitBudget}
                      onkeydown={(e) => { if (e.key === "Enter") commitBudget(); }} />
                  </TeamLock>
                  <span class={hintCls}>{budgetError || "Weighted tokens per day across all projects. 0 is no limit."}</span>
                </span>
              </div>
              <div class={rowCls}>
                <span class="text-faint">Merge queue</span>
                <TeamLock lock={mergeQueueLock} version={teamPolicy?.version ?? 0}>
                  <label class="flex cursor-pointer items-center gap-2">
                    <Checkbox checked={limits.mergeQueue} disabled={limitsBusy} onchange={() => void saveTeamLimits({ mergeQueue: !limits?.mergeQueue })} />
                    <span>Merge approved, green pull requests one at a time</span>
                  </label>
                </TeamLock>
              </div>
            {/if}
            {#if limitsErr}<p role="alert" class="text-sm text-bad">{limitsErr}</p>{/if}
          </SettingsGroup>
          <SettingsGroup title="Shells">
            <div class={rowCls}>
              <label for={`${formId}-temporary-directory`} class="text-faint">Temporary shell directory</label>
              <div><PathInput id={`${formId}-temporary-directory`} label="Temporary shell directory" placeholder="~" bind:value={d.temporaryDirectory} />
              <span class={hintCls}>Used for shells outside a project. Empty uses your home folder.</span></div>
            </div>
          </SettingsGroup>
          <div class="mt-6 border-t border-edge pt-4">
              <Button aria-expanded={pollingOpen} aria-controls={`${formId}-polling-frequency`} onclick={() => { pollingOpen = !pollingOpen; }}>
                <Chevron open={pollingOpen} />Polling frequency
              </Button>
              <div id={`${formId}-polling-frequency`} class="mt-3" hidden={!pollingOpen}>
                <div class={rowCls}>
                  <span class="text-faint">Poll interval</span>
                  <PresetInput label="Poll interval" value={d.pollInterval} options={POLL_INTERVALS} onChange={(v) => { d.pollInterval = v; }} />
                </div>
              </div>
          </div>
        </section>
      {:else if tab === "linear"}
        <section>
          {@render head("Linear")}
          <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
            <div class="min-w-0">
              {#if !keyStatus}
                <span class="text-faint">Checking…</span>
              {:else if keyStatus.resolvable}
                <span class="text-good"><Icon name="check" size={14} /> Key configured</span>
              {:else if keyStatus.configured}
                <span class="text-bad"><Icon name="x" size={14} /> Key configured but unreadable</span>
                <span class="mt-1 block text-sm text-faint">{keyStatus.source} — {keyStatus.detail}</span>
              {:else}
                <span class="text-warn"><Icon name="triangle-alert" size={14} /> No key configured</span>
                <span class="mt-1 block text-sm text-faint">Every poll will fail until one is set.</span>
              {/if}
            </div>
            {#if keyStatus?.configured && !keyEditing}
              <Button id={`${formId}-replace-key`} variant="secondary" onclick={() => editKey(true)}>Replace key…</Button>
            {/if}
          </div>

          {#if !keyStatus?.configured || keyEditing}
            <div class="space-y-2">
              <div class={rowTopCls}>
                <label for={`${formId}-linear-key`} class="text-faint">{keyStatus?.configured ? "New API key" : "API key"}</label>
                <span>
                  <input
                    id={`${formId}-linear-key`}
                    class="{inputCls} font-mono"
                    type="password"
                    autocomplete="off"
                    aria-label="Linear API key"
                    aria-describedby={`${formId}-linear-key-help`}
                    placeholder="lin_api_…"
                    bind:value={keyInput}
                    disabled={keyBusy !== ""}
                    oninput={() => (keyMsg = "")}
                  />
                  <span id={`${formId}-linear-key-help`} class={hintCls}>Create a personal key in Linear → Settings → Security &amp; access → API keys.</span>
                </span>
              </div>
              <div class={rowCls}>
                <span></span>
                <span class="flex flex-wrap items-center gap-2">
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
                  {#if keyStatus?.configured}
                    <Button disabled={keyBusy !== ""} onclick={() => editKey(false)}>Cancel replacement</Button>
                  {/if}
                </span>
              </div>
            </div>
          {/if}
          <p role="status" class="mt-2 text-sm {keyMsgKind === 'good' ? 'text-good' : 'text-bad'}">{keyMsg}</p>
        </section>
      {:else if tab === "project"}
        <section>
          {@render head("Project defaults")}
          <p class="mb-6 text-sm text-faint">Shared defaults. Override them in individual projects.</p>
          <SettingsGroup title="Worktree setup">
            <div class={rowCls}>
              <span class="text-faint">Branch prefix</span>
              <PresetInput label="Branch prefix" value={d.branchPrefix} options={BRANCH_PREFIXES} onChange={(v) => { d.branchPrefix = v; }} />
            </div>
            {@render areaRow("Symlinks", d.symlinks, (v) => { d.symlinks = v; }, ".env\nnode_modules", "one path per line")}
            {@render areaRow("Post-create", d.postCreate, (v) => { d.postCreate = v; }, "npm install", "one command per line")}
            {@render areaRow("Env", d.env, (v) => { d.env = v; }, "KEY=value", "one KEY=value per line")}

          </SettingsGroup>
          <SettingsGroup title="Issue pickup">
            {#if wsLoading}
              <p role="status" class="text-sm text-faint">Loading workspace labels…</p>
            {:else if wsErr}
              <div role="alert" class="rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">
                <p>Couldn’t load workspace labels. Retry or enter IDs manually below.</p>
                <HelpText label="workspace labels error" detail={wsErr} />
                <Button size="sm" onclick={() => void loadWorkspaceLabels(true)}>Retry workspace labels</Button>
              </div>
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
                  <CheckboxOptions label="Match labels" options={wsLabels ?? []} selected={d.matchLabels}
                    onChange={(value) => { d.matchLabels = value; }} />
                  <span class={hintCls}>Workspace labels only.</span>
                </span>
              </div>
            {:else}
              {@render areaRow(
                "Match labels",
                d.matchLabels,
                (v) => { d.matchLabels = v; },
                "one UUID per line",
                "Workspace labels only.",
              )}
            {/if}

            {@render selectRow(
              PICKUP_FIELDS.matchMode,
              d.matchMode,
              LABEL_MATCHING,
              (v) => { d.matchMode = v; },
            )}
            {@render selectRow(
              PICKUP_FIELDS.dedupMode,
              d.dedupMode,
              REPEAT_PICKUP,
              (v) => { d.dedupMode = v; },
            )}
            {#if d.dedupMode === "label"}
              {@render labelRow(PICKUP_FIELDS.onSentSetLabel, d.onSentSetLabel, (v) => { d.onSentSetLabel = v; })}
            {/if}
            {@render labelRow("Blocked label", d.blockedLabelId, (v) => { d.blockedLabelId = v; })}
            {#if sortKeys.length}
              <div class={rowTopCls}>
                <span class="flex items-center gap-1.5 text-faint">
                  <span>Priority sort</span>
                  <HelpText label="priority order" detail="Numbers show the tie-break order. Click a key to add or remove it. Empty uses priority, then creation time." />
                </span>
                <span>
                  <div class="space-y-1 rounded border border-edge p-2">
                    {#each sortKeys as k (k)}
                      {@const rank = (d.prioritySort ?? []).indexOf(k)}
                      <Button block onclick={() => toggleSortKey(k)}>
                        <span
                          class="w-4 shrink-0 text-center font-mono {rank >= 0 ? 'text-accent-ink' : 'text-faint/40'}"
                        >{rank >= 0 ? rank + 1 : "·"}</span>
                        <span>{k === "createdAt" ? "Creation time" : k === "priority" ? "Priority" : k}</span>
                        <span class="text-faint">{SORT_KEY_HELP[k] ?? ""}</span>
                      </Button>
                    {/each}
                  </div>
                  <span class={hintCls}>Click to order.</span>
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
          </SettingsGroup>
          <SettingsGroup title="PR follow-up">
            <label class="flex items-center gap-2">
              <Checkbox checked={!!d.autoPRComments} onchange={() => { d.autoPRComments = !d.autoPRComments; }} />
              Automatically address and resolve PR comments
            </label>
            <p class="text-sm text-faint">When idle, the agent verifies new review comments, pushes fixes and resolves only addressed threads. Projects can override this default. Existing review-provider handoffs are configured separately.</p>
          </SettingsGroup>
        </section>
      {:else if tab === "notify"}
        <section>
          {@render head("Notifications")}
          <SettingsGroup title="Desktop">
            <label class="flex cursor-pointer items-center gap-2">
              <Checkbox bind:checked={d.notifyDesktop} />
              <span>Desktop notifications</span>
            </label>
          </SettingsGroup>
          <SettingsGroup title="Slack">
            <label class={rowTopCls}>
              <span class="text-faint">Slack webhook env</span>
              <div>
                <input class={inputCls} type="text" placeholder="LOLA_SLACK_WEBHOOK" bind:value={d.slackWebhookEnv} />
                <span class={hintCls}>Variable name, not URL.</span>
              </div>
            </label>
          </SettingsGroup>
        </section>
      {:else if tab === "assistants"}
        <section>
          {@render head("Assistants")}
          <p class="mb-6 text-sm text-faint" data-assistants-cost>Helpers that run beside your agents. Both are off by default: each one makes extra model calls, and those use tokens.</p>
          <SettingsGroup title="Session summaries" description="Claude writes a short summary when a session escalates or is approved.">
            <label class="flex cursor-pointer items-center gap-2">
              <Checkbox bind:checked={d.brainEnabled} />
              <span>Summarize sessions</span>
            </label>
          </SettingsGroup>
          {#if d.brainEnabled}
          <SettingsGroup title="When to summarize">
              <label class="flex cursor-pointer items-center gap-2">
                <Checkbox bind:checked={d.brainSummarizeEscalation} />
                <span>Summarize on escalation</span>
              </label>
              <label class="flex cursor-pointer items-center gap-2">
                <Checkbox bind:checked={d.brainSummarizeApproved} />
                <span>Summarize on approved</span>
              </label>
          </SettingsGroup>
          <SettingsGroup title="Model and limits">
            <AgentModelFields provider="claude" model={d.brainModel} rowClass={rowCls}
              onModelChange={(value) => { d.brainModel = value; }} />
            <label class={rowCls}>
              <span class="text-faint">Timeout (s)</span>
              <input class={inputCls} type="number" min="0" bind:value={d.brainTimeout} />
            </label>
          </SettingsGroup>
          {/if}
          <SettingsGroup title="Status interpretation" description="An agent reads each session’s screen and adds display-only estimates and headlines.">
            <label class="flex cursor-pointer items-center gap-2">
              <Checkbox bind:checked={d.statusAgentEnabled} />
              <span>Interpret session status</span>
            </label>
          </SettingsGroup>
          {#if d.statusAgentEnabled}
          <SettingsGroup title="Agent and model">
            <AgentModelFields provider={d.statusAgentAgent || "claude"} model={d.statusAgentModel ?? ""} rowClass={rowCls}
              onProviderChange={(value) => {
                statusBinaries.set(d.statusAgentAgent || "claude", d.statusAgentBin);
                d.statusAgentAgent = value;
                d.statusAgentBin = statusBinaries.get(value) ?? "";
              }}
              onModelChange={(value) => { d.statusAgentModel = value; }} />
            <Disclosure label="Executable override">
              <div class="mt-3 {rowCls}">
                <span class="text-faint">Binary</span>
                <PresetInput label="Binary" value={d.statusAgentBin} pathKind="file"
                  options={[{ value: "", label: `Default (${d.statusAgentAgent || "claude"} on PATH)` }, { value: d.statusAgentAgent || "claude", label: d.statusAgentAgent || "claude" }]}
                  onChange={(v) => { d.statusAgentBin = v; }} placeholder={`/path/to/${d.statusAgentAgent || "claude"}`} />
              </div>
            </Disclosure>
          </SettingsGroup>
          <SettingsGroup title="Frequency and limits">
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
          </SettingsGroup>
          <SettingsGroup title="Evidence">
            <label class={rowCls}>
              <span class="text-faint">Min confidence</span>
              <input class={inputCls} type="number" min="0" max="1" step="0.05" bind:value={d.statusAgentMinConfidence} />
            </label>
            <label class="flex cursor-pointer items-center gap-2">
              <Checkbox bind:checked={d.statusAgentIncludeTranscript} />
              <span>Include transcript tail</span>
            </label>
          </SettingsGroup>
          {/if}
        </section>
      {:else if tab === "hub"}
        <!-- Drawn below, outside this chain: it stays mounted once visited. -->
      {:else if tab === "sandbox"}
        <section>
          {@render head("Sandbox")}
          <SandboxSettings formId={`${formId}-sandbox`} />
        </section>
      {:else if tab === "sshhosts"}
        <SSHHostSettings onDirty={(v) => (sshHostDirty = v)} />
      {:else if tab === "remote"}
        <section>
          {@render head("Phone access")}
          <SettingsGroup title="Connection" description="Let the Lola mobile app access sessions on this Mac.">
            <label class="flex cursor-pointer items-center gap-2">
              <Checkbox bind:checked={d.remoteEnabled} />
              <span>Enabled</span>
            </label>
            <div class={rowCls}>
              <span class="text-faint">Bind</span>
              <span class="grid gap-1">
                <Select
                  aria-label="Bind"
                  value={bindShowsLiteral ? BIND_LITERAL : d.remoteBind}
                  onchange={(e) => onBindChange(e.currentTarget.value)}>
                  {#each remoteBinds as b (b)}<option value={b}>{b}</option>{/each}
                  <option value={BIND_LITERAL}>IP literal…</option>
                </Select>
                {#if !bindShowsLiteral && BIND_HELP[d.remoteBind]}
                  <span class="text-sm text-faint">{BIND_HELP[d.remoteBind]}</span>
                {/if}
              </span>
            </div>
            {#if bindShowsLiteral}
              <label class={rowCls}>
                <span class="text-faint">Address</span>
                <input
                  class="{inputCls} font-mono"
                  type="text"
                  placeholder="192.168.1.20"
                  bind:value={d.remoteBind} />
              </label>
              <div class="copy text-sm text-faint"><HelpText label="bind address" summary="IP address, not hostname." detail="An IP literal, not a hostname — a name cannot be resolved at config-load time without turning a config read into a
                network call, so the daemon rejects one." /></div>
            {/if}
            <label class={rowCls}>
              <span class="text-faint">Port</span>
              <input class={inputCls} type="number" min="0" max="65535" bind:value={d.remotePort} />
            </label>

          </SettingsGroup>
          <SettingsGroup title="Network sharing">
            <!-- The bind rail's one hole. Two keys have to agree — this AND a
                 non-loopback bind — so a config that merely says "lan" still
                 binds loopback. It is a config key rather than an environment
                 variable because the daemon is normally started by the restart
                 button a few inches from here, which cannot set one. -->
            <div class="pt-1">
              <label class="flex cursor-pointer items-center gap-2">
                <Checkbox bind:checked={d.remoteInsecureLan} />
                <span>Allow a LAN bind</span>
              </label>
              <div class="ml-6"><HelpText label="Allow a LAN bind" summary="Shared key sent unencrypted." detail="Allows a physical phone to reach the configured network address. This development mode sends the shared key in the clear. The Simulator does not need it." /></div>
            </div>

            <!-- A DISCLOSURE rather than a convenience, which is why it is off
                 by default and why the copy leads with what it announces. What
                 it buys is RECONNECTION, not pairing: the key and the pin
                 already work on any network, and only the address the phone
                 stored at pairing time goes stale. -->
            <div class="pt-1">
              <label class="flex cursor-pointer items-center gap-2">
                <Checkbox bind:checked={d.remoteAdvertise} />
                <span>Advertise on the local network</span>
              </label>
              <div class="ml-6"><HelpText label="Advertise on the local network" summary="Let paired phones find this Mac." detail="Announces remote-control availability to nearby devices so paired phones can reconnect on new networks. The announcement contains a version, but no hostname or access key." /></div>
            </div>

            <!-- The ACTIVE session's dev servers, republished. Off by default
                 like the two above, and worth having because the alternative is
                 `--host 0.0.0.0` in every project: permanent, well-known and on
                 every network, where this is temporary, random and scoped to
                 one address lola discovered itself. -->
            <div class="pt-1">
              <label class="flex cursor-pointer items-center gap-2">
                <Checkbox bind:checked={d.remoteDevForward} />
                <span>Publish dev servers of the active session</span>
              </label>
              <div class="ml-6"><HelpText label="Publish dev servers of the active session" summary="Dev servers accessible on your network." detail="Shares the active session’s local dev servers on one private interface and a temporary port. Anyone on that network can reach them while the session is active." /></div>
            </div>
          </SettingsGroup>
          <SettingsGroup title="Pairing">
            <div class="flex items-center justify-between gap-3">
              <div>
                <div class="copy text-sm text-faint"><HelpText label="connect code" summary="Scan with the mobile app." detail="The code contains the running listener’s address and credentials. Save connection settings before generating a code." /></div>
              </div>
              {#if connect || connectErr}
                <div class="flex shrink-0 items-center gap-2">
                  {#if connectLeft > 0}
                    <!-- Says the clock exists, so the reveal disappearing is a
                         rule rather than a glitch. `num` keeps the countdown
                         from reflowing the button beside it. -->
                    <span class="num text-sm text-faint">Hides in {connectLeft}s</span>
                  {/if}
                  <Button size="sm" onclick={hideConnect}>Hide</Button>
                </div>
              {:else}
                <Button variant="primary" size="sm" loading={connectBusy} onclick={revealConnect}>Show code</Button>
              {/if}
            </div>

            <!-- Regenerating sits BESIDE the reveal rather than inside it: it is
                 not a step in connecting a phone, it is the undo for having
                 connected one. `ghost` keeps it quieter than Show code, which is
                 the action someone actually came here for. -->
            <div class="mt-2 flex items-center gap-3">
              <Button size="sm" loading={regenBusy} onclick={askRegenerate}>Regenerate key</Button>
              <HelpText label="regenerating the key" summary="Disconnects all phones." detail="Reconnect each phone using the new code." />
            </div>
            {#if regenDone}
              <p class="copy mt-2 text-sm text-good">{regenDone}</p>
            {/if}

            {#if connectErr}
              <p class="copy mt-3 text-sm text-bad">{connectErr}</p>
            {:else if connect && connect.problem}
              <p class="copy mt-3 text-sm text-warn">{connect.problem}</p>
            {:else if connect}
              <HelpText label="code privacy" summary="Secret code · hides after 90s."
                detail="Anyone with this code can control your coding agents. Copying also puts the access key on the clipboard, which may sync to your other devices. Copy something else afterward." />
              <div class="mt-3 flex flex-wrap items-start gap-4">
                <QRCode value={connect.code} size={228} label="Connect code" />
                <div class="min-w-[16rem] flex-1 space-y-1">
                  {@render connectRow("Address", connectHost, connectHost)}
                  {@render connectRow("Port", String(connect.port), String(connect.port))}
                  {@render connectRow("SPKI pin", connect.pin, connect.pin)}
                  {@render connectRow(
                    "Access key",
                    connectKeyShown ? connect.key : "•".repeat(Math.min(connect.key.length, 32)),
                    connect.key,
                  )}
                  <div class="flex flex-wrap gap-2 pt-2">
                    <Button size="sm" onclick={() => (connectKeyShown = !connectKeyShown)}>
                      {connectKeyShown ? "Hide key" : "Show key"}
                    </Button>
                    <!-- The label names what it hands over. The code CONTAINS
                         the key, so a button reading "Copy code" beside a key
                         row still drawn as dots implied the mask was a barrier
                         to more than shoulder-surfing. It is not: the copy is
                         the whole credential. -->
                    <Button size="sm" onclick={() => copyConnect("code", connect?.code ?? "")}>
                      {copied === "code" ? "Copied" : "Copy code (contains the key)"}
                    </Button>
                  </div>
                </div>
              </div>
              {#if connect.hosts && connect.hosts.length > 1}
                <HelpText label="additional addresses" summary="Fallback addresses included." detail={connect.hosts.slice(1).join(", ")} />
              {/if}
              {#if connect.insecure}
                <HelpText label="shared-key access" summary="One key for all phones." detail="Individual phones cannot be revoked separately. Regenerate the key to disconnect them all." />
              {/if}
            {/if}
          </SettingsGroup>


        </section>
      {:else if tab === "appearance"}
        <section>
          {@render head("Appearance")}
          <!-- Applies at once and never joins the form's save: it is this viewer's own preference,
               in localStorage, and config.toml and the daemon never read it. A session you picked
               a view for keeps it. -->
          <SettingsGroup title="Session view">
          <div class="grid gap-2" data-open-sessions-in>
            <span class="text-sm text-faint">Open sessions in</span>
            <div class="flex w-fit gap-0.5 rounded-md border border-edge/60 bg-canvas p-0.5" role="group" aria-label="Open sessions in">
              <Button size="xs" selected={terms.globalView === "conversation"} onclick={() => terms.setGlobalView("conversation")}>Conversation</Button>
              <Button size="xs" selected={terms.globalView === "terminal"} onclick={() => terms.setGlobalView("terminal")}>Terminal</Button>
            </div>
          </div>
          </SettingsGroup>
          <SettingsGroup title="Theme">
          <div>
            <SystemThemePicker value={themePreference} {themeIds} onchange={previewAppearance} />
          </div>
          <p class="text-sm text-faint">Save to keep the preview, or cancel to restore your theme.</p>
          <!-- Grid, not flex: WKWebView does not stretch a flex child inside a
               flex column, so a flex layout that fills correctly in Chrome
               collapses to content width in the packaged .app. -->
          {#if !themePreference.followSystem}
          <div class="grid grid-cols-2 gap-2">
            {#each themeIds as id (id)}
              <ThemeSwatch {id} selected={themeId === id} onclick={() => previewTheme(id)} />
            {/each}
          </div>
          {/if}
          </SettingsGroup>
        </section>
      {:else if tab === "review"}
        {@render head("Review")}
        <div class="space-y-5">
          <h4 class="text-lg text-ink">Review providers</h4>
          {#if d.reviewLegacy}
            <div class="rounded border border-warn/40 bg-warn/10 px-3 py-2 text-ink">
              <p>Migrate the legacy review config to edit providers.</p>
              <Button variant="secondary" size="md" class="mt-2" onclick={migrateReview}>Migrate to providers</Button>
            </div>
          {/if}

          {#if !reviewKinds.length}
            <!-- Not "no providers": the config may well have some. Without the
                 backend's kind descriptors this form cannot draw them with the
                 right fields, so it says so instead of drawing them wrong. -->
            <p class="text-faint">
              {#if reviewKindsError}
                Could not read the review provider catalog: {reviewKindsError}
              {:else}
                Loading the review provider catalog…
              {/if}
            </p>
          {/if}

          <TeamLock lock={reviewLock} version={teamPolicy?.version ?? 0} disable={false}>
          <div class="space-y-5">
          {#each reviewKinds.length ? providers() : [] as p (p.provider)}
            <section aria-label={`${providerName(p.provider)} review`} class="overflow-hidden rounded-lg border border-edge" class:opacity-60={d.reviewLegacy}>
              <header class="flex items-center gap-3 bg-canvas/40 px-4 py-3">
                <div class="min-w-0 flex-1">
                  <h5 class="text-base font-medium text-ink">{providerName(p.provider)}</h5>
                </div>
                <label class="flex cursor-pointer items-center gap-2">
                  <Checkbox disabled={d.reviewLegacy || !!reviewLock} bind:checked={p.enabled} />
                  <span>{p.enabled ? "Enabled" : "Disabled"}</span>
                </label>
                {#if !d.reviewLegacy}
                  <Button variant="danger" size="xs" onclick={() => removeProvider(p.provider)}>Remove</Button>
                {/if}
              </header>

              {#if p.enabled || d.reviewLegacy || reviewLock}
                <div class="space-y-4 p-4">
                  <div class="space-y-3">
                    {#if isCLI(p.provider)}
                      <label class="grid gap-1.5">
                        <span class="text-faint">Command{needsCommand(p.provider) ? " *" : ""}</span>
                        <input class={inputCls} type="text" disabled={d.reviewLegacy} bind:value={p.command}
                          placeholder={needsCommand(p.provider) ? "greptile review --plain" : "coderabbit review"} />
                      </label>
                    {/if}
                    {#if agentOf(p.provider)}
                      <AgentModelFields provider={agentOf(p.provider)} model={p.model} disabled={d.reviewLegacy}
                        onModelChange={(value) => { p.model = value; }} />
                    {/if}
                    {#if isWatch(p.provider)}
                      <label class="grid gap-1.5">
                        <span class="text-faint">Author{needsAuthor(p.provider) ? " *" : ""}</span>
                        <input class={inputCls} type="text" disabled={d.reviewLegacy} bind:value={p.author}
                          placeholder={needsAuthor(p.provider) ? "GitHub bot username" : "coderabbitai"} />
                      </label>
                    {:else}
                      <label class="flex cursor-pointer items-center gap-2">
                        <Checkbox disabled={d.reviewLegacy} bind:checked={p.onPrOpen} />
                        <span>On PR open</span>
                      </label>
                    {/if}
                  </div>

                  <div class="space-y-3 border-t border-edge pt-3">
                    <h6 class="font-medium text-ink">After review</h6>
                    <div class="grid grid-cols-2 gap-x-4 gap-y-2">
                      <label class="flex cursor-pointer items-center gap-2">
                        <Checkbox disabled={d.reviewLegacy} bind:checked={p.sendToAgent} />
                        <span>Send to agent</span>
                      </label>
                      <label class="flex cursor-pointer items-center gap-2">
                        <Checkbox disabled={d.reviewLegacy} bind:checked={p.notify} />
                        <span>Notify me</span>
                      </label>
                    </div>
                    <Disclosure label="Publishing" hint={[
                      ...(p.transports ?? []).filter((t: string) => t !== "lola").map((t: string) => t === "github" ? "GitHub" : "Linear"),
                      ...(!isWatch(p.provider) && p.githubStatus ? ["Status gate"] : []),
                    ].join(" · ")}>
                      <div class="mt-3 space-y-3">
                        <div class="grid grid-cols-2 gap-x-4 gap-y-2">
                          {#each transportsFor(p.provider).filter((t) => t !== "lola") as t}
                            <label class="flex cursor-pointer items-center gap-2">
                              <Checkbox disabled={d.reviewLegacy} checked={(p.transports ?? []).includes(t)}
                                onchange={(e) => toggleTransport(p, t, (e.currentTarget as HTMLInputElement).checked)} />
                              <span>{t === "github" ? "GitHub" : "Linear"}</span>
                            </label>
                          {/each}
                        </div>
                        {#if !isWatch(p.provider) && (p.transports ?? []).includes("github")}
                          <label class="ml-6 flex cursor-pointer items-center gap-2 text-sm text-faint">
                            <Checkbox disabled={d.reviewLegacy} bind:checked={p.githubInline} />
                            <span>Inline PR threads</span>
                          </label>
                        {/if}
                        {#if !isWatch(p.provider)}
                          <label class="flex cursor-pointer items-center gap-2 text-sm"
                            title={`Posts lola/review/${p.provider} on the PR head and re-reviews each new head; require it in a ruleset to block merges on blocking findings.`}>
                            <Checkbox disabled={d.reviewLegacy} checked={p.githubStatus ?? false}
                              onchange={(e) => (p.githubStatus = (e.currentTarget as HTMLInputElement).checked)} />
                            <span>Commit status gate</span>
                          </label>
                        {/if}
                      </div>
                    </Disclosure>
                  </div>

                  {#if !isWatch(p.provider)}
                    <Disclosure label="Advanced settings">
                      <div class="mt-3 space-y-3">
                        <label class="grid grid-cols-[1fr_8rem] items-center gap-3">
                          <span class="text-faint">Timeout (s)</span>
                          <input class={inputCls} type="number" min="0" disabled={d.reviewLegacy} bind:value={p.timeoutSeconds} />
                        </label>
                        <label class="flex cursor-pointer items-center gap-2">
                          <Checkbox disabled={d.reviewLegacy} bind:checked={p.visible} />
                          <span>Watch it run</span>
                        </label>
                        {#if isCLI(p.provider)}
                          <div class="grid gap-1.5">
                            <span class="text-faint">Base flag</span>
                            <PresetInput label="Base flag" value={p.baseFlag} options={BASE_FLAGS} disabled={d.reviewLegacy} onChange={(v) => { p.baseFlag = v; }} />
                          </div>
                        {/if}
                        <div class="space-y-2">
                          <HelpText label="fallback reviewers" summary="Fallback reviewers" detail="If this reviewer cannot complete a pass, try the selected reviewers in numbered order. Click to remove and re-add a reviewer to change its order." />
                          <div class="grid grid-cols-2 gap-x-4 gap-y-2">
                            {#each fallbackFor(p.provider) as k}
                              <label class="flex cursor-pointer items-center gap-2">
                                <Checkbox disabled={d.reviewLegacy} checked={(p.fallback ?? []).includes(k)}
                                  onchange={(e) => toggleFallback(p, k, (e.currentTarget as HTMLInputElement).checked)} />
                                <span>{providerName(k)}</span>
                                {#if (p.fallback ?? []).includes(k)}
                                  <span class="num text-sm text-faint">{p.fallback.indexOf(k) + 1}</span>
                                {/if}
                              </label>
                            {/each}
                          </div>
                        </div>
                      </div>
                    </Disclosure>
                  {/if}
                </div>
              {/if}
            </section>
          {/each}

          <!-- Empty state. Without it a fresh config showed the Review tab as three
               bare buttons with raw kind ids and no clue what a "provider" is. -->
          {#if !d.reviewLegacy && providers().length === 0}
            <div class="rounded border border-edge/60 px-3 py-3">
              <p class="text-ink">No review pass configured.</p>

            </div>
          {/if}

          {#if !d.reviewLegacy && missingKinds().length}
            <Disclosure label="Add provider">
              <div class="mt-3 flex flex-wrap items-center gap-2">
                {#each missingKinds() as k}
                  <Button variant="secondary" class="whitespace-normal! text-left" title={kindLabel(k)} onclick={() => addProvider(k)}>{providerName(k)}</Button>
                {/each}
              </div>
            </Disclosure>
          {/if}
          </div>
          </TeamLock>
          <Disclosure label="Specialist reviewers" hint={d.reviewProfiles?.length ? `${d.reviewProfiles.length} configured` : "Optional"}>
            <div class="mt-3">
              <ReviewProfiles bind:profiles={d.reviewProfiles} specialties={d.reviewSpecialties ?? []} providers={d.reviewProviders ?? []} kinds={reviewKinds} />
            </div>
          </Disclosure>
        </div>
      {/if}
      <!-- The Team hub tab stays MOUNTED once visited: its drafts (a key being
           typed, a number not valid yet) must survive a look at another tab,
           so the overlay's Save can lead back to them instead of losing them. -->
      {#if hubVisited}
        <section hidden={tab !== "hub"}>
          {@render head("Team hub")}
          <HubSettings values={d} policy={teamPolicy} onChange={(patch) => Object.assign(d, patch)}
            onSaved={(patch) => {
              // Already in config.toml: the values AND the dirty baseline take it.
              Object.assign(d, patch);
              if (loaded) loaded = { ...loaded, ...($state.snapshot(patch) as Partial<SettingsDTO>) };
            }}
            onIdentityChange={() => void loadTeamPolicy()} onOpenTab={selectTab} onDirty={(v) => (hubDirty = v)} />
        </section>
      {/if}
    </div>
    </div>
  {/if}

  {#snippet footer()}
  <!-- The save error, inline and above the footer where it can't hide behind the
       backdrop. A Go error can be long and multi-line, so it wraps rather than
       truncating and stays selectable; dismissable, and cleared on the next save. -->
  {#if saveErr}
    <div role="alert" class="mb-3 flex max-h-32 items-start gap-2 overflow-auto rounded border border-bad/40 bg-bad/10 px-3 py-2 text-sm text-bad">
      <span class="min-w-0 flex-1 font-mono break-words whitespace-pre-wrap select-text">{saveErr}</span>
      <Button variant="danger" size="xs" icon aria-label="dismiss error" onclick={() => (saveErr = "")}><Icon name="x" size={14} /></Button>
    </div>
  {/if}

    <div class="flex items-center justify-end gap-2">
      <Button size="md" onclick={requestClose}>Cancel</Button>
      <Button variant="primary" size="md" onclick={save} disabled={saving || loading || !dto}>
        {saving ? "Saving…" : "Save"}
      </Button>
    </div>
  {/snippet}
</Modal>

<style>
  .settings-content { container-type: inline-size; }
  .settings-heading, .settings-content :global([data-ssh-settings] > h3) {
    margin-bottom: 24px;
    font-size: 18px;
    line-height: 24px;
    font-weight: 600;
    color: var(--color-ink);
  }
  .settings-content :global(.settings-field > :first-child) { color: var(--color-ink); }
  .settings-content :global(input[type="number"]) { width: 7.5rem; max-width: 100%; }
  .settings-content :global(.settings-field > span) { min-width: 0; }
  .settings-nav :global([role="tablist"] > .label) {
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    padding-left: 8px;
    padding-top: 24px;
    padding-bottom: 4px;
  }
  .settings-nav :global([role="tablist"] > .label:first-child) { padding-top: 0; }
  .settings-nav :global([role="tab"]) { padding-left: 20px; }
  .settings-nav :global([role="tab"][aria-selected="false"]) { color: var(--color-ink-2); }
  .settings-nav :global([role="tab"][aria-selected="false"]:hover) { color: var(--color-ink); }
  @container (max-width: 440px) {
    .settings-content :global(.settings-field) { grid-template-columns: minmax(0, 1fr); gap: 0.375rem; }
  }
  @media (max-width: 540px) {
    .settings-layout { grid-template-columns: minmax(0, 1fr); overflow-y: auto; }
    .settings-nav { max-height: 10rem; border-right: 0; border-bottom: 1px solid var(--color-edge); }
  }
</style>
