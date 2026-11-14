<script lang="ts">
  // "Team admin": what the holder of the team's admin key sets for the whole
  // team. Shown only while this Mac holds that key (hub.admin); the hub checks
  // every request again and its refusal comes back in plain words.
  //
  //   - INVITE a teammate. The hub mints the token; the daemon builds the
  //     invite with this Mac's hub URL and admin key. The invite is a secret:
  //     it is shown once here, kept in no store, and gone when this closes.
  //   - LINEAR key and DIGEST webhook are WRITE-ONLY. A pasted value lives in
  //     its field until it is sent and is cleared the moment the daemon
  //     answers. Nothing ever comes back but "set" and the last error class.
  //   - The Linear TEAM is picked from the list the hub read from Linear with
  //     the stored key; the names in it were validated before they got here.
  //
  // Every action saves at once, like the rest of the Team hub tab.
  import { onMount } from "svelte";
  import Button from "$lib/components/Button.svelte";
  import Select from "$lib/components/Select.svelte";
  import SettingsGroup from "$lib/components/SettingsGroup.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { hub } from "$lib/hub.svelte";
  import { INVITE_LIFETIMES, digestWebhookError, inviteLabelError, linearKeyError, statusClassText, teamErrorText } from "$lib/hubteam";
  import type { HubInviteData, HubLinearTeam, HubTeamSettingsData } from "@bindings/internal/protocol";

  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder";
  const rowCls = "settings-field grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] items-baseline gap-x-6 gap-y-1.5";
  const rowTopCls = "settings-field grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] items-start gap-x-6 gap-y-1.5";

  // --- settings status --------------------------------------------------------
  let view = $state<HubTeamSettingsData | null>(null);
  let loadError = $state("");
  /** The workspace's teams, kept across answers that carry none. */
  let teams = $state<HubLinearTeam[]>([]);

  function apply(d: HubTeamSettingsData) {
    view = d;
    if (d.teams?.length) teams = d.teams;
    if (!d.linearKeySet) teams = [];
  }

  async function load() {
    loadError = "";
    const r = await hub.readTeamSettings(true);
    if (r.ok) {
      apply(r.data);
      return;
    }
    // The list needs Linear; the status alone does not.
    const plain = await hub.readTeamSettings(false);
    if (plain.ok) apply(plain.data);
    else loadError = teamErrorText(plain.error);
  }
  onMount(() => void load());

  // --- invite -----------------------------------------------------------------
  let label = $state("");
  let lifetime = $state(INVITE_LIFETIMES[1].value);
  let inviteError = $state("");
  /** The invite just minted. A secret: shown once, cleared on "Done". */
  let minted = $state<(HubInviteData & { label: string }) | null>(null);
  let copied = $state(false);
  const labelError = $derived(inviteLabelError(label));
  const expiryText = (at: string) => new Date(at).toLocaleDateString(undefined, { day: "numeric", month: "long", year: "numeric" });

  async function invite() {
    if (!label.trim() || labelError) return;
    inviteError = "";
    minted = null;
    const who = label.trim();
    const r = await hub.invite(who, Number(lifetime));
    if (!r.ok) {
      inviteError = teamErrorText(r.error);
      return;
    }
    minted = { ...r.data, label: who };
    label = "";
    copied = false;
  }

  async function copyInvite() {
    if (!minted) return;
    try {
      await navigator.clipboard.writeText(minted.invite);
      copied = true;
    } catch {
      copied = false;
    }
  }

  // --- Linear -----------------------------------------------------------------
  let linearKey = $state("");
  let linearError = $state("");
  let linearMsg = $state("");
  const keyError = $derived(linearKeyError(linearKey));

  async function saveLinearKey() {
    if (!linearKey.trim() || keyError) return;
    linearError = "";
    linearMsg = "";
    const r = await hub.teamSettings({ linearKey: linearKey.trim() }, "linear");
    // Sent or refused, the key does not stay in the field.
    linearKey = "";
    if (!r.ok) {
      linearError = teamErrorText(r.error);
      return;
    }
    apply(r.data);
    linearMsg = "Linear accepted the key.";
  }

  function askClearLinear() {
    confirm.ask({
      title: "Remove the Linear key?",
      body: "The hub stops opening Linear issues for chores nobody could finish.",
      detail: "Chores waiting to be escalated stay waiting until a key is set again.",
      confirmLabel: "Remove key",
      onConfirm: () => void clearLinear(),
    });
  }
  async function clearLinear() {
    linearError = "";
    linearMsg = "";
    const r = await hub.teamSettings({ linearKey: "" }, "linear");
    if (r.ok) apply(r.data);
    else linearError = teamErrorText(r.error);
  }

  async function pickTeam(id: string) {
    if (!view || id === (view.linearTeam ?? "")) return;
    linearError = "";
    linearMsg = "";
    const r = await hub.teamSettings({ linearTeam: id }, "linear");
    if (!r.ok) {
      linearError = teamErrorText(r.error);
      return;
    }
    apply(r.data);
    linearMsg = id ? "Team saved." : "";
  }

  const teamLabel = (t: HubLinearTeam) => (t.name ? `${t.name} (${t.key})` : t.key);
  /** A stored team the list does not name still shows, by its id. */
  const unknownTeam = $derived(!!view?.linearTeam && !teams.some((t) => t.id === view?.linearTeam));

  // --- digest -----------------------------------------------------------------
  let hook = $state("");
  let digestError = $state("");
  let digestMsg = $state("");
  const hookError = $derived(digestWebhookError(hook));

  async function saveDigest() {
    if (!hook.trim() || hookError) return;
    digestError = "";
    digestMsg = "";
    const r = await hub.teamSettings({ digestWebhook: hook.trim() }, "digest");
    hook = "";
    if (!r.ok) {
      digestError = teamErrorText(r.error);
      return;
    }
    apply(r.data);
    digestMsg = "Webhook saved. The next daily digest goes there.";
  }
  async function clearDigest() {
    digestError = "";
    digestMsg = "";
    const r = await hub.teamSettings({ digestWebhook: "" }, "digest");
    if (r.ok) apply(r.data);
    else digestError = teamErrorText(r.error);
  }

  const linearBusy = $derived(hub.isBusy("team:linear"));
  const digestBusy = $derived(hub.isBusy("team:digest"));
</script>

<SettingsGroup title="Team admin" help="This Mac holds the team’s admin key, so it can invite people and set where the team’s escalations and daily digest go. These apply to the whole team.">
  {#if loadError}
    <div role="alert" class="flex min-w-0 flex-wrap items-center gap-3 rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn" data-admin-error>
      <span class="min-w-0 flex-1">{loadError}</span>
      <Button size="sm" variant="secondary" onclick={() => void load()}>Try again</Button>
    </div>
  {/if}

  <!-- Invite -->
  <div class={rowTopCls} data-admin-invite>
    <span class="text-faint">Invite a teammate</span>
    <span class="grid min-w-0 gap-2">
      <span class="flex min-w-0 flex-wrap items-start gap-2">
        <input class="{inputCls} min-w-40 flex-1" type="text" aria-label="Teammate’s name" placeholder="Teammate’s name" spellcheck="false" autocomplete="off"
          bind:value={label} oninput={() => (inviteError = "")} aria-invalid={!!labelError}
          onkeydown={(e) => { if (e.key === "Enter") void invite(); }} />
        <Select aria-label="Invite lifetime" bind:value={lifetime}>
          {#each INVITE_LIFETIMES as l (l.value)}<option value={l.value}>Valid {l.label}</option>{/each}
        </Select>
        <Button variant="secondary" size="md" disabled={!label.trim() || !!labelError || hub.isBusy("invite")} loading={hub.isBusy("invite")} onclick={invite}>Create invite</Button>
      </span>
      {#if labelError}<span class="text-sm text-bad">{labelError}</span>{/if}
      {#if inviteError}<span role="alert" class="text-sm text-bad">{inviteError}</span>{/if}
      {#if minted}
        <span class="grid min-w-0 gap-2 rounded border border-edge p-3" data-invite-minted>
          <span class="text-sm text-ink">Invite for {minted.label}. It works once and expires on {expiryText(minted.expiresAt)}.</span>
          <span class="flex min-w-0 items-center gap-2">
            <input class="{inputCls} font-mono" type="text" readonly aria-label="Invite" value={minted.invite} onfocus={(e) => e.currentTarget.select()} />
            <Button size="md" variant="secondary" onclick={copyInvite}>{copied ? "Copied" : "Copy"}</Button>
          </span>
          <span class="text-sm text-faint">
            Send it over a private channel: whoever has it can join. It carries the team’s policy key
            <span class="font-mono text-ink">{minted.keyId}</span>; tell your teammate its fingerprint
            <span class="font-mono text-ink" data-fingerprint>{minted.fingerprint}</span> another way, so they can check it.
          </span>
          <span><Button size="sm" onclick={() => { minted = null; copied = false; }}>Done</Button></span>
        </span>
      {/if}
    </span>
  </div>

  {#if view && !view.secretsAvailable}
    <p role="status" class="rounded border border-warn/40 bg-warn/10 px-3 py-2 text-sm text-warn" data-no-secrets>
      This hub cannot store a Linear key or a webhook yet. Ask the hub operator to set its secret key.
    </p>
  {/if}

  <!-- Linear -->
  <div class={rowTopCls} data-admin-linear>
    <span class="text-faint">Linear</span>
    <span class="grid min-w-0 gap-2">
      <span class="text-sm text-ink" data-linear-state>
        {#if !view}Checking…{:else if view.linearKeySet}An API key is set. It is never shown again.{:else}No API key yet. Without one, chores nobody could finish are not escalated.{/if}
      </span>
      {#if view?.linearError}<span role="alert" class="text-sm text-warn">{statusClassText(view.linearError)}</span>{/if}
      <span class="flex min-w-0 flex-wrap items-start gap-2">
        <input class="{inputCls} min-w-40 flex-1 font-mono" type="password" aria-label="Linear API key" placeholder="lin_api_…" spellcheck="false" autocomplete="off"
          bind:value={linearKey} oninput={() => { linearError = ""; linearMsg = ""; }} aria-invalid={!!keyError}
          disabled={!view?.secretsAvailable} />
        <Button variant="secondary" size="md" disabled={!linearKey.trim() || !!keyError || linearBusy || !view?.secretsAvailable} loading={linearBusy && !!linearKey} onclick={saveLinearKey}>
          {view?.linearKeySet ? "Replace key" : "Save key"}
        </Button>
        {#if view?.linearKeySet}<Button variant="danger" size="md" disabled={linearBusy} onclick={askClearLinear}>Remove</Button>{/if}
      </span>
      {#if keyError}<span class="text-sm text-bad">{keyError}</span>{/if}
      <span class="text-sm text-faint">The hub checks the key with Linear before it stores it. Use a key that may only create and close issues.</span>
      {#if view?.linearKeySet}
        <span class="flex min-w-0 flex-wrap items-center gap-2">
          <span class="text-sm text-faint">Team</span>
          {#if teams.length || view.linearTeam}
            <Select aria-label="Linear team" value={view.linearTeam ?? ""} disabled={linearBusy} onchange={(e) => void pickTeam(e.currentTarget.value)}>
              <option value="">Choose a team</option>
              {#if unknownTeam}<option value={view.linearTeam}>{view.linearTeam}</option>{/if}
              {#each teams as t (t.id)}<option value={t.id}>{teamLabel(t)}</option>{/each}
            </Select>
          {:else}
            <span class="text-sm text-faint">Linear’s teams could not be listed.</span>
          {/if}
          <Button size="sm" disabled={linearBusy} onclick={() => void load()}>Reload teams</Button>
        </span>
        {#if !view.linearTeam}<span class="text-sm text-warn">Pick the team escalations go to.</span>{/if}
      {/if}
      {#if linearMsg}<span role="status" class="text-sm text-good">{linearMsg}</span>{/if}
      {#if linearError}<span role="alert" class="text-sm text-bad">{linearError}</span>{/if}
    </span>
  </div>

  <!-- Digest -->
  <div class={rowTopCls} data-admin-digest>
    <span class="text-faint">Daily digest</span>
    <span class="grid min-w-0 gap-2">
      <span class="text-sm text-ink" data-digest-state>
        {#if !view}Checking…{:else if view.digestSet}A Slack webhook is set. It is never shown again.{:else}No webhook. The team gets no daily digest.{/if}
      </span>
      {#if view?.digestError}<span role="alert" class="text-sm text-warn">{statusClassText(view.digestError)}</span>{/if}
      <span class="flex min-w-0 flex-wrap items-start gap-2">
        <input class="{inputCls} min-w-40 flex-1 font-mono" type="password" aria-label="Slack webhook" placeholder="https://hooks.slack.com/services/…" spellcheck="false" autocomplete="off"
          bind:value={hook} oninput={() => { digestError = ""; digestMsg = ""; }} aria-invalid={!!hookError}
          disabled={!view?.secretsAvailable} />
        <Button variant="secondary" size="md" disabled={!hook.trim() || !!hookError || digestBusy || !view?.secretsAvailable} loading={digestBusy && !!hook} onclick={saveDigest}>
          {view?.digestSet ? "Replace webhook" : "Save webhook"}
        </Button>
        {#if view?.digestSet}<Button variant="danger" size="md" disabled={digestBusy} onclick={clearDigest}>Remove</Button>{/if}
      </span>
      {#if hookError}<span class="text-sm text-bad">{hookError}</span>{/if}
      {#if digestMsg}<span role="status" class="text-sm text-good">{digestMsg}</span>{/if}
      {#if digestError}<span role="alert" class="text-sm text-bad">{digestError}</span>{/if}
    </span>
  </div>

  <p class={rowCls}>
    <span></span>
    <span class="text-sm text-faint">To remove a Mac from the team, open Team and use Remove beside it.</span>
  </p>
</SettingsGroup>
