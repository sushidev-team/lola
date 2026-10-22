<script lang="ts">
  import { onDestroy, tick } from "svelte";
  import { DaemonService } from "@bindings/desktop";
  import type { Record as Proposal, ProposedTask, Output, EditRequest } from "@bindings/internal/goalplan";
  import { graphProblems, moveTask, removeTask, splitLines, outputSignature } from "../goalplanedit";
  import Button from "./Button.svelte";
  import Checkbox from "./Checkbox.svelte";
  import GoalPlanTasks from "./GoalPlanTasks.svelte";

  let { proposal, onupdated, oncancel }: { proposal: Proposal; onupdated: (record: Proposal) => void; oncancel: () => void } = $props();
  type TaskForm = ProposedTask & { key: string; acceptanceText: string; pathText: { [id: string]: string } };
  const formTask = (task: ProposedTask): TaskForm => ({ ...task, key: crypto.randomUUID(), acceptanceText: (task.acceptance ?? []).join("\n"), pathText: Object.fromEntries(Object.entries(task.paths ?? {}).map(([id, paths]) => [id, (paths ?? []).join("\n")])) });
  // The enclosing planner keys this editor by proposal identity and revision.
  let tasks = $state<TaskForm[]>([]);
  let unknowns = $state("");
  let reason = $state("");
  let reviewed = $state<EditRequest | null>(null);
  let acknowledged = $state(false);
  let busy = $state(false);
  let uncertain = $state(false);
  let error = $state("");
  let discard = $state(false);
  let heading = $state<HTMLHeadingElement>();
  let active = true;
  onDestroy(() => { active = false; });
  $effect(() => {
    // Only runs when a different immutable proposal is mounted.
    tasks = (proposal.output?.tasks ?? []).map(formTask);
    unknowns = (proposal.output?.unknowns ?? []).join("\n");
  });
  const field = "mt-1 w-full min-w-0 rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent";
  const output = $derived<Output>({ tasks: tasks.map(t => ({ title: t.title.trim(), description: t.description, acceptance: splitLines(t.acceptanceText), apps: t.apps, label_ids: t.label_ids, blocked_by: t.blocked_by, covers: t.covers, paths: Object.fromEntries((t.apps ?? []).map(id => [id, splitLines(t.pathText[id] ?? "")])) })), unknowns: splitLines(unknowns) });
  const problems = $derived(graphProblems(output, proposal));
  const changed = $derived(outputSignature(output) !== outputSignature(proposal.output ?? { tasks: [] }));
  function toggle<T>(values: T[] | null | undefined, item: T, checked: boolean): T[] {
    return checked ? [...new Set([...(values ?? []), item])] : (values ?? []).filter(v => v !== item);
  }
  async function focusHeading() { await tick(); heading?.focus(); }
  function review() {
    if (!changed || problems.length || reason.trim().length < 8 || busy) return;
    reviewed = JSON.parse(JSON.stringify({ id: proposal.id, project: proposal.input.request.project, revision: proposal.revision, edit_id: crypto.randomUUID(), reason: reason.trim(), output }));
    acknowledged = false; error = ""; void focusHeading();
  }
  async function save() {
    if (!reviewed || !acknowledged || busy || uncertain) return;
    busy = true; error = "";
    try {
      const result = await DaemonService.GoalEditPlan(reviewed);
      if (active) onupdated(result);
    } catch (e) {
      uncertain = true;
      error = `The revision was not confirmed: ${String(e)}. Refresh the saved proposal before another decision. Your edit ID is retained below.`;
    } finally { busy = false; }
  }
  async function refresh() {
    if (busy) return;
    busy = true;
    try {
      const result = await DaemonService.GoalProposal(proposal.id);
      if (result.input.request.project !== proposal.input.request.project || result.id !== proposal.id || result.input.repair) throw new Error("Proposal identity changed");
      if (active) onupdated(result);
    } catch (e) { error = `Could not refresh the proposal: ${String(e)}. Refresh again when the daemon is available.`; }
    finally { busy = false; }
  }
</script>

<section aria-label="Edit task proposal" class="space-y-4 border-t border-edge pt-4">
  <div>
    <h4 bind:this={heading} tabindex="-1" class="font-medium text-ink">{reviewed ? "Review proposal revision" : "Edit task proposal"}</h4>
    <p class="mt-1 max-w-prose text-sm text-faint">{reviewed ? `Review the complete replacement for revision ${reviewed.revision}. Saving preserves the original model proposal and your reason for this change.` : "Revise the tasks, scope and dependencies. Your goal criteria above stay fixed. Each task needs an observable result and at least one writable path."}</p>
  </div>
  {#if error}<p role="alert" class="selectable whitespace-pre-wrap break-words text-sm text-bad">{error}</p>{/if}
  {#if reviewed}
    <p class="selectable break-all text-sm text-faint">Edit ID: {reviewed.edit_id}</p>
    <p class="selectable whitespace-pre-wrap break-words text-sm text-ink">Reason: {reviewed.reason}</p>
    <GoalPlanTasks output={reviewed.output} labels={proposal.input.labels} />
    <details class="text-sm"><summary class="cursor-pointer text-ink">Compare with saved revision {proposal.revision}</summary><div class="mt-3">{#if proposal.output}<GoalPlanTasks output={proposal.output} labels={proposal.input.labels} />{/if}</div></details>
    {#if busy}<p role="status" class="text-sm text-ink">Waiting for the saved proposal…</p>{/if}
    {#if uncertain}
      <p class="max-w-prose text-sm text-faint">Refresh returns to the current saved revision, including any confirmed edit. It does not resend this change. If it was not saved, you can edit that revision again.</p>
      <Button disabled={busy} onclick={() => void refresh()}>Refresh saved proposal</Button>
    {:else}
      <label class="flex items-start gap-2 text-sm text-ink"><Checkbox bind:checked={acknowledged} disabled={busy} />I reviewed this revision, including dependencies, repository scope and acceptance coverage.</label>
      <p class="max-w-prose text-sm text-faint">Saving rechecks Linear routing. It makes no model call, creates no issues and starts no workers. Accepting the saved revision as a goal is a separate step.</p>
      <div class="flex flex-wrap gap-2">
        <Button variant="primary" disabled={!acknowledged || busy} onclick={() => void save()}>Save proposal revision</Button>
        <Button disabled={busy} onclick={() => { reviewed = null; acknowledged = false; void focusHeading(); }}>Back to editing</Button>
      </div>
    {/if}
  {:else}
    <form class="space-y-4" onsubmit={(e) => { e.preventDefault(); review(); }}>
      <div class="divide-y divide-edge">
        {#each tasks as task, i (task.key)}
          <details open={i === 0} class="py-3">
            <summary class="cursor-pointer break-words text-sm font-medium text-ink">Task {i + 1}: {task.title || "Untitled task"}</summary>
            <div class="mt-3 space-y-3">
              <div class="flex flex-wrap gap-2">
                <Button size="sm" disabled={!moveTask(tasks, i, -1)} onclick={() => { tasks = moveTask(tasks, i, -1) ?? tasks; }}>Move earlier</Button>
                <Button size="sm" disabled={!moveTask(tasks, i, 1)} onclick={() => { tasks = moveTask(tasks, i, 1) ?? tasks; }}>Move later</Button>
                <Button size="sm" variant="danger" disabled={!removeTask(tasks, i)} onclick={() => { tasks = removeTask(tasks, i) ?? tasks; }}>Remove task</Button>
              </div>
              <p class="text-sm text-faint">Dependencies stay attached when tasks move. Remove references to this task before removing it.</p>
              <label class="block text-sm text-faint">Task {i + 1} title<input class={field} bind:value={task.title} required maxlength="240" /></label>
              <label class="block text-sm text-faint">Task {i + 1} description<textarea class={field} bind:value={task.description} rows="3" maxlength="16000"></textarea></label>
              <label class="block text-sm text-faint">Task {i + 1} acceptance criteria<textarea class={field} bind:value={task.acceptanceText} rows="3" required></textarea></label>
              <p class="text-sm text-faint">One observable result per line.</p>
              <fieldset class="space-y-2 text-sm"><legend class="mb-2 text-faint">Covers goal criteria</legend>
                {#each proposal.input.request.acceptance ?? [] as criterion, n}<label class="flex items-start gap-2 break-words text-ink"><Checkbox checked={task.covers?.includes(n + 1)} onchange={(e) => task.covers = toggle(task.covers, n + 1, e.currentTarget.checked)} />{n + 1}. {criterion}</label>{/each}
              </fieldset>
              <fieldset class="space-y-2 text-sm"><legend class="mb-2 text-faint">Depends on earlier tasks</legend>
                {#each tasks.slice(0, i) as earlier, n}<label class="flex items-start gap-2 break-words text-ink"><Checkbox checked={task.blocked_by?.includes(n + 1)} onchange={(e) => task.blocked_by = toggle(task.blocked_by, n + 1, e.currentTarget.checked)} />{n + 1}. {earlier.title || "Untitled task"}</label>{:else}<p class="text-faint">The first task has no dependencies.</p>{/each}
              </fieldset>
              <fieldset class="space-y-3 text-sm"><legend class="mb-2 text-faint">Repository scope</legend>
                {#each proposal.input.members ?? [] as member}
                  <div class="space-y-2">
                    <label class="flex items-start gap-2 break-words text-ink"><Checkbox checked={task.apps?.includes(member.id)} onchange={(e) => task.apps = toggle(task.apps, member.id, e.currentTarget.checked)} />{member.id}</label>
                    {#if task.apps?.includes(member.id)}<label class="block text-faint">Writable paths in {member.id}<textarea class={field} value={task.pathText[member.id] ?? ""} oninput={(e) => task.pathText[member.id] = e.currentTarget.value} rows="2"></textarea></label>{/if}
                    {#if proposal.input.stack}<p class="break-words text-faint">Routing labels: {member.routing_labels?.join(", ") || "Always included"}</p>{/if}
                  </div>
                {/each}
                <p class="max-w-prose text-faint">One repository-relative file or folder per line. Leave a selected repository's paths empty for read-only context. Shared write paths require dependency ordering.</p>
              </fieldset>
              {#if proposal.input.labels?.length}<fieldset class="space-y-2 text-sm"><legend class="mb-2 text-faint">Linear labels</legend>
                {#each proposal.input.labels as label}<label class="flex items-start gap-2 break-words text-ink"><Checkbox checked={task.label_ids?.includes(label.id)} onchange={(e) => task.label_ids = toggle(task.label_ids, label.id, e.currentTarget.checked)} />{label.group ? `${label.group}/` : ""}{label.name}</label>{/each}
                {#if proposal.input.stack}<p class="text-faint">Select labels that route to exactly the repositories selected above.</p>{/if}
              </fieldset>{/if}
            </div>
          </details>
        {/each}
      </div>
      <Button disabled={tasks.length >= proposal.input.request.max_tasks} onclick={() => { tasks = [...tasks, formTask({ title: "", description: "", acceptance: [], covers: [], apps: [], paths: {} })]; }}>Add task</Button>
      <label class="block text-sm text-faint">Open questions<textarea class={field} bind:value={unknowns} rows="3"></textarea></label>
      <p class="text-sm text-faint">One question per line, up to 20. Keep unresolved decisions visible.</p>
      <label class="block text-sm text-faint">Reason for this revision<textarea class={field} bind:value={reason} rows="2" required minlength="8" maxlength="2000"></textarea></label>
      {#if problems.length}<div class="text-sm text-warn"><p>Before reviewing:</p><ul class="mt-1 list-disc pl-5">{#each problems as problem}<li>{problem}</li>{/each}</ul></div>{/if}
      {#if discard}
        <p class="text-sm text-warn">Discard your unsaved task changes?</p>
        <div class="flex flex-wrap gap-2"><Button variant="danger" onclick={oncancel}>Discard changes</Button><Button onclick={() => discard = false}>Keep editing</Button></div>
      {:else}
        {#if !changed}<p class="text-sm text-faint">Change a task or open question to create a revision.</p>{/if}
        <div class="flex flex-wrap gap-2"><Button type="submit" variant="primary" disabled={!changed || problems.length > 0 || reason.trim().length < 8}>Review revision</Button><Button onclick={() => discard = true}>Cancel editing</Button></div>
      {/if}
    </form>
  {/if}
</section>
