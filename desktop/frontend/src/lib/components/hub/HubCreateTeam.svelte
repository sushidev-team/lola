<script lang="ts">
  // "Create a team": the second way into a hub, beside joining with an invite.
  //
  //   - The hub operator hands out ONE line, lola-hub-create:<url>#<code>. It
  //     names the hub, so there is no URL field. It is a secret and works
  //     once: it lives only in this component's field and is cleared the
  //     moment the daemon answers.
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
  import { parseCreateCode, slugTeamName, teamErrorText, teamNameError } from "$lib/hubteam";
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

  /** The create code as pasted. A secret: it works once. */
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

  const parsed = $derived(code.trim() ? parseCreateCode(code) : null);
  const nameError = $derived(teamNameError(team));
  const canCreate = $derived(hub.busy === "" && !!parsed?.ok && !!team && !nameError);

  async function create(replaceAdminKey = false) {
    if (!canCreate) return;
    error = "";
    timer = setTimeout(() => (slow = true), 10_000);
    const r = await hub.createTeam(code.trim(), team, name.trim(), replaceAdminKey);
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
      error = teamErrorText(r.error);
      return;
    }
    // The code is spent: never keep it on screen or in memory.
    code = "";
    team = "";
    onCreated(r.data);
  }
</script>

<SettingsGroup title="Create a team" help="Start a new team on a hub. The hub operator gives you a create code; this Mac becomes the team’s first member and its admin.">
  <label class={rowTopCls}>
    <span class="text-faint">Create code</span>
    <span class="grid min-w-0 gap-1">
      <input class="{inputCls} font-mono" type="password" aria-label="Create code" placeholder="lola-hub-create:https://…" spellcheck="false" autocomplete="off"
        bind:value={code} oninput={() => (error = "")} aria-invalid={!!parsed && !parsed.ok} />
      {#if parsed && !parsed.ok}
        <span class="text-sm text-bad">{parsed.error}</span>
      {:else if parsed?.ok}
        <span class="text-sm text-faint">Hub: <span class="font-mono text-ink">{parsed.url}</span></span>
      {:else}
        <span class="text-sm text-faint">Paste the whole line your hub operator sent. It works once.</span>
      {/if}
    </span>
  </label>
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
  {#if error}<p role="alert" class="text-sm text-bad">{error}</p>{/if}
</SettingsGroup>
