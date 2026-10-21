<script lang="ts">
  import { tick, onDestroy } from "svelte";
  import { DaemonService } from "@bindings/desktop";
  import type { Record as Proposal, Summary, Request } from "@bindings/internal/goalplan";
  import Button from "./Button.svelte";
  import Select from "./Select.svelte";
  import Checkbox from "./Checkbox.svelte";
  import Disclosure from "./Disclosure.svelte";

  let { project, onaccepted }: { project: string; onaccepted: (id: string) => void } = $props();
  let open = $state(false);
  let title = $state("");
  let outcome = $state("");
  let acceptance = $state("");
  let nonGoals = $state("");
  let milestone = $state("");
  let maxTasks = $state(10);
  let timeout = $state(180);
  let model = $state("");
  let summaries = $state<Summary[]>([]);
  let selected = $state("");
  let proposal = $state<Proposal | null>(null);
  let requestID = $state("");
  let busy = $state(false);
  let error = $state("");
  let acknowledged = $state(false);
  let acceptFailed = $state(false);
  let acceptedID = $state("");
  let titleField = $state<HTMLInputElement>();
  let active = true;
  onDestroy(() => { active = false; });
  const field = "w-full min-w-0 rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent";
  const lines = (s: string) => s.split("\n").map((v) => v.trim()).filter(Boolean);
  const valid = $derived(!!title.trim() && !!outcome.trim() && lines(acceptance).length > 0 && maxTasks >= 1 && maxTasks <= 50 && timeout >= 30 && timeout <= 240);

  async function show() {
    open = true;
    if (busy) return;
    await list();
    await tick();
    titleField?.focus();
  }
  async function list() {
    if (busy) return;
    busy = true;
    try { summaries = (await DaemonService.GoalProposals(project)) ?? []; error = ""; }
    catch (e) { error = `Could not load saved proposals: ${String(e)}. Refresh proposals to try again.`; }
    finally { busy = false; }
  }
  async function inspect(id = selected) {
    if (busy || !id) return;
    busy = true; error = ""; proposal = null; requestID = id; acknowledged = false;
    try {
      const result = await DaemonService.GoalProposal(id);
      if (result.input.request.project !== project || result.input.repair) throw new Error("This proposal belongs to another goal or project");
      proposal = result; requestID = result.id; selected = result.id;
      acknowledged = false; acceptFailed = false; acceptedID = result.state === "accepted" ? result.goal_id : "";
    } catch (e) { error = `Could not inspect this proposal: ${String(e)}. Refresh result to try again; this never requests another model call.`; }
    finally { busy = false; }
  }
  async function propose() {
    if (!valid || busy || requestID) return;
    requestID = crypto.randomUUID();
    const request: Request = { id: requestID, project, title: title.trim(), outcome: outcome.trim(), acceptance: lines(acceptance), non_goals: lines(nonGoals), milestone_name: milestone.trim(), max_tasks: maxTasks, timeout_seconds: timeout, model: model.trim() };
    busy = true; error = "";
    try { proposal = await DaemonService.GoalPropose(request); }
    catch (e) { error = `The proposal response was not confirmed: ${String(e)}. Refresh result to inspect the saved request before deciding on another run.`; }
    finally { busy = false; }
  }
  async function accept() {
    if (!proposal || proposal.state !== "proposed" || !acknowledged || acceptFailed || busy) return;
    const reviewed = proposal;
    busy = true; error = "";
    try {
      acceptedID = await DaemonService.GoalAcceptPlan(reviewed.id, reviewed.revision);
      proposal = { ...reviewed, state: "accepted" } as Proposal;
      if (active) onaccepted(acceptedID);
    } catch (e) { acceptFailed = true; error = `Acceptance was not confirmed: ${String(e)}. Refresh result and review it before another action.`; }
    finally { busy = false; }
  }
  async function newBrief() {
    proposal = null; requestID = ""; selected = ""; acknowledged = false; acceptedID = ""; acceptFailed = false; error = "";
    await tick(); titleField?.focus();
  }
</script>

<div class="border-t border-edge pt-3">
  <button class="rounded px-2 py-1.5 text-sm text-accent hover:bg-sel/50" aria-expanded={open} onclick={() => { if (!open) void show(); else open = false; }}>{open ? "Hide goal planner" : "Plan a goal"}</button>
  {#if open}
    <section aria-label="Goal planner" class="mt-3 space-y-4">
      <div>
        <h3 class="font-medium text-ink">Plan a goal</h3>
        <p class="mt-1 max-w-prose text-sm text-faint">Describe the outcome. The coordinator proposes tasks using committed repository files and relevant workspace lessons. You review the result before creating a goal.</p>
      </div>
      <div class="flex flex-wrap items-end gap-2">
        <label class="min-w-0 flex-1 text-sm text-faint">Saved proposals
          <Select class="mt-1" bind:value={selected} disabled={busy} onchange={(e) => void inspect(e.currentTarget.value)}>
            <option value="">Choose a proposal…</option>
            {#each summaries as item (item.id)}<option value={item.id}>{item.title} · {item.state}</option>{/each}
          </Select>
        </label>
        <Button size="sm" disabled={busy} onclick={() => void list()}>Refresh proposals</Button>
      </div>
      {#if error}<p role="alert" class="selectable whitespace-pre-wrap break-words text-sm text-bad">{error}</p>{/if}
      {#if requestID}
        <div class="space-y-2 text-sm">
          <p class="selectable break-all text-faint">Proposal ID: {requestID}</p>
          {#if busy}<p role="status" class="text-ink">Waiting for the current operation… You can return to this proposal later.</p>{/if}
          <div class="flex flex-wrap gap-2">
            <Button size="sm" disabled={busy} onclick={() => void inspect(requestID)}>Refresh result</Button>
            <Button size="sm" disabled={busy} onclick={() => void newBrief()}>New proposal brief</Button>
          </div>
        </div>
      {:else}
        <form class="space-y-3" onsubmit={(e) => { e.preventDefault(); void propose(); }}>
          <fieldset disabled={busy} class="min-w-0 space-y-3">
            <label class="block text-sm text-faint">Title<input bind:this={titleField} class={`${field} mt-1`} bind:value={title} required maxlength="240" /></label>
            <label class="block text-sm text-faint">Desired outcome<textarea class={`${field} mt-1`} bind:value={outcome} rows="3" required maxlength="12000"></textarea></label>
            <label class="block text-sm text-faint">Acceptance criteria<textarea class={`${field} mt-1`} bind:value={acceptance} rows="3" required></textarea></label>
            <p class="text-sm text-faint">One observable result per line. These criteria remain yours; the coordinator only proposes tasks to meet them.</p>
            <label class="block text-sm text-faint">Outside this goal (optional)<textarea class={`${field} mt-1`} bind:value={nonGoals} rows="2"></textarea></label>
            <Disclosure label="Planning limits and Linear milestone">
              <div class="mt-3 grid gap-3 sm:grid-cols-2">
                <label class="text-sm text-faint">Maximum tasks<input class={`${field} mt-1`} type="number" min="1" max="50" required bind:value={maxTasks} /></label>
                <label class="text-sm text-faint">Planning timeout (seconds)<input class={`${field} mt-1`} type="number" min="30" max="240" required bind:value={timeout} /></label>
                <label class="text-sm text-faint">Planning model (optional)<input class={`${field} mt-1`} bind:value={model} maxlength="200" placeholder="Agent default" /></label>
                <label class="text-sm text-faint">New milestone name (optional)<input class={`${field} mt-1`} bind:value={milestone} maxlength="240" /></label>
              </div>
              <p class="mt-2 text-sm text-faint">A milestone is created in the existing Linear project only when you publish the accepted goal. Leave blank to use just the goal parent issue and its tasks.</p>
            </Disclosure>
            <p class="max-w-prose text-sm text-faint">Requesting a proposal makes one paid model call, bounded to {timeout} seconds and {maxTasks} tasks. It requires goal mode and Docker setup for this project or stack. It creates no Linear issues and starts no coding workers.</p>
            <Button type="submit" variant="primary" disabled={!valid || busy}>Request task proposal</Button>
          </fieldset>
        </form>
      {/if}
      {#if proposal}
        <div class="space-y-3 border-t border-edge pt-3">
          <div class="flex flex-wrap items-baseline justify-between gap-2"><h4 class="font-medium text-ink">{proposal.input.request.title}</h4><span class="text-sm text-faint">{proposal.state} · revision {proposal.revision}</span></div>
          <p class="selectable whitespace-pre-wrap break-words text-sm text-ink">{proposal.input.request.outcome}</p>
          <ol class="list-decimal space-y-1 pl-5 text-sm text-faint">{#each proposal.input.request.acceptance as item}<li class="selectable break-words">{item}</li>{/each}</ol>
          {#if proposal.input.request.non_goals?.length}<p class="text-sm text-ink">Outside this goal</p><ul class="list-disc pl-5 text-sm text-faint">{#each proposal.input.request.non_goals as item}<li class="break-words">{item}</li>{/each}</ul>{/if}
          {#if proposal.error}<p class="selectable whitespace-pre-wrap break-words text-sm text-warn">{proposal.error}</p>{/if}
          {#if proposal.output}
            {#if proposal.output.unknowns?.length}<div class="text-sm"><h5 class="font-medium text-warn">Open questions from the coordinator</h5><ul class="mt-1 list-disc pl-5 text-faint">{#each proposal.output.unknowns as item}<li class="selectable break-words">{item}</li>{/each}</ul></div>{/if}
            <ol class="divide-y divide-edge">
              {#each proposal.output.tasks as task, i}
                <li class="py-3 text-sm">
                  <h5 class="font-medium text-ink">{i + 1}. {task.title}</h5>
                  {#if task.description}<p class="selectable mt-1 whitespace-pre-wrap break-words text-faint">{task.description}</p>{/if}
                  <p class="mt-1 text-faint">Repositories: {task.apps?.join(", ") || "Project repository"}</p>
                  <p class="text-faint">Covers criteria: {(task.covers ?? []).join(", ")}{task.blocked_by?.length ? ` · Depends on tasks: ${task.blocked_by.join(", ")}` : ""}</p>
                  <ul class="mt-1 list-disc pl-5 text-faint">{#each task.acceptance as item}<li class="selectable break-words">{item}</li>{/each}</ul>
                  {#each Object.entries(task.paths ?? {}) as [member, paths]}<p class="mt-1 break-words text-faint">{member}: {paths?.length ? paths.join(", ") : "Read-only context"}</p>{/each}
                </li>
              {/each}
            </ol>
          {/if}
          <Disclosure label="Sources and planning record">
            <div class="mt-3 space-y-2 text-sm">
              <p class="text-faint">Agent: {proposal.agent}. Selected committed files and lessons are retained in this record; uncommitted work was excluded.</p>
              {#each proposal.input.members as member}<p class="selectable break-words text-faint">{member.id} · {member.snapshot.target} · {member.snapshot.commit}<br />{(member.snapshot.files ?? []).map((f) => f.path).join(", ")}</p>{/each}
              <details><summary class="cursor-pointer text-ink">Full proposal record</summary><pre class="selectable mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-all text-xs text-faint">{JSON.stringify(proposal, null, 2)}</pre></details>
            </div>
          </Disclosure>
          {#if acceptedID}
            <p role="status" class="selectable break-words text-sm text-accent">Local goal created: {acceptedID}. Publishing to Linear and authorizing execution are separate steps.</p>
          {:else if proposal.state === "proposed"}
            <label class="flex items-start gap-2 text-sm text-ink"><Checkbox bind:checked={acknowledged} disabled={busy || acceptFailed} />I reviewed the tasks, scope, acceptance coverage and open questions.</label>
            <p class="text-sm text-faint">Accepting creates a local goal and rechecks repository commits and routing. It does not authorize execution or publish issues.</p>
            <Button variant="primary" disabled={!acknowledged || busy || acceptFailed} onclick={() => void accept()}>Accept as local goal</Button>
          {/if}
        </div>
      {/if}
    </section>
  {/if}
</div>
