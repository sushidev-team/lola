<script module lang="ts">
  import { nav } from "$lib/nav.svelte";
  import { store } from "$lib/store.svelte";
  import type { DoctorResultDTO } from "@bindings/desktop";

  /**
   * What a failed doctor row offers. A COMMAND is something only the human can
   * run (it installs software or signs in), so it is shown and copied, never
   * executed; an ACTION is something the app can do itself, so it is a button.
   */
  export type FixAction =
    | { kind: "command"; command: string }
    | { kind: "action"; label: string; run: () => unknown };

  /**
   * The project a poll fix should open: the first one that does not poll yet,
   * on its Issue pickup tab. With none at all, adding one is the fix.
   */
  function openPollProject() {
    const own = store.projects.filter((p) => !p.stack);
    const target = own.find((p) => !p.pollsEnabled) ?? own[0];
    if (target) nav.openOverlay("project", target.name, "filter");
    else nav.openOverlay("project", "");
  }

  /**
   * One entry per doctor.FixID (internal/doctor/fix.go), keyed by the id
   * string. desktop/doctorsvc_test.go fails when a FixID has no entry, so a new
   * fix cannot reach the UI as a row with no way out.
   */
  export const FIXES: Record<string, FixAction> = {
    start_daemon: { kind: "action", label: "Start daemon", run: () => store.startDaemon() },
    install_gh: { kind: "command", command: "brew install gh" },
    gh_auth_login: { kind: "command", command: "gh auth login" },
    add_project: { kind: "action", label: "Add project", run: () => nav.openOverlay("project", "") },
    enable_poll: { kind: "action", label: "Choose a Linear team", run: openPollProject },
    hub_join: { kind: "action", label: "Open team hub settings", run: () => nav.openOverlay("settings", "", "hub") },
    hub_rejoin: { kind: "action", label: "Open team hub settings", run: () => nav.openOverlay("settings", "", "hub") },
    hub_chores: { kind: "action", label: "Open team hub settings", run: () => nav.openOverlay("settings", "", "hub") },
    sandbox_project: { kind: "action", label: "Open team hub settings", run: () => nav.openOverlay("settings", "", "hub") },
    start_docker: { kind: "command", command: "open -a Docker" },
    sandbox_token: { kind: "action", label: "Open sandbox settings", run: () => nav.openOverlay("settings", "", "sandbox") },
    pin_policy_key: { kind: "action", label: "Open team hub settings", run: () => nav.openOverlay("settings", "", "hub") },
  };

  /** The fix for a row, or null when it passed or has no known fix. */
  export function fixFor(r: Pick<DoctorResultDTO, "ok" | "fix">): FixAction | null {
    if (r.ok || !r.fix) return null;
    return FIXES[r.fix] ?? null;
  }

  // Install commands for the tools doctor finds missing but names no fix for.
  const AGENT_INSTALL: Record<string, string> = {
    claude: "npm install -g @anthropic-ai/claude-code",
    codex: "npm install -g @openai/codex",
    opencode: "npm install -g opencode-ai",
  };
  const AGENTS = Object.keys(AGENT_INSTALL);

  export type ItemState = "ok" | "todo" | "unknown";

  /**
   * How long after a run a window focus may run the checks again. Each run is
   * a doctor pass (docker and sandbox probes, a networked `gh auth status`),
   * and a person switching windows focuses this one many times a minute.
   */
  export const FOCUS_RECHECK_MS = 60_000;
  export type ChecklistItem = { id: string; title: string; state: ItemState; sentence: string; fix: FixAction | null };

  /**
   * The six things between install and a first agent on a real issue, read
   * from one doctor report. Rows are found by their stable check names; a row
   * the report does not carry reads "unknown", never "ok".
   *
   * `setup` is the first-run screen: no config exists yet, so the key is
   * judged by the screen's own validation (`linearKey`) and projects come
   * after setup.
   */
  export function checklist(
    results: DoctorResultDTO[],
    opts: { setup?: boolean; linearKey?: ItemState } = {},
  ): ChecklistItem[] {
    const row = (name: string) => results.find((r) => r.name === name);
    const tool = (id: string, title: string, okText: string, missing: string, install: string): ChecklistItem => {
      const r = row(id);
      if (!r) return { id, title, state: "unknown", sentence: "Not checked yet.", fix: null };
      return r.ok
        ? { id, title, state: "ok", sentence: okText, fix: null }
        : { id, title, state: "todo", sentence: missing, fix: fixFor(r) ?? { kind: "command", command: install } };
    };

    const items: ChecklistItem[] = [
      tool("tmux", "tmux", "Installed.", "Lola runs every agent in a tmux session.", "brew install tmux"),
      tool("git", "git", "Installed.", "Lola gives every agent its own git worktree.", "xcode-select --install"),
    ];

    // gh counts only when it is installed AND signed in: an installed but
    // signed-out gh cannot read a pull request either.
    const gh = row("gh");
    const auth = row("gh auth");
    if (!gh) {
      items.push({ id: "gh", title: "GitHub CLI", state: "unknown", sentence: "Not checked yet.", fix: null });
    } else if (!gh.ok) {
      items.push({ id: "gh", title: "GitHub CLI", state: "todo", sentence: "Lola reads pull requests and CI through gh.", fix: fixFor(gh) ?? FIXES.install_gh });
    } else if (auth && !auth.ok) {
      items.push({ id: "gh", title: "GitHub CLI", state: "todo", sentence: "gh is installed but not signed in.", fix: fixFor(auth) ?? FIXES.gh_auth_login });
    } else if (!auth) {
      items.push({ id: "gh", title: "GitHub CLI", state: "unknown", sentence: "Installed; sign-in not checked yet.", fix: null });
    } else {
      items.push({ id: "gh", title: "GitHub CLI", state: "ok", sentence: "Installed and signed in.", fix: null });
    }

    const agents = results.filter((r) => AGENTS.includes(r.name));
    const missing = agents.find((r) => !r.ok);
    if (!agents.length) {
      items.push({ id: "agent", title: "Coding agent", state: "unknown", sentence: "Not checked yet.", fix: null });
    } else if (missing) {
      items.push({ id: "agent", title: "Coding agent", state: "todo", sentence: `The ${missing.name} CLI is missing or out of date.`, fix: { kind: "command", command: AGENT_INSTALL[missing.name] } });
    } else {
      items.push({ id: "agent", title: "Coding agent", state: "ok", sentence: `Found: ${agents.map((r) => r.name).join(", ")}.`, fix: null });
    }

    if (opts.setup) {
      const state = opts.linearKey ?? "unknown";
      items.push({
        id: "linear", title: "Linear API key", state,
        sentence: state === "ok" ? "The key works." : state === "todo" ? "Linear did not accept this key." : "Paste your key above. Lola checks it with Linear.",
        fix: null,
      });
      items.push({ id: "poll", title: "A project picks up issues", state: "todo", sentence: "After you start Lola, add a project and choose its Linear team.", fix: null });
      return items;
    }

    const key = row("linear api key");
    items.push(!key
      ? { id: "linear", title: "Linear API key", state: "unknown", sentence: "Not checked yet.", fix: null }
      : key.ok
        ? { id: "linear", title: "Linear API key", state: "ok", sentence: "Found.", fix: null }
        : { id: "linear", title: "Linear API key", state: "todo", sentence: "Lola needs a key to read your Linear issues.", fix: { kind: "action", label: "Open Linear settings", run: () => nav.openOverlay("settings", "", "linear") } });

    const projects = row("projects");
    const polling = row("polling");
    if (projects && !projects.ok) {
      items.push({ id: "poll", title: "A project picks up issues", state: "todo", sentence: "Add a project: a folder with a git checkout.", fix: fixFor(projects) ?? FIXES.add_project });
    } else if (polling) {
      items.push(polling.ok
        ? { id: "poll", title: "A project picks up issues", state: "ok", sentence: polling.detail || "A project polls Linear.", fix: null }
        : { id: "poll", title: "A project picks up issues", state: "todo", sentence: "Give a project a Linear team and turn on pickup.", fix: fixFor(polling) ?? FIXES.enable_poll });
    } else {
      items.push({ id: "poll", title: "A project picks up issues", state: "unknown", sentence: "Not checked yet.", fix: null });
    }
    return items;
  }
</script>

<script lang="ts">
  import { onMount } from "svelte";
  import { DoctorService } from "@bindings/desktop";
  import Button from "$lib/components/Button.svelte";
  import Icon from "$lib/components/Icon.svelte";

  let {
    setup = false,
    linearKey = "unknown",
    hideWhenComplete = false,
    optional = [],
    class: klass = "",
  }: {
    /** The first-run screen: no config yet (see checklist()). */
    setup?: boolean;
    /** The setup screen's own verdict on the typed key. */
    linearKey?: ItemState;
    /** Render nothing once every item passes (the empty workspace). */
    hideWhenComplete?: boolean;
    /**
     * Item ids that do not keep a hideWhenComplete checklist on screen. The
     * sidebar passes "poll": a Mac that only takes team hub chores or manual
     * sessions never polls Linear, and doctor says those still work, so a
     * checklist waiting for a poll would never go away.
     */
    optional?: string[];
    /** Classes for the checklist's own frame (it goes with the checklist when it hides). */
    class?: string;
  } = $props();

  let results = $state<DoctorResultDTO[] | null>(null);
  let failed = $state(false);
  let running = $state(false);
  let copied = $state("");

  const items = $derived(results ? checklist(results, { setup, linearKey }) : []);
  const complete = $derived(!!results && items.every((i) => i.state === "ok"));
  const settled = $derived(!!results && items.every((i) => i.state === "ok" || optional.includes(i.id)));
  const remaining = $derived(items.filter((i) => i.state !== "ok").length);

  // One run at a time: a focus while a run is in flight is already answered
  // by that run. The last list stays on screen while a re-check runs.
  let lastRun = 0;
  async function run() {
    if (running) return;
    running = true;
    lastRun = Date.now();
    try {
      results = (await DoctorService.Run()).results ?? [];
      failed = false;
    } catch {
      failed = true;
    } finally {
      running = false;
    }
  }

  // Most fixes happen outside the app (a terminal, a browser), so coming back
  // to the window is the moment to look again — at most once per
  // FOCUS_RECHECK_MS; "Check again" is never throttled.
  onMount(() => {
    void run();
    const onFocus = () => {
      if (Date.now() - lastRun >= FOCUS_RECHECK_MS) void run();
    };
    window.addEventListener("focus", onFocus);
    return () => window.removeEventListener("focus", onFocus);
  });

  async function copy(id: string, command: string) {
    try {
      await navigator.clipboard.writeText(command);
      copied = id;
      setTimeout(() => { if (copied === id) copied = ""; }, 1500);
    } catch {
      copied = ""; // the command stays selectable
    }
  }
</script>

{#if !(hideWhenComplete && settled)}
  <section class="grid gap-2 text-sm {klass}" aria-labelledby="setup-checklist-title">
    <div class="flex items-center gap-2">
      <h2 id="setup-checklist-title" class="font-medium text-ink">Setup checklist</h2>
      {#if results}
        <span class="text-faint" role="status">{complete ? "All set." : `${remaining} left`}</span>
      {/if}
      <Button size="xs" class="ml-auto" disabled={running} onclick={run}>{running ? "Checking…" : "Check again"}</Button>
    </div>
    {#if failed && !results}
      <p class="text-bad" role="alert">The checks could not run. Try again.</p>
    {:else if !results}
      <p class="text-faint">Checking…</p>
    {:else}
      <ul class="grid gap-1.5" aria-label="Setup checklist" class:opacity-60={running}>
        {#each items as item (item.id)}
          <li class="grid grid-cols-[1rem_minmax(0,1fr)] gap-x-2" data-item={item.id} data-state={item.state}>
            <span class="pt-0.5 {item.state === 'ok' ? 'text-good' : item.state === 'todo' ? 'text-warn' : 'text-faint'}"
              aria-label={item.state === "ok" ? "Done" : item.state === "todo" ? "To do" : "Not checked"}>
              <Icon name={item.state === "ok" ? "circle-check" : "circle"} size={14} />
            </span>
            <div class="min-w-0">
              <span class="text-ink">{item.title}</span>
              <span class="block text-faint">{item.sentence}</span>
              {#if item.fix?.kind === "command"}
                <span class="mt-1 flex min-w-0 flex-wrap items-center gap-1.5">
                  <code class="selectable min-w-0 break-all rounded bg-canvas px-1.5 py-0.5 font-mono text-ink">{item.fix.command}</code>
                  <Button size="xs" aria-label={`Copy command: ${item.fix.command}`} onclick={() => item.fix?.kind === "command" && copy(item.id, item.fix.command)}>
                    {copied === item.id ? "Copied" : "Copy"}
                  </Button>
                </span>
              {:else if item.fix?.kind === "action"}
                {@const action = item.fix}
                <span class="mt-1 block"><Button size="xs" variant="secondary" onclick={() => void action.run()}>{action.label}</Button></span>
              {/if}
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  </section>
{/if}
