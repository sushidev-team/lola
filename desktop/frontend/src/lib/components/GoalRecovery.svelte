<script lang="ts">
  import { onDestroy, tick } from "svelte";
  import { DaemonService } from "@bindings/desktop";
  import type { BriefContract, RecoveryView } from "@bindings/internal/goal";
  import { recoveryLabels as labels, recoveryConsequences as consequences, recoveryProblem, progressState } from "../goalrecovery";
  import { splitLines } from "../goalplanedit";
  import Button from "./Button.svelte";
  import GoalBrief from "./GoalBrief.svelte";
  let { goalID, project, disabled = false, onopen = () => {}, onclose = () => {} }: { goalID: string; project: string; disabled?: boolean; onopen?: () => void; onclose?: () => void } = $props();
  type Decision = { snapshot: RecoveryView; action: string; reason: string; contract: BriefContract; url: string };
  let open = $state(false), busy = $state(false), uncertain = $state(false), discard = $state(false);
  let data = $state<RecoveryView | null>(null), reviewed = $state<Decision | null>(null);
  let action = $state(""), reason = $state(""), url = $state(""), title = $state(""), outcome = $state(""), acceptance = $state(""), nonGoals = $state("");
  let error = $state(""), notice = $state("");
  let area = $state<HTMLDivElement>(), alert = $state<HTMLParagraphElement>();
  let active = true, generation = 0;
  onDestroy(() => { active = false; generation++; });
  const field = "mt-1 w-full min-w-0 rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent";
  async function fail(message: string) { error = message; await tick(); alert?.focus(); alert?.scrollIntoView({block:"nearest"}); }
  async function load() {
    if (busy) return;
    open = true; onopen(); busy = true; error = "";
    const seq = ++generation, id = goalID, scope = project;
    try {
      const value = await DaemonService.GoalRecovery(id);
      if (!active || seq !== generation || id !== goalID || scope !== project) return;
      if (value.goal.id !== id || value.goal.project !== scope || !value.goal.revision) throw Error("recovery snapshot does not match this goal");
      data = structuredClone(value); action = ""; reviewed = null; uncertain = false; discard = false;
      await tick(); area?.focus();
    } catch (e) { if (active && seq === generation) await fail(`Could not load recovery: ${String(e)}. Reload the local snapshot before another decision.`); }
    finally { if (active && seq === generation) busy = false; }
  }
  function close() {
    if (busy) return;
    if (action && !reviewed && !uncertain && !discard) { discard = true; return; }
    generation++; open = false; data = null; reviewed = null; action = ""; uncertain = false; error = ""; notice = ""; discard = false; onclose();
  }
  async function choose(next: string) {
    if (!data || busy || uncertain || !(data.actions ?? []).includes(next) || !labels[next]) return;
    action = next; reason = ""; url = data.deliveryEvidenceUrl; title = data.brief.title; outcome = data.brief.outcome;
    acceptance = data.brief.acceptance?.join("\n") ?? ""; nonGoals = data.brief.non_goals?.join("\n") ?? "";
    error = ""; notice = ""; reviewed = null;
    await tick(); area?.focus(); area?.scrollIntoView({block:"nearest"});
  }
  async function review() {
    if (!data || busy || uncertain || !(data.actions ?? []).includes(action)) return;
    const contract = { title: title.trim(), outcome: outcome.trim(), acceptance: splitLines(acceptance), non_goals: splitLines(nonGoals) };
    const problem = recoveryProblem(action, reason.trim(), contract, url.trim());
    if (problem) { await fail(problem); return; }
    reviewed = {snapshot: structuredClone($state.snapshot(data)), action, reason: reason.trim(), contract, url: url.trim()};
    error = ""; await tick(); area?.focus(); area?.scrollIntoView({block:"nearest"});
  }
  async function apply() {
    if (!reviewed || busy || uncertain || error) return;
    const decision = structuredClone($state.snapshot(reviewed));
    const id = decision.snapshot.goal.id, revision = decision.snapshot.goal.revision;
    busy = true;
    try {
      switch (decision.action) {
        case "sync-brief": await DaemonService.GoalSyncBrief(id); break;
        case "accept-brief": await DaemonService.GoalAcceptBrief({id,revision,source_id:decision.snapshot.pendingBrief!.id,contract:decision.contract,reason:decision.reason}); break;
        case "dismiss-progress": await DaemonService.GoalDismissProgress({id,revision,update_id:decision.snapshot.latestProgress!.id,reason:decision.reason}); break;
        case "delivery-evidence": await DaemonService.GoalDeliveryEvidence({id,revision,url:decision.url}); break;
        case "retry-verification": case "retry-replan": await DaemonService.GoalControl(id,revision,decision.action,decision.reason); break;
        default: throw Error("Unknown recovery action");
      }
      if (!active) return;
      notice = "Action confirmed. Review the refreshed state before another decision.";
      // Lock the reviewed request until a new local snapshot has been loaded.
      uncertain = true; busy = false; await load();
    } catch (e) { if (active) { uncertain = true; await fail(`Action was not confirmed: ${String(e)}. It may already have happened. Reload the local snapshot; do not repeat this request.`); } }
    finally { if (active) busy = false; }
  }
</script>

<div class="border-t border-edge pt-3">
  {#if !open}<Button variant="secondary" {disabled} onclick={() => void load()}>Recovery and Linear</Button>
  {:else}
    <section aria-label="Goal recovery" class="space-y-3">
      <div class="flex flex-wrap items-baseline justify-between gap-2"><h4 class="font-medium text-ink">Recovery and Linear</h4><Button disabled={busy} onclick={close}>Close recovery</Button></div>
      {#if notice}<p role="status" class="text-sm text-ink">{notice}</p>{/if}
      {#if busy}<p role="status" class="text-sm text-faint">Waiting for the daemon. No repeated submission.</p>{/if}
      {#if error}<p bind:this={alert} tabindex="-1" role="alert" class="selectable whitespace-pre-wrap break-words text-sm text-bad">{error}</p>{/if}
      {#if discard}<div role="group" aria-label="Discard recovery draft" class="space-y-2"><p class="text-sm text-warn">Discard this unsaved recovery draft?</p><div class="flex gap-2"><Button variant="danger" onclick={close}>Discard draft</Button><Button onclick={() => discard=false}>Keep editing</Button></div></div>{/if}
      {#if uncertain || !data}<Button disabled={busy} onclick={() => void load()}>Reload local snapshot</Button>{/if}
      {#if data}
        <p class="text-sm text-faint">{data.goal.title} · reviewed revision {data.goal.revision} · {data.goal.state}</p>
        {#if data.goal.syncError}<p class="selectable break-words text-sm text-warn">{data.goal.syncError}</p>{/if}
        {#if data.goal.reason}<p class="selectable whitespace-pre-wrap break-words text-sm text-warn">{data.goal.reason}</p>{/if}
        <div bind:this={area} tabindex="-1" role="group" aria-label={reviewed ? "Review recovery decision" : action ? "Edit recovery decision" : "Recovery snapshot"} class="space-y-3">
          {#if action}
            <h5 class="font-medium text-ink">{labels[action]}</h5>
            <p class="text-sm text-faint">{consequences[action]}</p>
            {#if action === "accept-brief" && data.pendingBrief}
              <details class="text-sm" open><summary class="cursor-pointer text-ink">Exact Linear source</summary><p class="selectable mt-2 break-words text-ink">{data.pendingBrief.title}</p><pre class="selectable mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-words text-faint">{data.pendingBrief.description}</pre><p class="selectable mt-1 break-all text-faint">Source {data.pendingBrief.id}</p></details>
              <details class="text-sm"><summary class="cursor-pointer text-ink">Retained tasks and acceptance</summary>{#each data.goal.tasks ?? [] as task}<p class="mt-2 break-words text-ink">{task.title}</p><ul class="list-disc pl-5 text-faint">{#each task.acceptance ?? [] as criterion}<li class="selectable break-words">{criterion}</li>{/each}</ul>{/each}</details>
            {/if}
            {#if action === "dismiss-progress" && data.latestProgress}<p class="selectable break-all text-sm text-faint">Update {data.latestProgress.id} · {progressState(data.latestProgress)}</p><pre class="selectable max-h-64 overflow-auto whitespace-pre-wrap break-words text-sm text-faint">{data.latestProgress.summary}</pre>{/if}
            {#if reviewed}
              <p class="text-sm text-ink">Apply to reviewed revision {reviewed.snapshot.goal.revision}.</p>
              {#if action === "accept-brief"}<div class="grid gap-4 sm:grid-cols-2"><GoalBrief brief={reviewed.snapshot.brief} label="Current brief" /><GoalBrief brief={reviewed.contract} label="Reviewed clarification" /></div>{/if}
              {#if action === "delivery-evidence"}<p class="selectable break-all text-sm text-ink">{reviewed.url}</p>{/if}
              {#if reviewed.reason}<p class="selectable whitespace-pre-wrap break-words text-sm text-ink">Reason: {reviewed.reason}</p>{/if}
              <div class="flex flex-wrap gap-2"><Button variant="primary" disabled={busy || uncertain || !!error || discard} onclick={apply}>Confirm {labels[action].toLowerCase()}</Button><Button disabled={busy || uncertain || discard} onclick={() => { reviewed=null; }}>Back to editing</Button></div>
            {:else}
              <form class="space-y-3" onsubmit={(e) => { e.preventDefault(); void review(); }}><fieldset disabled={busy || uncertain || discard} class="min-w-0 space-y-3">
                {#if action === "accept-brief"}
                  <GoalBrief brief={data.brief} label="Current brief" />
                  <p class="text-sm text-faint">Write the clarified contract explicitly. Explain how the retained tasks still cover it; broader work needs a new goal.</p>
                  <label class="block text-sm text-faint">Clarified title<input class={field} required bind:value={title} /></label>
                  <label class="block text-sm text-faint">Clarified outcome<textarea class={field} rows="3" required bind:value={outcome}></textarea></label>
                  <label class="block text-sm text-faint">Clarified acceptance criteria<textarea class={field} rows="3" required bind:value={acceptance}></textarea></label>
                  <label class="block text-sm text-faint">Non-goals<textarea class={field} rows="2" bind:value={nonGoals}></textarea></label>
                  <p class="text-sm text-faint">One acceptance criterion or non-goal per line.</p>
                {/if}
                {#if action === "delivery-evidence"}<label class="block text-sm text-faint">Shared evidence URL<input class={field} type="url" required bind:value={url} /></label>{/if}
                {#if action !== "sync-brief" && action !== "delivery-evidence"}<label class="block text-sm text-faint">{["accept-brief","dismiss-progress"].includes(action) ? "Decision reason (required)" : "Reason (optional)"}<textarea class={field} rows="3" bind:value={reason} required={["accept-brief","dismiss-progress"].includes(action)}></textarea></label>{/if}
                <Button type="submit" variant="primary">Review recovery decision</Button>
              </fieldset></form>
            {/if}
          {:else}
            <p class="text-sm text-faint">This snapshot is local. Reloading it does not contact Linear or start work. Each recovery action is reviewed separately.</p>
            <GoalBrief brief={data.brief} label="Current brief" />
            {#if data.pendingBrief}<p class="text-sm text-warn">A changed Linear brief needs reconciliation.</p><details class="text-sm"><summary class="cursor-pointer text-ink">Pending Linear source</summary><p class="selectable mt-2 break-words text-ink">{data.pendingBrief.title}</p><pre class="selectable max-h-64 overflow-auto whitespace-pre-wrap break-words text-faint">{data.pendingBrief.description}</pre></details>{/if}
            {#if data.latestBrief}<details class="text-sm"><summary class="cursor-pointer text-ink">Latest accepted brief decision</summary><p class="selectable my-2 break-words text-faint">{data.latestBrief.reason} · {data.latestBrief.acceptedAt}</p><div class="grid gap-4 sm:grid-cols-2"><GoalBrief brief={data.latestBrief.before} label="Previous brief" /><GoalBrief brief={data.latestBrief.after} label="Accepted clarification" /></div></details>{/if}
            {#if data.latestProgress}<div class="space-y-2 border-t border-edge pt-3 text-sm"><h5 class="font-medium text-ink">Latest Linear progress update</h5><p class="text-faint">{progressState(data.latestProgress)}</p><p class="selectable break-all text-faint">{data.latestProgress.id}</p><pre class="selectable max-h-64 overflow-auto whitespace-pre-wrap break-words text-faint">{data.latestProgress.summary}</pre>{#if data.latestProgress.resolution}<p class="selectable break-words text-ink">Resolution: {data.latestProgress.resolution}</p>{/if}</div>{/if}
            {#if data.progressError}<p class="selectable break-words text-sm text-warn">{data.progressError}</p>{/if}
            {#if data.deliveryEvidenceUrl}<div class="text-sm"><h5 class="font-medium text-ink">Shared delivery evidence</h5><p class="selectable break-all text-faint">{data.deliveryEvidenceUrl}</p></div>{/if}
            <p class="text-sm text-faint">Available actions depend on this revision and remaining budgets. The daemon checks live jobs and external state again before applying a decision.</p>
            <div class="flex flex-wrap gap-2">{#each (data.actions ?? []).filter(a => labels[a]) as available}<Button variant="secondary" disabled={busy || uncertain || !!error || discard} onclick={() => choose(available)}>{labels[available]}</Button>{/each}<Button disabled={busy || discard} onclick={() => void load()}>Reload local snapshot</Button></div>
          {/if}
        </div>
      {/if}
    </section>
  {/if}
</div>
