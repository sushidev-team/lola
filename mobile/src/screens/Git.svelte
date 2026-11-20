<script lang="ts">
  import { tick, untrack } from "svelte";
  import { agoShort } from "$lib/board";
  import CommitGraph from "@mobile/lib/components/CommitGraph.svelte";
  import ProjectSections from "@mobile/lib/components/ProjectSections.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import ProjectGitDiff from "$lib/components/ProjectGitDiff.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import { projectGitState } from "$lib/projectgit.svelte";
  import { folderFirstFiles, stagedFiles, unstagedFiles, type GitArea, type GitCommit, type GitFile } from "$lib/projectgit";
  import { store } from "$lib/store.svelte";
  import LargeTitle from "@mobile/lib/components/LargeTitle.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import ListRow from "@mobile/lib/components/ListRow.svelte";
  import SectionHeader from "@mobile/lib/components/SectionHeader.svelte";
  import SegmentBar, { type Segment } from "@mobile/lib/components/SegmentBar.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { copy, fill } from "@mobile/lib/copy";
  import { aheadBehind, fileStatus, HISTORY_PAGE, MAX_HISTORY, nextLimit, pickableWorktrees, shortSha, splitPath } from "@mobile/lib/gitphone";
  import { router } from "@mobile/lib/router.svelte";
  import { can } from "@mobile/lib/tiers.svelte";

  // The phone's Git client. The Mac runs every command with its own credentials
  // and refuses sandboxed or foreign checkouts (internal/daemon/projectgit.go);
  // this screen only asks. The state, the diff and the file grouping are the
  // desktop's own (`ProjectGitState`, `ProjectGitDiff`), so a rule fixed there is
  // fixed here. A control the device's tier does not grant is not drawn.
  const project = $derived(router.top.name === "tab" ? (router.top.project ?? "") : "");
  const canAct = $derived(can("projectGitAct"));
  const canDiscard = $derived(can("projectGitDiscard"));

  let worktree = $state("");
  let limit = $state(HISTORY_PAGE);
  const graph = $derived(router.top.name === "tab" && router.top.pick === "graph");
  const readable = $derived(!!store.projectByName(project)?.pathOk && can("projectGit"));
  let section = $state<"changes" | "stashes">("changes");
  let historyRef = $state("");
  let switchingFilter = $state(false);
  let returnFocus: HTMLElement | null = null;
  let pickingTree = $state(false);
  type View = { kind: "file"; file: GitFile; staged: boolean } | { kind: "commit"; commit: GitCommit };
  let view = $state<View | null>(null);
  let openFile = $state("");

  const git = $derived(projectGitState(project, worktree));
  const snap = $derived(git.snapshot);
  const staged = $derived(folderFirstFiles(stagedFiles(snap?.files ?? [])));
  const unstaged = $derived(folderFirstFiles(unstagedFiles(snap?.files ?? [])));
  const conflicts = $derived((snap?.files ?? []).some((f) => f.conflict));
  const operation = $derived(snap?.operation ?? "");
  const sync = $derived(aheadBehind(snap));
  const trees = $derived(pickableWorktrees(snap));
  const canCommit = $derived(
    canAct && staged.length > 0 && !!git.message.trim() && !conflicts && !git.busy && (!operation || operation === "merge"),
  );
  const segments = $derived<Segment<"changes" | "stashes">[]>([
    { id: "changes", label: `${copy.git.files}${(snap?.files.length ?? 0) > 0 ? ` ${snap!.files.length}` : ""}` },
    { id: "stashes", label: `${copy.git.segments.stashes}${git.stashes.length > 0 ? ` ${git.stashes.length}` : ""}` },
  ]);

  function reload(): void {
    void git.load(historyRef, limit);
  }

  async function filterHistory(ref: string) {
    historyRef = ref;
    limit = HISTORY_PAGE;
    const state = git;
    switchingFilter = true;
    await state.load(ref, limit);
    if (git === state && historyRef === ref) switchingFilter = false;
  }

  // Another project or checkout is another state: leave the open diff and reload.
  $effect(() => {
    void project;
    void worktree;
    const allowed = readable;
    untrack(() => {
      view = null;
      limit = HISTORY_PAGE;
      pickingTree = false;
      historyRef = "";
      switchingFilter = false;
      if (allowed) reload();
    });
  });

  async function act(action: "stage" | "unstage" | "pull" | "push" | "fetch" | "commit", paths: string[] = []): Promise<boolean> {
    return git.act(action, paths, historyRef, limit);
  }

  function open(next: View): void {
    returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    view = next;
    openFile = "";
    if (next.kind === "file") {
      const area: GitArea = next.staged ? "staged" : next.file.untracked ? "untracked" : "unstaged";
      void git.loadDiff(area, "", "", next.file.path);
    } else void git.loadDiff("commit", "", next.commit.sha, "");
  }

  function close(): void {
    view = null;
    git.clearDiff();
    void tick().then(() => returnFocus?.focus({ preventScroll: true }));
  }

  async function stageFromView(): Promise<void> {
    if (view?.kind !== "file") return;
    if (await act(view.staged ? "unstage" : "stage", [view.file.path])) close();
  }

  async function discardFromView(): Promise<void> {
    if (view?.kind !== "file" || view.staged) return;
    const path = view.file.path;
    const plan = await git.prepareDiscard([path]);
    if (!plan) return;
    confirm.ask({
      title: copy.git.discardTitle,
      subject: path,
      body: copy.git.discardNote,
      confirmLabel: copy.git.discard,
      onConfirm: () => {
        void git.discardUnstaged(plan.token, historyRef, limit, [path]).then((ok) => ok && close());
      },
    });
  }

  function dropStash(sha: string, subject: string): void {
    confirm.ask({
      title: copy.git.dropTitle,
      subject,
      body: copy.git.dropNote,
      confirmLabel: copy.git.drop,
      onConfirm: () => void git.stashAction("drop", sha, snap?.head ?? "", historyRef, limit),
    });
  }

  function more(): void {
    limit = nextLimit(limit);
    reload();
  }

  const BUSY: Record<string, string> = { fetch: copy.git.fetch, pull: copy.git.pull, push: copy.git.push };
  const TONE = { good: "text-good", warn: "text-warn", bad: "text-bad", info: "text-info", faint: "text-faint" } as const;
  const filesOf = $derived(git.diff?.files ?? []);
</script>

{#snippet fileRow(f: GitFile, isStaged: boolean)}
  {@const st = fileStatus(f, isStaged)}
  {@const p = splitPath(f.path)}
  <div class="flex items-center border-b border-edge-soft pl-4 last:border-b-0">
    <button
      type="button"
      class="flex min-h-12 min-w-0 flex-1 touch-manipulation items-center gap-3 py-2 text-left active:bg-sel"
      onclick={() => open({ kind: "file", file: f, staged: isStaged })}
    >
      <span class="num w-4 shrink-0 text-center text-callout font-semibold {TONE[st.tone]}" aria-hidden="true">{st.letter}</span>
      <span class="flex min-w-0 flex-1 flex-col">
        <span class="truncate text-body text-ink">{p.name}</span>
        {#if p.dir}<span class="num truncate text-footnote text-faint">{p.dir}</span>{/if}
      </span>
    </button>
    {#if canAct}
      <TouchButton
        icon
        aria-label={`${isStaged ? copy.git.unstage : copy.git.stage} ${f.path}`}
        disabled={!!git.busy}
        onclick={() => act(isStaged ? "unstage" : "stage", [f.path])}
      >
        <Icon name={isStaged ? "minus" : "plus"} size={18} />
      </TouchButton>
    {/if}
  </div>
{/snippet}

{#snippet diffBody()}
  {#if git.diffLoading}
    <p class="px-8 py-12 text-center text-callout text-faint">{copy.git.loading.status}</p>
  {:else if git.diffError}
    <p class="px-8 py-12 text-center text-callout text-bad" role="alert">{git.diffError}</p>
  {:else}
    {#each filesOf as f (f.path)}
      {@const expanded = filesOf.length === 1 || openFile === f.path}
      <div class="border-b border-edge-soft">
        {#if filesOf.length > 1}
          <button
            type="button"
            class="flex min-h-12 w-full touch-manipulation items-center gap-2 px-4 py-2 text-left active:bg-sel"
            aria-expanded={expanded}
            onclick={() => (openFile = expanded ? "" : f.path)}
          >
            <Icon name={expanded ? "chevron-down" : "chevron-right"} size={16} />
            <span class="min-w-0 flex-1 truncate text-body text-ink">{f.path}</span>
            <span class="num shrink-0 text-footnote text-good">+{f.additions}</span>
            <span class="num shrink-0 text-footnote text-bad">−{f.deletions}</span>
          </button>
        {/if}
        {#if expanded}
          <div class="h-[60dvh] min-h-0"><ProjectGitDiff file={f} /></div>
        {/if}
      </div>
    {/each}
  {/if}
{/snippet}

<div class="flex h-full min-h-0 flex-col bg-canvas">
  {#if view && readable}
    <LargeTitle
      title={view.kind === "file" ? splitPath(view.file.path).name : shortSha(view.commit.sha)}
      back={{ label: graph ? copy.git.graph : copy.git.segments.changes, onclick: close }}
    />
    <div class="min-h-0 flex-1 overflow-y-auto overscroll-contain">
      {#if view.kind === "commit"}
        <p class="px-4 pb-3 text-callout text-ink">{view.commit.subject}</p>
      {/if}
      {@render diffBody()}
    </div>
    {#if git.error}
      <p class="mx-4 mb-2 shrink-0 rounded-xl bg-sel px-3 py-2 text-subhead text-bad" role="alert">{git.error}</p>
    {/if}
    {#if view.kind === "file" && (canAct || (!view.staged && canDiscard))}
      <div class="grid auto-cols-fr grid-flow-col shrink-0 gap-2 border-t border-edge-soft p-3 pb-[calc(env(safe-area-inset-bottom,0px)+12px)]">
        {#if canAct}
          <TouchButton wide variant="secondary" disabled={!!git.busy} onclick={stageFromView}>
            {view.staged ? copy.git.unstage : copy.git.stage}
          </TouchButton>
        {/if}
        {#if !view.staged && canDiscard}
          <TouchButton wide variant="danger" disabled={!!git.busy} onclick={discardFromView}>{copy.git.discard}</TouchButton>
        {/if}
      </div>
    {/if}
  {/if}
  <!-- Keep the list mounted behind a diff so returning preserves the reading position. -->
  <div hidden={!!view && readable} class={view && readable ? "hidden" : "flex min-h-0 flex-1 flex-col"}>
    <LargeTitle title={store.displayNameFor(project)} back={{ label: copy.projects.back, onclick: () => router.replace({ name: "tab", tab: "projects" }) }}>
      {#snippet trailing()}
        {#if readable}
          <TouchButton icon aria-label={copy.projects.refresh} loading={git.loading} disabled={git.loading} onclick={reload}>
            <Icon name="refresh-cw" size={20} />
          </TouchButton>
        {/if}
      {/snippet}
    </LargeTitle>

    {#if readable}<ProjectSections {project} value={graph ? "graph" : "git"} />{/if}
    <div class={graph ? "flex min-h-0 flex-1 flex-col gap-2" : "grid min-h-0 flex-1 grid-cols-[minmax(0,1fr)] content-start gap-4 overflow-y-auto overscroll-contain pb-6"}>
      {#if !readable}
        <p class="px-4 py-6 text-callout text-faint">{copy.git.unavailable.body}</p>
      {:else if git.error}
        <div class="flex flex-col items-center gap-3 px-8 py-8 text-center" role="alert">
          <span class="text-headline text-ink">{copy.git.failed.body}</span>
          <span class="text-callout text-bad">{git.error}</span>
          <TouchButton variant="secondary" loading={git.loading} onclick={reload}>{copy.projects.retry}</TouchButton>
          {#if graph && historyRef}<TouchButton onclick={() => filterHistory("")}>{copy.git.allBranches}</TouchButton>{/if}
        </div>
      {:else if !snap}
        <p class="px-8 py-12 text-center text-callout text-faint">{copy.git.loading.status}</p>
      {:else}
        <div class="max-h-[35dvh] shrink-0 overflow-y-auto">
          <ListGroup>
            <ListRow
              label={snap.branch || copy.git.detached}
              icon="git-branch"
              detail={snap.upstream || undefined}
              detailMono
              value={[sync.ahead > 0 ? fill(copy.git.ahead, { n: sync.ahead }) : "", sync.behind > 0 ? fill(copy.git.behind, { n: sync.behind }) : ""].filter(Boolean).join(" · ") || shortSha(snap.head)}
              chevron={trees.length > 1}
              onclick={trees.length > 1 ? () => (pickingTree = !pickingTree) : undefined}
            />
            {#if pickingTree}
              {#each trees as t (t.path)}
                <ListRow
                  label={t.branch || shortSha(t.head)}
                  detail={t.path}
                  detailMono
                  checked={t.path === snap.path}
                  onclick={() => {
                    worktree = t.path;
                    pickingTree = false;
                  }}
                />
              {/each}
            {/if}
          </ListGroup>
        </div>

        {#if graph}
          <CommitGraph snapshot={snap} {project} {historyRef} onfilter={filterHistory} onopen={(commit) => open({ kind: "commit", commit })}
            more={snap.hasMore && limit < MAX_HISTORY} onmore={more} loading={git.loading} {switchingFilter} />
        {:else}
          {#if canAct}
            <div class="grid grid-cols-3 gap-2 px-4">
              {#each ["fetch", "pull", "push"] as const as a (a)}
                <TouchButton wide variant="secondary" loading={git.busy === a || (a === "fetch" && git.fetching)} disabled={!!git.busy || git.fetching} onclick={() => act(a)}>
                  {BUSY[a]}
                </TouchButton>
              {/each}
            </div>
          {/if}

          {#if operation}
            <p class="mx-4 rounded-xl bg-sel px-3 py-2 text-subhead text-warn" role="status">{copy.git.operation.body}</p>
          {:else if conflicts}
            <p class="mx-4 rounded-xl bg-sel px-3 py-2 text-subhead text-warn" role="status">{copy.git.conflicts.body}</p>
          {/if}

          <div class="px-4">
            <SegmentBar segments={segments} value={section} onchange={(id) => (section = id)} label={copy.git.title} />
          </div>

          {#if section === "changes"}
            {#if staged.length === 0 && unstaged.length === 0}
              <p class="px-8 py-6 text-center text-callout text-faint">{copy.git.noChanges.body}</p>
            {/if}
            {#if staged.length > 0}
              <SectionHeader title={copy.git.staged} count={staged.length} />
              <ListGroup>{#each staged as f (f.path)}{@render fileRow(f, true)}{/each}</ListGroup>
              {#if canAct}
                <div class="px-4">
                  <TouchButton wide variant="secondary" disabled={!!git.busy} onclick={() => act("unstage", staged.map((f) => f.path))}>{copy.git.unstageAll}</TouchButton>
                </div>
              {/if}
            {/if}
            {#if unstaged.length > 0}
              <SectionHeader title={copy.git.unstaged} count={unstaged.length} />
              <ListGroup>{#each unstaged as f (f.path)}{@render fileRow(f, false)}{/each}</ListGroup>
              {#if canAct}
                <div class="px-4">
                  <TouchButton wide variant="secondary" disabled={!!git.busy} onclick={() => act("stage", unstaged.map((f) => f.path))}>{copy.git.stageAll}</TouchButton>
                </div>
              {/if}
            {/if}
            {#if canAct && (staged.length > 0 || operation === "merge")}
              <div class="flex flex-col gap-2 px-4">
                <textarea
                  class="min-h-24 w-full resize-none rounded-xl border border-edge bg-panel p-3 text-body text-ink placeholder:text-faint"
                  placeholder={copy.git.messagePlaceholder}
                  aria-label={copy.git.messagePlaceholder}
                  bind:value={git.message}
                ></textarea>
                <TouchButton wide variant="primary" loading={git.busy === "commit"} disabled={!canCommit} onclick={() => act("commit")}>
                  {copy.git.commit}
                </TouchButton>
              </div>
            {/if}
          {:else}
            {#if canAct && (snap.files.length > 0)}
              <div class="px-4">
                <TouchButton wide variant="secondary" disabled={!!git.busy} onclick={() => git.stashAction("stash", "", snap.head, historyRef, limit)}>{copy.git.stashChanges}</TouchButton>
              </div>
            {/if}
            {#if git.stashes.length === 0}
              <p class="px-8 py-6 text-center text-callout text-faint">{copy.git.noStashes.body}</p>
            {:else}
              <ListGroup>
                {#each git.stashes as s (s.sha)}
                  <div class="flex flex-col gap-1 border-b border-edge-soft py-2.5 pr-2 pl-4 last:border-b-0">
                    <span class="truncate text-body text-ink">{s.subject}</span>
                    <span class="flex items-center gap-1">
                      <span class="num flex-1 text-subhead text-subtext">{s.selector} · {agoShort(s.date)}</span>
                      {#if canAct}
                        <TouchButton disabled={!!git.busy} onclick={() => git.stashAction("apply", s.sha, snap.head, historyRef, limit)}>{copy.git.apply}</TouchButton>
                      {/if}
                      {#if canDiscard}
                        <TouchButton disabled={!!git.busy} onclick={() => git.stashAction("pop", s.sha, snap.head, historyRef, limit)}>{copy.git.pop}</TouchButton>
                        <TouchButton variant="danger" disabled={!!git.busy} onclick={() => dropStash(s.sha, s.subject)}>{copy.git.drop}</TouchButton>
                      {/if}
                    </span>
                  </div>
                {/each}
              </ListGroup>
            {/if}
          {/if}
        {/if}
      {/if}
    </div>
  </div>
</div>
