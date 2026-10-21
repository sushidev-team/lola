<script lang="ts">
  import { onDestroy, tick } from "svelte";
  import { DaemonService } from "@bindings/desktop";
  import type { StartOptions, Authorization } from "@bindings/internal/goal";
  import type { Limits } from "@bindings/internal/goalpolicy";
  import Button from "./Button.svelte";
  import Checkbox from "./Checkbox.svelte";
  import Select from "./Select.svelte";
  import Disclosure from "./Disclosure.svelte";

  let { goalID, onstarted }: { goalID: string; onstarted: () => void } = $props();
  let open = $state(false);
  let busy = $state(false);
  let error = $state("");
  let options = $state<StartOptions | null>(null);
  let reviewed = $state<Authorization | null>(null);
  let acknowledged = $state(false);
  let failed = $state(false);
  let reviewArea = $state<HTMLDivElement>();
  let active = true;
  onDestroy(() => { active = false; });
  const field = "mt-1 w-full min-w-0 rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent";
  const limitFields: {key: keyof Limits; label: string; min: number; max: number; step?: number}[] = [
    {key:"concurrency",label:"Concurrent tasks",min:1,max:8},
    {key:"max_tasks",label:"Maximum tasks",min:1,max:50},
    {key:"runtime_minutes",label:"Goal runtime (minutes)",min:1,max:10080},
    {key:"tokens",label:"Observed token limit",min:1,max:Number.MAX_SAFE_INTEGER},
    {key:"attempts_per_task",label:"Attempts per task",min:1,max:5},
    {key:"attempt_minutes",label:"Attempt runtime (minutes)",min:1,max:10080},
    {key:"review_rounds",label:"Review rounds",min:1,max:3},
    {key:"agent_runs",label:"Total agent calls",min:1,max:1000},
    {key:"target_refreshes",label:"Target refreshes",min:0,max:3},
    {key:"spend_usd",label:"Observed spend limit (USD, 0 disables)",min:0,max:1000000,step:0.01},
  ];

  async function load() {
    if (busy) return;
    open = true; busy = true; error = ""; reviewed = null; acknowledged = false; options = null;
    try { options = await DaemonService.GoalStartOptions(goalID); failed = false; }
    catch (e) { error = `Could not prepare authorization: ${String(e)}. Reload settings after resolving the issue.`; }
    finally { busy = false; }
  }
  function addCheck() {
    if (!options) return;
    const checks = options.authorization.policy.checks ?? [];
    let id = "acceptance";
    for (let n=2; checks.some((c)=>c.id===id); n++) id=`acceptance-${n}`;
    options.authorization.policy.checks = [...checks,{id,command:"",member:options.authorization.policy.members?.[0]?.id ?? "",image:"",timeout_seconds:300,artifacts:[]}];
  }
  function profile(id: string, checked: boolean) {
    if (!options) return;
    const current = options.authorization.policy.profiles ?? [];
    options.authorization.policy.profiles = checked ? [...new Set([...current,id])] : current.filter((p)=>p!==id);
  }
  async function review() {
    if (!options || busy || !options.authorization.policy.checks?.length) return;
    reviewed = structuredClone($state.snapshot(options.authorization));
    for (const member of reviewed.policy.members ?? []) member.paths = (member.paths ?? []).map(p => p.trim()).filter(Boolean);
    acknowledged = false; failed = false; error = "";
    await tick(); reviewArea?.focus();
  }
  async function start() {
    if (!reviewed || !acknowledged || busy || failed) return;
    busy = true;
    try { await DaemonService.GoalStart($state.snapshot(reviewed)); if (active) onstarted(); open = false; }
    catch (e) { failed = true; error = `Authorization was not confirmed: ${String(e)}. Reload settings to inspect current state before another action. The daemon may already have accepted this request.`; }
    finally { busy = false; }
  }
</script>

<div class="border-t border-edge pt-3">
  {#if !open}<Button variant="secondary" onclick={() => void load()}>Set execution limits</Button>
  {:else}
    <section aria-label="Goal execution authorization" class="space-y-3">
      <div class="flex flex-wrap items-baseline justify-between gap-2"><h4 class="font-medium text-ink">Authorize autonomous execution</h4><Button disabled={busy} onclick={() => { open = false; }}>Close authorization</Button></div>
      {#if busy}<p role="status" class="text-sm text-faint">{reviewed ? "Submitting the reviewed authorization…" : "Loading current goal settings…"}</p>{/if}
      {#if error}<p role="alert" class="selectable whitespace-pre-wrap break-words text-sm text-bad">{error}</p>{/if}
      {#if !options || failed}<Button disabled={busy} onclick={() => void load()}>Reload settings</Button>{/if}
      {#if options}
        <p class="selectable whitespace-pre-wrap break-words text-sm text-ink">{options.goal.outcome}</p>
        <details class="text-sm"><summary class="cursor-pointer text-ink">Goal and task acceptance</summary>
          <ul class="mt-2 list-disc space-y-1 pl-5 text-faint">{#each options.goal.acceptance ?? [] as item}<li class="selectable break-words">{item}</li>{/each}</ul>
          {#if options.goal.nonGoals?.length}<p class="mt-2 text-ink">Outside this goal</p><ul class="list-disc pl-5 text-faint">{#each options.goal.nonGoals as item}<li class="break-words">{item}</li>{/each}</ul>{/if}
          {#each options.goal.tasks ?? [] as task}<p class="mt-2 text-ink">{task.title}</p><ul class="list-disc pl-5 text-faint">{#each task.acceptance ?? [] as item}<li class="selectable break-words">{item}</li>{/each}</ul>{/each}
        </details>
      {/if}
      {#if options && !reviewed}
        <p class="text-sm text-faint">{options.goal.title} · revision {options.authorization.revision}. Choose the checks and limits that will govern this goal. Workers use isolated repositories; you retain the final merge decision.</p>
        <form class="space-y-4" onsubmit={(e) => { e.preventDefault(); void review(); }}>
          <fieldset disabled={busy} class="min-w-0 space-y-4">
            <div class="space-y-2">
              <h5 class="font-medium text-ink">Repositories</h5>
              {#each options.authorization.policy.members as member}
                <div class="text-sm"><p class="selectable break-words text-ink">{member.repo || member.id} → {member.target}</p><p class="selectable break-all text-faint">{member.source}</p>
                  <label class="mt-2 block text-faint">Allowed path prefixes for {member.id}<textarea class={field} rows="2" value={(member.paths ?? []).join("\n")} oninput={(e) => { member.paths = e.currentTarget.value.split("\n"); }}></textarea></label>
                </div>
              {/each}
              <p class="text-sm text-faint">One repository-relative prefix per line. Blank permits the repository; each task still stays within its declared write scope.</p>
            </div>
            <div class="space-y-3">
              <div class="flex flex-wrap items-center justify-between gap-2"><h5 class="font-medium text-ink">Acceptance checks</h5><Button disabled={(options.authorization.policy.checks?.length ?? 0)>=50} onclick={addCheck}>Add check</Button></div>
              <p class="text-sm text-faint">Checks run in disposable containers without network access. Use an already installed image pinned to its SHA-256 digest. Supply commands that verify your acceptance criteria.</p>
              {#if !options.authorization.policy.checks?.length}<p class="text-sm text-warn">Add at least one acceptance check before reviewing authorization.</p>{/if}
              {#each options.authorization.policy.checks ?? [] as check, i (check)}
                <div class="space-y-2 border-b border-edge pb-3" role="group" aria-label={`Acceptance check ${i+1}`}>
                  <div class="flex items-center justify-between gap-2"><span class="text-sm text-ink">Check {i+1}</span><Button variant="danger" onclick={() => { if(options) options.authorization.policy.checks=options.authorization.policy.checks?.filter(c=>c!==check) ?? []; }}>Remove check</Button></div>
                  <div class="grid gap-3 sm:grid-cols-2">
                    <label class="text-sm text-faint">Check ID<input class={field} required maxlength="128" bind:value={check.id} /></label>
                    <label class="text-sm text-faint">Run from<Select class="mt-1" bind:value={check.member}><option value="">Combined repository root</option>{#each options.authorization.policy.members as member}<option value={member.id}>{member.id}</option>{/each}</Select></label>
                  </div>
                  <label class="block text-sm text-faint">Check command<textarea class={field} rows="2" required maxlength="16000" bind:value={check.command}></textarea></label>
                  <label class="block text-sm text-faint">Check image digest<input class={field} required bind:value={check.image} placeholder="sha256:…" /></label>
                  <label class="block text-sm text-faint">Check timeout (seconds)<input class={field} type="number" min="1" max="3600" required bind:value={check.timeout_seconds} /></label>
                  <Disclosure label="Captured evidence">
                    <p class="mt-2 text-sm text-faint">Evidence paths are relative to the combined repository root and include the repository ID. Specialist reviewers require their evidence kinds to be present.</p>
                    {#each check.artifacts ?? [] as artifact, n (artifact)}
                      <div class="mt-2 grid gap-2 sm:grid-cols-3" role="group" aria-label={`Evidence ${n+1}`}>
                        <label class="text-sm text-faint">Evidence ID<input class={field} required bind:value={artifact.id} /></label>
                        <label class="text-sm text-faint">Evidence kind<input class={field} required bind:value={artifact.kind} /></label>
                        <label class="text-sm text-faint">Evidence path<input class={field} required bind:value={artifact.path} /></label>
                        <Button variant="danger" onclick={() => { check.artifacts=check.artifacts?.filter(a=>a!==artifact) ?? []; }}>Remove evidence</Button>
                      </div>
                    {/each}
                    <Button class="mt-2" disabled={(check.artifacts?.length ?? 0)>=20} onclick={() => { check.artifacts=[...(check.artifacts ?? []),{id:"",kind:"",path:""}]; }}>Add evidence</Button>
                  </Disclosure>
                </div>
              {/each}
            </div>
            <div class="space-y-2">
              <h5 class="font-medium text-ink">Specialist reviewers</h5>
              {#if !options.profiles?.length}<p class="text-sm text-faint">No enabled specialists apply to this project. Configure them in Settings → Review before authorizing if needed.</p>{/if}
              {#each options.profiles as item}
                <label class="flex items-start gap-2 text-sm text-ink"><Checkbox checked={options.authorization.policy.profiles?.includes(item.id) ?? false} disabled={item.required} onchange={(e) => profile(item.id,e.currentTarget.checked)} /><span>{item.id} · {item.kind}{item.required ? " · required" : ""}{item.artifacts?.length ? ` · evidence: ${item.artifacts.join(", ")}` : ""}</span></label>
              {/each}
            </div>
            <div class="space-y-2"><h5 class="font-medium text-ink">Execution limits</h5>
              <div class="grid gap-3 sm:grid-cols-2">{#each limitFields as f}<label class="text-sm text-faint">{f.label}<input class={field} type="number" min={f.min} max={f.max} step={f.step ?? 1} required bind:value={options.authorization.policy.limits[f.key]} /></label>{/each}</div>
              <p class="text-sm text-faint">Agent calls include planning, workers and reviews. Token and spend limits use delayed observations; missing usage is unknown. They are not exact billing caps. Task, call and runtime limits still apply.</p>
            </div>
            <label class="flex items-start gap-2 text-sm text-ink"><Checkbox bind:checked={options.authorization.policy.publish_pr} />Allow verified goal branches and draft PRs to be published. Merging stays manual.</label>
            <label class="flex items-start gap-2 text-sm text-ink"><Checkbox checked={options.authorization.policy.replan ?? false} onchange={(e) => { if(options) options.authorization.policy.replan=e.currentTarget.checked; }} />Allow bounded task replanning after conclusive verification failures.</label>
            <Button type="submit" variant="primary" disabled={!options.authorization.policy.checks?.length}>Review authorization</Button>
          </fieldset>
        </form>
      {:else if options && reviewed}
        <div bind:this={reviewArea} tabindex="-1" role="group" aria-label="Review execution authorization" class="space-y-3">
          <p class="text-sm text-ink">Start {options.goal.title} at reviewed revision {reviewed.revision}.</p>
          <p class="text-sm text-faint">Workers may begin after this authorization is accepted. Scope, checks and budgets below are the complete policy you are approving.</p>
          <ul class="list-disc space-y-1 pl-5 text-sm text-faint">{#each reviewed.policy.members as member}<li class="selectable break-words">{member.repo || member.id} → {member.target}; allowed paths: {member.paths?.length ? member.paths.join(", ") : "entire repository, restricted by each task’s scope"}</li>{/each}</ul>
          {#each reviewed.policy.checks as check}<div class="space-y-1 text-sm"><h5 class="font-medium text-ink">{check.id} · {check.member || "combined root"} · {check.timeout_seconds}s</h5><pre class="selectable whitespace-pre-wrap break-all text-faint">{check.command}</pre><p class="selectable break-all text-faint">{check.image}</p>{#each check.artifacts ?? [] as artifact}<p class="selectable break-words text-faint">{artifact.id}: {artifact.kind} from {artifact.path}</p>{/each}</div>{/each}
          <p class="text-sm text-faint">Required reviews: {reviewed.policy.profiles?.join(", ") || "None selected"}</p>
          <dl class="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1 text-sm">{#each limitFields as f}<dt class="text-faint">{f.label}</dt><dd class="num text-ink">{reviewed.policy.limits[f.key]}</dd>{/each}</dl>
          <p class="text-sm text-ink">Draft PR publication: {reviewed.policy.publish_pr ? "allowed after verification" : "disabled"}. Task replanning: {reviewed.policy.replan ? "allowed within these limits" : "disabled"}.</p>
          <label class="flex items-start gap-2 text-sm text-ink"><Checkbox bind:checked={acknowledged} disabled={busy || failed} />I authorize this scope, checks and limits, including paid agent calls.</label>
          <div class="flex flex-wrap gap-2"><Button variant="primary" disabled={!acknowledged || busy || failed} onclick={() => void start()}>Authorize and start</Button><Button disabled={busy || failed} onclick={() => { reviewed=null; acknowledged=false; }}>Back to limits</Button></div>
        </div>
      {/if}
    </section>
  {/if}
</div>
