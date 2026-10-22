<script lang="ts">
  import { onDestroy, tick, untrack } from "svelte";
  import { DaemonService } from "@bindings/desktop";
  import type { Improvement, ImprovementDecision, ImprovementRecord, ImprovementSources, ImprovementTriage, Lesson, Reference, Scope } from "@bindings/internal/knowledge";
  import Button from "./Button.svelte";
  import Select from "./Select.svelte";
  import ImprovementBrief from "./ImprovementBrief.svelte";
  let { project, onclose = () => {} }: { project: string; onclose?: () => void } = $props();
  type Review = { proposal: Improvement; request: ImprovementTriage };
  const labels: Record<string,string> = {tracked:"Track",dismissed:"Dismiss",reopened:"Reopen"};
  const consequences: Record<string,string> = {
    tracked:"Record where this proposal is being followed up. This does not create an issue or authorize implementation.",
    dismissed:"Record why this proposal should not be pursued at this source revision. Its sources and decisions remain inspectable.",
    reopened:"Return this source revision to proposed. This does not reactivate lessons or authorize implementation.",
  };
  const field = "mt-1 w-full min-w-0 rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent";
  let goal = $state(""), scope = $state<Scope>({project:""}), busy = $state(false), error = $state(""), notice = $state("");
  let records = $state<ImprovementRecord[]>([]), total = $state(0), next = $state(""), loaded = $state(false);
  let record = $state<ImprovementRecord | null>(null), proposal = $state<Improvement | null>(null), history = $state<ImprovementDecision[]>([]);
  let sources = $state<ImprovementSources | null>(null), lesson = $state<Lesson | null>(null), files = $state<Record<string,string>>({});
  let decision = $state(""), evidence = $state(""), reviewed = $state<Review | null>(null), uncertain = $state(false), discard = $state(false);
  let alert = $state<HTMLParagraphElement>(), area = $state<HTMLDivElement>();
  let active = true, generation = 0;
  const locked = $derived(busy || !!decision || uncertain);
  const sameScope = (a: Scope, b: Scope) => a.project === b.project && (a.goal_id ?? "") === (b.goal_id ?? "");
  const clone = <T,>(value: T): T => structuredClone($state.snapshot(value) as T);
  onDestroy(() => { active=false; generation++; });
  $effect(() => { const owner=project; untrack(() => { generation++; busy=false; scope={project:owner}; goal=""; records=[]; loaded=false; clearDetail(); void loadCatalog(); }); });
  function clearDetail() { record=null;proposal=null;history=[];sources=null;lesson=null;files={};decision="";reviewed=null;uncertain=false;discard=false; }
  async function fail(message: string) { error=message; await tick(); alert?.focus(); alert?.scrollIntoView({block:"nearest"}); }
  async function focusArea() { await tick(); area?.focus(); area?.scrollIntoView({block:"nearest"}); }
  async function loadCatalog(more = false) {
    if (locked) return;
    const target: Scope = more ? clone(scope) : {project,...(goal.trim() ? {goal_id:goal.trim()} : {})};
    const after=more ? next : "", seq=++generation;
    busy=true;error="";notice="";
    if (!more) {clearDetail();loaded=false;records=[];next="";scope=target;}
    try {
      const result=await DaemonService.KnowledgeImprovementCatalog(target,after);
      if (!active || seq!==generation) return;
      const rows=result.records ?? [];
      if (rows.some((r,i) => !sameScope(r.scope,target) || !r.id || !r.revision || r.id<=after || (i>0 && r.id<=rows[i-1].id)) || (result.next_after && (result.next_after<=after || result.next_after!==rows.at(-1)?.id))) throw Error("catalog does not match this scope or page");
      records=more ? [...records,...clone(rows)] : clone(rows);total=result.total;next=result.next_after ?? "";loaded=true;
    } catch(e) {if (active && seq===generation) await fail(`Could not load proposals: ${String(e)}. Reload the catalog before another decision.`);}
    finally {if (active && seq===generation) busy=false;}
  }
  async function inspect(selected: ImprovementRecord, recovering = false) {
    if (busy || (!recovering && (decision || uncertain))) return;
    const target=clone(selected), seq=++generation;
    busy=true;error="";
    try {
      const answers=await Promise.allSettled([
        target.available ? DaemonService.KnowledgeImprovement(target.scope,target.id) : Promise.resolve(null),
        DaemonService.KnowledgeImprovementDecisions(target.scope,target.id),
      ]);
      if (!active || seq!==generation) return;
      const [p,h]=answers;
      if (p.status==="rejected" || h.status==="rejected") throw Error([p,h].flatMap(a => a.status==="rejected" ? [String(a.reason)] : []).join("; "));
      const current=p.value, decisions=h.value ?? [];
      if ((current && (current.id!==target.id || !sameScope(current.scope,target.scope) || !current.revision)) || decisions.some(d => !sameScope(d.scope,target.scope) || !d.revision)) throw Error("proposal/history does not match this scope");
      record=current ? {...target,revision:current.revision,state:current.state,topic:current.topic,scope:clone(current.scope)} : target;
      const refreshed={...record,decision_count:decisions.length};
      records=records.map(row=>row.id===target.id ? clone(refreshed) : row);
      proposal=current ? clone(current) : null;history=clone(decisions);sources=null;lesson=null;files={};decision="";reviewed=null;uncertain=false;discard=false;
      await focusArea();
    } catch(e) {if (active && seq===generation) await fail(`Could not inspect this proposal: ${String(e)}. Reload before another decision.`);}
    finally {if (active && seq===generation) busy=false;}
  }
  async function loadSources(revision: string, more = false) {
    if (!record || locked) return;
    const target=clone(record), after=more ? sources?.next_after ?? "" : "", seq=++generation;
    busy=true;error="";lesson=null;
    try {
      const page=await DaemonService.KnowledgeImprovementSources(target.scope,target.id,revision,after);
      if (!active || seq!==generation) return;
      const refs=page.references ?? [];
      if (page.id!==target.id || page.revision!==revision || !sameScope(page.scope,target.scope) || refs.some((r,i) => !r.id || r.version<1 || r.id<=after || (i>0 && r.id<=refs[i-1].id)) || (page.next_after && (page.next_after<=after || page.next_after!==refs.at(-1)?.id))) throw Error("source page does not match the reviewed revision");
      sources={...clone(page),references:more ? [...(sources?.references ?? []),...refs] : refs};
    } catch(e) {if (active && seq===generation) await fail(`Could not load source references: ${String(e)}. Reload the proposal before triage or export.`);}
    finally {if (active && seq===generation) busy=false;}
  }
  async function inspectLesson(ref: Reference) {
    if (!record || locked) return;
    const target=clone(record.scope), seq=++generation;busy=true;error="";lesson=null;
    try {
      const value=await DaemonService.KnowledgeVersion(target,ref.id,ref.version);
      if (!active || seq!==generation) return;
      if (value.id!==ref.id || value.version!==ref.version || !sameScope(value.scope,target)) throw Error("lesson does not match the exact source version");
      lesson=clone(value);
    } catch(e) {if (active && seq===generation) await fail(`Could not inspect source lesson: ${String(e)}.`);}
    finally {if (active && seq===generation) busy=false;}
  }
  async function begin(nextDecision: string) {
    if (!proposal || locked || error || !labels[nextDecision]) return;
    decision=nextDecision;evidence="";reviewed=null;files={};notice="";await focusArea();
  }
  async function review() {
    if (!proposal || busy || uncertain || discard) return;
    const value=evidence.trim(), bytes=new TextEncoder().encode(value).length;
    if (bytes<8 || bytes>2000 || /[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(value)) {await fail("Provide decision evidence of 8–2000 UTF-8 bytes, without control characters.");return;}
    const frozen=clone(proposal);
    reviewed={proposal:frozen,request:{scope:clone(frozen.scope),id:frozen.id,revision:frozen.revision,decision,evidence:value}};
    error="";await focusArea();
  }
  async function submit() {
    if (!reviewed || busy || uncertain || error || discard) return;
    const action=clone(reviewed.request), selected=record ? clone(record) : null, seq=++generation;
    busy=true;error="";
    try {
      const result=await DaemonService.KnowledgeDecideImprovement(action);
      if (!active || seq!==generation) return;
      if (result.id!==action.id || result.revision!==action.revision || !sameScope(result.scope,action.scope)) throw Error("reply does not identify the reviewed decision");
      notice="Decision saved. Review the current proposal and retained history before another action.";
      uncertain=true;busy=false;
      if (selected) await inspect(selected,true);
    } catch(e) {if (active && seq===generation) {uncertain=true;await fail(`Decision was not confirmed: ${String(e)}. It may already be saved. Reload this proposal; do not repeat the request.`);}}
    finally {if (active && seq===generation) busy=false;}
  }
  async function exportPreview() {
    if (!proposal || locked || error) return;
    const selected=clone(proposal),seq=++generation;busy=true;error="";files={};
    try {
      const result=await DaemonService.KnowledgeExportImprovement(selected.scope,selected.id,selected.revision);
      if (!active || seq!==generation) return;
      files=Object.fromEntries(Object.entries(result ?? {}).filter((e): e is [string,string] => typeof e[1]==="string"));
    } catch(e) {if (active && seq===generation) await fail(`Could not prepare this revision's export: ${String(e)}. Reload the proposal before retrying.`);}
    finally {if (active && seq===generation) busy=false;}
  }
  function close() {if (busy) return;if (decision && !uncertain && !discard) {discard=true;return;}generation++;onclose();}
</script>

<section aria-label="Improvement proposals" class="min-w-0 space-y-3">
  <div class="flex flex-wrap items-baseline justify-between gap-2"><h4 class="font-medium text-ink">Improvement proposals</h4><Button disabled={busy} onclick={close}>Back to lessons</Button></div>
  <p class="text-sm text-faint">Recurring lessons can suggest tests, lint rules or skills. Triage records your decision; implementation follows normal task and code review.</p>
  {#if busy}<p role="status" class="text-sm text-faint">Loading or saving the reviewed request. No repeated submission.</p>{/if}
  {#if notice}<p role="status" class="text-sm text-ink">{notice}</p>{/if}
  {#if error}<p bind:this={alert} tabindex="-1" role="alert" class="selectable whitespace-pre-wrap break-words text-sm text-bad">{error}</p>{/if}
  {#if discard}<div role="group" aria-label="Discard improvement draft" class="space-y-2"><p class="text-sm text-warn">Discard this unsaved decision?</p><div class="flex gap-2"><Button variant="danger" onclick={close}>Discard draft</Button><Button onclick={() => discard=false}>Keep editing</Button></div></div>{/if}
  {#if !decision && !uncertain}
    <form class="flex flex-wrap items-end gap-2" onsubmit={(e)=>{e.preventDefault();void loadCatalog();}}><label class="min-w-0 flex-1 text-sm text-faint">Goal scope (optional)<input class={field} disabled={busy} bind:value={goal} placeholder="Blank for project-wide proposals" /></label><Button type="submit" disabled={busy}>Load proposals</Button></form>
    <p class="text-sm text-faint">Showing {records.length} of {total} for {scope.goal_id ? `goal ${scope.goal_id}` : "project scope"}. Retained decisions remain visible when a proposal is withdrawn.</p>
    {#if loaded && !records.length}<p class="text-sm text-faint">No proposals or retained decisions in this scope yet.</p>{/if}
    <label class="block text-sm text-faint">Proposal<Select disabled={busy || !records.length} value={record?.id ?? ""} onchange={(e)=>{const r=records.find(r=>r.id===e.currentTarget.value);if(r)void inspect(r);}}><option value="" disabled>Choose a proposal</option>{#each records as r (r.id)}<option value={r.id}>{r.topic} · {r.available ? r.state : "withdrawn; history retained"}</option>{/each}</Select></label>
    {#if next}<Button disabled={busy} onclick={()=>void loadCatalog(true)}>Load more proposals</Button>{/if}
  {/if}
  <div bind:this={area} tabindex="-1" role="group" aria-label={reviewed ? "Review improvement decision" : decision ? "Edit improvement decision" : "Improvement inspection"} class="min-w-0 space-y-3">
    {#if reviewed}
      <h5 class="font-medium text-ink">{labels[reviewed.request.decision]} reviewed proposal</h5><p class="text-sm text-faint">{consequences[reviewed.request.decision]}</p>
      <ImprovementBrief proposal={reviewed.proposal} />
      <p class="selectable whitespace-pre-wrap break-words text-sm text-ink">Decision evidence: {reviewed.request.evidence}</p>
      <p class="text-sm text-faint">This decision does not change lesson status, install rules, start agents or create issues.</p>
      <div class="flex flex-wrap gap-2"><Button variant="primary" disabled={busy || uncertain || !!error || discard} onclick={submit}>Confirm {labels[reviewed.request.decision].toLowerCase()}</Button><Button disabled={busy || uncertain || discard} onclick={()=>reviewed=null}>Back to editing</Button></div>
    {:else if decision && proposal}
      <h5 class="font-medium text-ink">{labels[decision]} proposal</h5><p class="text-sm text-faint">{consequences[decision]}</p><ImprovementBrief {proposal} />
      <form class="space-y-3" onsubmit={(e)=>{e.preventDefault();void review();}}><fieldset disabled={busy || uncertain || discard} class="min-w-0 space-y-3"><label class="block text-sm text-faint">Decision evidence<textarea class={field} rows="3" required bind:value={evidence}></textarea></label><p class="text-sm text-faint">Describe what you checked or where follow-up is tracked. Use 8–2000 UTF-8 bytes; exclude credentials and private user data.</p><Button type="submit" variant="primary">Review improvement decision</Button></fieldset></form>
    {:else if record}
      {#if proposal}<ImprovementBrief {proposal} />{:else}<h4 class="break-words font-medium text-ink">{record.topic}</h4><p class="text-sm text-warn">This proposal is no longer supported by current sources. Retained decisions and their source versions remain inspectable; triage and export are unavailable.</p>{/if}
      <div class="flex flex-wrap gap-2"><Button disabled={busy} onclick={()=>record && void inspect(record,true)}>Reload proposal</Button>{#if proposal}<Button disabled={locked} onclick={()=>proposal && void loadSources(proposal.revision)}>Inspect all sources</Button><Button disabled={locked || !!error} onclick={exportPreview}>Preview improvement export</Button>{/if}</div>
      {#if proposal}<div class="flex flex-wrap gap-2">{#each Object.entries(labels) as [key,label]}<Button variant="secondary" disabled={locked || !!error} onclick={()=>void begin(key)}>{label} proposal</Button>{/each}</div>{/if}
      <details class="text-sm" open><summary class="cursor-pointer text-ink">Decision history ({history.length})</summary>{#if !history.length}<p class="mt-2 text-faint">No triage decisions recorded yet.</p>{/if}{#each history as item,i}<div class="mt-3 space-y-1 border-t border-edge pt-2"><p class="break-words text-ink">{item.decision} · {item.at}</p><p class="selectable whitespace-pre-wrap break-words text-faint">{item.evidence}</p><p class="selectable break-all text-faint">Revision {item.revision}</p><Button disabled={locked} onclick={()=>void loadSources(item.revision)}>Inspect sources for decision {i+1}</Button></div>{/each}</details>
      {#if sources}<section aria-label="Exact source references" class="space-y-2 border-t border-edge pt-3 text-sm"><h5 class="font-medium text-ink">Exact source references</h5><p class="selectable break-all text-faint">Revision {sources.revision}</p><p class="text-faint">{sources.references?.length ?? 0} of {sources.total} references loaded. Open a lesson to inspect its recorded content and provenance.</p><div class="flex flex-wrap gap-2">{#each sources.references ?? [] as ref}<Button disabled={locked} onclick={()=>void inspectLesson(ref)}>{ref.id} · v{ref.version}</Button>{/each}</div>{#if sources.next_after}<Button disabled={locked} onclick={()=>sources && void loadSources(sources.revision,true)}>Load more sources</Button>{/if}</section>{/if}
      {#if lesson}<section aria-label="Source lesson version" class="min-w-0 space-y-2 border-t border-edge pt-3 text-sm"><h5 class="break-words font-medium text-ink">{lesson.topic} · recorded version {lesson.version}</h5><p class="text-faint">Recorded status: {lesson.status}. This is the cited version; its current eligibility may have changed.</p><p class="selectable whitespace-pre-wrap break-words text-ink">{lesson.summary}</p><p class="selectable whitespace-pre-wrap break-words text-faint">{lesson.rationale}</p><p class="break-words text-faint">Source: {lesson.source.kind} · {lesson.source.id}{lesson.source.session ? ` · session ${lesson.source.session}` : ""}</p><p class="break-words text-faint">Verification: {lesson.verification || "Not verified"}</p><ul class="list-disc pl-5 text-faint">{#each lesson.evidence ?? [] as item}<li class="selectable break-words">{item}</li>{/each}</ul><details><summary class="cursor-pointer text-ink">Full source lesson and provenance</summary><pre class="selectable mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-all text-faint">{JSON.stringify(lesson,null,2)}</pre></details></section>{/if}
      {#if Object.keys(files).length}<section aria-label="Improvement export preview" class="space-y-2 border-t border-edge pt-3 text-sm"><h5 class="font-medium text-ink">Improvement export preview</h5><p class="text-faint">No files were written or rules installed. Copy reviewed content into an ordinary task branch for implementation and code review.</p>{#each Object.entries(files) as [name,content]}<details><summary class="cursor-pointer break-all text-ink">{name}</summary><textarea class={field+" selectable"} rows="8" readonly aria-label={`Export ${name}`} value={content}></textarea></details>{/each}</section>{/if}
    {/if}
    {#if uncertain}<Button disabled={busy} onclick={()=>record && void inspect(record,true)}>Reload saved proposal</Button>{/if}
  </div>
</section>
