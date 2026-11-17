<script lang="ts" module>
  import { DEFAULT_BRANCH_PREFIX, branchFromPrompt } from "$lib/sessionview";

  /** The branch a worktree session gets: from the prompt when it has words, else a fresh unique one. */
  export function branchFor(prompt: string, now: number = Date.now()): string {
    return branchFromPrompt(prompt) || `${DEFAULT_BRANCH_PREFIX}session-${now.toString(36)}`;
  }

  export const AGENT_CHOICES = ["claude", "codex", "opencode"] as const;
</script>

<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";
  import Select from "$lib/components/Select.svelte";
  import { store } from "$lib/store.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import ListRow from "@mobile/lib/components/ListRow.svelte";
  import SegmentBar from "@mobile/lib/components/SegmentBar.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import Switch from "@mobile/lib/components/Switch.svelte";
  import { copy, fill } from "@mobile/lib/copy";
  import { haptic } from "@mobile/lib/haptics";
  import { router, type Route } from "@mobile/lib/router.svelte";
  import { can } from "@mobile/lib/tiers.svelte";

  // Starts work (spec §5.3): a PR or a ticket through their pickers, or a new
  // worktree from a prompt. "Shell, no agent" starts no agent and needs `shell`.
  // The project defaults to the one whose screen opened the sheet.
  //
  // An inset grouped form: the pickers are rows (label left, value and an up/down
  // glyph right) built on the shared Select, whose trigger app.css restyles inside a
  // group; the prompt is its own borderless group. Start being disabled is the only
  // signal that a prompt is missing, so there is no helper line. `dirty` guards a
  // typed prompt against a stray dismissal.

  const top = router.top;
  let project = $state(top.name === "tab" && top.project ? top.project : (store.projects[0]?.name ?? ""));
  let agent = $state("");
  let prompt = $state("");
  let shellOnly = $state(false);
  let busy = $state(false);
  let error = $state("");
  let source = $state<"blank" | "pr" | "ticket">("blank");

  const shellAllowed = can("shellCreate");
  const launchable = $derived(store.projectByName(project)?.agentOk !== false);

  /** The agent a blank choice resolves to: the project's own, else Claude. */
  const resolvedKind = $derived(agent || store.projectByName(project)?.agent || "claude");
  const resolved = $derived(
    resolvedKind in copy.agents ? copy.agents[resolvedKind as keyof typeof copy.agents] : resolvedKind,
  );
  /** The blank option already says the project's own agent; listing it twice would repeat a name. */
  const defaultKind = $derived(store.projectByName(project)?.agent || "claude");
  const canStart = $derived(!busy && !!project && launchable && (shellOnly || !!prompt.trim()));
  const SEGMENTS = [
    { id: "blank", label: copy.newSession.from.blank },
    { id: "pr", label: copy.newSession.from.pr },
    { id: "ticket", label: copy.newSession.from.ticket },
  ] as const;

  function go(pick: "prs" | "tickets"): void {
    const next: Route = { name: "tab", tab: "projects", project, pick };
    if (router.top.name === "tab") router.replace(next);
    else router.push(next);
  }

  function choose(next: "blank" | "pr" | "ticket"): void {
    source = next;
    if (next === "pr" && project) go("prs");
    else if (next === "ticket" && project) go("tickets");
  }

  /** Grows 44 -> 120pt with the text; beyond that it scrolls. */
  function autogrow(node: HTMLTextAreaElement, _text?: string) {
    const fit = () => {
      node.style.height = "auto";
      node.style.height = `${Math.min(120, Math.max(44, node.scrollHeight))}px`;
    };
    node.addEventListener("input", fit);
    fit();
    return { update: fit, destroy: () => node.removeEventListener("input", fit) };
  }

  async function start(): Promise<void> {
    if (!canStart) return;
    busy = true;
    error = "";
    const r = await store.openManual({
      project,
      branch: branchFor(prompt),
      agent: !shellOnly,
      prompt: shellOnly ? "" : prompt.trim(),
      agentKind: agent,
    });
    busy = false;
    if (!r?.sessionId) {
      error = store.flash?.text ?? "";
      return;
    }
    haptic("success");
    router.close();
    router.push({ name: "session", id: r.sessionId, seg: shellOnly ? "terminal" : "conversation" });
  }
</script>

<Sheet
  label={copy.newSession.title}
  title={copy.newSession.title}
  detent="fit"
  dirty={prompt.length > 0}
  action={{ label: copy.newSession.start, onclick: () => void start(), disabled: !canStart }}
  onclose={() => router.close()}
>
  <div class="flex flex-col gap-4 pb-2">
    <ListGroup>
      <div class="relative flex items-center" data-list-row>
        <span class="shrink-0 pl-4 text-body text-ink" aria-hidden="true">Project</span>
        <div class="min-w-0 flex-1">
          <Select aria-label="Project" bind:value={project}>
            {#each store.projects as p (p.name)}
              <option value={p.name}>{store.displayNameFor(p.name)}</option>
            {/each}
          </Select>
        </div>
        <span class="pointer-events-none absolute right-4 text-faint" aria-hidden="true"><Icon name="chevrons-up-down" size={14} /></span>
      </div>
      <div class="relative flex items-center" data-list-row>
        <span class="shrink-0 pl-4 text-body text-ink" aria-hidden="true">Agent</span>
        <div class="min-w-0 flex-1">
          <Select aria-label="Agent" bind:value={agent}>
            <option value="">{fill(copy.newSession.agentDefault, { agent: resolved })}</option>
            {#each AGENT_CHOICES.filter((k) => k !== defaultKind) as k (k)}<option value={k}>{copy.agents[k]}</option>{/each}
          </Select>
        </div>
        <span class="pointer-events-none absolute right-4 text-faint" aria-hidden="true"><Icon name="chevrons-up-down" size={14} /></span>
      </div>
    </ListGroup>
    <div class="px-gutter">
      <SegmentBar segments={SEGMENTS} value={source} onchange={choose} label={copy.newSession.title} />
    </div>
    <ListGroup>
      <textarea
        use:autogrow={prompt}
        class="w-full resize-none text-body text-ink outline-none placeholder:text-placeholder disabled:opacity-60"
        style="min-height: 44px; max-height: 120px"
        aria-label="Prompt"
        placeholder={fill(copy.newSession.placeholder, { agent: resolved })}
        bind:value={prompt}
        disabled={busy || shellOnly}
      ></textarea>
    </ListGroup>
    {#if shellAllowed}
      <ListGroup>
        <ListRow label={copy.newSession.shellOnly}>
          {#snippet trailing()}
            <Switch checked={shellOnly} onchange={(v: boolean) => (shellOnly = v)} label={copy.newSession.shellOnly} />
          {/snippet}
        </ListRow>
      </ListGroup>
    {/if}
    {#if error}<p class="px-gutter text-subhead text-warn" role="alert">{error}</p>{/if}
  </div>
</Sheet>
