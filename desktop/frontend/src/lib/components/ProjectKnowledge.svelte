<script lang="ts">
  import { onDestroy, tick, untrack } from "svelte";
  import { DaemonService } from "@bindings/desktop";
  import type { Acceptance, CaptureRequest, Lesson } from "@bindings/internal/knowledge";
  import Button from "./Button.svelte";
  import KnowledgeImprovements from "./KnowledgeImprovements.svelte";
  import Checkbox from "./Checkbox.svelte";
  import Select from "./Select.svelte";
  import Disclosure from "./Disclosure.svelte";

  let { project }: { project: string } = $props();
  type Mode = "capture" | "accept" | "dispute";
  type Review = { mode: "capture"; request: CaptureRequest; correction: boolean } | { mode: "accept" | "dispute"; request: Acceptance };
  const field = "mt-1 w-full min-w-0 rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent";
  const lines = (value: string) => value.split("\n").map(v => v.trim()).filter(Boolean);
  function localExpiry(value?: string) {
    if (!value || value.startsWith("0001-")) return "";
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? "" : new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 19);
  }
  let improvements = $state(false);
  let expanded = $state(false), loading = $state(false), busy = $state(false), loaded = $state(false);
  let error = $state(""), notice = $state(""), selected = $state(""), search = $state(""), status = $state("");
  let lessons = $state<Lesson[]>([]);
  let mode = $state<Mode | null>(null), subject = $state<Lesson | null>(null), reviewed = $state<Review | null>(null);
  let acknowledged = $state(false), uncertain = $state(false), discard = $state(false);
  let topic = $state(""), summary = $state(""), rationale = $state(""), paths = $state(""), tags = $state(""), evidence = $state(""), goalID = $state("");
  let correction = $state(false), policy = $state(false), supersedes = $state<string[]>([]), watch = $state(""), expires = $state("");
  let prevention = $state(""), change = $state(""), validation = $state("");
  let files = $state<Record<string, string>>({});
  let reviewArea = $state<HTMLDivElement>(), formArea = $state<HTMLDivElement>();
  let generation = 0;
  const current = $derived(lessons.find(l => l.id === selected));
  const visible = $derived(lessons.filter(l => (!status || l.status === status) && (!search || `${l.topic} ${l.summary} ${(l.tags ?? []).join(" ")} ${(l.paths ?? []).join(" ")}`.toLowerCase().includes(search.toLowerCase()))));
  const conflicts = $derived(subject ? lessons.filter(l => l.id !== subject!.id && l.status === "active" && l.scope.goal_id === subject!.scope.goal_id && l.topic === subject!.topic && JSON.stringify(l.paths ?? []) === JSON.stringify(subject!.paths ?? []) && l.summary !== subject!.summary) : []);

  function clearForm() { mode = null; subject = null; reviewed = null; acknowledged = false; uncertain = false; discard = false; }
  $effect(() => {
    const scope = project;
    untrack(() => { generation++; improvements=false; lessons = []; loaded = false; loading = false; busy = false; selected = ""; search = ""; status = ""; error = ""; notice = ""; files = {}; clearForm(); if (expanded) void refresh(scope); });
  });
  onDestroy(() => { generation++; });
  async function refresh(scope = project) {
    if (loading || busy || mode) return;
    const gen = ++generation;
    loading = true; error = "";
    try {
      const result = await DaemonService.KnowledgeList(scope);
      if (gen !== generation || scope !== project) return;
      if ((result ?? []).some(l => l.scope.project !== scope)) throw new Error("Lesson list does not match this project");
      lessons = result ?? []; loaded = true;
      if (!lessons.some(l => l.id === selected)) selected = "";
    } catch (e) { if (gen === generation) error = `Could not load lessons: ${String(e)}. Refresh to try again.`; }
    finally { if (gen === generation) loading = false; }
  }
  async function openForm(next: Mode, lesson?: Lesson) {
    mode = next; subject = lesson ? structuredClone($state.snapshot(lesson)) : null;
    reviewed = null; acknowledged = false; uncertain = false; discard = false; error = ""; notice = ""; files = {};
    topic = ""; summary = ""; rationale = ""; paths = ""; tags = ""; evidence = ""; goalID = ""; correction = false; policy = false; supersedes = [];
    watch = (lesson?.code ?? []).map(r => `${r.member}:${r.path}`).join("\n"); expires = localExpiry(lesson?.expires_at); prevention = ""; change = ""; validation = "";
    await tick(); formArea?.focus();
  }
  async function closeForm() { clearForm(); error = ""; await refresh(); }
  async function review() {
    if (!mode || busy) return;
    error = "";
    try {
      if (mode === "capture") {
        if (!topic.trim() || summary.trim().length < 8) throw new Error("Enter a topic and a lesson of at least 8 characters.");
        reviewed = { mode, correction, request: { capture_id: crypto.randomUUID(), scope: { project, ...(goalID.trim() ? { goal_id: goalID.trim() } : {}) }, proposal: {
          topic: topic.trim(), summary: summary.trim(), rationale: rationale.trim(), paths: lines(paths), tags: lines(tags), evidence: lines(evidence), policy,
          ...(prevention ? { prevention: { kind: prevention, change: change.trim(), validation: validation.trim() } } : {}),
        } } };
      } else {
        if (!subject || evidence.trim().length < 8) throw new Error("Describe the verification or dispute evidence in at least 8 characters.");
        const targets = lines(watch).map(line => {
          const at = line.indexOf(":"); if (at <= 0 || at === line.length - 1) throw new Error("Code references use member:path, one per line.");
          return { member: line.slice(0, at).trim(), path: line.slice(at + 1).trim() };
        });
        if (mode === "accept" && subject.code?.length && !targets.length) throw new Error("Retain committed code references when accepting this lesson.");
        reviewed = { mode, request: { scope: structuredClone($state.snapshot(subject.scope)), id: subject.id, version: subject.version, evidence: evidence.trim(), supersedes: [...supersedes], ...(targets.length ? { code_targets: targets } : {}), ...(expires ? { expires: expires === localExpiry(subject.expires_at) ? subject.expires_at : new Date(expires).toISOString() } : {}) } };
      }
      acknowledged = false; await tick(); reviewArea?.focus();
    } catch (e) { reviewed = null; error = String(e); }
  }
  async function submit() {
    if (!reviewed || !acknowledged || busy || uncertain) return;
    const action = structuredClone($state.snapshot(reviewed)), scope = project, gen = generation;
    busy = true; error = "";
    try {
      let result: Lesson | undefined;
      if (action.mode === "capture") result = await DaemonService.KnowledgeCapture(action.request, action.correction);
      else if (action.mode === "accept") result = await DaemonService.KnowledgeAccept(action.request);
      else await DaemonService.KnowledgeDispute(action.request.scope, action.request.id, action.request.version, action.request.evidence);
      if (gen !== generation || scope !== project) return;
      if (result && (result.scope.project !== scope || (action.mode !== "capture" && result.id !== action.request.id) || (action.mode === "capture" && result.source.id !== action.request.capture_id))) throw new Error("Reply does not identify the reviewed lesson");
      selected = result?.id ?? (action.mode === "capture" ? "" : action.request.id);
      notice = result ? `Saved as ${result.status}, version ${result.version}.` : "Lesson disputed. It is excluded from future guidance.";
      clearForm();
    } catch (e) {
      if (gen === generation) { uncertain = true; acknowledged = false; error = `The result was not confirmed: ${String(e)}. Close and refresh before another decision; the daemon may already have saved it.`; }
    } finally {
      if (gen === generation) { busy = false; if (!mode) await refresh(); }
    }
  }
  async function exportFiles() {
    if (busy || loading || mode) return;
    const scope = project, gen = generation; busy = true; error = ""; files = {};
    try {
      const result = await DaemonService.KnowledgeExport(scope);
      if (gen === generation && scope === project) files = Object.fromEntries(Object.entries(result ?? {}).filter((entry): entry is [string, string] => typeof entry[1] === "string"));
    } catch (e) { if (gen === generation) error = `Could not prepare the export: ${String(e)}.`; }
    finally { if (gen === generation) busy = false; }
  }
</script>

<section class="min-w-0 rounded-lg border border-edge" aria-label="Workspace learning">
  <button class="flex w-full items-center justify-between gap-3 rounded-lg px-3 py-2 text-left text-ink hover:bg-sel/50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent" aria-expanded={expanded} disabled={improvements} onclick={() => { expanded = !expanded; if (expanded && !loaded && !mode) void refresh(); }}>
    <span class="font-medium">Workspace learning</span><span class="text-sm text-faint">{expanded ? "Hide" : "Inspect and curate"}</span>
  </button>
  {#if expanded}
    <div class="min-w-0 space-y-4 border-t border-edge p-3">
      {#if improvements}
        {#key project}<KnowledgeImprovements {project} onclose={() => { improvements=false; }} />{/key}
      {:else}
      <p class="text-sm text-faint">Corrections and verified lessons can guide future sessions. Candidates remain evidence to review; they do not change project rules.</p>
      {#if error}<p role="alert" class="selectable whitespace-pre-wrap break-words text-sm text-bad">{error}</p>{/if}
      {#if notice}<p role="status" class="text-sm text-good">{notice}</p>{/if}
      {#if busy || loading}<p role="status" class="text-sm text-faint">{busy ? "Waiting for the reviewed operation…" : "Loading lessons…"}</p>{/if}
      {#if !mode}
        <div class="flex flex-wrap gap-2"><Button disabled={loading || busy} onclick={() => { improvements=true; }}>Improvement proposals</Button><Button disabled={loading || busy} onclick={() => void openForm("capture")}>Record a lesson</Button><Button disabled={loading || busy} onclick={() => void refresh()}>Refresh lessons</Button><Button disabled={loading || busy} onclick={() => void exportFiles()}>Preview LEARNINGS.md export</Button></div>
        <div class="grid gap-3 sm:grid-cols-2"><label class="text-sm text-faint">Find lessons<input class={field} bind:value={search} type="search" /></label><label class="text-sm text-faint">Status<Select class="mt-1" bind:value={status}><option value="">All statuses</option>{#each ["candidate", "active", "disputed", "superseded"] as value}<option value={value}>{value}</option>{/each}</Select></label></div>
        {#if loaded && !lessons.length}<p class="text-sm text-faint">No lessons recorded for this project yet. Record a correction or let workers submit discoveries for review.</p>{:else if loaded && !visible.length}<p class="text-sm text-faint">No lessons match these filters.</p>{/if}
        <div class="divide-y divide-edge">{#each visible as lesson (lesson.id)}
          <button class="flex w-full min-w-0 flex-col gap-1 py-2 text-left text-sm text-ink hover:bg-sel/50 focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent" aria-pressed={selected === lesson.id} onclick={() => { selected = lesson.id; files = {}; }}>
            <span class="break-words font-medium">{lesson.topic}</span><span class="break-words text-faint">{lesson.status} · v{lesson.version} · {lesson.scope.goal_id ? `Goal ${lesson.scope.goal_id}` : "Project scope"}{lesson.policy ? " · policy proposal" : ""}</span>
          </button>
        {/each}</div>
        {#if current}
          <article class="min-w-0 space-y-3 border-t border-edge pt-3" aria-label="Selected lesson">
            <h4 class="break-words font-medium text-ink">{current.topic}</h4><p class="selectable whitespace-pre-wrap break-words text-sm text-ink">{current.summary}</p>
            {#if current.rationale}<p class="selectable whitespace-pre-wrap break-words text-sm text-faint">{current.rationale}</p>{/if}
            <dl class="space-y-1 text-sm text-faint"><div><dt class="inline text-ink">Scope:</dt>{" "}<dd class="inline break-words">{current.scope.goal_id ? `Goal ${current.scope.goal_id}` : "Project"}; {(current.paths ?? []).join(", ") || "all paths"}</dd></div><div><dt class="inline text-ink">Source:</dt>{" "}<dd class="inline break-all">{current.source.kind} · {current.source.id}{current.source.session ? ` · session ${current.source.session}` : ""}</dd></div><div><dt class="inline text-ink">Verification:</dt>{" "}<dd class="inline break-words">{current.verification || "Not verified"}</dd></div></dl>
            {#if current.review_reason}<p class="text-sm text-warn">Needs review: {current.review_reason}</p>{/if}
            {#if current.evidence?.length}<ul class="list-disc space-y-1 pl-5 text-sm text-faint">{#each current.evidence as item}<li class="selectable break-words">{item}</li>{/each}</ul>{/if}
            <p class="text-sm text-faint">{current.code?.length ? `${current.code.length} committed code references watched for changes.` : "No committed code references tracked."}</p>
            <details class="text-sm"><summary class="cursor-pointer text-ink">Full lesson and provenance</summary><pre class="selectable mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-all text-faint">{JSON.stringify(current, null, 2)}</pre></details>
            {#if current.policy}<p class="text-sm text-warn">Mandatory policy changes require normal code review and cannot be activated here.</p>{/if}
            <div class="flex flex-wrap gap-2"><Button disabled={busy || loading || current.policy || current.status === "superseded"} onclick={() => void openForm("accept", current)}>Review acceptance</Button><Button disabled={busy || loading || current.status === "superseded"} onclick={() => void openForm("dispute", current)}>Dispute lesson</Button></div>
          </article>
        {/if}
        {#if Object.keys(files).length}
          <div class="space-y-2 border-t border-edge pt-3"><h4 class="font-medium text-ink">Reviewable export</h4><p class="text-sm text-faint">Accepted project lessons were rechecked. No repository files were written. Copy reviewed content into your branch for normal code review.</p>{#each Object.entries(files) as [name, content]}<details class="text-sm"><summary class="cursor-pointer break-all text-ink">{name}</summary><textarea class={field + " selectable"} aria-label={`Export ${name}`} rows="8" readonly value={content}></textarea></details>{/each}</div>
        {/if}
      {:else if reviewed}
        <div bind:this={reviewArea} tabindex="-1" role="group" aria-label="Review learning decision" class="space-y-3">
          <h4 class="font-medium text-ink">{reviewed.mode === "capture" ? "Review new lesson" : reviewed.mode === "accept" ? "Accept reviewed lesson" : "Dispute reviewed lesson"}</h4>
          <p class="text-sm text-faint">{reviewed.mode === "capture" ? reviewed.correction ? "This is your explicit correction. It becomes active within its scope unless it conflicts with existing guidance or proposes a mandatory policy change." : "This discovery remains a candidate until verified and accepted." : reviewed.mode === "accept" ? "Activate this lesson version with your verification evidence. Selected conflicting lessons become superseded; future sessions can receive this guidance." : "Exclude this lesson from future guidance and preserve its history."}</p>
          {#if subject}<p class="selectable whitespace-pre-wrap break-words text-sm text-ink">{subject.topic} · version {subject.version}<br />{subject.summary}</p>{/if}
          <p class="text-sm text-faint">Scope: {reviewed.request.scope.project}{reviewed.request.scope.goal_id ? ` · goal ${reviewed.request.scope.goal_id}` : " · project scope"}</p>
          {#if reviewed.mode === "capture"}
            <h5 class="break-words font-medium text-ink">{reviewed.request.proposal.topic}</h5>
            <p class="selectable whitespace-pre-wrap break-words text-sm text-ink">{reviewed.request.proposal.summary}</p>
            <p class="selectable whitespace-pre-wrap break-words text-sm text-faint">{reviewed.request.proposal.rationale}</p>
            <p class="text-sm text-faint">Paths: {reviewed.request.proposal.paths?.join(", ") || "All paths"}. Tags: {reviewed.request.proposal.tags?.join(", ") || "None"}.</p>
            <p class="text-sm text-faint">Mandatory policy proposal: {reviewed.request.proposal.policy ? "yes; cannot activate here" : "no"}.</p>
            <ul class="list-disc pl-5 text-sm text-faint">{#each reviewed.request.proposal.evidence ?? [] as item}<li class="selectable break-words">{item}</li>{/each}</ul>
            {#if reviewed.request.proposal.prevention}<p class="selectable whitespace-pre-wrap break-words text-sm text-faint">Prevention proposal: {reviewed.request.proposal.prevention.kind}<br />{reviewed.request.proposal.prevention.change}<br />Verify with: {reviewed.request.proposal.prevention.validation}</p>{/if}
            <p class="selectable break-all text-sm text-faint">Capture reference: {reviewed.request.capture_id}</p>
          {:else}
            <p class="text-sm text-faint">Paths: {subject?.paths?.join(", ") || "All paths"}.</p>
            <p class="selectable whitespace-pre-wrap break-words text-sm text-ink">Evidence: {reviewed.request.evidence}</p>
            {#if reviewed.mode === "accept"}
              <p class="text-sm text-faint">Expiry: {reviewed.request.expires ? new Date(reviewed.request.expires).toLocaleString() : "No expiry"}.</p>
              <p class="text-sm text-faint">Code references: {reviewed.request.code_targets?.map(r => `${r.member}:${r.path}`).join(", ") || "None; code changes will not automatically invalidate this lesson"}.</p>
              <p class="selectable break-words text-sm text-faint">Supersedes: {reviewed.request.supersedes?.join(", ") || "No other lessons"}.</p>
            {/if}
          {/if}
          <details class="text-sm"><summary class="cursor-pointer text-ink">Full decision record</summary><pre class="selectable mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-all text-faint">{JSON.stringify(reviewed.request, null, 2)}</pre></details>
          {#if !uncertain}<label class="flex items-start gap-2 text-sm text-ink"><Checkbox bind:checked={acknowledged} disabled={busy} />I reviewed this lesson, scope, evidence and decision.</label>{/if}
          <div class="flex flex-wrap gap-2"><Button variant="primary" disabled={!acknowledged || busy || uncertain} onclick={() => void submit()}>Confirm learning decision</Button>{#if !uncertain}<Button disabled={busy} onclick={() => { reviewed = null; acknowledged = false; }}>Back to edit</Button>{:else}<Button disabled={busy} onclick={() => void closeForm()}>Close and refresh lessons</Button>{/if}</div>
        </div>
      {:else}
        <div bind:this={formArea} tabindex="-1" role="group" aria-label="Edit learning decision" class="space-y-3">
          <div class="flex flex-wrap items-baseline justify-between gap-2"><h4 class="font-medium text-ink">{mode === "capture" ? "Record a lesson" : `${mode === "accept" ? "Accept" : "Dispute"} ${subject?.topic}`}</h4><Button onclick={() => { discard = true; }}>Close form</Button></div>
          {#if discard}<div class="flex flex-wrap items-center gap-2 text-sm text-warn"><span>Discard this unsaved decision?</span><Button onclick={() => void closeForm()}>Discard changes</Button><Button onclick={() => { discard = false; }}>Keep editing</Button></div>{/if}
          <form class="space-y-3" onsubmit={(e) => { e.preventDefault(); void review(); }}>
            {#if mode === "capture"}
              <label class="block text-sm text-faint">Topic<input class={field} required maxlength="160" bind:value={topic} /></label>
              <label class="block text-sm text-faint">Lesson<textarea class={field} rows="3" required minlength="8" maxlength="2000" bind:value={summary}></textarea></label>
              <label class="block text-sm text-faint">Why it matters<textarea class={field} rows="2" maxlength="4000" bind:value={rationale}></textarea></label>
              <div class="grid gap-3 sm:grid-cols-2"><label class="text-sm text-faint">Applicable path prefixes<textarea class={field} rows="2" bind:value={paths}></textarea></label><label class="text-sm text-faint">Tags<textarea class={field} rows="2" bind:value={tags}></textarea></label></div>
              <p class="text-sm text-faint">One path or tag per line. Blank paths apply across this project. Keep credentials and private user data out of lessons.</p>
              <label class="block text-sm text-faint">Goal ID (optional)<input class={field} bind:value={goalID} /></label>
              <label class="flex items-start gap-2 text-sm text-ink"><Checkbox bind:checked={correction} />This is my explicit correction; activate within its scope when eligible.</label>
              <label class="flex items-start gap-2 text-sm text-ink"><Checkbox bind:checked={policy} />This proposes a mandatory policy change; keep it as a proposal.</label>
              <Disclosure label="Prevent this mistake"><div class="mt-2 space-y-2"><label class="block text-sm text-faint">Proposed prevention<Select class="mt-1" bind:value={prevention}><option value="">None</option><option value="regression_test">Regression test</option><option value="lint">Lint rule</option><option value="skill">Reusable skill</option></Select></label>{#if prevention}<label class="block text-sm text-faint">Proposed change<textarea class={field} required bind:value={change}></textarea></label><label class="block text-sm text-faint">How to verify it<textarea class={field} required bind:value={validation}></textarea></label>{/if}</div></Disclosure>
            {:else}
              <p class="selectable whitespace-pre-wrap break-words text-sm text-ink">{subject?.summary}</p><p class="text-sm text-faint">Reviewing version {subject?.version}; {subject?.scope.goal_id ? `goal ${subject.scope.goal_id}` : "project scope"}.</p>
            {/if}
            <label class="block text-sm text-faint">{mode === "capture" ? "Evidence (one reference per line)" : mode === "accept" ? "Verification evidence" : "Dispute evidence"}<textarea class={field} rows="3" required={mode !== "capture"} minlength={mode !== "capture" ? 8 : undefined} maxlength={mode === "dispute" ? 2000 : 4000} bind:value={evidence}></textarea></label>
            {#if mode === "accept"}
              <label class="block text-sm text-faint">Watch committed code<textarea class={field} rows="2" bind:value={watch}></textarea></label><p class="text-sm text-faint">Use member:path, one per line. Lola reads the configured default branch and rechecks these references before using the lesson. Existing references must be retained or replaced.</p>
              <label class="block text-sm text-faint">Expiry (optional, local time)<input class={field} type="datetime-local" step="any" bind:value={expires} /></label>
              {#if conflicts.length}<fieldset class="space-y-2"><legend class="text-sm text-warn">Conflicting guidance — select every lesson this replaces</legend>{#each conflicts as other}<label class="flex items-start gap-2 text-sm text-ink"><Checkbox checked={supersedes.includes(other.id)} onchange={(e) => { supersedes = e.currentTarget.checked ? [...supersedes, other.id] : supersedes.filter(id => id !== other.id); }} /><span class="break-words">{other.summary} · v{other.version} · {other.id}</span></label>{/each}</fieldset>{/if}
            {/if}
            <Button type="submit" variant="primary">Review learning decision</Button>
          </form>
        </div>
      {/if}
      {/if}
    </div>
  {/if}
</section>
