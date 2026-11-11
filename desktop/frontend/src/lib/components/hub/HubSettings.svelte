<script lang="ts" module>
  // hubproto's key-id and public-key shapes: a key id is 1..64 of
  // [A-Za-z0-9_.-]; an ed25519 public key is 32 bytes, i.e. 43 base64url
  // characters without padding. The daemon's Validate is the real gate; these
  // only let the form say what is wrong before a save is refused.
  export const KEY_ID_RE = /^[A-Za-z0-9_.-]{1,64}$/;
  export const PUBLIC_KEY_RE = /^[A-Za-z0-9_-]{43}$/;

  export type TrustedKeyRow = { keyId: string; publicKey: string };

  /** The inline error for one trusted-key row ("" = fine or blank). */
  export function trustedKeyError(row: TrustedKeyRow, rows: TrustedKeyRow[]): string {
    const id = row.keyId.trim();
    const pub = row.publicKey.trim();
    if (!id && !pub) return "";
    if (!id) return "Enter the key id.";
    if (!KEY_ID_RE.test(id)) return "Key ids use letters, digits, “.”, “_” and “-” (64 at most).";
    if (!pub) return "Paste the public key.";
    if (!PUBLIC_KEY_RE.test(pub)) return "Public keys are 43 base64url characters.";
    if (rows.filter((r) => r.keyId.trim() === id).length > 1) return "This key id is listed twice.";
    return "";
  }
</script>

<script lang="ts">
  // The "Team hub" settings tab. Two halves with different save paths:
  //
  //   - JOIN and LEAVE act at once, through the daemon (HubService, the same
  //     path as `lola hub join`). They own [hub].url / enabled / device_name, so
  //     those keys are never SettingsDTO fields and a settings save cannot
  //     half-join a hub. The enrollment token lives only in this component's
  //     field and is cleared the moment the join succeeds.
  //   - The local hub RULES (accepted kinds, daily claims, sandbox, cadence,
  //     policy, trusted keys) are ordinary settings fields: edited here,
  //     written by the overlay's Save with everything else.
  //
  // A key the applied team policy carries is drawn through <TeamLock>.
  import { onDestroy, onMount } from "svelte";
  import Button from "$lib/components/Button.svelte";
  import Checkbox from "$lib/components/Checkbox.svelte";
  import CheckboxOptions from "$lib/components/CheckboxOptions.svelte";
  import SettingsGroup from "$lib/components/SettingsGroup.svelte";
  import TeamLock from "$lib/components/TeamLock.svelte";
  import TimeAgo from "$lib/components/TimeAgo.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { hub, connectionState } from "$lib/hub.svelte";
  import { HubService } from "@bindings/desktop";
  import type { HubKindDTO, SettingsDTO, TeamPolicyDTO } from "@bindings/desktop/models";

  type HubFields = Pick<SettingsDTO,
    "hubAcceptKinds" | "hubMaxClaimsPerDay" | "hubRequireSandbox" | "hubPollSeconds" | "hubApplyPolicy" | "hubTrustedKeys">;

  let { values, policy = null, onChange, onIdentityChange = () => {} }: {
    values: HubFields;
    /** ConfigService.TeamPolicy: which settings the applied policy locks. */
    policy?: TeamPolicyDTO | null;
    onChange: (patch: Partial<HubFields>) => void;
    /** Called after a join or leave, so the overlay can re-read the policy. */
    onIdentityChange?: () => void;
  } = $props();

  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder";
  const rowCls = "settings-field grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] items-baseline gap-x-6 gap-y-1.5";
  const rowTopCls = "settings-field grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] items-start gap-x-6 gap-y-1.5";
  const hintCls = "mt-1 block text-sm text-faint";
  const formId = $props.id();

  let release: (() => void) | null = null;
  onMount(() => {
    release = hub.watch();
    void hub.loadDefaultName().then((n) => {
      if (!joinName) joinPlaceholder = n;
    });
    void loadKinds();
  });
  onDestroy(() => release?.());

  // --- join -----------------------------------------------------------------
  let joinURL = $state("");
  let joinToken = $state("");
  let joinName = $state("");
  let joinPlaceholder = $state("");
  let joinError = $state("");
  let joinDone = $state("");

  const joinURLError = $derived.by(() => {
    const u = joinURL.trim();
    if (!u) return "";
    try {
      const url = new URL(u);
      const loopback = ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname);
      if (url.protocol !== "https:" && !(url.protocol === "http:" && loopback)) return "Use https (plain http only for localhost).";
      if (url.pathname !== "/" && url.pathname !== "") return "Enter the hub’s base URL, without a path.";
      return "";
    } catch {
      return "Enter a full URL, like https://hub.example.com.";
    }
  });
  const canJoin = $derived(!!joinURL.trim() && !joinURLError && !!joinToken.trim() && hub.busy === "");

  async function join() {
    if (!canJoin) return;
    joinError = "";
    joinDone = "";
    const token = joinToken;
    const r = await hub.join(joinURL.trim(), token, joinName.trim());
    if (r.ok) {
      // The token is spent: never keep it on screen or in memory.
      joinToken = "";
      joinDone = `Joined ${r.data.org} as ${r.data.deviceName}.`;
      onIdentityChange();
    } else {
      joinError = r.error;
    }
  }

  // --- joined ---------------------------------------------------------------
  const st = $derived(hub.status);
  const conn = $derived(connectionState(st));
  const toneCls: Record<string, string> = {
    good: "border-good/40 bg-good/10 text-good",
    bad: "border-bad/40 bg-bad/10 text-bad",
    warn: "border-warn/40 bg-warn/10 text-warn",
  };
  let leaveError = $state("");
  let syncMsg = $state("");

  function askLeave() {
    leaveError = "";
    confirm.ask({
      title: "Leave the team hub?",
      body: `Leave ${st?.org || "this hub"}? This Mac stops claiming chores and the team policy no longer applies.`,
      detail: "Sessions already working a chore keep running.",
      confirmLabel: "Leave",
      choices: { label: "Device key", items: [{ id: "forget", label: "Also delete this Mac’s device key (a later join is a new device)", checked: false }] },
      onConfirm: (checked) => void leave(checked.includes("forget")),
    });
  }

  async function leave(forgetKey: boolean) {
    const r = await hub.leave(forgetKey);
    if (r.ok) {
      joinDone = "";
      onIdentityChange();
    } else {
      leaveError = r.error;
    }
  }

  async function sync() {
    syncMsg = "";
    const r = await hub.sync();
    syncMsg = r.ok ? "Sync queued." : r.error;
  }

  // --- policy ---------------------------------------------------------------
  const lockFor = (field: string) => (policy?.applied ? (policy.locked ?? []).find((l) => l.field === field) ?? null : null);
  const policyRows = $derived.by(() => {
    if (policy?.locked?.length) return policy.locked.map((l) => ({ key: l.label, value: l.value, local: l.local }));
    return (st?.policy?.appliedKeys ?? []).map((k) => ({ key: k.key, value: k.value, local: k.local }));
  });

  // --- local rules ----------------------------------------------------------
  let kinds = $state<HubKindDTO[]>([]);
  async function loadKinds() {
    try {
      kinds = (await HubService.Kinds()) ?? [];
    } catch {
      kinds = [];
    }
  }

  const claims = $derived(values.hubMaxClaimsPerDay ?? 4);
  const claimsError = $derived(!Number.isInteger(claims) || claims < 0 || claims > 100 ? "Enter a whole number from 0 to 100." : "");
  const poll = $derived(values.hubPollSeconds ?? 300);
  const pollError = $derived(!Number.isInteger(poll) || poll < 60 || poll > 86400 ? "Enter seconds from 60 to 86400." : "");
  const sandbox = $derived(values.hubRequireSandbox ?? true);
  const applyPolicy = $derived(values.hubApplyPolicy ?? true);

  const keys = $derived<TrustedKeyRow[]>(values.hubTrustedKeys ?? []);
  function setKey(i: number, patch: Partial<TrustedKeyRow>) {
    const next = keys.map((k) => ({ ...k }));
    // lola-hub prints "<key_id>:<public key>": a paste of that into the id
    // field fills both halves.
    if (patch.keyId !== undefined && patch.keyId.includes(":")) {
      const [id, pub] = patch.keyId.split(":", 2);
      patch = { keyId: id.trim(), publicKey: (pub ?? "").trim() };
    }
    next[i] = { ...next[i], ...patch };
    onChange({ hubTrustedKeys: next });
  }
  function addKey() {
    onChange({ hubTrustedKeys: [...keys.map((k) => ({ ...k })), { keyId: "", publicKey: "" }] });
  }
  function removeKey(i: number) {
    onChange({ hubTrustedKeys: keys.filter((_, j) => j !== i).map((k) => ({ ...k })) });
  }
</script>

<div class="grid min-w-0 gap-0" data-hub-settings>
  {#if hub.outdated}
    <div role="alert" class="mb-6 rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">
      {hub.statusError}
    </div>
  {:else if !hub.loaded}
    <p role="status" class="mb-6 text-sm text-faint">Checking the hub…</p>
  {:else if hub.statusError}
    <div role="alert" class="mb-6 rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn">
      The lola daemon is not answering, so joining is unavailable. {hub.statusError}
    </div>
  {/if}

  {#if hub.loaded && !hub.outdated && !hub.statusError && !hub.joined}
    <SettingsGroup title="Join a hub" description="Your team’s hub hands out security and dependency chores and shares the team policy.">
      <label class={rowCls}>
        <span class="text-faint">Hub URL</span>
        <span>
          <input class={inputCls} type="url" aria-label="Hub URL" placeholder="https://hub.example.com" spellcheck="false" autocomplete="off"
            bind:value={joinURL} oninput={() => (joinError = "")} aria-invalid={!!joinURLError} />
          {#if joinURLError}<span class="mt-1 block text-sm text-bad">{joinURLError}</span>{/if}
        </span>
      </label>
      <label class={rowCls}>
        <span class="text-faint">Enrollment token</span>
        <span>
          <input class="{inputCls} font-mono" type="password" aria-label="Enrollment token" autocomplete="off" spellcheck="false"
            placeholder="From your hub admin" bind:value={joinToken} oninput={() => (joinError = "")} />
          <span class={hintCls}>Used once to enroll this Mac. It is not saved.</span>
        </span>
      </label>
      <label class={rowCls}>
        <span class="text-faint">Device name</span>
        <span>
          <input class={inputCls} type="text" aria-label="Device name" spellcheck="false" placeholder={joinPlaceholder || "This Mac’s name"} bind:value={joinName} />
          <span class={hintCls}>How this Mac appears to your team. Empty uses the hostname.</span>
        </span>
      </label>
      <div class={rowCls}>
        <span></span>
        <span class="flex flex-wrap items-center gap-2">
          <Button variant="primary" size="md" disabled={!canJoin} loading={hub.busy === "join"} onclick={join}>Join hub</Button>
        </span>
      </div>
      {#if joinError}<p role="alert" class="text-sm text-bad">{joinError}</p>{/if}
    </SettingsGroup>
  {/if}

  {#if hub.joined && st}
    <SettingsGroup title="Connection">
      {#if joinDone}<p role="status" class="text-sm text-good">{joinDone}</p>{/if}
      <div class="grid min-w-0 gap-3 rounded-lg border border-edge p-4" aria-label="Hub connection">
        <div class="flex min-w-0 flex-wrap items-center gap-3">
          <span class="min-w-0 flex-1 text-base font-medium text-ink">{st.org || "Team hub"}</span>
          <span class="inline-flex items-center rounded-full border px-2 py-0.5 text-sm {toneCls[conn.tone]}" data-connection={conn.tone}>{conn.label}</span>
        </div>
        <dl class="grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] gap-x-6 gap-y-1.5 text-sm">
          <dt class="text-faint">Device</dt><dd class="min-w-0 break-words text-ink">{st.deviceName || st.device || "—"}</dd>
          <dt class="text-faint">Hub URL</dt><dd class="min-w-0 break-all font-mono text-ink">{st.url || "—"}</dd>
          <dt class="text-faint">Last heartbeat</dt>
          <dd class="text-ink">{#if st.lastHeartbeat}<TimeAgo at={st.lastHeartbeat} />{:else}Not yet{/if}</dd>
          <dt class="text-faint">Claims today</dt><dd class="text-ink">{st.claimsToday} of {st.maxClaimsPerDay}</dd>
          {#if st.pendingOutcomes}<dt class="text-faint">Waiting to report</dt><dd class="text-ink">{st.pendingOutcomes}</dd>{/if}
        </dl>
        {#if st.lastError}
          <p role="alert" class="min-w-0 break-words text-sm text-bad">
            {st.lastError}{#if st.lastErrorAt} · <TimeAgo at={st.lastErrorAt} />{/if}
          </p>
        {/if}
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <Button variant="secondary" size="md" loading={hub.busy === "sync"} disabled={hub.busy !== ""} onclick={sync}>Sync now</Button>
        <Button variant="danger" size="md" loading={hub.busy === "leave"} disabled={hub.busy !== ""} onclick={askLeave}>Leave hub…</Button>
        {#if syncMsg}<span role="status" class="text-sm text-faint">{syncMsg}</span>{/if}
      </div>
      {#if leaveError}<p role="alert" class="text-sm text-bad">{leaveError}</p>{/if}
    </SettingsGroup>

    <SettingsGroup title="Team policy" description="Settings your team’s admins sign. A locked setting uses the team’s value.">
      {#if st.policy}
        {@const p = st.policy}
        <div class="grid min-w-0 gap-3 rounded-lg border border-edge p-4" aria-label="Policy details">
          <dl class="grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] gap-x-6 gap-y-1.5 text-sm">
            <dt class="text-faint">Version</dt><dd class="text-ink">v{p.version}</dd>
            <dt class="text-faint">Signed by</dt><dd class="min-w-0 break-all font-mono text-ink">{p.keyId || "—"}</dd>
            {#if p.issuedAt}<dt class="text-faint">Issued</dt><dd class="text-ink"><TimeAgo at={p.issuedAt} /></dd>{/if}
            <dt class="text-faint">Status</dt>
            <dd class="min-w-0 break-words {p.applied ? 'text-good' : 'text-warn'}">{p.applied ? "Applied" : `Not applied${p.applyNote ? `: ${p.applyNote}` : ""}`}</dd>
          </dl>
          {#if policyRows.length}
            <table class="w-full border-collapse text-sm">
              <thead>
                <tr>
                  <th class="border-b border-edge py-1.5 pr-6 text-left font-normal text-faint">Setting</th>
                  <th class="border-b border-edge py-1.5 pr-6 text-left font-normal text-faint">Team value</th>
                  <th class="border-b border-edge py-1.5 text-left font-normal text-faint">Your value</th>
                </tr>
              </thead>
              <tbody>
                {#each policyRows as row (row.key)}
                  <tr>
                    <td class="border-b border-edge/40 py-2 pr-6 align-top text-ink">{row.key}</td>
                    <td class="border-b border-edge/40 py-2 pr-6 align-top text-ink">{row.value}</td>
                    <td class="border-b border-edge/40 py-2 align-top text-ink-2">{row.local || "—"}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          {:else if p.summary?.length}
            <ul class="grid gap-1 text-sm text-ink-2">{#each p.summary as line, i (i)}<li>{line}</li>{/each}</ul>
          {/if}
        </div>
      {:else}
        <p class="text-sm text-faint">Your team has not published a policy yet, or this Mac has no trusted policy keys to verify one.</p>
      {/if}
    </SettingsGroup>
  {/if}

  <SettingsGroup title="Chores on this Mac" description="Local limits. They apply to every hub this Mac joins.">
    <div class={rowTopCls}>
      <span class="text-faint">Accepted chores</span>
      <span>
        <TeamLock lock={lockFor("hubAcceptKinds")} version={policy?.version ?? 0}>
          <CheckboxOptions label="Accepted chores" options={kinds} selected={values.hubAcceptKinds ?? []}
            onChange={(v) => onChange({ hubAcceptKinds: v })} />
        </TeamLock>
        <span class={hintCls}>None ticked accepts every kind.</span>
      </span>
    </div>
    <label class={rowCls}>
      <span class="text-faint">Claims per day</span>
      <span>
        <input class={inputCls} type="number" min="0" max="100" step="1" aria-label="Claims per day" value={claims}
          oninput={(e) => onChange({ hubMaxClaimsPerDay: e.currentTarget.valueAsNumber })} aria-invalid={!!claimsError} />
        <span class="mt-1 block text-sm {claimsError ? 'text-bad' : 'text-faint'}">{claimsError || "How many chores this Mac starts on its own each day. 0 pauses it."}</span>
      </span>
    </label>
    <label class={rowCls}>
      <span class="text-faint">Check every</span>
      <span>
        <input class={inputCls} type="number" min="60" max="86400" step="1" aria-label="Check every (seconds)" value={poll}
          oninput={(e) => onChange({ hubPollSeconds: e.currentTarget.valueAsNumber })} aria-invalid={!!pollError} />
        <span class="mt-1 block text-sm {pollError ? 'text-bad' : 'text-faint'}">{pollError || "Seconds between hub checks."}</span>
      </span>
    </label>
    <div class="grid gap-1.5">
      <label class="flex cursor-pointer items-center gap-2">
        <Checkbox checked={sandbox} onchange={() => onChange({ hubRequireSandbox: !sandbox })} />
        <span>Only work chores in sandboxed projects</span>
      </label>
      {#if sandbox}
        <p class="text-sm text-faint">A chore runs only in a project with the Docker sandbox.</p>
      {:else}
        <p role="alert" class="flex items-start gap-1.5 text-sm text-warn">
          <Icon name="triangle-alert" size={14} />
          <span>Chores come from your team’s hub. Without the sandbox, their agents run directly on this Mac.</span>
        </p>
      {/if}
    </div>
    <div class="grid gap-1.5">
      <label class="flex cursor-pointer items-center gap-2">
        <Checkbox checked={applyPolicy} onchange={() => onChange({ hubApplyPolicy: !applyPolicy })} />
        <span>Apply the team policy</span>
      </label>
      <p class="text-sm text-faint">Off shows the policy without enforcing it.</p>
    </div>
  </SettingsGroup>

  <SettingsGroup title="Trusted policy keys" description="Admin keys that may sign your team’s policy. The hub cannot add one.">
    {#each keys as row, i (i)}
      {@const err = trustedKeyError(row, keys)}
      <div class="grid min-w-0 gap-1.5" data-trusted-key={i}>
        <div class="grid min-w-0 grid-cols-[10rem_minmax(0,1fr)_auto] items-center gap-2">
          <input class="{inputCls} font-mono" aria-label={`Key id ${i + 1}`} placeholder="Key id" spellcheck="false" value={row.keyId}
            oninput={(e) => setKey(i, { keyId: e.currentTarget.value })} aria-invalid={!!err} />
          <input class="{inputCls} font-mono" aria-label={`Public key ${i + 1}`} placeholder="Public key" spellcheck="false" value={row.publicKey}
            oninput={(e) => setKey(i, { publicKey: e.currentTarget.value })} aria-invalid={!!err} />
          <Button variant="danger" size="sm" onclick={() => removeKey(i)} aria-label={`Remove key ${i + 1}`}>Remove</Button>
        </div>
        {#if err}<p class="text-sm text-bad">{err}</p>{/if}
      </div>
    {:else}
      <p class="text-sm text-faint">No trusted keys. Without one, a team policy is never applied.</p>
    {/each}
    <div>
      <Button variant="secondary" size="md" onclick={addKey} id={`${formId}-add-key`}><Icon name="plus" size={14} />Add key</Button>
    </div>
    <p class="text-sm text-faint">Paste “key id:public key” as your hub admin prints it into the key id field to fill both.</p>
  </SettingsGroup>
</div>
