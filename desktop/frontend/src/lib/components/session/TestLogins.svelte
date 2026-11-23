<script lang="ts">
  import { onDestroy, onMount, tick } from "svelte";
  import { PreviewService } from "@bindings/desktop";
  import type { Profile } from "@bindings/internal/devlogin/models";
  import Button from "../Button.svelte";
  import Checkbox from "../Checkbox.svelte";
  import Icon from "../Icon.svelte";

  let { session, url, ready, signIn, close }: {
    session: string; url: string; ready: boolean; signIn: (project: string, name: string, signal: AbortSignal) => Promise<void>; close: () => void;
  } = $props();
  let busy = $state(false);
  let error = $state("");
  let project = $state("");
  let autoLogin = $state({ name: "", origin: "" });
  let profiles = $state<Profile[]>([]);
  let mode = $state<"list" | "add" | "edit">("list");
  let name = $state(""), username = $state(""), password = $state(""), agent = $state(false);
  let removeName = $state("");
  let panel: HTMLElement;
  const lifetime = new AbortController();
  onDestroy(() => lifetime.abort());
  onMount(() => { panel.focus(); void perform(refresh); });

  async function refresh() {
    const list = await PreviewService.TestLogins(session, url);
    project = list.project;
    autoLogin = list.autoLogin;
    profiles = list.profiles ?? [];
  }
  async function perform(action: () => Promise<void>) {
    if (busy) return;
    busy = true;
    error = "";
    try { await action(); }
    catch (e) { error = String(e); }
    finally { busy = false; }
  }
  function edit(p?: Profile) {
    name = p?.name ?? "";
    username = p?.username ?? "";
    agent = p?.agent ?? false;
    password = error = removeName = "";
    mode = p ? "edit" : "add";
    void tick().then(() => panel?.querySelector<HTMLInputElement>("input:not(:disabled)")?.focus());
  }
  async function save() {
    await PreviewService.SaveTestLogin(session, url, project, { name, username, agent }, password, mode === "edit");
    password = "";
    mode = "list";
    await refresh();
  }
  async function setAutoLogin(p: Profile, enabled: boolean) {
    await PreviewService.SetAutoLogin(session, url, project, p.name, enabled);
    await refresh();
  }
  async function forget(p: Profile) {
    await PreviewService.RemoveTestLogin(session, url, project, p.name);
    removeName = "";
    await refresh();
  }
  function onkey(e: KeyboardEvent) {
    if (e.key === "Escape") { e.preventDefault(); close(); }
  }
</script>

<div role="dialog" bind:this={panel} tabindex="-1" aria-label="Test logins" onkeydown={onkey}
  class="absolute bottom-full left-1/2 mb-2 grid max-h-[65dvh] w-80 max-w-[90vw] -translate-x-1/2 gap-2 overflow-y-auto rounded-lg border border-edge bg-panel p-3 text-sm text-ink shadow-xl">
  <header class="flex items-center justify-between gap-2">
    <h3 class="font-semibold">{mode === "list" ? "Test logins" : mode === "add" ? "Add test login" : "Edit test login"}</h3>
    <Button size="xs" icon aria-label="Close test logins" onclick={close}><Icon name="x" size={14} /></Button>
  </header>
  {#if project}<p class="text-xs text-faint">{project} · {new URL(url).host}</p>{/if}
  {#if error}<p role="alert" class="text-bad">{error}</p>{/if}
  {#if mode === "list"}
    {#if busy && !profiles.length}<p role="status">Loading…</p>
    {:else if !profiles.length && !error}<p class="text-faint">No test logins saved for this app.</p>{/if}
    {#each profiles as p (p.name)}
      <div class="border-b border-edge pb-2">
        <div class="flex min-w-0 items-center gap-1">
          <button type="button" class="min-w-0 flex-1 rounded px-1 py-1 text-left enabled:hover:bg-sel disabled:opacity-60"
            title={ready ? 'Sign in as ' + p.name : 'Open the local sign-in page to use this login'} disabled={busy || !ready}
            onclick={() => perform(async () => { await signIn(project, p.name, lifetime.signal); if (!lifetime.signal.aborted) close(); })}>
            <span class="block truncate font-medium">{p.name}</span><span class="block truncate text-xs text-faint">{p.username}</span>
          </button>
          <Button size="xs" icon aria-label={'Edit ' + p.name} disabled={busy} onclick={() => edit(p)}><Icon name="pencil" size={13} /></Button>
          {#if removeName === p.name}
            <Button size="xs" variant="danger" disabled={busy} onclick={() => perform(() => forget(p))}>Remove</Button>
          {:else}
            <Button size="xs" icon aria-label={'Remove ' + p.name} disabled={busy} onclick={() => removeName = p.name}><Icon name="trash-2" size={13} /></Button>
          {/if}
        </div>
        <label class="flex items-center gap-2 px-1 text-xs text-ink-2" title={'Automatically sign in on ' + new URL(url).origin}>
          <Checkbox aria-label={'Auto-login as ' + p.name} checked={autoLogin?.name === p.name}
            disabled={busy} onclick={(e) => { e.preventDefault(); void perform(() => setAutoLogin(p, autoLogin?.name !== p.name)); }} />
          Auto-login{#if autoLogin?.name === p.name && autoLogin.origin !== new URL(url).origin} (currently on {autoLogin.origin}){/if}
        </label>
      </div>
    {/each}
    <Button size="xs" variant="secondary" class="justify-self-start" disabled={busy || !project} onclick={() => edit()}><Icon name="plus" size={13} />Add login</Button>
  {:else}
    <form class="grid gap-2" onsubmit={(e) => { e.preventDefault(); void perform(save); }}>
      <label>Login name<input class="login-input" required maxlength="100" disabled={mode === "edit" || busy} bind:value={name} placeholder="e.g. Store admin" /></label>
      <label>Username or email<input class="login-input" required disabled={busy} autocomplete="off" spellcheck="false" bind:value={username} /></label>
      <label>Password<input class="login-input" type="password" required={mode === "add"} disabled={busy} autocomplete="new-password"
        placeholder={mode === "edit" ? "Leave blank to keep password" : undefined} bind:value={password} /></label>
      <p class="text-xs text-faint">Passwords are stored in macOS Keychain.</p>
      <label class="flex items-center gap-2"><Checkbox bind:checked={agent} disabled={busy} />Allow local agents to use this login</label>
      <div class="flex justify-end gap-1">
        <Button size="xs" disabled={busy} onclick={() => { password = error = ""; mode = "list"; }}>Cancel</Button>
        <Button type="submit" size="xs" variant="primary" loading={busy}>Save login</Button>
      </div>
    </form>
  {/if}
</div>

<style>
  .login-input { display:block; width:100%; margin-top:3px; padding:5px 8px; border-radius:5px; border:1px solid var(--color-edge); background:var(--color-canvas); color:var(--color-ink); }
</style>
