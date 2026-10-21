<script lang="ts">
  import { tick, untrack } from "svelte";
  import { DaemonService } from "@bindings/desktop";
  import type { Overview } from "@bindings/internal/goal";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import Button from "./Button.svelte";
  import Select from "./Select.svelte";

  let { project }: { project: string } = $props();
  let expanded = $state(false);
  let goals = $state<Overview[]>([]);
  let selected = $state("");
  let loading = $state(false);
  let loaded = $state(false);
  let error = $state("");
  let busy = $state(false);
  let pending = $state<{ goal: Overview; action: string } | null>(null);
  let reason = $state("");
  let reasonField = $state<HTMLTextAreaElement>();
  let controlsArea = $state<HTMLDivElement>();
  let toggle = $state<HTMLButtonElement>();
  const selections = new Map<string, string>();
  let generation = 0;
  const current = $derived(goals.find((g) => g.id === selected));
  const labels: Record<string, string> = { pause: "Pause", resume: "Resume", "stop-workers": "Stop workers", cancel: "Cancel goal" };
  const consequences: Record<string, string> = {
    pause: "Hold new tasks, verification and integration. Running workers continue until stopped separately.",
    resume: "Resume within the accepted scope and remaining budgets. Lola rechecks Linear and execution policy first.",
    "stop-workers": "Stop the current workers and keep their files and evidence. Resume is required to dispatch again.",
    cancel: "End this goal and stop its workers. Keep its issues, branches, files and audit history. Cancellation cannot be resumed.",
  };

  async function refresh(scope = project) {
    if (loading || busy || pending) return;
    const gen = ++generation;
    loading = true;
    try {
      const result = await DaemonService.GoalOverview(scope);
      if (gen !== generation || scope !== project) return;
      goals = result ?? [];
      loaded = true;
      error = "";
      if (!goals.some((g) => g.id === selected)) selected = goals[0]?.id || "";
      selections.set(scope, selected);
    } catch (e) {
      if (gen === generation && scope === project) error = `Could not load goals: ${String(e)}. Refresh to try again.`;
    } finally {
      if (gen === generation) loading = false;
    }
  }

  // A project switch invalidates earlier responses and confirmations. Poll only
  // while this section is open; never refresh underneath a reviewed action.
  $effect(() => {
    const scope = project;
    const open = expanded;
    generation++;
    goals = [];
    selected = selections.get(scope) || "";
    loaded = false;
    loading = false;
    error = "";
    pending = null;
    if (!open) return;
    untrack(() => void refresh(scope));
    const timer = window.setInterval(() => void refresh(scope), 5000);
    return () => { generation++; window.clearInterval(timer); };
  });

  async function reviewAction(g: Overview, action: string) {
    pending = { goal: g, action };
    reason = "";
    error = "";
    await tick();
    reasonField?.focus();
  }
  async function closeAction() {
    pending = null;
    error = "";
    await refresh();
    await tick();
    (controlsArea || toggle)?.focus();
  }
  async function submit() {
    const reviewed = pending;
    if (!reviewed || busy) return;
    const scope = project;
    busy = true;
    try {
      await DaemonService.GoalControl(reviewed.goal.id, reviewed.goal.revision, reviewed.action, reason.trim());
      if (scope === project) { pending = null; reason = ""; }
    } catch (e) {
      if (scope === project) error = `Action was not confirmed: ${String(e)}. Close this action and refresh before trying again.`;
    } finally {
      busy = false;
      if (scope === project && !pending) {
        await refresh(scope);
        await tick();
        (controlsArea || toggle)?.focus();
      }
    }
  }
  function openSession(id: string) { nav.select(id); nav.goCockpit(project); }
  const number = (n: number) => n.toLocaleString();
</script>

<section class="rounded-[10px] border border-edge" aria-label="Project goals">
  <button bind:this={toggle} class="flex w-full items-center justify-between gap-3 rounded-[10px] px-3 py-2 text-left hover:bg-sel/50" aria-expanded={expanded} onclick={() => (expanded = !expanded)}>
    <span><span class="block font-medium text-ink">Goals</span><span class="block text-sm text-faint">Autonomous work, verified progress and controls</span></span>
    <span class="text-sm text-faint">{expanded ? "Hide" : "Show"}</span>
  </button>
  {#if expanded}
    <div class="flex flex-col gap-3 border-t border-edge p-3">
      <div class="flex items-center justify-between gap-2">
        <p class="text-sm text-faint">Work stays on isolated goal branches. You merge the final PRs.</p>
        <Button size="sm" disabled={loading || busy || !!pending} onclick={() => void refresh()}>Refresh</Button>
      </div>
      {#if error}<p role="alert" class="selectable text-sm text-bad">{error}</p>{/if}
      {#if !loaded && loading}<p role="status" class="text-sm text-faint">Loading goals…</p>{/if}
      {#if loaded && goals.length === 0}
        <p class="text-sm text-faint">No goals in this project yet. Create a reviewed goal with <code class="text-ink">lola goal</code>; it will appear here.</p>
      {/if}
      {#if goals.length > 0}
        <label class="flex flex-col gap-1 text-sm text-faint">Goal
          <Select class="min-w-0" bind:value={selected} onchange={(e) => selections.set(project, e.currentTarget.value)} disabled={busy || !!pending}>
            {#each goals as g (g.id)}<option value={g.id}>{g.title} · {g.state}</option>{/each}
          </Select>
        </label>
      {/if}
      {#if current}
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h3 class="min-w-0 break-words font-medium text-ink">{current.title}</h3>
          <span class="text-sm text-faint">{current.state}{current.stopping ? " · workers stopping or stopped" : ""}</span>
        </div>
        <p class="selectable max-w-prose whitespace-pre-wrap break-words text-sm text-ink">{current.outcome}</p>
        {#if current.syncError}<p role="status" class="selectable text-sm text-warn">Linear needs reconciliation: {current.syncError}</p>{/if}
        {#if current.reason}<p class="selectable whitespace-pre-wrap break-words text-sm text-warn">{current.reason}</p>{/if}
        <div class="flex flex-wrap items-center gap-2 text-sm">
          {#if current.parent.confirmed}
            <Button size="sm" variant="secondary" onclick={() => store.openURL(`https://linear.app/issue/${current.parent.id}`)}>Open {current.parent.identifier || "goal"} in Linear</Button>
          {/if}
          {#if current.milestoneName}<span class="text-faint">Milestone: {current.milestoneName}</span>{/if}
          <span class="num text-faint">Revision {current.revision}</span>
        </div>
        <details class="text-sm">
          <summary class="cursor-pointer text-ink">Acceptance criteria</summary>
          <ul class="mt-2 list-disc space-y-1 pl-5 text-faint">{#each current.acceptance as criterion}<li class="selectable break-words">{criterion}</li>{/each}</ul>
          {#if current.nonGoals?.length}<p class="mt-3 text-ink">Outside this goal</p><ul class="mt-1 list-disc space-y-1 pl-5 text-faint">{#each current.nonGoals as nonGoal}<li class="selectable break-words">{nonGoal}</li>{/each}</ul>{/if}
        </details>
        {#if current.authorized}
          <div class="border-y border-edge py-3 text-sm">
            <h4 class="font-medium text-ink">Observed usage</h4>
            <p class="num mt-1 text-ink">{number(current.tokens)} / {number(current.tokenLimit)} tokens · {current.coverage.runs} / {current.runLimit} calls</p>
            <p class="num text-faint">${current.spendUSD.toFixed(2)} reported estimate{current.spendLimitUSD > 0 ? ` / $${current.spendLimitUSD.toFixed(2)} limit` : ""}</p>
            <p class="mt-1 text-faint">{current.coverage.partial} partial · {current.coverage.missing} missing usage · {current.coverage.cost_unavailable} without dollar cost. Missing usage is not zero.</p>
            <p class="mt-1 text-faint">Deadline: {new Date(current.deadline).toLocaleString()}</p>
          </div>
        {:else}<p class="text-sm text-faint">Execution has not been authorized.</p>{/if}
        <div>
          <h4 class="mb-1 font-medium text-ink">Tasks</h4>
          <ol class="divide-y divide-edge">
            {#each current.tasks as task, i (task.id)}
              <li class="py-2">
                <details>
                  <summary class="cursor-pointer text-sm"><span class="text-ink">{i + 1}. {task.title}</span> <span class="text-faint">— {task.state}{task.replacedBy?.length ? " · decomposed" : ""}</span></summary>
                  <div class="mt-2 flex flex-col gap-2 pl-3 text-sm">
                    {#if task.apps?.length}<p class="text-faint">Repositories: {task.apps.join(", ")}</p>{/if}
                    {#if task.blockedBy?.length}<p class="text-faint">Depends on tasks {task.blockedBy.join(", ")}</p>{/if}
                    <ul class="list-disc space-y-1 pl-4 text-faint">{#each task.acceptance as criterion}<li class="selectable break-words">{criterion}</li>{/each}</ul>
                    {#if task.issue.confirmed}<Button size="sm" class="self-start" onclick={() => store.openURL(`https://linear.app/issue/${task.issue.id}`)}>Open {task.issue.identifier || "task"} in Linear</Button>{/if}
                    {#each task.attempts as attempt, n (attempt.id)}
                      <div class="border-t border-edge pt-2">
                        <div class="flex flex-wrap items-center gap-2"><span class="text-ink">Attempt {n + 1} · {attempt.state}</span>{#if attempt.sessionId}<Button size="sm" onclick={() => openSession(attempt.sessionId)}>View session</Button>{/if}</div>
                        {#if attempt.error}<p class="selectable whitespace-pre-wrap break-words text-warn">{attempt.error}</p>{/if}
                        {#if attempt.summary}<p class="selectable whitespace-pre-wrap break-words text-faint">{attempt.summary}</p>{/if}
                      </div>
                    {/each}
                  </div>
                </details>
              </li>
            {/each}
          </ol>
        </div>
        {#if current.members?.length}
          <div class="border-t border-edge pt-3">
            <h4 class="font-medium text-ink">{current.verified ? "Verified deliverable" : "Integrated revisions"}</h4>
            {#each current.members as member (member.id)}
              <div class="mt-2 flex flex-wrap items-center gap-2 text-sm">
                <span class="break-all text-ink">{member.repo || member.id}</span><span class="text-faint">to {member.target}</span>
                <code class="selectable text-faint" title={member.commit}>{member.commit?.slice(0, 12) || "Not initialized"}</code>
                {#if member.prUrl}<Button size="sm" variant="secondary" onclick={() => store.openURL(member.prUrl)}>PR #{member.prNumber} · {member.deliveryState}</Button>{/if}
              </div>
            {/each}
          </div>
        {/if}
        {#if pending}
          <div class="border-t border-edge pt-3">
            <h4 class="font-medium text-ink">{labels[pending.action]} — {pending.goal.title}</h4>
            <p class="mt-1 text-sm text-faint">{consequences[pending.action]}</p>
            <p class="mt-1 text-sm text-faint">Applies to reviewed revision {pending.goal.revision}.</p>
            <label class="mt-2 flex flex-col gap-1 text-sm text-faint">Reason (optional)
              <textarea bind:this={reasonField} class="min-h-16 rounded border border-edge bg-canvas px-2 py-1.5 text-ink" bind:value={reason} maxlength="2000" disabled={busy}></textarea>
            </label>
            <div class="mt-2 flex flex-wrap gap-2">
              <Button variant={pending.action === "cancel" ? "danger-solid" : "primary"} disabled={busy || !!error} onclick={submit}>{busy ? "Applying…" : labels[pending.action]}</Button>
              <Button disabled={busy} onclick={closeAction}>Close action</Button>
            </div>
          </div>
        {:else if current.actions?.length}
          <div bind:this={controlsArea} tabindex="-1" role="group" aria-label="Goal controls" class="flex flex-wrap gap-2 border-t border-edge pt-3">
            {#each current.actions as action}<Button variant={action === "cancel" ? "danger" : "secondary"} disabled={loading || !!error || busy} onclick={() => reviewAction(current, action)}>{labels[action]}</Button>{/each}
          </div>
        {/if}
      {/if}
    </div>
  {/if}
</section>
