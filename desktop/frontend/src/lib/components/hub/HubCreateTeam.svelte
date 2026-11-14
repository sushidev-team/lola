<script lang="ts">
  // "Create a team": the second way into a hub, beside joining with an invite.
  // Two steps:
  //
  //   1. The hub's address. The daemon asks the hub whether a team can be
  //      created there without a create code (an unsigned question that says
  //      nothing about this Mac).
  //   2. The team: its name and this Mac's name. A hub that is not open asks
  //      for a create code here too; an open hub does not.
  //
  //   - A create code is a secret and works once: it lives only in this
  //     component's field and is cleared the moment the daemon answers. A
  //     whole create line (lola-hub-create:<url>#<code>) pasted as the address
  //     names the hub AND carries the code, so nothing more is asked.
  //   - What the hub said in step 1 is a hint. The hub decides again when the
  //     team is created: if it then wants a code, the code field appears.
  //   - The daemon generates the team's admin key on this Mac, registers it
  //     with the hub and pins it for team settings. The key never reaches this
  //     page: only its id and fingerprint come back, for the person to read to
  //     the teammates they invite.
  //   - A Mac that already holds ANOTHER team's admin key is asked first:
  //     creating a team discards that key.
  import { onDestroy } from "svelte";
  import Button from "$lib/components/Button.svelte";
  import SettingsGroup from "$lib/components/SettingsGroup.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { hub } from "$lib/hub.svelte";
  import { createCodeFor, parseHubAddress, slugTeamName, teamErrorText, teamNameError } from "$lib/hubteam";
  import type { HubCreateTeamData } from "@bindings/internal/protocol";

  let { placeholderName = "", onCreated = () => {} }: {
    /** This Mac's default device name. */
    placeholderName?: string;
    /** Called once the team exists and this Mac is in it. */
    onCreated?: (d: HubCreateTeamData) => void;
  } = $props();

  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder";
  const rowCls = "settings-field grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] items-baseline gap-x-6 gap-y-1.5";
  const rowTopCls = "settings-field grid min-w-0 grid-cols-[10rem_minmax(0,1fr)] items-start gap-x-6 gap-y-1.5";

  /** Step 1: what the person typed as the hub's address. */
  let address = $state("");
  /** Step 2 is open once the hub answered: its address as lola will use it. */
  let hubURL = $state("");
  /** The hub wants a create code. */
  let needCode = $state(false);
  /** A whole create line pasted as the address: the code is already in hand. A secret. */
  let line = $state("");
  /** The create code as pasted in step 2. A secret: it works once. */
  let code = $state("");
  let team = $state("");
  let name = $state("");
  let error = $state("");
  let slow = $state(false);
  let timer: ReturnType<typeof setTimeout> | null = null;
  function stopTimer() {
    if (timer) clearTimeout(timer);
    timer = null;
    slow = false;
  }
  onDestroy(stopTimer);

  const parsedAddress = $derived(address.trim() ? parseHubAddress(address) : null);
  const codeArg = $derived(needCode && code.trim() ? createCodeFor(hubURL, code) : null);
  const nameError = $derived(teamNameError(team));
  const canCreate = $derived(hub.busy === "" && !!hubURL && !!team && !nameError && (!needCode || !!codeArg?.ok));

  /** Step 1: ask the hub whether it wants a create code. */
  async function next() {
    const p = parseHubAddress(address);
    if (!p.ok || hub.busy !== "") return;
    error = "";
    if (p.line) {
      // The line names the hub and carries the code: nothing to ask the hub.
      line = p.line;
      needCode = false;
      hubURL = p.url;
      address = "";
      return;
    }
    const r = await hub.probeHub(p.url);
    if (!r.ok) {
      error = teamErrorText(r.error);
      return;
    }
    needCode = !r.data.openTeams;
    hubURL = r.data.url || p.url;
  }

  /** Back to step 1. The secrets go. */
  function change() {
    address = line ? "" : hubURL;
    hubURL = "";
    line = "";
    code = "";
    needCode = false;
    error = "";
  }

  async function create(replaceAdminKey = false) {
    if (!canCreate) return;
    error = "";
    const arg = line || (needCode && codeArg?.ok ? codeArg.code : hubURL);
    timer = setTimeout(() => (slow = true), 10_000);
    const r = await hub.createTeam(arg, team, name.trim(), replaceAdminKey);
    stopTimer();
    if (!r.ok) {
      if (!replaceAdminKey && /already holds a team admin key/i.test(r.error)) {
        confirm.ask({
          title: "Discard the other team’s admin key?",
          body: "This Mac holds the admin key of another team. Creating a team replaces it.",
          detail: "Without that key this Mac can no longer invite people to the other team or change its settings. Only the hub operator can register a new one.",
          confirmLabel: "Discard and create",
          onConfirm: () => void create(true),
        });
        return;
      }
      // The hub decides when the team is created: it may want a code after all.
      if (!needCode && !line && /needs a create code|as many teams as it allows/i.test(r.error)) needCode = true;
      error = teamErrorText(r.error);
      return;
    }
    // The code is spent: never keep it on screen or in memory.
    code = "";
    line = "";
    team = "";
    hubURL = "";
    address = "";
    needCode = false;
    onCreated(r.data);
  }
</script>

<SettingsGroup title="Create a team" help="Start a new team on a hub. This Mac becomes the team’s first member and its admin.">
  {#if !hubURL}
    <label class={rowTopCls}>
      <span class="text-faint">Hub address</span>
      <span class="grid min-w-0 gap-1">
        <input class="{inputCls} font-mono" type={address.trim().startsWith("lola-hub") ? "password" : "text"} aria-label="Hub address" placeholder="https://hub.example.com" spellcheck="false" autocomplete="off"
          bind:value={address} oninput={() => (error = "")} onkeydown={(e) => { if (e.key === "Enter") void next(); }}
          aria-invalid={!!parsedAddress && !parsedAddress.ok} />
        {#if parsedAddress && !parsedAddress.ok}
          <span class="text-sm text-bad">{parsedAddress.error}</span>
        {:else}
          <span class="text-sm text-faint">Your hub operator knows it. A create code line (lola-hub-create:…) works here too.</span>
        {/if}
      </span>
    </label>
    <div class={rowCls}>
      <span></span>
      <span class="flex flex-wrap items-center gap-2">
        <Button variant="primary" size="md" disabled={!parsedAddress?.ok || hub.busy !== ""} loading={hub.isBusy("probe")} onclick={next}>Continue</Button>
      </span>
    </div>
    {#if hub.isBusy("probe")}<p role="status" class="text-sm text-faint">Asking the hub…</p>{/if}
  {:else}
    <div class={rowCls}>
      <span class="text-faint">Hub</span>
      <span class="flex min-w-0 flex-wrap items-center gap-2">
        <span class="min-w-0 truncate font-mono text-ink" data-create-hub>{hubURL}</span>
        <Button size="sm" variant="secondary" disabled={hub.busy !== ""} onclick={change}>Change</Button>
      </span>
    </div>
    {#if needCode}
      <label class={rowTopCls}>
        <span class="text-faint">Create code</span>
        <span class="grid min-w-0 gap-1">
          <input class="{inputCls} font-mono" type="password" aria-label="Create code" spellcheck="false" autocomplete="off"
            bind:value={code} oninput={() => (error = "")} aria-invalid={!!codeArg && !codeArg.ok} />
          {#if codeArg && !codeArg.ok}
            <span class="text-sm text-bad">{codeArg.error}</span>
          {:else}
            <span class="text-sm text-faint">This hub needs a create code from its operator. It works once.</span>
          {/if}
        </span>
      </label>
    {/if}
    <label class={rowTopCls}>
      <span class="text-faint">Team name</span>
      <span class="grid min-w-0 gap-1">
        <input class={inputCls} type="text" aria-label="Team name" placeholder="acme" spellcheck="false" autocomplete="off"
          value={team} oninput={(e) => { team = slugTeamName(e.currentTarget.value); e.currentTarget.value = team; error = ""; }} aria-invalid={!!nameError} />
        {#if nameError}
          <span class="text-sm text-bad">{nameError}</span>
        {:else}
          <span class="text-sm text-faint">Lowercase letters, digits and dashes. It cannot be changed later.</span>
        {/if}
      </span>
    </label>
    <label class={rowCls}>
      <span class="text-faint">Device name</span>
      <span>
        <input class={inputCls} type="text" aria-label="Device name for the new team" spellcheck="false" placeholder={placeholderName || "This Mac’s name"} bind:value={name} />
        <span class="mt-1 block text-sm text-faint">Optional. How your team sees this Mac.</span>
      </span>
    </label>
    <div class={rowCls}>
      <span></span>
      <span class="flex flex-wrap items-center gap-2">
        <Button variant="primary" size="md" disabled={!canCreate} loading={hub.busy === "create"} onclick={() => create()}>Create team</Button>
      </span>
    </div>
    {#if hub.busy === "create"}
      <p role="status" class="text-sm text-faint">
        {slow ? "Still waiting for the hub. This can take up to 90 seconds." : "Contacting the hub…"}
      </p>
    {/if}
  {/if}
  {#if error}<p role="alert" class="text-sm text-bad">{error}</p>{/if}
</SettingsGroup>
