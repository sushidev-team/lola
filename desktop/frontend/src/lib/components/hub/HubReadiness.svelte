<script lang="ts" module>
  // The hub READINESS checklist: can this Mac take the team's chores right
  // now? Built from the same rows `lola hub status` prints (ConfigService.
  // HubReadiness → doctor.HubReadiness, keyed by stable fix ids), the
  // projects list (ConfigService.HubProjects) and the hub status the daemon
  // reports (findings health per repository). The row model is a pure
  // function, so its rules are tested without a DOM.
  //
  // Rules it keeps:
  //   - Nothing is pre-ticked. A project whose repository has chores on the
  //     team board is SUGGESTED; it takes chores only after the person presses
  //     its button.
  //   - Every fact is lola's own words. A doctor detail is shown only for a
  //     row this list does not know, and never carries a token or key value.
  import type { HubProjectDTO, HubReadinessRowDTO } from "@bindings/desktop/models";
  import type { HubRepoHealth } from "@bindings/internal/protocol";

  export type ReadinessId = "projects" | "repo" | "docker" | "image" | "login" | "github" | "policy";
  /** Where a fix button leads. "projects" / "keys" scroll this tab; "sandbox" opens the Sandbox tab. */
  export type FixTarget = "projects" | "keys" | "sandbox";
  export type ReadinessFix =
    | { kind: "goto"; target: FixTarget; label: string }
    | { kind: "copy"; command: string; label: string };
  /** ok = done; missing = blocks chores; info = worth knowing, blocks nothing. */
  export type ReadinessState = "ok" | "missing" | "info";
  export type ReadinessRow = {
    id: ReadinessId;
    label: string;
    state: ReadinessState;
    detail: string;
    fix?: ReadinessFix;
    /** projects only: projects whose repository has chores on the team board. */
    suggest?: HubProjectDTO[];
  };

  const REPO_RE = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;

  /** doctor's row names (internal/doctor/hubready.go). */
  const DOCTOR = {
    docker: "hub docker",
    image: "hub sandbox image",
    login: "hub sandbox login",
    github: "hub github access",
    policy: "hub policy key",
  } as const;

  export function readinessRows(input: {
    doctor: HubReadinessRowDTO[];
    projects: HubProjectDTO[];
    health?: HubRepoHealth[] | null;
    boardRepos?: string[];
  }): ReadinessRow[] {
    const byName = new Map(input.doctor.map((r) => [r.name, r]));
    const rows: ReadinessRow[] = [];
    const chores = input.projects.filter((p) => p.hubChores);
    const board = new Set((input.boardRepos ?? []).map((r) => r.toLowerCase()));
    const repoOf = (p: HubProjectDTO) => (REPO_RE.test(p.repo) ? p.repo : p.suggestedRepo);

    if (chores.length) {
      rows.push({ id: "projects", label: "Pick projects", state: "ok", detail: `${chores.map((p) => p.label).join(", ")} ${chores.length === 1 ? "takes" : "take"} chores.` });
    } else {
      const suggest = input.projects.filter((p) => !p.hubChores && repoOf(p) && board.has(repoOf(p).toLowerCase()));
      rows.push({
        id: "projects",
        label: "Pick projects",
        state: "missing",
        detail: suggest.length
          ? "No project takes chores yet. Your team’s board has chores for the repositories below."
          : "No project takes chores yet. Tick the projects that may take them.",
        fix: { kind: "goto", target: "projects", label: "Choose projects" },
        suggest,
      });
    }

    const noRepo = chores.filter((p) => !REPO_RE.test(p.repo));
    if (chores.length) {
      rows.push(noRepo.length
        ? { id: "repo", label: "Repository set", state: "missing", detail: `${noRepo.map((p) => p.label).join(", ")} ${noRepo.length === 1 ? "has" : "have"} no GitHub repository.`, fix: { kind: "goto", target: "projects", label: "Set repository" } }
        : { id: "repo", label: "Repository set", state: "ok", detail: "Every chore project names its GitHub repository." });
    }

    const docker = byName.get(DOCTOR.docker);
    if (docker) {
      rows.push(docker.ok
        ? { id: "docker", label: "Docker running", state: "ok", detail: "Docker answers." }
        : { id: "docker", label: "Docker running", state: "missing", detail: "Docker does not answer. Start Docker Desktop, OrbStack or Colima; sandboxed chores cannot start without it.", fix: { kind: "goto", target: "sandbox", label: "Open Sandbox settings" } });
    }
    const image = byName.get(DOCTOR.image);
    if (image) {
      const pulls = /not pulled yet/i.test(image.detail);
      rows.push({
        id: "image",
        label: "Sandbox image",
        state: image.ok && !pulls ? "ok" : "info",
        detail: !image.ok ? "Checked once Docker runs." : pulls ? "Not downloaded yet. Lola downloads it before the first chore." : "Downloaded.",
      });
    }
    const login = byName.get(DOCTOR.login);
    if (login) {
      rows.push(login.ok
        ? { id: "login", label: "Sandbox Claude login", state: "ok", detail: "Sandboxed agents can sign in to Claude." }
        : { id: "login", label: "Sandbox Claude login", state: "missing", detail: "Sandboxed agents cannot sign in to Claude yet.", fix: { kind: "goto", target: "sandbox", label: "Log in" } });
    }

    const gh = byName.get(DOCTOR.github);
    const blocked = (input.health ?? []).filter((h) => h.alerts === "no_access" || h.pullRequests === "no_access").map((h) => h.repo);
    if (gh && !gh.ok) {
      rows.push(gh.fix === "install_gh"
        ? { id: "github", label: "GitHub access to alerts", state: "missing", detail: "The GitHub CLI is not installed, so lola cannot read security alerts or open pull requests.", fix: { kind: "copy", command: "brew install gh", label: "Copy install command" } }
        : { id: "github", label: "GitHub access to alerts", state: "missing", detail: "The GitHub CLI is not signed in. Run the command in a terminal on this Mac.", fix: { kind: "copy", command: "gh auth login", label: "Copy command" } });
    } else if (blocked.length) {
      rows.push({ id: "github", label: "GitHub access to alerts", state: "missing", detail: `Your GitHub account cannot read the security alerts of ${blocked.join(", ")}. Ask a repository admin for access.` });
    } else if (gh) {
      rows.push({ id: "github", label: "GitHub access to alerts", state: "ok", detail: "Lola can read security alerts and open pull requests." });
    }

    const policy = byName.get(DOCTOR.policy);
    if (policy) {
      rows.push(policy.ok
        ? { id: "policy", label: "Policy key pinned", state: "ok", detail: "Team settings can be verified." }
        : { id: "policy", label: "Policy key pinned", state: "info", detail: "No team key is trusted, so team settings are not checked or applied here.", fix: { kind: "goto", target: "keys", label: "Add key" } });
    }
    return rows;
  }

  /** Something blocks chores. */
  export function readinessMissing(rows: ReadinessRow[]): boolean {
    return rows.some((r) => r.state === "missing");
  }
</script>

<script lang="ts">
  import { onDestroy, onMount } from "svelte";
  import Button from "$lib/components/Button.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import { hub } from "$lib/hub.svelte";
  import type { IconName } from "$lib/icons";
  import { ConfigService } from "@bindings/desktop";
  import type { HubProjectDTO as Project } from "@bindings/desktop/models";

  let { refresh = 0, hideWhenReady = false, onFix = () => {}, onProjectsChanged = () => {} }: {
    /** Bump to check again (a project or key was saved elsewhere in the tab). */
    refresh?: number;
    /** Render nothing while every row is fine (the top of the tab). */
    hideWhenReady?: boolean;
    onFix?: (target: FixTarget) => void;
    /** A suggestion turned a project on. */
    onProjectsChanged?: () => void;
  } = $props();

  let doctor = $state<HubReadinessRowDTO[] | null>(null);
  let projects = $state<Project[]>([]);
  let error = $state("");
  let checking = $state(false);
  let busy = $state("");
  let copied = $state("");
  let seq = 0;

  async function check() {
    const mine = ++seq;
    checking = true;
    try {
      const [d, p] = await Promise.all([ConfigService.HubReadiness(), ConfigService.HubProjects()]);
      if (mine !== seq) return;
      doctor = d ?? [];
      projects = p ?? [];
      error = "";
    } catch (e) {
      if (mine !== seq) return;
      doctor = doctor ?? [];
      error = String(e).replace(/^Error:\s*/, "");
    } finally {
      if (mine === seq) checking = false;
    }
  }

  // The board feeds the project suggestions; watching it polls only while
  // this list is on screen.
  let release: (() => void) | null = null;
  const onFocus = () => void check();
  onMount(() => {
    release = hub.watch({ board: true });
    window.addEventListener("focus", onFocus);
  });
  onDestroy(() => {
    release?.();
    window.removeEventListener("focus", onFocus);
  });
  $effect(() => {
    void refresh;
    void check();
  });

  const rows = $derived(
    doctor ? readinessRows({ doctor, projects, health: hub.status?.findingsHealth ?? [], boardRepos: hub.items.map((i) => i.repo) }) : [],
  );
  const missing = $derived(readinessMissing(rows));
  const hidden = $derived(hideWhenReady && (!doctor || (!missing && !error)));

  async function enable(p: Project) {
    busy = p.name;
    error = "";
    try {
      // The suggestion's button IS the person's confirmation: one project, now.
      await ConfigService.SaveHubProject({ name: p.name, repo: REPO_RE.test(p.repo) ? null : p.suggestedRepo, hubChores: true, sandboxed: null });
      onProjectsChanged();
      await check();
    } catch (e) {
      error = String(e).replace(/^Error:\s*/, "");
    } finally {
      busy = "";
    }
  }

  async function copy(command: string) {
    try {
      await navigator.clipboard.writeText(command);
      copied = command;
      setTimeout(() => (copied = ""), 1500);
    } catch {
      copied = "";
    }
  }

  const tone: Record<ReadinessState, { icon: IconName; cls: string; word: string }> = {
    ok: { icon: "circle-check", cls: "text-good", word: "Done" },
    missing: { icon: "circle-alert", cls: "text-bad", word: "Needed" },
    info: { icon: "info", cls: "text-faint", word: "Note" },
  };
</script>

{#if !hidden}
  <section class="mb-6 grid min-w-0 gap-3 rounded-lg border border-edge p-4" aria-label="Ready for chores" data-hub-readiness>
    <div class="flex min-w-0 flex-wrap items-center gap-2">
      <h4 class="min-w-0 flex-1 text-base font-medium text-ink">
        {#if !doctor}Checking this Mac…{:else if missing}Before this Mac takes chores{:else}Ready for chores{/if}
      </h4>
      <Button size="sm" loading={checking} disabled={checking} onclick={() => void check()}>Check again</Button>
    </div>
    {#if error}<p role="alert" class="text-sm text-bad">{error}</p>{/if}
    {#if doctor}
      <ul class="grid min-w-0 gap-2.5" aria-label="Readiness">
        {#each rows as r (r.id)}
          {@const t = tone[r.state]}
          <li class="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] gap-x-2.5 gap-y-1" data-readiness={r.id} data-state={r.state}>
            <span class="pt-0.5 {t.cls}" title={t.word}><Icon name={t.icon} size={16} /></span>
            <span class="grid min-w-0 gap-1">
              <span class="text-ink">{r.label}<span class="sr-only">: {t.word}</span></span>
              <span class="text-sm text-faint">{r.detail}</span>
              {#if r.suggest?.length}
                <span class="flex min-w-0 flex-wrap items-center gap-1.5">
                  {#each r.suggest as p (p.name)}
                    <Button size="xs" variant="secondary" loading={busy === p.name} disabled={busy !== ""} onclick={() => void enable(p)}>
                      Turn on {p.label}
                    </Button>
                  {/each}
                </span>
              {/if}
              {#if r.fix && r.state !== "ok"}
                {@const fix = r.fix}
                <span class="flex flex-wrap items-center gap-1.5">
                  {#if fix.kind === "copy"}
                    <code class="rounded bg-canvas px-1.5 py-0.5 font-mono text-sm text-ink">{fix.command}</code>
                    <Button size="xs" onclick={() => void copy(fix.command)}>{copied === fix.command ? "Copied" : fix.label}</Button>
                  {:else}
                    <Button size="xs" onclick={() => onFix(fix.target)}>{fix.label}</Button>
                  {/if}
                </span>
              {/if}
            </span>
          </li>
        {/each}
      </ul>
    {/if}
  </section>
{/if}
