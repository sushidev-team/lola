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
  import { onMount, tick, untrack, type Snippet } from "svelte";
  import { DaemonService, PreviewService } from "@bindings/desktop";
  import { store } from "$lib/store.svelte";
  import { feedback, type ElementDraft } from "$lib/feedback.svelte";
  import "./annotation-editor.css";
  import { devUrlGroups, devUrlLabel } from "$lib/devurl";
  import { DEFAULT_VIEWPORT, PICKER_SOURCE, VIEWPORTS, devPageURL, frameStyle, parsePick, preview, type PickedElement, type PreviewRequest, type ViewportId } from "$lib/preview.svelte";
  import Modal from "../Modal.svelte";
  import Button from "../Button.svelte";
  import Icon from "../Icon.svelte";
  import Tabs from "../Tabs.svelte";

  // The canvas keys this instance by immutable package and page. A workspace
  // refresh must not restart a live sign-in while the reviewer is looking at it.
  let { inline, active = true, canComment = true, resumeKey, reviewAction }: { inline?: PreviewRequest; reviewAction?: Snippet; active?: boolean; canComment?: boolean; resumeKey?: string } = $props();
  const inlinePage = untrack(() => inline);
  const req = $derived(inlinePage ?? preview.request);

  let frame = $state<HTMLIFrameElement | null>(null);
  let src = $state("");
  const frameOrigin = $derived(src ? new URL(src).origin : "");
  let error = $state("");
  let viewport = $state<ViewportId>(DEFAULT_VIEWPORT);
  const viewportIcons = { mobile: "smartphone", tablet: "tablet", desktop: "monitor" } as const;
  let picking = $state(false);
  let picked = $state<PickedElement | null>(null);
  let note = $state("");
  let sending = $state(false);
  const commentId = $props.id();
  let editor = $state<HTMLDivElement>();
  let stage = $state<HTMLDivElement>();
  let confirmDiscard = $state(false);
  let saveError = $state("");
  type Rect = { x: number; y: number; w: number; h: number };
  type CommentRect = Rect & { number: number };
  let bounds = $state<Rect>();
  let editingDraft = $state.raw<ElementDraft>();
  const drafts = $derived(inlinePage ? feedback.elementsFor(inlinePage.sessionId) : []);
  let commentRects = $state.raw<CommentRect[]>([]);
  let revision = 0;
  let trackedNumbers = new Set<number>();
  let position = $state({ left: 12, top: 12 });
  $effect(() => {
    const rect = editingDraft ? commentRects.find(r => drafts[r.number - 1] === editingDraft) : bounds;
    const popup = editor, area = stage, iframe = frame;
    if (!popup || !area || !iframe) return;
    function align() {
      const a = area!.getBoundingClientRect(), f = iframe!.getBoundingClientRect();
      const w = popup!.offsetWidth, h = popup!.offsetHeight, gap = 12;
      const x = f.left - a.left + (rect?.x ?? 0), y = f.top - a.top + (rect?.y ?? 0);
      const right = x + (rect?.w ?? 0) + gap, below = y + (rect?.h ?? 0) + gap;
      const beside = right + w <= a.width - gap || x - w >= 2 * gap;
      const left = beside ? (right + w <= a.width - gap ? right : x - w - gap) : x;
      const top = beside ? y : below + h <= a.height - gap ? below : y - h - gap;
      position = { left: Math.max(gap, Math.min(left, a.width - w - gap)), top: Math.max(gap, Math.min(top, a.height - h - gap)) };
    }
    const observer = new ResizeObserver(align);
    observer.observe(area); observer.observe(iframe); observer.observe(popup);
    area.addEventListener("scroll", align, true);
    align();
    return () => { observer.disconnect(); area.removeEventListener("scroll", align, true); };
  });
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
  $effect(() => {
    const win = frame?.contentWindow, origin = frameOrigin, doc = documentId;
    commentRects = [];
    if (!inlinePage || !active || !win || !origin || !doc) return;
    const devOrigin = new URL(inlinePage.url).origin;
    const anchors = drafts.flatMap((draft, i) => {
      if (!draft.url || (draft.reviewKey && draft.reviewKey !== resumeKey)) return [];
      try {
        const url = new URL(draft.url);
        return url.origin === devOrigin ? [{ number: i + 1, path: url.pathname + url.search, selector: draft.selector }] : [];
      } catch { return []; }
    });
    if (!anchors.length) return;
    trackedNumbers = new Set(anchors.map(a => a.number));
    const message = { target: PICKER_SOURCE, type: "comment-anchors", documentId: doc, revision: ++revision };
    win.postMessage({ ...message, anchors }, origin);
    return () => {
      revision++;
      trackedNumbers.clear();
      win.postMessage({ ...message, anchors: [] }, origin);
    };
  });
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
    editingDraft = undefined;
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
    if ((replay || inlinePage) && frameOrigin && frameOrigin !== "null") {
      frame?.contentWindow?.postMessage({ target: PICKER_SOURCE, type: "hello" }, frameOrigin);
    }
  }

  function tell(on: boolean) {
    if (src) frame?.contentWindow?.postMessage({ target: PICKER_SOURCE, type: "pick", on }, frameOrigin);
  }

  function togglePick() {
    if (picked || !canComment) return;
    picking = !picking;
    tell(picking);
  }

  onMount(() => {
    const onMessage = (e: MessageEvent) => {
      // Only the framed page, and only a well-formed pick, ever reaches the form.
      if (!frame || e.source !== frame.contentWindow || !src || e.origin !== frameOrigin) return;
      const d = e.data as { source?: string; type?: string; id?: number; documentId?: string; picking?: boolean; url?: string; found?: unknown; waited?: unknown; i?: unknown; failed?: { index?: unknown; step?: unknown; why?: unknown } | null } | null;
      if (d?.source === PICKER_SOURCE && d.type === "mode") picking = !!d.picking;
      if (inlinePage && active && d?.source === PICKER_SOURCE && d.type === "comment-bounds" && e.data.revision === revision && Array.isArray(e.data.rects)) {
        commentRects = e.data.rects.slice(0, 10).filter((r: CommentRect) =>
          r && trackedNumbers.has(r.number) && [r.x, r.y, r.w, r.h].every(Number.isFinite) && r.w > 0 && r.h > 0);
      }
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
      if (d?.source === PICKER_SOURCE && d.type === "selection-bounds" && picked) {
        const r = e.data.rect;
        if (r && [r.x, r.y, r.w, r.h].every(Number.isFinite)) bounds = { x: r.x, y: r.y, w: r.w, h: r.h };
      }
      const p = parsePick(e.data);
      if (p && picking && active && canComment && !picked) {
        openComment(p);
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

  function openComment(p: PickedElement, draft?: ElementDraft) {
    picked = p;
    editingDraft = draft;
    bounds = undefined;
    confirmDiscard = false;
    saveError = "";
    note = draft?.note ?? "";
    void tick().then(() => editor?.querySelector("textarea")?.focus({ preventScroll: true }));
  }

  function reopenComment(draft: ElementDraft) {
    if (picked || !canComment) return;
    if (picking) tell(false);
    openComment({ url: draft.url ?? "", selector: draft.selector, html: draft.html ?? "", text: draft.text ?? "", tag: "" }, draft);
  }

  function finishComment() {
    tell(false);
    picked = null;
    editingDraft = undefined;
    note = "";
    void tick().then(() => surface?.querySelector<HTMLButtonElement>('[aria-label="Inspect element"]')?.focus({ preventScroll: true }));
  }

  function closeComment() {
    if (sending) return;
    if (note.trim() !== (editingDraft?.note ?? "")) confirmDiscard = true;
    else finishComment();
  }

  function deleteComment() {
    if (!inlinePage || !canComment || sending) return;
    if (editingDraft) feedback.removeElement(inlinePage.sessionId, editingDraft);
    finishComment();
  }

  function commentKey(e: KeyboardEvent) {
    if (e.isComposing || e.repeat) return;
    if (inlinePage && e.metaKey && !e.ctrlKey && !e.altKey && !e.shiftKey && (e.key === "Backspace" || e.key === "Delete")) {
      e.preventDefault(); e.stopPropagation();
      deleteComment();
      return;
    }
    if (e.key === "Escape" || (e.key === "Enter" && (e.metaKey || e.ctrlKey))) {
      e.preventDefault(); e.stopPropagation();
      if (e.key === "Escape") closeComment();
      else void send();
    }
  }

  async function send() {
    if (!req || !picked || !canComment || sending || !note.trim()) return;
    sending = true;
    const element = { url: devPageURL(picked.url, req.url), selector: picked.selector, html: picked.html, text: picked.text, note: note.trim() };
    try {
      if (inlinePage) {
        if (!editingDraft || editingDraft.note !== note.trim()) {
          feedback.addElement(req.sessionId, { ...element, reviewKey: resumeKey }, editingDraft);
          store.setFlash("Comment saved to review feedback.", "good");
        }
      } else {
        const r = await DaemonService.SendFeedback({ session: req.sessionId, elements: [element] });
        store.setFlash(r?.delivered ? "element sent to the agent" : "element queued — it is sent when the agent is at its prompt", r?.delivered ? "good" : "warn");
        void store.refresh();
      }
      finishComment();
    } catch (e) {
      saveError = String(e);
    } finally {
      sending = false;
    }
  }

  function replayView() {
    if (stalled || stepFailure || req?.prep?.steps?.length) replayFromStart();
    else prepare();
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
        <Button size="xs" selected={picking} disabled={!src || !!picked} title="Click an element in the page to send it to the agent" onclick={togglePick}>Pick element</Button>
        <Button size="xs" icon disabled={!src} aria-label="Reload" title="Reload" onclick={() => reload++}><Icon name="refresh-cw" size={14} /></Button>
        <Button size="xs" icon aria-label="Open in external browser" title="Open in external browser" onclick={() => store.openURL(req!.url)}><Icon name="external-link" size={14} /></Button>
      </div>
    </div>
  {/snippet}
  {#snippet issues()}
    {#if stepFailure}<p class="text-sm text-warn" role="status">Step {stepFailure.index + 1} ({stepFailure.step}) didn't work: {stepFailure.why}</p>{/if}
    {#if missing.length}<p class="text-sm text-warn" role="status">Not found on the page: {missing.join(", ")}</p>{/if}
    {#if prepared?.waited && !missing.length}<p class="text-sm text-warn" role="status">The page did not reach the saved review target.</p>{/if}
    {#if stalled}<p class="text-sm text-warn" role="status">{documentId ? "The page did not finish its saved steps. Retry to start again." : "The dev page did not connect to the preview. Check that its server is running, then retry."}</p>{/if}
  {/snippet}
  {#snippet content(req: PreviewRequest)}
    <div bind:this={surface} tabindex="-1" class="relative grid h-full min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] outline-none" data-session-preview>
      {#if inlinePage}
        <div class="review-issues">{@render issues()}</div>
      {:else}
        <nav aria-label="Dev servers" class="flex max-h-[30dvh] flex-wrap items-start gap-x-6 gap-y-2 overflow-y-auto border-b border-edge px-4 py-2">
          {#if preview.tabs.length > 1}
            <div class="w-full" data-preview-tabs>
              <Tabs tabs={preview.tabs.map((t, i) => ({ id: String(i), label: t.title || devUrlLabel(t.url) }))} ariaLabel="Open pages" active={String(preview.active)} onSelect={(id) => preview.select(Number(id))} pill />
            </div>
          {/if}
          {#if req.caption || replay}
            <div class="flex w-full min-w-0 flex-wrap items-center gap-x-3 gap-y-1" data-preview-prep>
              <p class="min-w-0 flex-1 basis-60 text-sm wrap-anywhere text-ink">{req.caption ?? ""}{#if req.prep?.steps?.length}<span class="text-faint"> · it performs {req.prep.steps.length} {req.prep.steps.length === 1 ? "step" : "steps"} here (clicks, typing, sign-in) on this local address</span>{/if}</p>
              {@render issues()}
              {#if replay}<Button size="xs" disabled={!src || preparing} onclick={replayView}>{stalled || stepFailure ? "Retry" : "Show it again"}</Button>{/if}
            </div>
          {/if}
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
        </nav>
      {/if}

      <div bind:this={stage} class="relative min-h-0 min-w-0">
        <div class="absolute inset-0 grid place-items-center overflow-auto bg-canvas {inlinePage ? 'grid-cols-[minmax(0,1fr)] grid-rows-[minmax(0,1fr)]' : 'p-3'}">
          {#if error}
            <p class="text-sm text-bad" role="alert">{error} <Button size="xs" onclick={() => reload++}>Retry</Button></p>
          {:else if src}
            <iframe bind:this={frame} {src} onload={frameLoaded} title="Preview of {devUrlLabel(req.url)}" class="bg-white {inlinePage ? 'col-start-1 row-start-1 max-h-full max-w-full border-0' : 'rounded border border-edge'}" style={frameStyle(vp)} sandbox="allow-scripts allow-same-origin allow-forms allow-popups"></iframe>
            {#if inlinePage && commentRects.length && !preparing}
              <div class="comment-layer" style={frameStyle(vp)}>
                {#each commentRects as r,i}
                  {@const draft = drafts[r.number - 1]}
                  {@const slot = commentRects.slice(0, i).filter(other => Math.abs(other.x - r.x) < 24 && Math.abs(other.y - r.y) < 24).length}
                  {#if draft}
                    <div class="comment-border" class:focused={draft === editingDraft} style:left={`${r.x}px`} style:top={`${r.y}px`} style:width={`${r.w}px`} style:height={`${r.h}px`}></div>
                    <button class="comment-pin" class:focused={draft === editingDraft} style:left={`clamp(0px, ${r.x + 4 + slot * 24}px, 100% - 22px)`} style:top={`clamp(0px, ${r.y - 11}px, 100% - 22px)`} aria-label={`Live comment ${r.number}`} aria-expanded={draft === editingDraft} title={draft.note} disabled={!canComment || !!picked} onclick={() => reopenComment(draft)}>{r.number}</button>
                  {/if}
                {/each}
              </div>
            {/if}
            {#if preparing}<div class="absolute inset-0 grid place-items-center bg-canvas" role="status"><span class="text-sm text-faint">Preparing live view…</span></div>{/if}
          {:else}
            <p class="text-sm text-faint">Starting preview…</p>
          {/if}
        </div>
        {#if inlinePage}
            <div class="review-dock" role="group" aria-label="Live page tools">
              <div role="group" aria-label="Viewport size" class="mr-0.5 flex items-center border-r border-edge pr-1">
                {#each VIEWPORTS as preset (preset.id)}
                  {@const label = preset.id === "desktop" ? "Full width" : preset.label}
                  <Button icon size="sm" selected={viewport === preset.id} aria-pressed={viewport === preset.id} aria-label={label} title={preset.width ? `${label} · ${preset.width} × ${preset.height}` : label} onclick={() => viewport = preset.id}>
                    <Icon name={viewportIcons[preset.id]} size={15} />
                  </Button>
                {/each}
              </div>
              <Button size="sm" selected={picking} disabled={!src || preparing || !!picked || !canComment} aria-label="Inspect element" title="Inspect an element to send feedback" onclick={togglePick}>
                <Icon name="square-mouse-pointer" size={15} /><span>Inspect</span>
              </Button>
              {#if replay}
                <Button icon={!(stalled || stepFailure)} size="sm" disabled={!src || preparing || !!picked} aria-label={stalled || stepFailure ? "Retry preparation" : "Replay prepared view"} title={req.prep?.steps?.length ? `Replay ${req.prep.steps.length} saved ${req.prep.steps.length === 1 ? "step" : "steps"} on this local page` : "Show the prepared view again"} onclick={replayView}><Icon name="rotate-ccw" size={15} />{#if stalled || stepFailure}Retry{/if}</Button>
              {/if}
              {@render reviewAction?.()}
            </div>
        {/if}
        {#if exitHint}
          <div role="status" class="ssh-fade-in pointer-events-none absolute inset-0 z-10 grid place-items-center bg-black/35 p-4 motion-reduce:animate-none!">
            <p class="rounded-xl border border-edge bg-panel px-6 py-4 text-center text-xl leading-loose text-balance text-ink shadow-xl">
              Press <kbd class="rounded-md border border-edge bg-canvas px-2 py-1 font-mono text-lg">Esc</kbd> again to close preview
            </p>
          </div>
        {/if}
        {#if picked}
          <div class="annotation-editor" bind:this={editor} role="dialog" aria-label="Element comment" tabindex="-1" style:left={`${position.left}px`} style:top={`${position.top}px`} onkeydown={commentKey}>
            <div class="editor-heading">
              <label for={commentId}>Comment{editingDraft && drafts.includes(editingDraft) ? ` ${drafts.indexOf(editingDraft) + 1}` : ""}<span class="editor-side">Live</span></label>
              <div class="editor-controls">
                {#if inlinePage}
                  <Button icon size="xs" variant="danger" disabled={sending || !canComment} aria-label="Delete comment" title="Delete comment · ⌘Backspace / ⌘Delete" aria-keyshortcuts="Meta+Backspace Meta+Delete" onclick={deleteComment}><Icon name="trash-2" size={14} /></Button>
                {/if}
                <Button icon size="xs" disabled={sending} aria-label="Close comment" title="Close comment · Esc" onclick={closeComment}><Icon name="x" size={14} /></Button>
              </div>
            </div>
            <textarea id={commentId} rows="3" maxlength="2000" placeholder="What should change?" aria-label="Element comment" bind:value={note}></textarea>
            {#if confirmDiscard}<p class="save-question">Save changes?</p>{/if}
            <div class="editor-actions">
              {#if confirmDiscard}
                <Button size="xs" onclick={() => confirmDiscard = false}>Keep editing</Button>
                <Button size="xs" variant="danger" onclick={finishComment}>Discard</Button>
              {/if}
              <Button variant="primary" size="sm" disabled={!note.trim()} loading={sending} aria-keyshortcuts="Meta+Enter Control+Enter" onclick={send}><Icon name="check" size={14} />{inlinePage ? editingDraft?.note === note.trim() ? "Done" : "Save" : "Send to agent"}</Button>
            </div>
            {#if saveError}<p role="status" class="mt-2 text-xs text-warn">{saveError}</p>{/if}
          </div>
        {/if}
      </div>
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

<style>
  .comment-layer { grid-area:1 / 1; position:relative; max-width:100%; max-height:100%; overflow:hidden; pointer-events:none; }
  .comment-border { position:absolute; border:1px solid rgb(124 92 255 / 55%); border-radius:3px; background:rgb(124 92 255 / 3%); }
  .comment-border.focused { border:2px solid #7c5cff; background:rgb(124 92 255 / 15%); }
  .comment-pin { position:absolute; z-index:1; display:grid; place-items:center; width:22px; height:22px; border:1px solid var(--color-accent); border-radius:50%; background:var(--color-panel); color:var(--color-accent); font-size:12px; font-weight:650; font-variant-numeric:tabular-nums; box-shadow:0 1px 4px #0003; pointer-events:auto; cursor:pointer; }
  .comment-pin:hover, .comment-pin:focus-visible, .comment-pin.focused { background:var(--color-accent); color:var(--color-canvas); }
  .review-issues { max-height:30dvh; overflow-y:auto; }
  .review-issues :global([role="status"]) { padding:4px 12px; overflow-wrap:anywhere; }
</style>
