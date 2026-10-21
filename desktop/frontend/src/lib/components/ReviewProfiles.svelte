<script lang="ts">
  import Button from "./Button.svelte";
  import Checkbox from "./Checkbox.svelte";
  import Select from "./Select.svelte";
  import Disclosure from "./Disclosure.svelte";
  import type { Profile, Specialty } from "@bindings/internal/reviewprofile/models";
  import type { ReviewProviderDTO, ReviewKindDTO } from "@bindings/desktop/models";
  import { linesToText, splitLines } from "$lib/lines";

  let { profiles = $bindable(), specialties, providers, kinds }: {
    profiles: Profile[] | null | undefined;
    specialties: Specialty[];
    providers: ReviewProviderDTO[];
    kinds: ReviewKindDTO[];
  } = $props();
  let added = $state<Profile | null>(null);
  const agents = $derived(kinds.filter((k) => !!k.agent));
  const enabledAgents = $derived(agents.filter((k) => providers.some((p) => p.provider === k.kind && p.enabled)));
  const label = (kind: string) => specialties.find((s) => s.kind === kind)?.label ?? kind;
  const providerLabel = (kind: string) => agents.find((a) => a.kind === kind)?.agent || kind;
  const available = (kind: string) => enabledAgents.some((a) => a.kind === kind);
  const field = "w-full min-w-0 rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder";
  const row = "settings-field grid min-w-0 grid-cols-[11rem_minmax(0,1fr)] items-baseline gap-3";

  function add(kind: string) {
    if (!profiles) return;
    let id = kind;
    for (let n = 2; profiles.some((p) => p.id === id); n++) id = `${kind}-${n}`;
    const p: Profile = { id, kind, provider: enabledAgents[0]?.kind ?? agents[0]?.kind ?? "codex-session", version: "1", enabled: false, required: false, on_pr_open: false, projects: [], paths: [], artifacts: [], rubric: "", timeout_seconds: 0, max_rounds: 0 };
    profiles = [...profiles, p];
    added = profiles[profiles.length - 1];
  }
</script>

<section aria-label="Specialist reviewers" class="space-y-3">
  <h3 class="text-base font-medium text-ink">Specialist reviewers</h3>
  <p class="text-sm text-faint">Choose what each reviewer checks. Several specialists can use the same agent provider and keep separate findings.</p>
  {#if profiles == null}
    <p class="text-sm text-warn">Rebuild the desktop app to configure specialist reviewers here.</p>
  {:else}
    <div class="flex flex-wrap items-center gap-2">
      <span class="text-sm text-faint">Add specialist:</span>
      {#each specialties as specialty (specialty.kind)}
        <Button variant="secondary" disabled={!kinds.length} onclick={() => add(specialty.kind)}>{specialty.label}</Button>
      {/each}
    </div>
    {#if profiles.length === 0}
      <p class="text-sm text-faint">No specialists configured. Add Backend or UI/UX to start, then choose a provider and enable it.</p>
    {/if}
    {#if kinds.length && enabledAgents.length === 0}
      <p class="text-sm text-warn">Enable Claude, Codex or OpenCode under Review providers below before enabling a specialist.</p>
    {/if}
    {#each profiles as p, i (p)}
      {@const specialty = specialties.find((s) => s.kind === p.kind)}
      <details open={p === added} class="border-b border-edge pb-3">
        <summary class="cursor-pointer py-2 text-ink">
          <span class="font-medium">{label(p.kind)}</span>
          <span class="ml-2 break-all text-sm text-faint">{p.id} · {providerLabel(p.provider)} · {p.enabled ? "Enabled" : "Disabled"}</span>
        </summary>
        <div class="mt-3 space-y-3" role="group" aria-label={`${p.id || `Specialist ${i + 1}`} settings`}>
          <div class="flex flex-wrap items-center justify-between gap-3">
            <label class="flex items-center gap-2">
              <Checkbox bind:checked={p.enabled} disabled={!p.enabled && !available(p.provider)} />
              Enabled
            </label>
            <Button variant="danger" aria-label={`Remove specialist ${p.id}`} onclick={() => { profiles = profiles?.filter((item) => item !== p); }}>Remove specialist</Button>
          </div>
          {#if !available(p.provider)}<p class="text-sm text-warn">This provider is unavailable or disabled. Select an enabled agent provider, or keep this specialist disabled.</p>{/if}
          <label class={row}>
            <span class="text-faint">ID</span>
            <input class={field} bind:value={p.id} placeholder="backend" />
          </label>
          <label class={row}>
            <span class="text-faint">Provider</span>
            <Select bind:value={p.provider}>
              {#if !agents.some((a) => a.kind === p.provider)}<option value={p.provider}>{p.provider} (unavailable)</option>{/if}
              {#each agents as a (a.kind)}<option value={a.kind}>{a.agent}{available(a.kind) ? "" : " (disabled)"}</option>{/each}
            </Select>
          </label>
          <p class="text-sm text-faint">Uses this provider’s configured model. Goal runs use the models and profiles in their accepted execution policy.</p>
          <div class="flex flex-wrap gap-x-6 gap-y-2">
            <label class="flex items-center gap-2"><Checkbox bind:checked={p.on_pr_open} />On PR open</label>
            <label class="flex items-center gap-2"><Checkbox bind:checked={p.required} />Required for delivery</label>
          </div>
          <p class="text-sm text-faint">On PR open runs a separate specialist pass for ordinary sessions. Required profiles hold delivery until current evidence passes. Autonomous goals select their specialists in the accepted policy.</p>
          {#if specialty?.artifacts?.length}
            <p class="text-sm text-faint">Required evidence: {specialty.artifacts.join(", ")}. Missing evidence leaves the review incomplete.</p>
          {/if}
          <Disclosure label="Rubric and scope">
            <div class="mt-3 space-y-3">
              <p class="text-sm text-faint">{specialty?.rubric ?? "This specialty is not in the current catalog. Its configuration is preserved."}</p>
              <label class={row}><span class="text-faint">Rubric version</span><input class={field} bind:value={p.version} /></label>
              <label class="block space-y-1"><span class="text-faint">Additional review instructions</span><textarea class={field} rows="4" value={p.rubric ?? ""} oninput={(e) => { p.rubric = e.currentTarget.value; }}></textarea></label>
              <label class="block space-y-1"><span class="text-faint">Project or stack IDs</span><textarea class={field} rows="2" value={linesToText(p.projects)} oninput={(e) => { p.projects = splitLines(e.currentTarget.value); }}></textarea></label>
              <p class="text-sm text-faint">One configured ID per line. Blank applies to all projects and stacks.</p>
              <label class="block space-y-1"><span class="text-faint">Path prefixes</span><textarea class={field} rows="2" value={linesToText(p.paths)} oninput={(e) => { p.paths = splitLines(e.currentTarget.value); }}></textarea></label>
              <p class="text-sm text-faint">One repository-relative file or directory per line, such as internal or src/components. Blank covers all changed paths.</p>
              <label class="block space-y-1"><span class="text-faint">Additional evidence kinds</span><textarea class={field} rows="2" value={linesToText(p.artifacts)} oninput={(e) => { p.artifacts = splitLines(e.currentTarget.value); }}></textarea></label>
              <label class={row}><span class="text-faint">Timeout (seconds)</span><input class={field} type="number" min="0" max="900" bind:value={p.timeout_seconds} /></label>
              <label class={row}><span class="text-faint">Review attempt limit</span><input class={field} type="number" min="0" max="3" bind:value={p.max_rounds} /></label>
              <p class="text-sm text-faint">Timeout 0 uses the provider limit. Attempt limit 0 uses 3; retries stay bounded.</p>
            </div>
          </Disclosure>
        </div>
      </details>
    {/each}
  {/if}
</section>
