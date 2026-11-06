<script lang="ts">
  // New session, prompt first. What the agent should do is the one thing only you know, so it is
  // the first thing asked and it is enough: the branch derives from it, and the rest waits under
  // Options. The prompt becomes the story's first row.
  //
  // Run: Shell hides the prompt, because nothing would read it. A shell with no project is the
  // temporary form, and that one asks for a directory instead.
  import { tick, onMount, untrack } from "svelte";
  import { overlayClose } from "$lib/overlayClose";
  import { nav } from "$lib/nav.svelte";
  import { store } from "$lib/store.svelte";
  import { openSession } from "$lib/projectActions";
  import { agentName } from "$lib/ssh";
  import { branchFromPrompt, DEFAULT_BRANCH_PREFIX } from "$lib/sessionview";
  import { ConfigService } from "@bindings/desktop";
  import Modal from "./Modal.svelte";
  import Button from "./Button.svelte";
  import Select from "./Select.svelte";
  import Disclosure from "./Disclosure.svelte";

  let project = $state(nav.overlayProject);
  let location = $state(nav.sessionLocation);
  let useAgent = $state(nav.sessionAgent);
  let prompt = $state("");
  let promptEl = $state<HTMLTextAreaElement>();
  let nameEl = $state<HTMLInputElement>();
  let name = $state("");
  let branch = $state("");
  let branchEdited = $state(false);
  let prefix = $state(DEFAULT_BRANCH_PREFIX);
  let base = $state("");
  let path = $state("");
  let agentKind = $state("");
  let busy = $state(false);
  let picking = $state(false);
  let error = $state("");

  const selected = $derived(store.projectByName(project));
  const temporary = $derived(location === "temporary");
  const asksPrompt = $derived(!temporary && useAgent);
  const agent = $derived(agentName(agentKind || selected?.agent));
  const canStart = $derived(temporary ? true : !!selected && (location !== "worktree" || !!branch.trim()));
  const field = "w-full rounded border border-edge bg-canvas px-2 py-1.5 text-ink outline-none focus:border-accent";

  function close() { if (!busy && !picking) nav.closeOverlay(); }

  // A branch nobody typed follows the prompt. One somebody typed is theirs from then on.
  $effect(() => {
    const next = branchFromPrompt(asksPrompt ? prompt : "", prefix);
    if (!untrack(() => branchEdited)) branch = next;
  });

  // The project's own prefix, else the defaults'. An answer for a project someone has since moved
  // off is dropped, and a failed read leaves lola's own default, which is what the daemon falls
  // back to as well.
  $effect(() => {
    const p = project;
    if (!p) return;
    void (async () => {
      try {
        const own = (await ConfigService.GetProject(p))?.branchPrefix;
        const found = own || (await ConfigService.GetSettings())?.branchPrefix;
        if (p === project && found) prefix = found;
      } catch {
        /* keep the default */
      }
    })();
  });

  function grow() {
    const el = promptEl;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight + 2, 8 * 22 + 14)}px`;
  }

  // ↩ starts, ⇧↩ and ⌥↩ are a newline, same as the composer. Composition is an IME's Enter.
  function promptKey(e: KeyboardEvent) {
    if (e.key !== "Enter" || e.shiftKey || e.altKey || e.isComposing) return;
    e.preventDefault();
    void create();
  }

  async function pickFolder() {
    if (busy || picking) return;
    picking = true; error = "";
    try {
      const chosen = await ConfigService.PickFolder(path.trim());
      if (chosen) path = chosen;
    } catch (err) {
      error = String(err);
    } finally {
      picking = false;
    }
  }

  onMount(() => {
    overlayClose.register(close);
    void tick().then(() => (promptEl ?? nameEl)?.focus());
    return () => overlayClose.unregister(close);
  });

  async function create() {
    if (busy || picking || !canStart) return;
    busy = true; error = "";
    const text = asksPrompt ? prompt.trim() : "";
    const r = await store.openManual({
      project: temporary ? "" : project,
      name: name.trim(),
      location,
      branch: location === "worktree" ? branch.trim() : "",
      base: location === "worktree" ? base.trim() || undefined : undefined,
      agent: !temporary && useAgent,
      agentKind: !temporary && useAgent ? agentKind || undefined : undefined,
      prompt: text || undefined,
      path: temporary ? path.trim() : undefined,
    });
    busy = false;
    if (!r) { error = store.flash?.text || "Could not start the session. Check the directory and try again."; return; }
    nav.closeOverlay(); await tick(); openSession(r.sessionId, temporary ? "" : project);
  }
</script>

<Modal title={temporary ? "New temporary session" : "New session"} titleHelp={temporary ? "A shell without project setup. It stays available when you leave the view or close the app." : ""} onClose={close} width="560px">
  <form class="grid gap-4" onsubmit={(e) => { e.preventDefault(); void create(); }}>
    {#if asksPrompt}
      <label class="grid gap-2">
        <span class="text-lg text-ink">What should {agent} do?</span>
        <textarea bind:this={promptEl} bind:value={prompt} rows="3" class="{field} resize-none px-3! text-prose caret-accent focus:border-ink/35 focus-visible:outline-none!" disabled={busy} oninput={grow} onkeydown={promptKey}></textarea>
      </label>
    {/if}

    <div class="grid grid-cols-2 gap-3">
      <label class="grid gap-1.5 text-sm">Project
        <Select bind:value={project} disabled={busy} onchange={() => { location = project ? (location === "temporary" ? "worktree" : location) : "temporary"; if (project && !nav.sessionAgent && location === "temporary") useAgent = true; }}>
          <option value="">No project, temporary shell</option>
          {#each store.projects.filter(p => p.path) as p (p.name)}<option value={p.name}>{store.displayNameFor(p.name)}</option>{/each}
        </Select>
      </label>
      {#if !temporary}
        <label class="grid gap-1.5 text-sm">Run
          <Select value={useAgent ? "agent" : "shell"} disabled={busy} onchange={(e) => useAgent = e.currentTarget.value === "agent"}>
            <option value="agent">Coding agent</option><option value="shell">Shell</option>
          </Select>
        </label>
      {/if}
    </div>

    {#if temporary}
      <label class="grid gap-1.5 text-sm">Name
        <input bind:this={nameEl} class={field} bind:value={name} maxlength="100" placeholder="e.g. Investigate checkout (optional)" disabled={busy} />
      </label>
      <div class="grid gap-1.5 text-sm">
        <label for="temporary-session-directory">Directory</label>
        <div class="flex items-center gap-2">
          <input id="temporary-session-directory" class={field} bind:value={path} placeholder="Optional, uses the default from Settings" disabled={busy || picking} />
          <Button variant="secondary" onclick={pickFolder} disabled={busy || picking}>{picking ? "Choosing…" : "Choose folder…"}</Button>
        </div>
      </div>
    {:else}
      <Disclosure label="Options" hint={useAgent ? "agent, location, branch, name" : "location, branch, name"} class="-ml-2">
        <div class="grid gap-4 pt-3 pl-2">
          <div class="grid grid-cols-2 gap-3">
            {#if useAgent}
              <label class="grid gap-1.5 text-sm">Agent
                <Select bind:value={agentKind} disabled={busy}>
                  <option value="">Project default ({selected?.agent || "claude"})</option>
                  <option value="claude">Claude</option><option value="codex">Codex</option><option value="opencode">OpenCode</option>
                </Select>
              </label>
            {/if}
            <label class="grid gap-1.5 text-sm">Location
              <Select bind:value={location} disabled={busy}>
                <option value="worktree">New worktree</option><option value="main">Main checkout</option>
              </Select>
            </label>
          </div>
          {#if location === "worktree"}
            <div class="grid grid-cols-2 gap-3">
              <label class="grid gap-1.5 text-sm">New branch<input class={field} bind:value={branch} oninput={() => (branchEdited = true)} required placeholder="fix/checkout" disabled={busy} /></label>
              <label class="grid gap-1.5 text-sm">Base branch<input class={field} bind:value={base} placeholder={selected?.defaultBranch || "Project default"} disabled={busy} /></label>
            </div>
          {:else}
            <div class="text-sm text-faint"><p class="break-all font-mono">{selected?.path}</p><p class="mt-1">Uses the current branch and shares files with other sessions in this checkout.</p></div>
          {/if}
          <label class="grid gap-1.5 text-sm">Name
            <input bind:this={nameEl} class={field} bind:value={name} maxlength="100" placeholder="e.g. Investigate checkout (optional)" disabled={busy} />
          </label>
        </div>
      </Disclosure>
    {/if}

    {#if error}<p class="text-sm text-bad" role="alert">{error}</p>{/if}
    <div class="flex items-center justify-end gap-2">
      {#if asksPrompt}<span class="mr-auto text-sm text-faint">⇧↩ for a new line</span>{/if}
      <Button onclick={close} disabled={busy || picking}>Cancel</Button>
      <Button variant="primary" type="submit" loading={busy} disabled={busy || picking || !canStart}>Start <span aria-hidden="true">↩</span></Button>
    </div>
  </form>
</Modal>
