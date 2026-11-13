<script lang="ts" module>
  // hubproto's key-id and public-key shapes: a key id is 1..64 of
  // [A-Za-z0-9_.-]; an ed25519 public key is 32 bytes, i.e. 43 base64url
  // characters without padding. The daemon's Validate is the real gate; these
  // only let the form say what is wrong before a save is refused.
  export const KEY_ID_RE = /^[A-Za-z0-9_.-]{1,64}$/;
  export const PUBLIC_KEY_RE = /^[A-Za-z0-9_-]{43}$/;

  export type TrustedKeyRow = { keyId: string; publicKey: string };

  /** config.MaxHubPollSeconds: leases are renewed on the poll cadence. */
  export const MAX_POLL_SECONDS = 900;

  /** The inline error for "Claims per day" ("" = fine). A cleared field is NaN. */
  export function hubClaimsError(v: number | undefined | null): string {
    const n = v ?? 4;
    return !Number.isInteger(n) || n < 0 || n > 100 ? "Enter a whole number from 0 to 100." : "";
  }

  /** The inline error for "Check every" ("" = fine). */
  export function hubPollError(v: number | undefined | null): string {
    const n = v ?? 300;
    return !Number.isInteger(n) || n < 60 || n > MAX_POLL_SECONDS ? `Enter seconds from 60 to ${MAX_POLL_SECONDS}.` : "";
  }

  /** The aria-labels of the two inputs, for the overlay's save gate to focus. */
  export const CLAIMS_LABEL = "Claims per day";
  export const POLL_LABEL = "Check every (seconds)";

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

  /** How long a join may take: the daemon bounds it at 60s, the app at 90s. */
  export const JOIN_SLOW_MS = 10_000;
</script>

<script lang="ts">
  // The "Team hub" settings tab. ONE save model: everything here acts at once,
  // through its own service call, like join, leave and the project toggles.
  //
  //   - JOIN and LEAVE go through the daemon (HubService, the same path as
  //     `lola hub join`). The invite holds the one-time token: it lives only in
  //     this component's field and is cleared the moment the join returns.
  //   - The local RULES (accepted kinds, daily claims, sandbox, cadence,
  //     grouping, applying the policy) save on change through
  //     ConfigService.SaveHubRules. A number saves when it is valid and the
  //     field is left; until then it is an unsaved draft the overlay guards.
  //   - TRUSTED KEYS are a draft with an explicit "Save keys": a pasted key
  //     is never written half-typed, and never dropped silently either — the
  //     draft reports itself through onDirty, so the overlay's close and Save
  //     stop at it.
  //
  // Every successful save calls onSaved(patch): the overlay folds it into its
  // values AND its baseline, so its own Save writes the same values again and
  // a close does not call them unsaved.
  //
  // A policy key reaches this Mac only from text the person pasted (an invite)
  // or typed, and is pinned only after they tick "Trust this key". Never from
  // a hub response.
  //
  // A key the applied team policy carries is drawn through <TeamLock>.
  import { onDestroy, onMount, tick, untrack } from "svelte";
  import Button from "$lib/components/Button.svelte";
  import Checkbox from "$lib/components/Checkbox.svelte";
  import CheckboxOptions from "$lib/components/CheckboxOptions.svelte";
  import SettingsGroup from "$lib/components/SettingsGroup.svelte";
  import HubSyncButton from "./HubSyncButton.svelte";
  import TeamLock from "$lib/components/TeamLock.svelte";
  import TimeAgo from "$lib/components/TimeAgo.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import HelpText from "$lib/components/HelpText.svelte";
  import HubProjects from "./HubProjects.svelte";
  import HubReadiness, { type FixTarget } from "./HubReadiness.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { hub, connectionState } from "$lib/hub.svelte";
  import { hubURLError, joinErrorText, parseInvite } from "$lib/hubinvite";
  import { ConfigService, HubService } from "@bindings/desktop";
  import type { HubKindDTO, HubRulesDTO, SettingsDTO, TeamPolicyDTO } from "@bindings/desktop/models";

  type HubFields = Pick<SettingsDTO,
    "hubAcceptKinds" | "hubMaxClaimsPerDay" | "hubRequireSandbox" | "hubPollSeconds" | "hubApplyPolicy" | "hubGroupChores" | "hubTrustedKeys">;

  let { values, policy = null, onChange, onSaved, onIdentityChange = () => {}, onOpenTab = () => {}, onDirty = () => {} }: {
    values: HubFields;
    /** ConfigService.TeamPolicy: which settings the applied policy locks. */
    policy?: TeamPolicyDTO | null;
    /** Folds a patch into the overlay's values (used when onSaved is absent). */
    onChange: (patch: Partial<HubFields>) => void;
    /** A patch that is already written to config.toml: values AND baseline. */
    onSaved?: (patch: Partial<HubFields>) => void;
    /** Called after a join or leave, so the overlay can re-read the policy. */
    onIdentityChange?: () => void;
    /** A readiness fix that lives in another settings tab ("sandbox"). */
    onOpenTab?: (tab: string) => void;
    /** Unsaved drafts (keys, an invalid number) exist. */
    onDirty?: (dirty: boolean) => void;
  } = $props();

  const commit = (patch: Partial<HubFields>) => (onSaved ?? onChange)(patch);

  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder";
  const rowCls = "settings-field grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] items-baseline gap-x-6 gap-y-1.5";
  const rowTopCls = "settings-field grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] items-start gap-x-6 gap-y-1.5";
  const formId = $props.id();
  const clean = (e: unknown) => String(e instanceof Error ? e.message : e).replace(/^Error:\s*/, "");

  let release: (() => void) | null = null;
  onMount(() => {
    release = hub.watch();
    void hub.loadDefaultName().then((n) => {
      if (!joinName) joinPlaceholder = n;
    });
    void loadKinds();
  });
  onDestroy(() => {
    release?.();
    stopJoinTimer();
  });

  // --- join -----------------------------------------------------------------
  /** The invite as pasted. A secret: it holds the one-time token. */
  let invite = $state("");
  /** "Enter details by hand": the old URL + token fields. */
  let manual = $state(false);
  let joinURL = $state("");
  let joinToken = $state("");
  let joinName = $state("");
  let joinPlaceholder = $state("");
  let joinError = $state("");
  let joinDone = $state("");
  /** The policy key the invite carried, and whether the person trusts it. */
  let trustKey = $state(false);
  let fingerprint = $state("");
  let joinSlow = $state(false);
  let joinTimer: ReturnType<typeof setTimeout> | null = null;
  function stopJoinTimer() {
    if (joinTimer) clearTimeout(joinTimer);
    joinTimer = null;
    joinSlow = false;
  }

  const parsed = $derived(invite.trim() ? parseInvite(invite) : null);
  const inviteKey = $derived(parsed?.ok && parsed.invite.keyId ? { keyId: parsed.invite.keyId, publicKey: parsed.invite.publicKey } : null);
  const bareToken = $derived(!!parsed?.ok && !parsed.invite.url);
  const joinURLError = $derived(joinURL.trim() ? hubURLError(joinURL.trim()) : "");
  const canJoin = $derived(
    hub.busy === "" &&
      (manual ? !!joinURL.trim() && !joinURLError && !!joinToken.trim() : !!parsed?.ok && !bareToken),
  );

  // The fingerprint is computed by lola (hub.KeyFingerprint), the same one an
  // admin reads out; a stale answer for an older paste is dropped.
  $effect(() => {
    const pub = inviteKey?.publicKey ?? "";
    trustKey = false;
    fingerprint = "";
    if (!pub) return;
    let stale = false;
    ConfigService.PolicyKeyFingerprint(pub).then(
      (fp) => { if (!stale) fingerprint = fp; },
      () => { if (!stale) fingerprint = ""; },
    );
    return () => { stale = true; };
  });

  function enterByHand() {
    // A bare token pasted into the invite field carries over.
    if (bareToken && parsed?.ok) joinToken = parsed.invite.token;
    invite = "";
    manual = true;
    joinError = "";
  }

  async function join() {
    if (!canJoin) return;
    joinError = "";
    joinDone = "";
    const key = manual ? null : inviteKey;
    const trusted = trustKey;
    joinTimer = setTimeout(() => (joinSlow = true), JOIN_SLOW_MS);
    // The daemon parses the invite itself (url "" = take the invite's).
    const r = manual
      ? await hub.join(joinURL.trim(), joinToken, joinName.trim())
      : await hub.join("", invite.trim(), joinName.trim());
    stopJoinTimer();
    if (!r.ok) {
      joinError = joinErrorText(r.error);
      return;
    }
    // The token is spent: never keep it on screen or in memory.
    invite = "";
    joinToken = "";
    manual = false;
    joinDone = `Joined ${r.data.org} as ${r.data.deviceName}.`;
    justJoined = true;
    if (key && trusted) await pinInviteKey(key);
    onIdentityChange();
  }

  async function pinInviteKey(key: TrustedKeyRow) {
    try {
      const added = await ConfigService.PinPolicyKey(key.keyId, key.publicKey);
      if (added) {
        const next = [...savedKeys.filter((k) => k.keyId !== key.keyId), { ...key }];
        commit({ hubTrustedKeys: next });
      }
    } catch (e) {
      keysError = `Joined, but the policy key was not trusted: ${clean(e)}`;
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
  /** The list shows in full right after a join, then only while something is missing. */
  let justJoined = $state(false);
  /** Bumped after a project or key change, so the readiness list checks again. */
  let readyRev = $state(0);
  let projectsRev = $state(0);

  function askLeave() {
    leaveError = "";
    const org = st?.org || "this team";
    confirm.ask({
      title: "Leave the team hub?",
      body: `This Mac stops taking chores from ${org}, and the team’s settings stop applying here.`,
      detail: "Chores this Mac holds are handed back to the team, so another device can pick them up. Sessions already working on one keep running on this Mac.",
      confirmLabel: "Leave",
      choices: { label: "Device key", items: [{ id: "forget", label: "Also delete this Mac’s device key (joining again makes it a new device)", checked: false }] },
      onConfirm: (checked) => void leave(checked.includes("forget")),
    });
  }

  async function leave(forgetKey: boolean) {
    const r = await hub.leave(forgetKey);
    if (r.ok) {
      joinDone = "";
      justJoined = false;
      onIdentityChange();
    } else {
      leaveError = r.error;
    }
  }

  let section = $state<HTMLElement>();
  async function fix(target: FixTarget) {
    if (target === "sandbox") {
      onOpenTab("sandbox");
      return;
    }
    if (target === "keys" && !draftKeys.length) addKey();
    await tick();
    const el = section?.querySelector<HTMLElement>(target === "projects" ? "[data-hub-projects-group]" : "[data-hub-keys]");
    el?.scrollIntoView?.({ block: "start", behavior: "smooth" });
  }

  // --- policy ---------------------------------------------------------------
  const lockFor = (field: string) => (policy?.applied ? (policy.locked ?? []).find((l) => l.field === field) ?? null : null);
  const policyRows = $derived.by(() => {
    if (policy?.locked?.length) return policy.locked.map((l) => ({ key: l.label, value: l.ceiling ? `ceiling ${l.value}` : l.value, local: l.local }));
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

  let rulesBusy = $state(false);
  let rulesMsg = $state("");
  let rulesError = $state("");

  /** Writes one rule change at once; the overlay sees it only after it landed. */
  async function saveRules(patch: Partial<HubFields>): Promise<boolean> {
    const dto: HubRulesDTO = {
      acceptKinds: patch.hubAcceptKinds ?? null,
      maxClaimsPerDay: patch.hubMaxClaimsPerDay ?? null,
      requireSandbox: patch.hubRequireSandbox ?? null,
      pollSeconds: patch.hubPollSeconds ?? null,
      applyPolicy: patch.hubApplyPolicy ?? null,
      groupChores: patch.hubGroupChores ?? null,
      trustedKeys: patch.hubTrustedKeys ?? null,
    };
    rulesBusy = true;
    rulesMsg = "";
    rulesError = "";
    try {
      await ConfigService.SaveHubRules(dto);
      commit(patch);
      rulesMsg = "Saved";
      readyRev++;
      return true;
    } catch (e) {
      rulesError = clean(e);
      return false;
    } finally {
      rulesBusy = false;
    }
  }

  const savedClaims = $derived(values.hubMaxClaimsPerDay ?? 4);
  const savedPoll = $derived(values.hubPollSeconds ?? 300);
  // Number drafts: null = the field shows the saved value.
  let claimsDraft = $state<number | null>(null);
  let pollDraft = $state<number | null>(null);
  const claims = $derived(claimsDraft ?? savedClaims);
  const poll = $derived(pollDraft ?? savedPoll);
  const claimsError = $derived(hubClaimsError(claims));
  const pollError = $derived(hubPollError(poll));
  const claimsDirty = $derived(claimsDraft !== null && !Object.is(claimsDraft, savedClaims));
  const pollDirty = $derived(pollDraft !== null && !Object.is(pollDraft, savedPoll));

  async function commitClaims() {
    if (!claimsDirty || claimsError) return;
    const n = claims;
    if (await saveRules({ hubMaxClaimsPerDay: n })) claimsDraft = null;
  }
  async function commitPoll() {
    if (!pollDirty || pollError) return;
    const n = poll;
    if (await saveRules({ hubPollSeconds: n })) pollDraft = null;
  }

  const sandbox = $derived(values.hubRequireSandbox ?? true);
  const grouped = $derived(values.hubGroupChores ?? true);
  const applyPolicy = $derived(values.hubApplyPolicy ?? true);

  // --- trusted keys (draft + explicit save) ---------------------------------
  const savedKeys = $derived<TrustedKeyRow[]>(values.hubTrustedKeys ?? []);
  let draftKeys = $state<TrustedKeyRow[]>([]);
  let keysBusy = $state(false);
  let keysError = $state("");
  let keysMsg = $state("");
  const sameKeys = (a: TrustedKeyRow[], b: TrustedKeyRow[]) =>
    a.length === b.length && a.every((k, i) => k.keyId.trim() === b[i].keyId.trim() && k.publicKey.trim() === b[i].publicKey.trim());
  const keysDirty = $derived(!sameKeys(draftKeys, savedKeys));
  const keysValid = $derived(draftKeys.every((k) => !trustedKeyError(k, draftKeys) && (k.keyId.trim() || !k.publicKey.trim())));
  // A saved list that changes from outside (a join pinned the invite's key)
  // replaces a draft that has no edits of its own.
  // Only the saved list is a dependency: the draft is read untracked, or each
  // reset would re-run this effect.
  let lastSaved: TrustedKeyRow[] = [];
  $effect(() => {
    const saved = savedKeys.map((k) => ({ ...k }));
    untrack(() => {
      if (sameKeys(draftKeys, lastSaved) && !sameKeys(draftKeys, saved)) draftKeys = saved.map((k) => ({ ...k }));
    });
    lastSaved = saved;
  });

  function setKey(i: number, patch: Partial<TrustedKeyRow>) {
    // lola-hub prints "<key_id>:<public key>": a paste of that into the id
    // field fills both halves.
    if (patch.keyId !== undefined && patch.keyId.includes(":")) {
      const [id, pub] = patch.keyId.split(":", 2);
      patch = { keyId: id.trim(), publicKey: (pub ?? "").trim() };
    }
    draftKeys = draftKeys.map((k, j) => (j === i ? { ...k, ...patch } : k));
    keysMsg = "";
  }
  function addKey() {
    draftKeys = [...draftKeys, { keyId: "", publicKey: "" }];
    keysMsg = "";
  }
  function removeKey(i: number) {
    draftKeys = draftKeys.filter((_, j) => j !== i);
    keysMsg = "";
  }
  async function saveKeys() {
    if (!keysValid || keysBusy) return;
    const rows = draftKeys.filter((k) => k.keyId.trim() || k.publicKey.trim()).map((k) => ({ keyId: k.keyId.trim(), publicKey: k.publicKey.trim() }));
    keysBusy = true;
    keysError = "";
    try {
      await ConfigService.SaveHubRules({ acceptKinds: null, maxClaimsPerDay: null, requireSandbox: null, pollSeconds: null, applyPolicy: null, groupChores: null, trustedKeys: rows });
      commit({ hubTrustedKeys: rows });
      draftKeys = rows.map((k) => ({ ...k }));
      keysMsg = "Keys saved";
      readyRev++;
    } catch (e) {
      keysError = clean(e);
    } finally {
      keysBusy = false;
    }
  }
  function discardKeys() {
    draftKeys = savedKeys.map((k) => ({ ...k }));
    keysError = "";
  }

  const dirty = $derived(keysDirty || claimsDirty || pollDirty);
  $effect(() => onDirty(dirty));
</script>

<div class="grid min-w-0 gap-0" data-hub-settings bind:this={section}>
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
    <SettingsGroup title="Join a hub" help="Your team’s hub hands out security and dependency chores and shares the team policy. Ask your hub admin for an invite.">
      {#if hub.revoked}
        <div role="alert" class="rounded border border-bad/40 bg-bad/10 px-3 py-2 text-sm text-bad" data-hub-revoked>
          The hub no longer accepts this Mac. Ask your hub admin for a new invite and join again.
        </div>
      {/if}
      {#if !manual}
        <label class={rowTopCls}>
          <span class="text-faint">Invite</span>
          <span class="grid min-w-0 gap-1">
            <input class="{inputCls} font-mono" type="password" aria-label="Invite" placeholder="lola-hub:https://…" spellcheck="false" autocomplete="off"
              bind:value={invite} oninput={() => (joinError = "")} aria-invalid={!!parsed && !parsed.ok} />
            {#if parsed && !parsed.ok}
              <span class="text-sm text-bad">{parsed.error}</span>
            {:else if bareToken}
              <span class="text-sm text-faint">This is a token without the hub’s address. Enter the details by hand.</span>
            {:else if parsed?.ok}
              <span class="text-sm text-faint">Hub: <span class="font-mono text-ink">{parsed.invite.url}</span></span>
            {:else}
              <span class="text-sm text-faint">Paste the whole line your hub admin sent. It works once.</span>
            {/if}
          </span>
        </label>
        {#if inviteKey}
          <div class={rowTopCls} data-invite-key>
            <span class="text-faint">Team policy key</span>
            <span class="grid min-w-0 gap-1.5 rounded border border-edge p-3">
              <span class="text-sm text-ink">Key <span class="font-mono">{inviteKey.keyId}</span></span>
              <span class="text-sm text-faint">Fingerprint <span class="font-mono text-ink" data-fingerprint>{fingerprint || "…"}</span></span>
              <span class="text-sm text-faint">Check the fingerprint with your hub admin over another channel before you trust it. A trusted key decides which team settings apply here.</span>
              <label class="flex cursor-pointer items-center gap-2">
                <Checkbox bind:checked={trustKey} disabled={!fingerprint} />
                <span>Trust this key for team settings</span>
              </label>
            </span>
          </div>
        {/if}
      {:else}
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
          </span>
        </label>
      {/if}
      <label class={rowCls}>
        <span class="text-faint">Device name</span>
        <span>
          <input class={inputCls} type="text" aria-label="Device name" spellcheck="false" placeholder={joinPlaceholder || "This Mac’s name"} bind:value={joinName} />
          <span class="mt-1 block text-sm text-faint">Optional. How your team sees this Mac.</span>
        </span>
      </label>
      <div class={rowCls}>
        <span></span>
        <span class="flex flex-wrap items-center gap-2">
          <Button variant="primary" size="md" disabled={!canJoin} loading={hub.busy === "join"} onclick={join}>Join hub</Button>
          {#if !manual}
            <Button size="sm" disabled={hub.busy !== ""} onclick={enterByHand}>Enter details by hand</Button>
          {:else}
            <Button size="sm" disabled={hub.busy !== ""} onclick={() => { manual = false; joinToken = ""; joinError = ""; }}>Paste an invite instead</Button>
          {/if}
        </span>
      </div>
      {#if hub.busy === "join"}
        <p role="status" class="text-sm text-faint">
          {joinSlow ? "Still waiting for the hub. A join can take up to 90 seconds." : "Contacting the hub…"}
        </p>
      {/if}
      {#if joinError}<p role="alert" class="text-sm text-bad">{joinError}</p>{/if}
    </SettingsGroup>
  {/if}

  {#if hub.joined && st}
    {#if joinDone}<p role="status" class="mb-4 text-sm text-good">{joinDone}</p>{/if}
    <HubReadiness refresh={readyRev} hideWhenReady={!justJoined} onFix={(t) => void fix(t)}
      onProjectsChanged={() => { projectsRev++; }} />

    <SettingsGroup title="Connection">
      <div class="grid min-w-0 gap-3 rounded-lg border border-edge p-4" aria-label="Hub connection">
        <div class="flex min-w-0 flex-wrap items-center gap-3">
          <span class="min-w-0 flex-1 text-base font-medium text-ink">{st.org || "Team hub"}</span>
          <span class="inline-flex items-center rounded-full border px-2 py-0.5 text-sm {toneCls[conn.tone]}" data-connection={conn.tone}>{conn.label}</span>
        </div>
        <dl class="grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] gap-x-6 gap-y-1.5 text-sm">
          <dt class="text-faint">Device</dt><dd class="min-w-0 break-words text-ink">{st.deviceName || st.device || "—"}</dd>
          <dt class="text-faint">Hub URL</dt><dd class="min-w-0 break-all font-mono text-ink">{st.url || "—"}</dd>
          <dt class="text-faint">Claims today</dt><dd class="text-ink">{st.claimsToday} of {st.maxClaimsPerDay}</dd>
          {#if st.lastSync}<dt class="text-faint">Last sync</dt><dd class="text-ink"><TimeAgo at={st.lastSync} /></dd>{/if}
          {#if st.pendingOutcomes}<dt class="text-faint">Waiting to report</dt><dd class="text-ink">{st.pendingOutcomes}</dd>{/if}
        </dl>
        {#if st.lastError}
          <p role="alert" class="min-w-0 break-words text-sm text-bad">
            {st.lastError}{#if st.lastErrorAt} · <TimeAgo at={st.lastErrorAt} />{/if}
          </p>
        {/if}
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <Button variant="danger" size="md" loading={hub.busy === "leave"} disabled={hub.busy !== ""} onclick={askLeave}>Leave hub…</Button>
        <span class="ml-auto min-w-0"><HubSyncButton /></span>
      </div>
      {#if leaveError}<p role="alert" class="text-sm text-bad">{leaveError}</p>{/if}
    </SettingsGroup>

    <div data-hub-projects-group>
      <SettingsGroup title="Projects" help="A ticked project takes chores from the hub. A chore needs the project's GitHub repository and, while “Sandboxed only” is on, the Docker sandbox. Changes here save at once.">
        <HubProjects requireSandbox={sandbox} refresh={projectsRev} onSaved={() => { readyRev++; }} />
      </SettingsGroup>
    </div>

    <SettingsGroup title="Chores on this Mac" help="Limits for this Mac. They apply to every hub it joins. Each change saves at once.">
      {#snippet aside()}
        <span role="status" class="text-sm {rulesError ? 'text-bad' : 'text-faint'}">{rulesBusy ? "Saving…" : rulesError ? "" : rulesMsg}</span>
      {/snippet}
      {#if rulesError}<p role="alert" class="text-sm text-bad">{rulesError}</p>{/if}
      <div class={rowTopCls}>
        <span class="flex items-center gap-1 text-faint">Accepted chores <HelpText label="Accepted chores" detail="None ticked accepts every kind." /></span>
        <TeamLock lock={lockFor("hubAcceptKinds")} version={policy?.version ?? 0}>
          <CheckboxOptions label="Accepted chores" options={kinds} selected={values.hubAcceptKinds ?? []}
            onChange={(v) => void saveRules({ hubAcceptKinds: v })} />
        </TeamLock>
      </div>
      <div class={rowCls}>
        <span class="flex items-center gap-1 text-faint">Claims per day <HelpText label="Claims per day" detail="How many chores this Mac starts on its own each day. 0 pauses it; claiming by hand still works." /></span>
        <span>
          <input class="{inputCls} max-w-[8rem]" type="number" min="0" max="100" step="1" aria-label={CLAIMS_LABEL} value={claims}
            data-hub-unsaved={claimsDirty ? "" : undefined}
            oninput={(e) => (claimsDraft = e.currentTarget.valueAsNumber)}
            onchange={() => void commitClaims()}
            onkeydown={(e) => { if (e.key === "Enter") void commitClaims(); }}
            aria-invalid={!!claimsError} />
          {#if claimsError}<span class="mt-1 block text-sm text-bad">{claimsError}</span>{/if}
        </span>
      </div>
      <div class={rowCls}>
        <span class="flex items-center gap-1 text-faint">Check every <HelpText label="Check every" detail="How often the daemon asks the hub for news and renews the chores it holds. Sync now asks at once." /></span>
        <span>
          <span class="flex items-center gap-2">
            <input class="{inputCls} max-w-[8rem]" type="number" min="60" max={MAX_POLL_SECONDS} step="1" aria-label={POLL_LABEL} value={poll}
              data-hub-unsaved={pollDirty ? "" : undefined}
              oninput={(e) => (pollDraft = e.currentTarget.valueAsNumber)}
              onchange={() => void commitPoll()}
              onkeydown={(e) => { if (e.key === "Enter") void commitPoll(); }}
              aria-invalid={!!pollError} />
            <span class="text-sm text-faint">seconds</span>
          </span>
          {#if pollError}<span class="mt-1 block text-sm text-bad">{pollError}</span>{/if}
        </span>
      </div>
      <span class="flex items-center gap-1">
        <label class="flex cursor-pointer items-center gap-2">
          <Checkbox checked={grouped} disabled={rulesBusy} onchange={() => void saveRules({ hubGroupChores: !grouped })} />
          <span>Combine a repository’s issues into one session</span>
        </label>
        <HelpText label="Combine a repository’s issues into one session" detail="All open advisories of a repository go to one agent and one pull request. Off: one session per advisory." />
      </span>
      <div class="grid gap-1.5">
        <span class="flex items-center gap-1">
          <label class="flex cursor-pointer items-center gap-2">
            <Checkbox checked={sandbox} disabled={rulesBusy} onchange={() => void saveRules({ hubRequireSandbox: !sandbox })} />
            <span>Sandboxed only</span>
          </label>
          <HelpText label="Sandboxed only" detail="A chore runs only in a project with the Docker sandbox. Chores come from your team’s hub, so this keeps their agents off your Mac." />
        </span>
        {#if !sandbox}
          <p role="alert" class="flex items-start gap-1.5 text-sm text-warn">
            <Icon name="triangle-alert" size={14} />
            <span>Chore agents run directly on this Mac.</span>
          </p>
        {/if}
      </div>
    </SettingsGroup>

    <SettingsGroup title="Team policy" help="Settings your team’s admins sign. A locked setting uses the team’s value. Only keys you trust below can sign it; the hub cannot add one.">
      {#if st.policy}
        {@const p = st.policy}
        <div class="grid min-w-0 gap-3 rounded-lg border border-edge p-4" aria-label="Policy details">
          <div class="flex min-w-0 flex-wrap items-center gap-2 text-sm">
            <span class="inline-flex items-center gap-1.5 {p.applied ? 'text-good' : 'text-warn'}">
              <Icon name={p.applied ? "shield-check" : "circle-alert"} size={14} />
              {p.applied ? "Applied" : "Not applied"}
            </span>
            <span class="text-faint">· v{p.version}</span>
            {#if p.issuedAt}<span class="text-faint">· <TimeAgo at={p.issuedAt} /></span>{/if}
            <span class="ml-auto truncate font-mono text-faint" title="Signed by">{p.keyId || ""}</span>
          </div>
          {#if !p.applied && p.applyNote}<p class="text-sm text-warn">{p.applyNote}</p>{/if}
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
          {#if policy?.escalateAfter}
            <p class="text-sm text-ink-2" data-escalate-after>
              The hub hands a chore to a person in Linear after {policy.escalateAfter} failed {policy.escalateAfter === 1 ? "attempt" : "attempts"}.
            </p>
          {/if}
        </div>
      {:else}
        <div class="grid justify-items-center gap-2 rounded-lg border border-dashed border-edge px-4 py-7 text-center" data-policy-empty>
          <span class="text-faint"><Icon name="shield-check" size={22} /></span>
          <p class="text-ink">No team policy</p>
          <p class="text-sm text-faint">{savedKeys.length ? "Your admins have not published one yet." : "Add your admin’s key to verify one."}</p>
          {#if !draftKeys.length}
            <Button variant="secondary" size="sm" onclick={addKey}><Icon name="plus" size={14} />Add key</Button>
          {/if}
        </div>
      {/if}
      {#if st.policy}
        <div class="grid gap-1.5">
          <label class="flex cursor-pointer items-center gap-2">
            <Checkbox checked={applyPolicy} disabled={rulesBusy} onchange={() => void saveRules({ hubApplyPolicy: !applyPolicy })} />
            <span>Apply the team policy</span>
            <HelpText label="Apply the team policy" detail="Off shows the policy without enforcing it on this Mac." />
          </label>
          {#if !applyPolicy}
            <p class="flex items-start gap-1.5 text-sm text-warn" data-policy-optout>
              <Icon name="triangle-alert" size={14} />
              <span>A local opt-out. This Mac still reports which policy version it applies, so your team can see it does not follow the policy.</span>
            </p>
          {/if}
        </div>
      {/if}
      {#if draftKeys.length || savedKeys.length || st.policy}
        <div class="grid min-w-0 gap-2" data-hub-keys>
          <span class="flex items-center gap-1 text-faint">Trusted keys
            <HelpText label="Trusted keys" detail="Paste “key id:public key” as your hub admin prints it into the key id field to fill both." />
          </span>
          {#each draftKeys as row, i (i)}
            {@const err = trustedKeyError(row, draftKeys)}
            <div class="grid min-w-0 gap-1.5" data-trusted-key={i}>
              <div class="grid min-w-0 grid-cols-[10rem_minmax(0,1fr)_auto] items-center gap-2">
                <input class="{inputCls} font-mono" aria-label={`Key id ${i + 1}`} placeholder="Key id" spellcheck="false" value={row.keyId}
                  oninput={(e) => setKey(i, { keyId: e.currentTarget.value })} aria-invalid={!!err} />
                <input class="{inputCls} font-mono" aria-label={`Public key ${i + 1}`} placeholder="Public key" spellcheck="false" value={row.publicKey}
                  oninput={(e) => setKey(i, { publicKey: e.currentTarget.value })} aria-invalid={!!err} />
                <Button size="sm" icon onclick={() => removeKey(i)} aria-label={`Remove key ${i + 1}`}><Icon name="x" size={14} /></Button>
              </div>
              {#if err}<p class="text-sm text-bad">{err}</p>{/if}
            </div>
          {/each}
          <div class="flex flex-wrap items-center gap-2">
            <Button size="sm" onclick={addKey} id={`${formId}-add-key`}><Icon name="plus" size={14} />Add key</Button>
            {#if keysDirty}
              <Button size="sm" variant="primary" disabled={!keysValid} loading={keysBusy} onclick={saveKeys} data-hub-unsaved="">Save keys</Button>
              <Button size="sm" disabled={keysBusy} onclick={discardKeys}>Discard</Button>
              <span role="status" class="text-sm text-warn">Keys not saved yet</span>
            {:else if keysMsg}
              <span role="status" class="text-sm text-good">{keysMsg}</span>
            {/if}
          </div>
          {#if keysError}<p role="alert" class="text-sm text-bad">{keysError}</p>{/if}
        </div>
      {/if}
    </SettingsGroup>
  {/if}
</div>
