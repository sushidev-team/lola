<script lang="ts">
  // Settings › SSH hosts: a list above a form. It reads and writes the config file
  // directly, so it works with no daemon, and it never connects to anything: "What it
  // means" runs `ssh -G`, which only reads your ssh config.
  // Key contents and authentication stay with OpenSSH.
  import { onMount } from "svelte";
  import { ConfigService } from "@bindings/desktop";
  import type { SSHHostDTO, SSHHostPreviewDTO } from "@bindings/desktop/models";
  import { confirm } from "$lib/confirm.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { store } from "$lib/store.svelte";
  import { deepEqual } from "$lib/deepEqual";
  import { SSH_REMOTE_OUTLIVES, tierOf } from "$lib/ssh";
  import Button from "../Button.svelte";
  import Chevron from "../Chevron.svelte";
  import Checkbox from "../Checkbox.svelte";
  import Disclosure from "../Disclosure.svelte";
  import Select from "../Select.svelte";
  import SSHTierTag from "./SSHTierTag.svelte";

  let { onDirty = undefined }: { onDirty?: (dirty: boolean) => void } = $props();

  const inputCls = "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent placeholder:text-placeholder";
  const rowCls = "grid min-w-0 grid-cols-[11rem_minmax(0,1fr)] items-baseline gap-3";
  const hintCls = "mt-1 block text-sm text-faint";

  const blank = (): SSHHostDTO => ({
    name: "",
    alias: "",
    label: "",
    user: "",
    port: 0,
    identityFile: "",
    tier: "production",
    allowUnsandboxed: false,
    sandbox: "",
    agent: "",
    defaultCwd: "",
    grantMaxMinutes: 0,
    commandTimeoutSeconds: 0,
    maxCommandTimeoutSeconds: 0,
    outputCapKb: 0,
    controlPersistSeconds: 0,
    connectWaitSeconds: 0,
    approvalTimeoutSeconds: 0,
  });

  let aliases = $state<string[]>([]);
  let aliasesLoading = $state(true);
  let aliasesError = $state("");
  let source = $state("");
  let resolveVersion = 0;
  let resolving = $state(false);
  let resolvedUser = $state("");
  let hosts = $state<SSHHostDTO[]>([]);
  let previews = $state<Record<string, SSHHostPreviewDTO>>({});
  let editing = $state<SSHHostDTO | null>(null);
  let original = $state("");
  let baseline = $state<SSHHostDTO | null>(null);
  let nameTouched = $state(false);
  let error = $state("");
  let saving = $state(false);
  let preview = $state<SSHHostPreviewDTO | null>(null);
  let resolved = $state("");
  let resolveFailed = $state("");
  let aliasEl = $state<HTMLInputElement | null>(null);

  const tier = $derived(tierOf(editing?.tier));
  const dirty = $derived(!!editing && !!baseline && !deepEqual($state.snapshot(editing), $state.snapshot(baseline)));
  $effect(() => onDirty?.(dirty));

  const idOf = (h: SSHHostDTO) => h.name || h.alias;

  async function load() {
    try {
      hosts = (await ConfigService.SSHHostForms()) ?? [];
      const next: Record<string, SSHHostPreviewDTO> = {};
      for (const h of hosts) next[idOf(h)] = await ConfigService.SSHHostPreview(h);
      previews = next;
    } catch (err) {
      error = String(err);
    }
  }
  async function loadAliases() {
    aliasesLoading = true;
    aliasesError = "";
    try { aliases = (await ConfigService.SSHConfigAliases()) ?? []; }
    catch (err) { aliasesError = String(err); }
    finally { aliasesLoading = false; }
  }
  onMount(() => { void load(); void loadAliases(); });

  function edit(h: SSHHostDTO | null) {
    error = "";
    resolved = "";
    resolveFailed = "";
    editing = h ? { ...blank(), ...h } : blank();
    source = h ? (aliases.includes(h.alias) && !h.port && !h.identityFile ? h.alias : ":custom") : "";
    preview = null;
    resolvedUser = "";
    resolveVersion++;
    baseline = $state.snapshot(editing) as SSHHostDTO;
    original = h ? idOf(h) : "";
    nameTouched = !!h?.name;
    void refreshPreview();
    if (h) void resolve();
  }

  async function refreshPreview() {
    if (!editing) return;
    try {
      preview = await ConfigService.SSHHostPreview($state.snapshot(editing) as SSHHostDTO);
    } catch {
      preview = null;
    }
  }

  // Asks `ssh -G`, on blur and on a user change. A failure does not block saving: ssh
  // answers at connect time, and this machine may not be where the alias lives yet.
  async function resolve() {
    const version = ++resolveVersion;
    resolved = "";
    resolveFailed = "";
    resolvedUser = "";
    resolving = false;
    if (!editing?.alias.trim()) return;
    const snapshot = $state.snapshot(editing) as SSHHostDTO;
    const current = () => version === resolveVersion && !!editing && editing.alias === snapshot.alias && editing.user === snapshot.user && editing.port === snapshot.port && editing.identityFile === snapshot.identityFile;
    resolving = true;
    try {
      const r = await ConfigService.ResolveSSHHost(snapshot);
      if (!current()) return;
      if (r.error) { resolveFailed = r.error; return; }
      resolvedUser = r.user || "";
      const who = r.user ? `${r.user}@` : "";
      const hostname = r.hostname || snapshot.alias;
      const host = hostname.includes(":") ? `[${hostname}]` : hostname;
      const port = r.port ? `:${r.port}` : "";
      const through = r.proxyJump ? ` through ${r.proxyJump}` : "";
      resolved = `${who}${host}${port}${through}`;
    } catch (err) {
      if (current()) resolveFailed = String(err);
    } finally {
      if (version === resolveVersion) resolving = false;
    }
  }

  function selectSource(value: string) {
    source = value;
    if (!editing) return;
    if (value === ":custom") {
      queueMicrotask(() => aliasEl?.focus());
      return;
    }
    setAlias(value);
    editing.port = 0;
    editing.identityFile = "";
    void resolve();
    void refreshPreview();
  }

  async function save() {
    if (!editing || saving) return;
    saving = true;
    error = "";
    try {
      await ConfigService.SaveSSHHost(original, $state.snapshot(editing) as SSHHostDTO);
      editing = null;
      baseline = null;
      await load();
      void ssh.loadHosts();
      void store.refresh();
    } catch (err) {
      error = String(err);
    } finally {
      saving = false;
    }
  }

  function remove() {
    if (!editing) return;
    const id = original;
    const name = editing.label || id;
    const anyRan = store.sessions.some((s) => s.ssh?.connections?.some((c) => c.host === id && (c.commandsRun ?? 0) > 0));
    confirm.ask({
      title: `Remove ${name}?`,
      body: "Its live connections close, and agents can no longer ask for it.",
      detail: anyRan ? SSH_REMOTE_OUTLIVES : undefined,
      confirmLabel: "Remove host",
      onConfirm: () => {
        void (async () => {
          try {
            await ConfigService.RemoveSSHHost(id);
            editing = null;
            baseline = null;
            await load();
            void ssh.loadHosts();
          } catch (err) {
            error = String(err);
          }
        })();
      },
    });
  }

  function setAlias(v: string) {
    if (!editing) return;
    editing.alias = v;
    resolveVersion++;
    resolved = "";
    resolveFailed = "";
    resolvedUser = "";
    resolving = false;
    if (!nameTouched) editing.name = "";
  }

  function num(v: string): number {
    const n = Number(v);
    return Number.isFinite(n) && n > 0 ? Math.round(n) : 0;
  }

  const tierHelp = $derived(
    tier === "production"
      ? "Every command asks. No grants. Refuses agents that are not sandboxed."
      : tier === "staging"
        ? "Every command asks, unless you open a grant, up to 30 minutes."
        : "Every command asks, unless you open a grant, up to 2 hours.",
  );
</script>

{#snippet limit(label: string, help: string, unit: string, value: number, placeholder: number | undefined, set: (n: number) => void)}
  <label class={rowCls}>
    <span class="text-faint">{label}</span>
    <span>
      <span class="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-2">
        <input class={inputCls} type="number" min="0" value={value || ""} placeholder={String(placeholder ?? "")} aria-label={label} oninput={(e) => set(num(e.currentTarget.value))} />
        <span class="text-sm text-faint">{unit}</span>
      </span>
      <span class={hintCls}>{help}</span>
    </span>
  </label>
{/snippet}

<section data-ssh-settings>
  <h3 class="mb-2 text-lg text-ink">SSH hosts</h3>
  <p class="copy mb-3 text-sm text-faint">
    Choose a host from your SSH config or enter connection details. You sign in when connecting; keys and passwords stay with SSH.
  </p>

  {#if !editing}
    <div class="grid gap-0.5">
      {#each hosts as h (idOf(h))}
        {@const p = previews[idOf(h)]}
        <!-- A card-shaped row: all of it opens the form. -->
        <button
          type="button"
          class="grid grid-cols-[auto_minmax(0,1fr)_auto_auto] items-center gap-2.5 rounded-md px-2 py-1.5 text-left transition-colors hover:bg-sel/50"
          onclick={() => edit(h)}
        >
          <SSHTierTag tier={h.tier} />
          <span class="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] items-baseline gap-2">
            <span class="truncate text-ink">{h.label || idOf(h)}</span>
            <span class="truncate font-mono text-sm text-faint">{h.user ? `${h.user}@${h.alias}` : h.alias}</span>
          </span>
          <span class="text-sm whitespace-nowrap text-faint">
            {#if p?.directRefused}Direct: cannot start{:else if p?.directSandbox === "docker"}Direct: container{:else}Direct: this Mac{/if}
          </span>
          <span class="text-faint"><Chevron /></span>
        </button>
      {/each}
    </div>
    <div class="mt-2"><Button variant="secondary" size="sm" onclick={() => edit(null)}><span aria-hidden="true">+</span> Add host</Button></div>
    {#if error}<p class="mt-2 text-sm text-bad" role="alert">{error}</p>{/if}
  {:else}
    <div class="grid gap-3" data-ssh-form>
      <label class={rowCls}>
        <span class="text-faint">Connection</span>
        <span>
          <Select aria-label="Connection" value={source} onchange={(e) => selectSource(e.currentTarget.value)}>
            <option value="" disabled>{aliasesLoading ? "Reading SSH config…" : "Choose a host…"}</option>
            {#each aliases as alias (alias)}<option value={alias}>{alias}</option>{/each}
            <option value=":custom">Custom connection…</option>
          </Select>
          {#if aliasesError}
            <span class="mt-1 block text-sm text-warn" role="status">Could not read SSH config. You can enter a custom connection.</span>
            <Button size="sm" onclick={loadAliases}>Retry</Button>
          {:else if !aliasesLoading && aliases.length === 0}
            <span class={hintCls}>No named hosts found in ~/.ssh/config. Choose Custom connection to enter one.</span>
          {:else if source === ":custom"}
            <span class={hintCls}>Saved in Lola. Your SSH config is not changed.</span>
          {:else}
            <span class={hintCls}>From ~/.ssh/config and included files. Host settings stay in your SSH config.</span>
          {/if}
        </span>
      </label>
      {#if source === ":custom"}
        <label class={rowCls}>
          <span class="text-faint">Hostname or IP</span>
          <span>
            <input bind:this={aliasEl} class="{inputCls} font-mono" aria-label="Hostname or IP" required value={editing.alias} placeholder="server.example.com" oninput={(e) => setAlias(e.currentTarget.value)} onblur={() => { void resolve(); void refreshPreview(); }} />
            <span class={hintCls}>An address or SSH alias. No entry in ~/.ssh/config is required.</span>
          </span>
        </label>
        <label class={rowCls}>
          <span class="text-faint">Port</span>
          <span>
            <input class={inputCls} aria-label="Port" type="number" min="1" max="65535" step="1" value={editing.port || ""} placeholder="SSH default (22)" oninput={(e) => { if (editing) editing.port = Number(e.currentTarget.value); }} onblur={resolve} />
            <span class={hintCls}>Leave blank to use your SSH settings.</span>
          </span>
        </label>
        <label class={rowCls}>
          <span class="text-faint">Identity file</span>
          <span>
            <input class="{inputCls} font-mono" aria-label="Identity file" bind:value={editing.identityFile} placeholder="~/.ssh/id_ed25519" onblur={resolve} />
            <span class={hintCls}>Optional key-file path on this Mac. Leave blank to use your SSH keys or agent.</span>
          </span>
        </label>
      {/if}
      {#if editing.alias.trim()}
        <div class={rowCls}>
          <span class="text-faint">Connects to</span>
          <span aria-live="polite">
            {#if resolving}<span class="text-faint">Reading SSH settings…</span>
            {:else if resolveFailed}<span class="text-warn">Could not preview this connection. Check the details or save to try later.</span>
            {:else if resolved}<span class="selectable break-all font-mono text-sm text-ink">{resolved}</span>
            {:else}<span class="text-faint">—</span>{/if}
            <span class={hintCls}>Preview only. Nothing connected.</span>
          </span>
        </div>
      {/if}
      <label class={rowCls}>
        <span class="text-faint">Label</span>
        <span>
          <input class={inputCls} aria-label="Label" bind:value={editing.label} />
          <span class={hintCls}>Shown in Lola. Defaults to the name.</span>
        </span>
      </label>
      <label class={rowCls}>
        <span class="text-faint">Name</span>
        <span>
          <input class="{inputCls} font-mono" aria-label="Name" placeholder={editing.alias} bind:value={editing.name} oninput={() => (nameTouched = true)} />
          <span class={hintCls}>Agents and the CLI use this. Letters, digits, dots, dashes.</span>
        </span>
      </label>
      <label class={rowCls}>
        <span class="text-faint">Remote user</span>
        <span>
          <input class="{inputCls} font-mono" aria-label="Remote user" placeholder={resolvedUser || "From SSH settings"} bind:value={editing.user} onblur={resolve} />
          <span class={hintCls}>A restricted user is the real limit on what a command can do. Lola does not filter commands.</span>
          {#if !editing.user.trim() && resolvedUser}<span class={hintCls}>Leave blank to connect as {resolvedUser}.</span>{/if}
        </span>
      </label>
      <div class={rowCls}>
        <span class="text-faint">Tier</span>
        <span>
          <span class="flex items-center gap-0.5" role="radiogroup" aria-label="Tier">
            {#each [["production", "Production"], ["staging", "Staging"], ["dev", "Dev"]] as [id, label] (id)}
              <Button size="sm" role="radio" aria-pressed={undefined} aria-checked={tier === id} selected={tier === id} onclick={() => { if (editing) editing.tier = id; void refreshPreview(); }}>{label}</Button>
            {/each}
          </span>
          <span class={hintCls}>{tierHelp}</span>
        </span>
      </div>
      <label class={rowCls}>
        <span class="text-faint">Direct session agent runs</span>
        <span>
          <Select aria-label="Direct session agent runs" bind:value={editing.sandbox} onchange={refreshPreview}>
            <option value="">Tier default</option>
            <option value="docker">Container</option>
            <option value="none">On this Mac</option>
          </Select>
          {#if preview}<span class="mt-1 block text-sm {preview.directRefused ? 'text-warn' : 'text-faint'}">{preview.directRefused || preview.directLine}</span>{/if}
        </span>
      </label>
      {#if tier === "production"}
        <div class={rowCls}>
          <span class="text-faint">Allow unsandboxed agents</span>
          <span>
            <label class="flex cursor-pointer items-center gap-2">
              <Checkbox bind:checked={editing.allowUnsandboxed} onchange={refreshPreview} />
              <span>Allow</span>
            </label>
            <span class="mt-1 block text-sm {editing.allowUnsandboxed ? 'text-warn' : 'text-faint'}">Lets an agent that runs as you work here. It could read your keys or talk to Lola directly, so the approval gate would stop accidents and well-behaved agents, not a determined one. Leave off unless you accept that for this host.</span>
          </span>
        </div>
      {/if}
      <label class={rowCls}>
        <span class="text-faint">Agent</span>
        <Select aria-label="Agent" bind:value={editing.agent}>
          <option value="">Default</option>
          <option value="claude">Claude</option>
          <option value="codex">Codex</option>
          <option value="opencode">OpenCode</option>
        </Select>
      </label>
      <label class={rowCls}>
        <span class="text-faint">Default directory</span>
        <span>
          <input class="{inputCls} font-mono" aria-label="Default directory" bind:value={editing.defaultCwd} />
          <span class={hintCls}>Absolute path on the host. Commands that name none run here.</span>
        </span>
      </label>

      <Disclosure label="Limits">
        <div class="mt-3 grid gap-3">
          {#if tier !== "production"}
            {@render limit("Longest grant", "The longest time a human may allow every command for.", "min", editing.grantMaxMinutes, preview?.grantMaxMinutes, (n) => { if (editing) editing.grantMaxMinutes = n; })}
          {/if}
          {@render limit("Command timeout", "When a command names none.", "s", editing.commandTimeoutSeconds, preview?.commandTimeoutSeconds, (n) => { if (editing) editing.commandTimeoutSeconds = n; })}
          {@render limit("Longest command timeout", "The most a command may ask for.", "s", editing.maxCommandTimeoutSeconds, preview?.maxCommandTimeoutSeconds, (n) => { if (editing) editing.maxCommandTimeoutSeconds = n; })}
          {@render limit("Output kept per stream", "Past this the head and the last 64 KB stay.", "KB", editing.outputCapKb, preview?.outputCapKb, (n) => { if (editing) editing.outputCapKb = n; })}
          {@render limit("Idle linger", "How long the connection stays up with nothing using it.", "s", editing.controlPersistSeconds, preview?.controlPersistSeconds, (n) => { if (editing) editing.controlPersistSeconds = n; })}
          {@render limit("Sign-in wait", "Time for a passphrase and MFA.", "s", editing.connectWaitSeconds, preview?.connectWaitSeconds, (n) => { if (editing) editing.connectWaitSeconds = n; })}
          {@render limit("Approval wait", "Then a command expires, unrun.", "s", editing.approvalTimeoutSeconds, preview?.approvalTimeoutSeconds, (n) => { if (editing) editing.approvalTimeoutSeconds = n; })}
        </div>
      </Disclosure>

      {#if error}<p class="text-sm text-bad" role="alert">{error}</p>{/if}

      <div class="flex items-center gap-2 border-t border-edge/60 pt-3">
        {#if original}<Button variant="danger" size="md" onclick={remove}>Remove host</Button>{/if}
        <span class="ml-auto flex gap-2">
          <Button size="md" onclick={() => { editing = null; baseline = null; error = ""; }}>Back</Button>
          <Button variant="primary" size="md" loading={saving} disabled={saving || !editing.alias.trim()} onclick={save}>Save host</Button>
        </span>
      </div>
    </div>
  {/if}
</section>
