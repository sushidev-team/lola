<script lang="ts">
  import { tick, onMount } from "svelte";
  import { overlayClose } from "$lib/overlayClose";
  import { nav } from "$lib/nav.svelte";
  import { store } from "$lib/store.svelte";
  import { openSession } from "$lib/projectActions";
  import Modal from "./Modal.svelte";
  import Button from "./Button.svelte";
  import Select from "./Select.svelte";

  let project = $state(nav.overlayProject);
  let location = $state(nav.sessionLocation);
  let useAgent = $state(nav.sessionAgent);
  let name = $state("");
  let nameInput: HTMLInputElement;
  let branch = $state("");
  let base = $state("");
  let path = $state("");
  let agentKind = $state("");
  let busy = $state(false);
  let error = $state("");
  const selected = $derived(store.projectByName(project));
  const temporary = $derived(location === "temporary");
  const field = "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent";
  function close() { if (!busy) nav.closeOverlay(); }
  onMount(() => { overlayClose.register(close); void tick().then(() => nameInput?.focus()); return () => overlayClose.unregister(close); });
  async function create() {
    if (busy || (!temporary && !selected) || (location === "worktree" && !branch.trim())) return;
    busy = true; error = "";
    const r = await store.openManual({ project: temporary ? "" : project, name: name.trim(), location,
      branch: location === "worktree" ? branch.trim() : "", base: location === "worktree" ? base.trim() : undefined,
      agent: !temporary && useAgent, agentKind: !temporary && useAgent ? agentKind || undefined : undefined,
      path: temporary ? path.trim() : undefined });
    busy = false;
    if (!r) { error = store.flash?.text || "Could not start the session. Check the directory and try again."; return; }
    nav.closeOverlay(); await tick(); openSession(r.sessionId, temporary ? "" : project);
  }
</script>

<Modal title="New session" onClose={close} width="520px">
  <form class="grid gap-4" onsubmit={(e) => { e.preventDefault(); void create(); }}>
    <label class="grid gap-1.5 text-sm">Name
      <input bind:this={nameInput} class={field} bind:value={name} maxlength="100" placeholder="e.g. Investigate checkout (optional)" disabled={busy} />
    </label>
    <label class="grid gap-1.5 text-sm">Project
      <Select bind:value={project} disabled={busy} onchange={() => { location = project ? "worktree" : "temporary"; }}>
        <option value="">No project — temporary shell</option>
        {#each store.projects.filter(p => p.path) as p (p.name)}<option value={p.name}>{store.displayNameFor(p.name)}</option>{/each}
      </Select>
    </label>
    {#if !temporary}
      <div class="grid grid-cols-2 gap-3">
        <label class="grid gap-1.5 text-sm">Run
          <Select value={useAgent ? "agent" : "shell"} disabled={busy} onchange={(e) => useAgent = e.currentTarget.value === "agent"}>
            <option value="agent">Coding agent</option><option value="shell">Shell</option>
          </Select>
        </label>
        <label class="grid gap-1.5 text-sm">Location
          <Select bind:value={location} disabled={busy}>
            <option value="worktree">New worktree</option><option value="main">Main checkout</option>
          </Select>
        </label>
      </div>
      {#if useAgent}
        <label class="grid gap-1.5 text-sm">Agent
          <Select bind:value={agentKind} disabled={busy}>
            <option value="">Project default ({selected?.agent || "claude"})</option>
            <option value="claude">Claude</option><option value="codex">Codex</option><option value="opencode">OpenCode</option>
          </Select>
        </label>
      {/if}
      {#if location === "worktree"}
        <div class="grid grid-cols-2 gap-3">
          <label class="grid gap-1.5 text-sm">New branch<input class={field} bind:value={branch} required placeholder="fix/checkout" disabled={busy} /></label>
          <label class="grid gap-1.5 text-sm">Base branch<input class={field} bind:value={base} placeholder={selected?.defaultBranch || "Project default"} disabled={busy} /></label>
        </div>
      {:else}
        <div class="text-sm text-faint"><p class="break-all font-mono">{selected?.path}</p><p class="mt-1">Uses the current branch and shares files with other sessions in this checkout.</p></div>
      {/if}
    {:else}
      <label class="grid gap-1.5 text-sm">Directory<input class={field} bind:value={path} placeholder="Use the default from Settings" disabled={busy} /></label>
      <p class="text-sm text-faint">A shell without project setup. It stays available when you leave the view or close the app.</p>
    {/if}
    {#if error}<p class="text-sm text-bad" role="alert">{error}</p>{/if}
    <div class="flex justify-end gap-2 border-t border-edge pt-3">
      <Button onclick={close} disabled={busy}>Cancel</Button>
      <Button variant="primary" type="submit" loading={busy} disabled={busy || (location === "worktree" && !branch.trim())}>Create session</Button>
    </div>
  </form>
</Modal>
