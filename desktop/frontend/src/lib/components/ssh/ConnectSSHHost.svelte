<script lang="ts">
  // Connecting a session that already exists. Pressing Connect is the go. The tier tag
  // and one line about an unsandboxed agent are all it asks you to read; the approval
  // card carries the rest at the moment it matters. With no hosts there is nothing to
  // choose, so this goes straight to host settings.
  import { onMount, tick } from "svelte";
  import { SSHService } from "@bindings/desktop";
  import { overlayClose } from "$lib/overlayClose";
  import { nav } from "$lib/nav.svelte";
  import { store } from "$lib/store.svelte";
  import { ssh, PREDATES_RE } from "$lib/ssh.svelte";
  import {
    SSH_UNSANDBOXED_NOTE,
    agentName,
    connectRows,
  } from "$lib/ssh";
  import Modal from "../Modal.svelte";
  import Button from "../Button.svelte";
  import SSHTierTag from "./SSHTierTag.svelte";

  const sessionId = nav.overlayProject;
  const session = $derived(store.sessionById(sessionId));
  const rows = $derived(connectRows(ssh.hosts, session));
  const agent = $derived(agentName(session?.agent));

  let host = $state("");
  let busy = $state(false);
  let error = $state("");
  let ready = $state(false);
  const picked = $derived(rows.find((r) => r.host.name === host));
  const predates = $derived(ready && PREDATES_RE.test(ssh.hostsError));

  function close() {
    if (!busy) nav.closeOverlay();
  }

  onMount(() => {
    overlayClose.register(close);
    void ssh.loadHosts().then(async () => {
      ready = true;
    });
    return () => overlayClose.unregister(close);
  });

  // An effect, not a step after the first read: a restart from this dialog can be
  // what turns "predates" into a host list, or into no hosts at all.
  $effect(() => {
    if (!ready || ssh.hostsError || host) return;
    if (rows.length === 0) {
      nav.openOverlay("settings", "", "sshhosts");
      return;
    }
    host = (rows.find((r) => r.live) ?? rows.find((r) => !r.refused))?.host.name ?? "";
    if (host) void tick().then(() => document.getElementById(`ssh-connect-${host}`)?.focus());
  });

  function land(conn: string) {
    nav.closeOverlay();
    ssh.open(sessionId, { conn });
    void ssh.view(sessionId).fetch();
  }

  async function connect() {
    if (!picked || picked.refused || busy) return;
    busy = true;
    error = "";
    try {
      const r = await SSHService.Connect({ session: sessionId, host });
      await store.refresh();
      busy = false;
      await tick();
      land(r.conn.id);
    } catch (err) {
      busy = false;
      error = String(err);
    }
  }

  function hostKey(e: KeyboardEvent) {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    e.preventDefault();
    const usable = rows.filter((r) => !r.refused);
    if (!usable.length) return;
    const i = usable.findIndex((r) => r.host.name === host);
    const next = usable[(i + (e.key === "ArrowDown" ? 1 : -1) + usable.length) % usable.length];
    host = next.host.name;
    void tick().then(() => document.getElementById(`ssh-connect-${next.host.name}`)?.focus());
  }
</script>

<Modal title="Connect to host" onClose={close} width="560px">
  {#if predates}
    <div class="grid justify-items-start gap-2 py-2">
      <p class="copy text-ink">This daemon predates host connections. Restart it to use them.</p>
      <Button variant="secondary" size="sm" onclick={() => ssh.restartDaemon()}>Restart daemon</Button>
    </div>
  {:else if ssh.hostsError}
    <p class="copy py-2 text-bad" role="alert">Could not read your hosts: {ssh.hostsError}</p>
  {:else}
    <form class="grid gap-4" onsubmit={(e) => { e.preventDefault(); void connect(); }}>
      <div class="grid gap-1.5">
        <span class="text-sm text-faint" id="ssh-connect-label">Host for {session?.title || "this session"}</span>
        <!-- svelte-ignore a11y_interactive_supports_focus -->
        <div class="grid gap-0.5" role="radiogroup" aria-labelledby="ssh-connect-label" onkeydown={hostKey}>
          {#each rows as r (r.host.name)}
            <button
              type="button"
              id="ssh-connect-{r.host.name}"
              role="radio"
              aria-checked={host === r.host.name}
              disabled={!!r.refused}
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
              <span class="text-sm whitespace-nowrap text-faint">{r.note}</span>
            </button>
            {#if r.refused}<p class="ml-6 text-sm text-faint">{r.refused}</p>{/if}
          {/each}
        </div>
      </div>

      {#if picked && !picked.live && !picked.refused && !session?.sandbox}
        <p class="text-sm text-warn" title={SSH_UNSANDBOXED_NOTE} data-unsandboxed>{agent} runs unsandboxed on this Mac.</p>
      {/if}

      {#if error}<p class="text-sm text-bad" role="alert">{error}</p>{/if}

      <div class="flex items-center justify-between gap-2">
        <Button size="md" onclick={close} disabled={busy}>Cancel</Button>
        <Button variant="primary" size="md" type="submit" loading={busy} disabled={busy || !picked || !!picked.refused}>Connect to {picked?.name ?? "host"}</Button>
      </div>
    </form>
  {/if}
</Modal>
