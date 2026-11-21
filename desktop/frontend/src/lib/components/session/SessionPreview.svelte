<script module lang="ts">
  // These are immutable review item IDs. Authentication stays in the proxy's
  // session-scoped browser cookies. Relaunching Lola starts with a fresh proxy.
  const verifiedRoutes = new Map<string, string>();
  function rememberRoute(key: string, entryPath: string) {
    if (verifiedRoutes.size >= 128) verifiedRoutes.delete(verifiedRoutes.keys().next().value!);
    verifiedRoutes.set(key, entryPath);
  }
</script>

<script lang="ts">
  // The session's running app, framed through the daemon-side loopback proxy
  // (internal/preview), with viewport presets and an element picker. A picked
  // element plus the human's note goes to the agent as feedback, through the
  // same deferred hand-off gate as diff comments — this component never types
  // into a pane itself.
  import { onMount, untrack } from "svelte";
  import { DaemonService, PreviewService } from "@bindings/desktop";
  import { store } from "$lib/store.svelte";
  import { devUrlGroups, devUrlLabel } from "$lib/devurl";
  import { DEFAULT_VIEWPORT, PICKER_SOURCE, VIEWPORTS, devPageURL, frameStyle, parsePick, preview, type PickedElement, type PreviewRequest, type ViewportId } from "$lib/preview.svelte";
  import Modal from "../Modal.svelte";
  import Button from "../Button.svelte";
  import Icon from "../Icon.svelte";
  import Tabs from "../Tabs.svelte";

  // The canvas keys this instance by immutable package and page. A workspace
  // refresh must not restart a live sign-in while the reviewer is looking at it.
  let { inline, active = true, resumeKey }: { inline?: PreviewRequest; active?: boolean; resumeKey?: string } = $props();
  const inlinePage = untrack(() => inline);
  const req = $derived(inlinePage ?? preview.request);

  let frame = $state<HTMLIFrameElement | null>(null);
  let src = $state("");
  const frameOrigin = $derived(src ? new URL(src).origin : "");
  let error = $state("");
  let viewport = $state<ViewportId>(DEFAULT_VIEWPORT);
  let picking = $state(false);
  let picked = $state<PickedElement | null>(null);
  let note = $state("");
  let sending = $state(false);
  let reload = $state(0);
  // What the agent prepared for the active page, and what the page said about it.
  let prepared = $state<{ found: boolean[]; waited: boolean } | null>(null);
  let stepFailure = $state<{ index: number; step: string; why: string } | null>(null);
  // Where a prepared flow (a login that redirects) stands. A step that navigates
  // ends the page's script, so the next document is told to carry on after the
  // last step it saw finish (had its effect).
  let flow = { started: -1, armed: true, finished: false, resumes: 0 };
  // Set only after this frame has reached the declared target. On return, a
  // lightweight request from inside the frame can check its own session cookie.
  let verifiedPath = "";
  let entryPath = "";
  let firstDocumentPath = "";
  let documentId = $state("");
  let restoring = false;
  const finalRoute = $derived.by(() => {
    const p = inlinePage?.prep;
    const last = p?.steps?.at(-1);
    if (!inlinePage || !p || !(p.waitFor || p.marks?.length) || last?.do !== "goto" || !last.path) return "";
    const target = new URL(last.path, inlinePage.url);
    return target.origin === new URL(inlinePage.url).origin ? target.href : "";
  });
  function replayFromStart() {
    if (resumeKey) verifiedRoutes.delete(resumeKey);
    reload++;
  }
  let checkId = 0;
  let pendingCheck = 0;
  function checkSession() {
    if (!inlinePage || !active || !verifiedPath || !src) return;
    pendingCheck = ++checkId;
    frame?.contentWindow?.postMessage({
      target: PICKER_SOURCE, type: "check-session", id: pendingCheck,
      path: verifiedPath, entry: entryPath,
    }, frameOrigin);
  }
  $effect(() => {
    if (active) untrack(checkSession);
    else pendingCheck = 0;
  });
  let surface = $state<HTMLDivElement>();
  let exitHint = $state(false);
  const exitWindowMs = 3000;
  let exitDeadline = 0;
  let exitTimer: ReturnType<typeof setTimeout>;
  let returnFocus: HTMLIFrameElement | null = null;

  function clearExitHint() {
    clearTimeout(exitTimer);
    exitDeadline = 0;
    exitHint = false;
    if (returnFocus && document.activeElement === surface) returnFocus.focus({ preventScroll: true });
    returnFocus = null;
  }

  function showExitHint() {
    clearTimeout(exitTimer);
    exitDeadline = performance.now() + exitWindowMs;
    exitHint = true;
    exitTimer = setTimeout(clearExitHint, exitWindowMs);
  }

  function onPreviewKey(e: KeyboardEvent) {
    if (!req || e.defaultPrevented || e.isComposing) return;
    if (e.key !== "Escape" || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) {
      if (exitHint) clearExitHint();
      return;
    }
    e.preventDefault();
    if (e.repeat) return;
    if (performance.now() < exitDeadline) close();
    else showExitHint();
  }


  const markName = (m: { selector?: string; text?: string; label?: string }) => m.label || m.text || m.selector || "element";
  const missing = $derived(prepared ? (req?.prep?.marks ?? []).filter((_, i) => !prepared!.found[i]).map(markName) : []);

  const replay = $derived(!!(req?.prep?.steps?.length || req?.prep?.marks?.length || req?.prep?.waitFor || req?.prep?.scrollY));
  let stalled = $state(false);
  let progressTimer: ReturnType<typeof setTimeout>;
  function awaitProgress() {
    clearTimeout(progressTimer);
    stalled = false;
    progressTimer = setTimeout(() => { stalled = true; }, 20000);
  }
  const preparing = $derived(replay && !prepared && !stalled && !error);

  const vp = $derived(VIEWPORTS.find((v) => v.id === viewport) ?? VIEWPORTS[2]);
  const session = $derived(req ? store.sessions.find((s) => s.id === req.sessionId) : undefined);
  const groups = $derived(devUrlGroups(
    [...new Set([...(session?.devUrls ?? []), ...(req ? [req.url] : [])])],
    session?.apps ?? [],
  ));

  $effect(() => {
    const request = req;
    reload;
    verifiedPath = "";
    documentId = "";
    firstDocumentPath = "";
    entryPath = (resumeKey && verifiedRoutes.get(resumeKey)) || (request ? new URL(request.url).pathname : "");
    restoring = !!(finalRoute && resumeKey && verifiedRoutes.has(resumeKey));
    pendingCheck = 0;
    src = "";
    error = "";
    picked = null;
    picking = false;
    prepared = null;
    stepFailure = null;
    flow = { started: -1, armed: true, finished: false, resumes: 0 };
    const device = request?.prep?.device;
    if (device && VIEWPORTS.some((v) => v.id === device)) viewport = device as ViewportId;
    clearExitHint();
    stalled = false;
    if (!request) return;
    if (replay) awaitProgress();
    let current = true;
    PreviewService.Open(request.sessionId, restoring ? finalRoute : request.url).then(
      (address) => { if (current) src = address; },
      (err) => { if (current) error = String(err); },
    );
    return () => { current = false; clearTimeout(progressTimer); };
  });

  /** Asks the framed page to carry out the agent's preparation (scroll, outline, wait). */
  function prepare(from = 0, redirected = false) {
    const p = req?.prep;
    if (!p || !frame?.contentWindow) return;
    prepared = null;
    stepFailure = null;
    if (from === 0) flow = { started: -1, armed: false, finished: false, resumes: 0 };
    awaitProgress();
    const spec = { ...(redirected ? { redirected: true } : {}), waitFor: p.waitFor, scrollY: p.scrollY, marks: p.marks, steps: restoring ? undefined : p.steps ?? undefined, ...(from > 0 && !restoring ? { from } : {}) };
    // postMessage uses structured clone. Steps and marks from the review
    // workspace are Svelte state proxies until they are snapshotted.
    frame.contentWindow.postMessage({ target: PICKER_SOURCE, type: "prepare", spec: $state.snapshot(spec) }, frameOrigin);
  }

  /** A document announced itself (a first load, or the one a step navigated to). */
  function onFrameReady(frameUrl: unknown) {
    const p = req?.prep;
    if (!p) return;
    const steps = p.steps?.length ?? 0;
    if (restoring && !flow.finished) {
      flow.armed = false;
      if (sameDocument(frameUrl, finalRoute)) prepare(steps);
      else replayFromStart();
      return;
    }
    if (steps && flow.started >= 0 && !flow.finished) {
      // Mid-flow: a step navigated. Carry on after it, a bounded number of times.
      if (flow.resumes++ < steps) prepare(flow.started + 1);
      else { clearTimeout(progressTimer); stalled = true; }
      return;
    }
    // A flow starts on the first document whatever its path (an app may redirect
    // to its login page); a plain preparation only on the page it names.
    if (steps ? flow.armed : sameDocument(frameUrl)) {
      if (steps && typeof frameUrl === "string") {
        try { firstDocumentPath = new URL(frameUrl).pathname; } catch { /* Malformed frame location. */ }
      }
      flow.armed = false;
      prepare(0, !!steps && !sameDocument(frameUrl));
    }
  }

  /** Only the page the agent prepared is prepared: a link the reviewer followed is theirs. */
  function sameDocument(frameUrl: unknown, target = req!.url): boolean {
    try {
      return new URL(String(frameUrl)).pathname === new URL(target).pathname;
    } catch {
      return false;
    }
  }

  function frameLoaded() {
    if (replay && frameOrigin && frameOrigin !== "null") {
      frame?.contentWindow?.postMessage({ target: PICKER_SOURCE, type: "hello" }, frameOrigin);
    }
  }

  function tell(on: boolean) {
    if (src) frame?.contentWindow?.postMessage({ target: PICKER_SOURCE, type: "pick", on }, frameOrigin);
  }

  function togglePick() {
    picking = !picking;
    if (picking) picked = null;
    tell(picking);
  }

  onMount(() => {
    const onMessage = (e: MessageEvent) => {
      // Only the framed page, and only a well-formed pick, ever reaches the form.
      if (!frame || e.source !== frame.contentWindow || !src || e.origin !== frameOrigin) return;
      const d = e.data as { source?: string; type?: string; id?: number; documentId?: string; picking?: boolean; url?: string; found?: unknown; waited?: unknown; i?: unknown; failed?: { index?: unknown; step?: unknown; why?: unknown } | null } | null;
      if (d?.source === PICKER_SOURCE && d.type === "mode") picking = !!d.picking;
      if (d?.source === PICKER_SOURCE && d.type === "ready") {
        if (d.documentId && d.documentId === documentId) return;
        documentId = d.documentId ?? "";
        onFrameReady(d.url);
        if (verifiedPath && entryPath && sameDocument(d.url, frameOrigin + entryPath)) checkSession();
      }
      if (d?.source === PICKER_SOURCE && d.type === "session-expired" && pendingCheck && d.id === pendingCheck && active) {
        pendingCheck = 0;
        replayFromStart();
      }
      if (d?.source === PICKER_SOURCE && d.type === "step" && typeof d.i === "number") {
        if (d.i === 0 && req?.prep?.steps?.[0]?.do === "fill" && firstDocumentPath) entryPath = firstDocumentPath;
        flow.started = d.i;
        awaitProgress();
      }
      if (d?.source === PICKER_SOURCE && d.type === "prepared" && Array.isArray(d.found)) {
        clearTimeout(progressTimer);
        stalled = false;
        flow.finished = true;
        prepared = { found: d.found.map(Boolean), waited: !!d.waited };
        const f = d.failed;
        // A step failure is text from the page's script: shown as text, trimmed.
        stepFailure = f && typeof f.index === "number" && f.index >= 0
          ? { index: f.index, step: String(f.step ?? "").slice(0, 120), why: String(f.why ?? "").slice(0, 200) }
          : null;
        if (restoring && (f || d.waited || !prepared.found.every(Boolean))) {
          replayFromStart();
          return;
        }
        if (resumeKey && finalRoute && !f && !d.waited && prepared.found.every(Boolean) && sameDocument(d.url, finalRoute)) rememberRoute(resumeKey, entryPath);
        if (inlinePage?.prep?.steps?.some(s => s.do === "fill") && !f && !d.waited && prepared.found.every(Boolean) && typeof d.url === "string") {
          try {
            const destination = new URL(d.url);
            if (destination.origin === e.origin && destination.pathname !== entryPath) verifiedPath = destination.pathname + destination.search;
          } catch { /* The frame reported no usable destination. */ }
        }
      }
      // A frame may ask for the hint. Only a second keypress in Lola can close
      // the preview, so a page cannot discard a form by posting two messages.
      if (d?.source === PICKER_SOURCE && !inlinePage && d.type === "escape" && document.activeElement === frame) {
        returnFocus = frame;
        surface?.focus({ preventScroll: true });
        showExitHint();
        return;
      }
      const p = parsePick(e.data);
      if (p) {
        picked = p;
        note = "";
      }
    };
    window.addEventListener("message", onMessage);
    if (!inlinePage) window.addEventListener("keydown", onPreviewKey);
    else window.addEventListener("focus", checkSession);
    window.addEventListener("pointerdown", clearExitHint, true);
    return () => {
      window.removeEventListener("message", onMessage);
      window.removeEventListener("keydown", onPreviewKey);
      window.removeEventListener("focus", checkSession);
      window.removeEventListener("pointerdown", clearExitHint, true);
      clearExitHint();
    };
  });

  async function send() {
    if (!req || !picked || sending || !note.trim()) return;
    sending = true;
    try {
      const r = await DaemonService.SendFeedback({
        session: req.sessionId,
        elements: [{ url: devPageURL(picked.url, req.url), selector: picked.selector, html: picked.html, text: picked.text, note: note.trim() }],
      });
      store.setFlash(r?.delivered ? "element sent to the agent" : "element queued — it is sent when the agent is at its prompt", r?.delivered ? "good" : "warn");
      picked = null;
      note = "";
      void store.refresh();
    } catch (e) {
      store.setFlash(String(e), "bad");
    } finally {
      sending = false;
    }
  }

  function close() {
    picking = false;
    picked = null;
    returnFocus = null;
    clearExitHint();
    preview.close();
  }
</script>

{#if req}
  {#snippet tools()}
    <div class="flex flex-wrap items-center gap-x-1 gap-y-1 pr-1 sm:gap-x-4 sm:pr-2">
      <h2 class="drag mr-auto self-stretch content-center text-lg text-ink">Preview</h2>
      <Tabs tabs={VIEWPORTS.map((v) => ({ id: v.id, label: v.label }))} ariaLabel="Viewport" active={viewport} onSelect={(id) => (viewport = id as ViewportId)} pill />
      <div class="ml-auto flex items-center gap-1 sm:ml-0">
        <Button size="xs" selected={picking} disabled={!src} title="Click an element in the page to send it to the agent" onclick={togglePick}>Pick element</Button>
        <Button size="xs" icon disabled={!src} aria-label="Reload" title="Reload" onclick={() => reload++}><Icon name="refresh-cw" size={14} /></Button>
        <Button size="xs" icon aria-label="Open in external browser" title="Open in external browser" onclick={() => store.openURL(req!.url)}><Icon name="external-link" size={14} /></Button>
      </div>
    </div>
  {/snippet}
  {#snippet content(req: PreviewRequest)}
    <div bind:this={surface} tabindex="-1" class="relative grid h-full min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] outline-none" data-session-preview>
      <nav aria-label="Dev servers" class="flex max-h-[30dvh] flex-wrap items-start gap-x-6 gap-y-2 overflow-y-auto border-b border-edge px-4 py-2">
        {#if !inlinePage && preview.tabs.length > 1}
          <div class="w-full" data-preview-tabs>
            <Tabs tabs={preview.tabs.map((t, i) => ({ id: String(i), label: t.title || devUrlLabel(t.url) }))} ariaLabel="Open pages" active={String(preview.active)} onSelect={(id) => preview.select(Number(id))} pill />
          </div>
        {/if}
        {#if req.caption || replay}
          <div class="flex w-full min-w-0 flex-wrap items-center gap-x-3 gap-y-1" data-preview-prep>
            <p class="min-w-0 flex-1 basis-60 text-sm wrap-anywhere text-ink">{req.caption ?? ""}{#if req.prep?.steps?.length}<span class="text-faint"> · it performs {req.prep.steps.length} {req.prep.steps.length === 1 ? "step" : "steps"} here (clicks, typing, sign-in) on this local address</span>{/if}</p>
            {#if stepFailure}<p class="text-sm text-warn" role="status">Step {stepFailure.index + 1} ({stepFailure.step}) didn't work: {stepFailure.why}</p>{/if}
            {#if missing.length}<p class="text-sm text-warn" role="status">Not found on the page: {missing.join(", ")}</p>{/if}
            {#if prepared?.waited && !missing.length}<p class="text-sm text-warn" role="status">The page did not reach the saved review target.</p>{/if}
            {#if stalled}<p class="text-sm text-warn" role="status">{documentId ? "The page did not finish its saved steps. Retry to start again." : "The dev page did not connect to the preview. Check that its server is running, then retry."}</p>{/if}
            {#if replay}<Button size="xs" disabled={!src || preparing} onclick={() => { if (stalled || stepFailure || req.prep?.steps?.length) replayFromStart(); else prepare(); }}>{stalled || stepFailure ? "Retry" : "Show it again"}</Button>{/if}
          </div>
        {/if}
        {#if inlinePage}
          <Button size="xs" selected={picking} disabled={!src || preparing} onclick={togglePick}>Pick element</Button>
        {:else}
        {#each groups as group (group.name)}
          <div role="group" aria-label={group.label || "Addresses"} class="grid min-w-0 max-w-full gap-0.5">
            {#if group.label}<h3 class="px-1.5 text-sm font-medium text-ink">{group.label}</h3>{/if}
            <div class="flex min-w-0 flex-wrap items-center gap-1">
              {#each group.urls as url (url)}
                <Button size="xs" selected={url === req.url} class="max-w-full font-mono!" title={url} onclick={() => preview.open(req.sessionId, url)}>
                  <span class="truncate">{devUrlLabel(url)}</span>
                </Button>
              {/each}
            </div>
          </div>
        {/each}
        {/if}
      </nav>

      <div class="relative min-h-0 min-w-0">
        <div class="absolute inset-0 grid place-items-center overflow-auto bg-canvas p-3">
          {#if error}
            <p class="text-sm text-bad" role="alert">{error} <Button size="xs" onclick={() => reload++}>Retry</Button></p>
          {:else if src}
            <iframe bind:this={frame} {src} onload={frameLoaded} title="Preview of {devUrlLabel(req.url)}" class="rounded border border-edge bg-white" style={frameStyle(vp)} sandbox="allow-scripts allow-same-origin allow-forms allow-popups"></iframe>
            {#if preparing}<div class="absolute inset-0 grid place-items-center bg-canvas" role="status"><span class="text-sm text-faint">Preparing live view…</span></div>{/if}
          {:else}
            <p class="text-sm text-faint">Starting preview…</p>
          {/if}
        </div>
        {#if exitHint}
          <div role="status" class="ssh-fade-in pointer-events-none absolute inset-0 z-10 grid place-items-center bg-black/35 p-4 motion-reduce:animate-none!">
            <p class="rounded-xl border border-edge bg-panel px-6 py-4 text-center text-xl leading-loose text-balance text-ink shadow-xl">
              Press <kbd class="rounded-md border border-edge bg-canvas px-2 py-1 font-mono text-lg">Esc</kbd> again to close preview
            </p>
          </div>
        {/if}
      </div>

      {#if picked}
        <form class="grid gap-2 border-t border-edge/70 px-4 py-3" onsubmit={(e) => { e.preventDefault(); void send(); }} data-preview-pick>
          <div class="flex min-w-0 items-baseline gap-2">
            <span class="text-xs text-faint">Selected</span>
            <code class="selectable min-w-0 truncate text-sm" title={picked.selector}>{picked.selector}</code>
          </div>
          <textarea class="min-h-16 w-full rounded border border-edge bg-panel p-2 text-sm" rows="2" placeholder="What should change about this element?" aria-label="Note for the agent" bind:value={note}></textarea>
          <div class="flex justify-end gap-2">
            <Button size="xs" onclick={() => (picked = null)}>Discard</Button>
            <Button size="xs" variant="primary" type="submit" loading={sending} disabled={!note.trim()}>Send to agent</Button>
          </div>
        </form>
      {/if}
    </div>
  {/snippet}
  {#if inlinePage}
    {@render content(req)}
  {:else}
    <Modal title="Preview" headerContent={tools} onClose={close} fullscreen bodyClass="min-w-0 overflow-hidden">
      {@render content(req)}
    </Modal>
  {/if}
{/if}
