<script lang="ts">
  // Starting a session on a host. Host is a radio list, not a Select, because tier
  // has to be visible before a pick. Starting it IS the go for connecting, and command
  // approval still applies; the approval card carries the warnings when it matters.
  import { onMount, tick } from "svelte";
  import { SSHService } from "@bindings/desktop";
  import { overlayClose } from "$lib/overlayClose";
  import { nav } from "$lib/nav.svelte";
  import { store } from "$lib/store.svelte";
  import { ssh, PREDATES_RE } from "$lib/ssh.svelte";
  import { terms, SSH } from "$lib/terms.svelte";
  import {
    SSH_UNSANDBOXED_NOTE,
    agentName,
    hostRows,
    initialHost,
  } from "$lib/ssh";
  import Modal from "../Modal.svelte";
  import Button from "../Button.svelte";
  import Select from "../Select.svelte";
  import SSHTierTag from "./SSHTierTag.svelte";

  const field = "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none placeholder:text-placeholder focus:border-accent focus-visible:outline-none!";

  const rows = $derived(hostRows(ssh.hosts, store.sessions));
  let host = $state("");
  let brief = $state("");
  let agentKind = $state("");
  let name = $state("");
  let busy = $state(false);
  let error = $state("");
  let briefEl = $state<HTMLTextAreaElement | null>(null);
  const picked = $derived(rows.find((r) => r.host.name === host));
  const agentLabel = $derived(agentName(agentKind || picked?.host.directAgent));
  const unsandboxed = $derived(!!picked && !picked.refused && picked.host.directSandbox !== "docker");
  const predates = $derived(PREDATES_RE.test(ssh.hostsError));

  function close() {
    if (!busy) nav.closeOverlay();
  }

  onMount(() => {
    overlayClose.register(close);
    void ssh.loadHosts().then(async () => {
      host = initialHost(rows, ssh.newHost);
      await tick();
      briefEl?.focus();
    });
    return () => overlayClose.unregister(close);
  });

  async function connect() {
    if (!picked || picked.refused || busy) return;
    busy = true;
    error = "";
    try {
      const r = await SSHService.Open({
        host,
        agentKind: agentKind || undefined,
        name: name.trim() || undefined,
        brief: brief.trim() || undefined,
      });
      await store.refresh();
      busy = false;
      nav.closeOverlay();
      await tick();
      land(r.session, r.conn.id);
    } catch (err) {
      busy = false;
      error = String(err);
    }
  }

  function land(id: string, conn = "") {
    nav.setTriage("");
    nav.goCockpit("");
    terms.select(id, SSH);
    if (nav.focusedTerm !== id) nav.openSession(id);
    ssh.open(id, { conn });
    if (conn) void ssh.view(id).fetch();
  }

  function hostKey(e: KeyboardEvent) {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const usable = rows.filter((r) => !r.refused);
    if (!usable.length) return;
    const i = usable.findIndex((r) => r.host.name === host);
    const next = usable[(i + (e.key === "ArrowDown" ? 1 : -1) + usable.length) % usable.length];
    host = next.host.name;
    void tick().then(() => document.getElementById(`ssh-host-${next.host.name}`)?.focus());
  }
</script>

<Modal title="New SSH session" onClose={close} width="640px">
  {#if predates}
    <div class="grid justify-items-start gap-2 py-2">
      <p class="copy text-ink">This daemon predates SSH sessions. Restart it to use them.</p>
      <Button variant="secondary" size="sm" onclick={() => ssh.restartDaemon()}>Restart daemon</Button>
    </div>
  {:else if ssh.hostsLoaded && rows.length === 0}
    <div class="grid justify-items-start gap-2 py-2">
      <p class="copy"><span class="font-medium text-ink">No hosts yet.</span> <span class="text-ink-2">Add one in Settings, using an alias from your ssh config. Lola holds no address and no key.</span></p>
      <Button variant="secondary" size="sm" onclick={() => nav.openOverlay("settings", "", "sshhosts")}>Open SSH host settings</Button>
    </div>
  {:else}
    <form class="grid gap-4" onsubmit={(e) => { e.preventDefault(); void connect(); }}>
      <div class="grid gap-1.5">
        <span class="text-sm text-faint" id="ssh-host-label">Host</span>
        <!-- svelte-ignore a11y_interactive_supports_focus -->
        <div class="grid gap-0.5" role="radiogroup" aria-labelledby="ssh-host-label" onkeydown={hostKey}>
          {#each rows as r (r.host.name)}
            <!-- A card-shaped row. Disabled when refused, with the daemon's reason whole. -->
            <button
              type="button"
              id="ssh-host-{r.host.name}"
              role="radio"
              aria-checked={host === r.host.name}
              disabled={r.refused}
              tabindex={host === r.host.name ? 0 : -1}
              class="grid grid-cols-[14px_auto_minmax(0,1fr)_auto] items-center gap-2 rounded-md border px-2 py-1.5 text-left transition-colors disabled:cursor-not-allowed {host === r.host.name
                ? 'border-accent bg-sel/40'
                : 'border-transparent enabled:hover:bg-sel/40'} {r.refused ? 'opacity-60' : ''}"
              onclick={() => (host = r.host.name)}
            >
              <span class="grid h-3 w-3 place-items-center rounded-full border {host === r.host.name ? 'border-accent' : 'border-edge'}" aria-hidden="true">
                {#if host === r.host.name}<span class="h-1.5 w-1.5 rounded-full bg-accent"></span>{/if}
              </span>
              <SSHTierTag tier={r.host.tier} />
              <span class="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] items-baseline gap-2">
                <span class="truncate text-ink">{r.name}</span>
                <span class="truncate font-mono text-sm text-faint">{r.who}</span>
              </span>
              <span class="text-sm whitespace-nowrap {r.noteCls}">{r.note}</span>
            </button>
            {#if r.refused}<p class="ml-6 text-sm text-faint">{r.host.directRefused}</p>{/if}
          {/each}
        </div>
      </div>

      <label class="grid gap-1.5">
        <span class="text-sm text-faint">What should {agentLabel} look at?</span>
        <textarea
          bind:this={briefEl}
          bind:value={brief}
          rows="3"
          class="{field} resize-none"
          placeholder="API restarts every 40 minutes since this morning's deploy…"
          disabled={busy}
          onkeydown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              void connect();
            }
          }}
        ></textarea>
        <span class="text-sm text-faint">Optional. Without it, {agentLabel} waits for you.</span>
      </label>

      <div class="grid gap-3 sm:grid-cols-2">
        <label class="grid gap-1.5">
          <span class="text-sm text-faint">Agent</span>
          <Select bind:value={agentKind} disabled={busy}>
            <option value="">{agentName(picked?.host.directAgent)} (host default)</option>
            <option value="claude">Claude</option>
            <option value="codex">Codex</option>
            <option value="opencode">OpenCode</option>
          </Select>
        </label>
        <label class="grid gap-1.5">
          <span class="text-sm text-faint">Name</span>
          <input class={field} bind:value={name} maxlength="100" placeholder="optional" disabled={busy} />
        </label>
      </div>

      {#if unsandboxed}<p class="text-sm text-warn" title={SSH_UNSANDBOXED_NOTE} data-unsandboxed>{agentLabel} runs unsandboxed on this Mac.</p>{/if}

      {#if error}<p class="text-sm text-bad" role="alert">{error}</p>{/if}

      <div class="dialog-actions">
        <Button class="cancel" size="md" onclick={close} disabled={busy}>Cancel</Button>
        <span class="flex items-center gap-2">
          {#if picked?.openIn}
            <Button
              variant="secondary"
              size="md"
              onclick={() => {
                const id = picked?.openIn?.id ?? "";
                nav.closeOverlay();
                land(id);
              }}>Go to that session</Button
            >
          {/if}
          <Button variant="primary" size="md" type="submit" loading={busy} disabled={busy || !picked || picked.refused}>Connect to {picked?.name ?? "host"}</Button>
        </span>
      </div>
    </form>
  {/if}
</Modal>
