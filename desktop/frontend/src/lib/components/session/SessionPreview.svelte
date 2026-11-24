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
  import { Events } from "@wailsio/runtime";
  import { isImagePath } from "$lib/composerpaste";
  import { DaemonService, PreviewService, TalkService } from "@bindings/desktop";
  import { ssh } from "$lib/ssh.svelte";
  import { store } from "$lib/store.svelte";
  import { elementForAgent, feedback, type ElementDraft } from "$lib/feedback.svelte";
  import "./annotation-editor.css";
  import { devUrlLabel } from "$lib/devurl";
  import { DEFAULT_VIEWPORT, PICKER_SOURCE, VIEWPORTS, devPageURL, previewLayout, parsePick, preview, previewIcon, type PickedElement, type PreviewRequest, type OpenPreviewTab, type ViewportId } from "$lib/preview.svelte";
  import Button from "../Button.svelte";
  import TestLogins from "./TestLogins.svelte";
  import PreviewLoadBar from "./PreviewLoadBar.svelte";
  import Icon from "../Icon.svelte";

  // The canvas keys this instance by immutable package and page. A workspace
  // refresh must not restart a live sign-in while the reviewer is looking at it.
  let { inline, tab, active = true, canComment = true, resumeKey, reviewAction, onPresetChange }: { inline?: PreviewRequest; tab?: OpenPreviewTab; reviewAction?: Snippet; active?: boolean; canComment?: boolean; resumeKey?: string; onPresetChange?: () => void } = $props();
  const inlinePage = untrack(() => inline);
  const req = $derived(inlinePage ?? tab?.request);

  let frame = $state<HTMLIFrameElement | null>(null);
  let src = $state("");
  let loadingPage = $state(false);
  const frameOrigin = $derived(src ? new URL(src).origin : "");
  let error = $state("");
  let viewport = $state<ViewportId>(untrack(() =>
    VIEWPORTS.find(v => v.id === req?.prep?.device)?.id ?? DEFAULT_VIEWPORT));
  const viewportIcons = { mobile: "smartphone", tablet: "tablet", desktop: "monitor" } as const;
  let pickerReady = $state(false);
  let picking = $state(false);
  let picked = $state<PickedElement | null>(null);
  let note = $state("");
  let sending = $state(false);
  const commentId = $props.id();
  type CommentImage = { id: number; path?: string; thumb?: string; error?: string };
  let images = $state.raw<CommentImage[]>([]);
  let imageSeq = 0, commentSeq = $state(0);
  const dropId = $derived("preview-comment-" + commentId + "-" + commentSeq);
  const imagePaths = $derived(images.flatMap(im => im.path ? [im.path] : []));
  const readyToSend = $derived((!!note.trim() || images.length > 0) && images.every(im => im.path));
  let editingDraft = $state.raw<ElementDraft>();
  const modified = $derived(note.trim() !== (editingDraft?.note ?? "") ||
    images.length !== (editingDraft?.images?.length ?? 0) || images.some((im, i) => im.path !== editingDraft?.images?.[i]));
  let editor = $state<HTMLDivElement>();
  let stage = $state<HTMLDivElement>();
  let stageWidth = $state(0), stageHeight = $state(0);
  let confirmDiscard = $state(false);
  let saveError = $state("");
  type Rect = { x: number; y: number; w: number; h: number };
  type CommentRect = Rect & { number: number };
  let bounds = $state<Rect>();
  const drafts = $derived(inlinePage ? feedback.elementsFor(inlinePage.sessionId) : []);
  let commentRects = $state.raw<CommentRect[]>([]);
  let revision = 0;
  let trackedNumbers = new Set<number>();
  let position = $state({ left: 12, top: 12 });
  $effect(() => {
    const rect = editingDraft ? commentRects.find(r => drafts[r.number - 1] === editingDraft) : bounds;
    const popup = editor, area = stage, iframe = frame, scale = layout.scale;
    if (!popup || !area || !iframe) return;
    function align() {
      const a = area!.getBoundingClientRect(), f = iframe!.getBoundingClientRect();
      const w = popup!.offsetWidth, h = popup!.offsetHeight, gap = 12;
      const x = f.left - a.left + (rect?.x ?? 0) * scale, y = f.top - a.top + (rect?.y ?? 0) * scale;
      const right = x + (rect?.w ?? 0) * scale + gap, below = y + (rect?.h ?? 0) * scale + gap;
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
  let load = $state({ route: "" });
  let openGeneration = 0;

  export function reloadPage() {
    if (!src) return;
    navigatePage(tab?.currentURL ?? "");
  }

  export function navigatePage(route: string) {
    autoAttempted = false;
    loginsOpen = false;
    load = { route };
  }

  export function backInFrame() {
    if (!frame?.contentWindow || !pickerReady || !documentId) return false;
    frame.contentWindow.postMessage({ target: PICKER_SOURCE, type: "back", documentId }, frameOrigin);
    return true;
  }

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
        return url.origin === devOrigin ? [{ number: i + 1, path: url.pathname + url.search + url.hash, selector: draft.selector }] : [];
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
    load = { route: "" };
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
    if (!active || !req || e.defaultPrevented || e.isComposing) return;
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
  const preparing = $derived(replay && !load.route && !prepared && !stalled && !error);

  const vp = $derived(VIEWPORTS.find((v) => v.id === viewport) ?? VIEWPORTS[2]);
  const layout = $derived(previewLayout(vp, stageWidth, stageHeight));
  const session = $derived(req ? store.sessions.find((s) => s.id === req.sessionId) : undefined);
  // A retained document can still run scripts after its listener is retired.
  // Remove it when a previously advertised session or stack origin disappears.
  let observedRequest: PreviewRequest | null = null;
  $effect(() => {
    const request = req;
    if (!request) { observedRequest = null; return; }
    const urls = [...(session?.devUrls ?? []), ...(session?.apps ?? []).flatMap(app => app.devUrls ?? [])];
    if (urls.some(raw => {
      try { return new URL(raw).origin === new URL(request.url).origin; }
      catch { return false; }
    })) {
      observedRequest = request;
    } else if (observedRequest === request) {
      observedRequest = null;
      openGeneration++;
      src = "";
      pickerReady = false;
      picking = false;
      error = "This session no longer reports this dev server.";
      clearTimeout(progressTimer);
    }
  });

  $effect(() => {
    const request = req;
    const route = load.route;
    loadingPage = !!request;
    if (tab) tab.canGoBackInFrame = false;
    verifiedPath = "";
    documentId = "";
    firstDocumentPath = "";
    entryPath = (resumeKey && verifiedRoutes.get(resumeKey)) || (request ? new URL(request.url).pathname : "");
    restoring = !!(finalRoute && resumeKey && verifiedRoutes.has(resumeKey));
    pendingCheck = 0;
    src = "";
    error = "";
    picked = null;
    images = [];
    editingDraft = undefined;
    picking = false;
    pickerReady = false;
    prepared = null;
    stepFailure = null;
    flow = { started: -1, armed: true, finished: false, resumes: 0 };
    clearExitHint();
    stalled = false;
    if (!request) return;
    if (replay && !route) awaitProgress();
    const generation = ++openGeneration;
    PreviewService.Open(request.sessionId, restoring ? finalRoute : route || request.url).then(
      (address) => { if (generation === openGeneration) src = address; },
      (err) => { if (generation === openGeneration) error = String(err); },
    );
    return () => { openGeneration++; clearTimeout(progressTimer); };
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
    if (!p || load.route) return;
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
    loadingPage = false;
    pendingLoginDoc = "";
    pickerReady = false;
    picking = false;
    // A navigation focuses its tab panel while Open is obtaining the frame.
    // Hand focus to the new page only if the user is still waiting there.
    if (active && tab && frame?.closest('[role="tabpanel"]') === document.activeElement) {
      frame?.focus({ preventScroll: true });
    }
    if (frameOrigin && frameOrigin !== "null") {
      frame?.contentWindow?.postMessage({ target: PICKER_SOURCE, type: "hello", watchLocation: !!tab }, frameOrigin);
    }
  }

  function tell(on: boolean) {
    if (src) frame?.contentWindow?.postMessage({ target: PICKER_SOURCE, type: "pick", on }, frameOrigin);
  }

  let loginsOpen = $state(false);
  let loginTrigger = $state<HTMLDivElement>();
  let pendingLoginDoc = "";
  let autoAttempted = false;
  let onLoginForm: (() => void) | undefined;
  $effect(() => { if (!active) { loginsOpen = false; pendingLoginDoc = ""; } });

  // Arm only the foreground page. One submission per tab load avoids loops if
  // the app redirects back to sign-in after rejecting the saved password.
  $effect(() => {
    const win = frame?.contentWindow, origin = frameOrigin, doc = documentId, request = req;
    if (!active || !tab || !win || !doc || !pickerReady || !request || loginsOpen || request.prep?.steps?.length || autoAttempted) return;
    const controller = new AbortController();
    const { signal } = controller;
    let watching = false;
    async function arm() {
      const list = await PreviewService.TestLogins(request!.sessionId, request!.url);
      const choice = list.autoLogin;
      if (signal.aborted || autoAttempted || !choice?.name || choice.origin !== new URL(request!.url).origin) return;
      onLoginForm = () => {
        onLoginForm = undefined;
        void signIn(list.project, choice.name, signal, true).catch(() => {
          if (!signal.aborted) store.setFlash("Auto-login unavailable. Open Logins to try again.", "warn");
        });
      };
      watching = true;
      win!.postMessage({ target: PICKER_SOURCE, type: "watch-login", documentId: doc, on: true }, origin);
    }
    // Older daemons and unattributed stack addresses may have no login store.
    void arm().catch(() => {});
    return () => {
      controller.abort();
      onLoginForm = undefined;
      if (watching) win.postMessage({ target: PICKER_SOURCE, type: "watch-login", documentId: doc, on: false }, origin);
    };
  });

  function closeLogins() {
    loginsOpen = false;
    loginTrigger?.querySelector("button")?.focus();
  }

  async function signIn(project: string, name: string, signal: AbortSignal, automatic = false) {
    const win = frame?.contentWindow, origin = frameOrigin, request = req;
    if (!active || !request || !win || !origin) throw new Error("The local preview is still opening.");
    if (!pickerReady || !documentId) {
      // Ask the current document to identify itself again after a redirect.
      win.postMessage({ target: PICKER_SOURCE, type: "hello", watchLocation: !!tab }, origin);
      for (let i = 0; i < 20 && (!pickerReady || !documentId); i++) {
        if (signal.aborted) return;
        await new Promise(resolve => setTimeout(resolve, 50));
      }
      if (signal.aborted) return;
      if (!active || frame?.contentWindow !== win || frameOrigin !== origin || req !== request || !pickerReady || !documentId) {
        throw new Error("The page didn’t connect to the local preview. Reload the preview and try again.");
      }
    }
    const doc = documentId;
    if (preparing && !automatic) throw new Error("Wait for the preview to finish preparing, then choose the login again.");
    const getAccount = automatic ? PreviewService.AutoTestLogin : PreviewService.UseTestLogin;
    const account = await getAccount(request.sessionId, request.url, project, name, origin);
    if (signal.aborted || !account) return;
    if (!active || frame?.contentWindow !== win || !pickerReady || documentId !== doc || frameOrigin !== origin) {
      throw new Error("The preview page changed. Choose the login again.");
    }
    autoAttempted = true;
    pendingLoginDoc = doc;
    win.postMessage({ target: PICKER_SOURCE, type: "test-login", ...(automatic && { automatic: true }), documentId: doc, username: account.username, password: account.password }, origin);
  }

  const commentAvailable = $derived(active && pickerReady && !preparing && !picked && canComment && !loginsOpen);
  $effect(() => {
    if (documentId && frame?.contentWindow) frame.contentWindow.postMessage({
      target: PICKER_SOURCE, type: "comment-shortcut", documentId, enabled: commentAvailable,
    }, frameOrigin);
  });
  export function commentKeyShortcut(e: KeyboardEvent) {
    if (!commentAvailable || e.defaultPrevented || e.isComposing || e.keyCode === 229 || e.repeat || e.key !== "c" ||
        e.altKey || e.ctrlKey || e.metaKey || e.shiftKey || e.composedPath().some(el =>
          el instanceof HTMLElement && (el.isContentEditable || el.matches('input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"], [role="combobox"], [role="searchbox"]')))) return;
    e.preventDefault();
    e.stopPropagation();
    togglePick();
  }

  function togglePick() {
    if (!commentAvailable) return;
    tell(!picking);
  }

  onMount(() => {
    const onMessage = (e: MessageEvent) => {
      // Only the framed page, and only a well-formed pick, ever reaches the form.
      if (!frame || e.source !== frame.contentWindow || !src || e.origin !== frameOrigin) return;
      const d = e.data as { source?: string; type?: string; id?: number; documentId?: string; picking?: boolean; url?: string; found?: unknown; waited?: unknown; i?: unknown; failed?: { index?: unknown; step?: unknown; why?: unknown } | null } | null;
      if (d?.source === PICKER_SOURCE && d.type === "page-loading" && documentId && d.documentId === documentId && typeof e.data.loading === "boolean") {
        loadingPage = e.data.loading;
      }
      if (d?.source === PICKER_SOURCE && d.type === "comment-shortcut" && d.documentId === documentId && commentAvailable) {
        togglePick();
        return;
      }
      if (active && tab && pickerReady && d?.source === PICKER_SOURCE && d.documentId === documentId) {
        if (d.type === "new-tab" && typeof d.url === "string") {
          let target: URL;
          try { target = new URL(d.url); } catch { return; }
          if (!["http:", "https:"].includes(target.protocol) || target.username || target.password) return;
          const address = target.origin === frameOrigin ? devPageURL(target.href, tab.request.url) : target.href;
          const servers = [...(session?.devUrls ?? []), ...(session?.apps ?? []).flatMap(app => app.devUrls ?? [])];
          preview.openLink(tab.request.sessionId, address, servers, viewport);
          return;
        }
        if (d.type === "login-form" && !loginsOpen && !autoAttempted) onLoginForm?.();
        if (d.type === "login-result" && pendingLoginDoc && pendingLoginDoc === documentId) {
          pendingLoginDoc = "";
          if (e.data.result === "submitted") store.setFlash("Sign-in submitted.");
          else if (e.data.result === "filled") store.setFlash("Login filled. Use the page’s sign-in button.");
          else store.setFlash("Open a local sign-in form, then choose the login again.", "warn");
        }
      }
      if (d?.source === PICKER_SOURCE && d.type === "mode" && pickerReady) picking = !!d.picking;
      if (inlinePage && active && d?.source === PICKER_SOURCE && d.type === "comment-bounds" && e.data.revision === revision && Array.isArray(e.data.rects)) {
        commentRects = e.data.rects.slice(0, 10).filter((r: CommentRect) =>
          r && trackedNumbers.has(r.number) && [r.x, r.y, r.w, r.h].every(Number.isFinite) && r.w > 0 && r.h > 0);
      }
      if (tab && d?.source === PICKER_SOURCE && (d.type === "ready" || d.type === "location") && typeof d.url === "string") {
        try {
          if (new URL(d.url).origin === frameOrigin) {
            const kind = e.data.kind;
            preview.visit(tab, devPageURL(d.url, tab.request.url),
              kind === "traverse" ? "traverse" : kind === "replace" || (d.type === "ready" && !documentId) ? "replace" : "push");
            tab.canGoBackInFrame = e.data.canGoBack === true;
            tab.documentTitle = typeof e.data.title === "string" ? e.data.title.trim().slice(0, 200) : "";
            tab.favicon = previewIcon(e.data.favicon, d.url, tab.currentURL);
          }
        } catch { /* Ignore malformed frame metadata. */ }
      }
      if (d?.source === PICKER_SOURCE && d.type === "ready") {
        pickerReady = true;
        picking = !!d.picking;
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
      if (d?.source === PICKER_SOURCE && active && !inlinePage && d.type === "escape" && document.activeElement === frame) {
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

  async function attachImage(file?: string) {
    if (!picked || !active || !canComment || sending) return;
    const id = ++imageSeq, root = session?.attachmentRoot || session?.worktree || "";
    images = [...images, { id }];
    let result: CommentImage;
    try {
      const attachment = file ? await TalkService.AttachImageFile(root, file) : await TalkService.AttachClipboardImage(root);
      result = { id, path: attachment.path, thumb: attachment.thumb };
    } catch (e) {
      result = { id, error: e instanceof Error ? e.message : String(e) };
    }
    // A late copy belongs only to its original tile, even if the editor reopened.
    if (images.some(im => im.id === id)) images = images.map(im => im.id === id ? result : im);
  }

  function pasteComment(e: ClipboardEvent) {
    const data = e.clipboardData;
    if (!data) return;
    const text = data.getData("text/plain");
    const hasImage = Array.from(data.items ?? []).some(item => item.type.startsWith("image/"));
    if (text && !hasImage && !isImagePath(text)) return;
    e.preventDefault();
    void attachImage(!hasImage && text ? text.trim() : undefined);
  }

  $effect(() => {
    if (!picked || !active || !canComment) return;
    const target = dropId;
    return Events.On("files:dropped", e => {
      if (e.data?.target !== target || target !== dropId) return;
      for (const path of e.data.files ?? []) {
        if (isImagePath(path)) void attachImage(path);
      }
    });
  });
  function openComment(p: PickedElement, draft?: ElementDraft) {
    commentSeq++;
    images = (draft?.images ?? []).map(path => ({ id: ++imageSeq, path, thumb: req && ssh.view(req.sessionId).sentThumbs.get(path) }));
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
    images = [];
    editingDraft = undefined;
    note = "";
    void tick().then(() => surface?.querySelector<HTMLButtonElement>('[aria-label="Comment on element"]')?.focus({ preventScroll: true }));
  }

  function closeComment() {
    if (sending) return;
    if (modified) confirmDiscard = true;
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
    if (!req || !picked || !canComment || sending || !readyToSend) return;
    sending = true;
    const sentPick = picked, sentNote = note, sentImages = images, sessionId = req.sessionId;
    const element = { url: devPageURL(picked.url, req.url), selector: picked.selector, html: picked.html, text: picked.text, note: note.trim(), ...(imagePaths.length ? { images: imagePaths } : {}) };
    try {
      const message = elementForAgent(element);
      if (inlinePage) {
        if (modified) {
          feedback.addElement(req.sessionId, { ...element, reviewKey: resumeKey }, editingDraft);
          store.setFlash("Comment saved to review feedback.", "good");
        }
      } else {
        const r = await DaemonService.SendFeedback({ session: req.sessionId, elements: [message] });
        store.setFlash(r?.delivered ? "element sent to the agent" : "element queued — it is sent when the agent is at its prompt", r?.delivered ? "good" : "warn");
        void store.refresh();
      }
      for (const image of sentImages) {
        if (image.path && image.thumb) ssh.view(sessionId).sentThumbs.set(image.path, image.thumb);
      }
      if (picked === sentPick && note === sentNote) finishComment();
    } catch (e) {
      if (picked === sentPick) saveError = String(e);
    } finally {
      sending = false;
    }
  }

  export function debugTarget() {
    return frame?.contentWindow && frameOrigin && documentId ? { frame: frame.contentWindow, origin: frameOrigin, documentId } : null;
  }
  export function viewportSize() { return { width: vp.width ?? stageWidth, height: vp.height ?? stageHeight }; }
  export function useResponsiveWidth() { viewport = "desktop"; }
  export function device() { return viewport; }

  function replayView() {
    if (load.route || stalled || stepFailure || req?.prep?.steps?.length) replayFromStart();
    else prepare();
  }

  function close() {
    returnFocus = null;
    clearExitHint();
    preview.visible = false;
  }
</script>

{#snippet issues()}
  {#if stepFailure}<p class="text-sm text-warn" role="status">Step {stepFailure.index + 1} ({stepFailure.step}) didn't work: {stepFailure.why}</p>{/if}
  {#if missing.length}<p class="text-sm text-warn" role="status">Not found on the page: {missing.join(", ")}</p>{/if}
  {#if prepared?.waited && !missing.length}<p class="text-sm text-warn" role="status">The page did not reach the saved review target.</p>{/if}
  {#if stalled}<p class="text-sm text-warn" role="status">{documentId ? "The page did not finish its saved steps. Retry to start again." : "The dev page did not connect to the preview. Check that its server is running, then retry."}</p>{/if}
{/snippet}
{#if req}
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <div bind:this={surface} onkeydown={commentKeyShortcut} tabindex="-1" class="relative grid h-full min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] outline-none" data-session-preview>
      {#if inlinePage}
        <div class="review-issues">{@render issues()}</div>
      {:else if req.caption || replay}
            <div class="flex w-full min-w-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-edge px-4 py-2" data-preview-prep>
              <p class="min-w-0 flex-1 basis-60 text-sm wrap-anywhere text-ink">{req.caption ?? ""}{#if req.prep?.steps?.length}<span class="text-faint"> · it performs {req.prep.steps.length} {req.prep.steps.length === 1 ? "step" : "steps"} here (clicks, typing, sign-in) on this local address</span>{/if}</p>
              {@render issues()}
            </div>
      {/if}

      <div bind:this={stage} bind:clientWidth={stageWidth} bind:clientHeight={stageHeight} class="row-start-2 relative min-h-0 min-w-0">
        {#if active && loadingPage && !error}<PreviewLoadBar />{/if}
        <!-- Fit the frame and its annotations together. Keep scrolling inside the page so
             WebKit has no outer scroll offset to apply when targeting a click. -->
        <div class="absolute inset-0 grid place-items-center bg-canvas grid-cols-[minmax(0,1fr)] grid-rows-[minmax(0,1fr)] {src && !error ? 'overflow-clip' : 'overflow-auto'}">
          {#if error}
            <p class="text-sm text-bad" role="alert">{error} <Button size="xs" onclick={() => load = { route: load.route }}>Retry</Button></p>
          {:else if src}
            <iframe bind:this={frame} {src} onload={frameLoaded} title="Preview of {devUrlLabel(req.url)}" class="col-start-1 row-start-1 max-w-none border-0 bg-white" style={layout.style} sandbox="allow-scripts allow-same-origin allow-forms allow-popups"></iframe>
            {#if inlinePage && commentRects.length && !preparing}
              <div class="comment-layer" style={layout.style}>
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
        <div class="review-dock" role="group" aria-label="Live page tools">
          <div role="group" aria-label="Viewport size" class="mr-0.5 flex items-center border-r border-edge pr-1">
            {#each VIEWPORTS as preset (preset.id)}
              {@const label = preset.id === "desktop" ? "Full width" : preset.label}
              <Button icon size="sm" selected={viewport === preset.id} aria-pressed={viewport === preset.id} aria-label={label} title={preset.width ? `${label} · ${preset.width} × ${preset.height}` : label} onclick={() => { viewport = preset.id; onPresetChange?.(); }}>
                <Icon name={viewportIcons[preset.id]} size={15} />
              </Button>
            {/each}
          </div>
          <Button size="sm" selected={picking} disabled={!pickerReady || preparing || !!picked || !canComment} aria-label="Comment on element" aria-keyshortcuts="c" title="Comment on an element · C" onclick={togglePick}>
            <Icon name="square-mouse-pointer" size={15} /><span>Comment</span>
          </Button>
          {#if tab}
            <div bind:this={loginTrigger}>
              <Button size="sm" selected={loginsOpen} aria-label="Test logins" aria-expanded={loginsOpen}
                onclick={() => loginsOpen = !loginsOpen}>
                <Icon name="key-round" size={15} /><span>Logins</span>
              </Button>
            </div>
            {#if active && loginsOpen}
              <TestLogins session={req.sessionId} url={req.url} {signIn} close={closeLogins} />
            {/if}
          {/if}
          {#if replay}
            <Button icon={!(stalled || stepFailure)} size="sm" disabled={!src || preparing || !!picked} aria-label={stalled || stepFailure ? "Retry preparation" : "Replay prepared view"} title={req.prep?.steps?.length ? `Replay ${req.prep.steps.length} saved ${req.prep.steps.length === 1 ? "step" : "steps"} on this local page` : "Show the prepared view again"} onclick={replayView}><Icon name="rotate-ccw" size={15} />{#if stalled || stepFailure}Retry{/if}</Button>
          {/if}
          {#if tab}
            <div role="group" aria-label="Page actions" class="ml-0.5 flex items-center border-l border-edge pl-1">
              <Button icon size="sm" disabled={!src} aria-label="Reload" aria-keyshortcuts="Meta+r" title="Reload page · ⌘R" onclick={reloadPage}><Icon name="refresh-cw" size={15} /></Button>
              <Button icon size="sm" aria-label="Open in external browser" title="Open in external browser" onclick={() => store.openURL(tab!.currentURL)}><Icon name="external-link" size={15} /></Button>
            </div>
          {/if}
          {@render reviewAction?.()}
        </div>
        {#if exitHint}
          <div role="status" class="ssh-fade-in pointer-events-none absolute inset-0 z-10 grid place-items-center bg-black/35 p-4 motion-reduce:animate-none!">
            <p class="rounded-xl border border-edge bg-panel px-6 py-4 text-center text-xl leading-loose text-balance text-ink shadow-xl">
              Press <kbd class="rounded-md border border-edge bg-canvas px-2 py-1 font-mono text-lg">Esc</kbd> again to close preview
            </p>
          </div>
        {/if}
        {#if picked}
          <div class="annotation-editor" id={dropId} data-file-drop-target onpaste={pasteComment} bind:this={editor} role="dialog" aria-label="Element comment" tabindex="-1" style:left={`${position.left}px`} style:top={`${position.top}px`} onkeydown={commentKey}>
            <div class="editor-heading">
              <label for={commentId}>Comment{editingDraft && drafts.includes(editingDraft) ? ` ${drafts.indexOf(editingDraft) + 1}` : ""}<span class="editor-side">Live</span></label>
              <div class="editor-controls">
                {#if inlinePage}
                  <Button icon size="xs" variant="danger" disabled={sending || !canComment} aria-label="Delete comment" title="Delete comment · ⌘Backspace / ⌘Delete" aria-keyshortcuts="Meta+Backspace Meta+Delete" onclick={deleteComment}><Icon name="trash-2" size={14} /></Button>
                {/if}
                <Button icon size="xs" disabled={sending} aria-label="Close comment" title="Close comment · Esc" onclick={closeComment}><Icon name="x" size={14} /></Button>
              </div>
            </div>
            <textarea id={commentId} rows="3" maxlength="2000" placeholder="What should change? Paste or drop images here." aria-label="Element comment" bind:value={note}></textarea>
            {#if images.length}
              <ul class="comment-images" aria-label="Attached images">
                {#each images as im, i (im.id)}
                  <li>
                    {#if im.thumb}<img src={im.thumb} alt={"Image " + (i + 1)}/>{/if}
                    <span class:text-warn={!!im.error} role="status">{im.error || (im.path ? "Image " + (i + 1) : "Preparing image " + (i + 1) + "…")}</span>
                    <Button icon size="xs" disabled={sending} aria-label={"Remove image " + (i + 1)} onclick={() => images = images.filter(item => item.id !== im.id)}><Icon name="x" size={12}/></Button>
                  </li>
                {/each}
              </ul>
            {/if}
            {#if confirmDiscard}<p class="save-question">Save changes?</p>{/if}
            <div class="editor-actions">
              {#if confirmDiscard}
                <Button size="xs" onclick={() => confirmDiscard = false}>Keep editing</Button>
                <Button size="xs" variant="danger" onclick={finishComment}>Discard</Button>
              {/if}
              <Button variant="primary" size="sm" disabled={!readyToSend || !canComment} loading={sending} aria-keyshortcuts="Meta+Enter Control+Enter" onclick={send}><Icon name="check" size={14} />{inlinePage ? editingDraft && !modified ? "Done" : "Save" : "Send to agent"}</Button>
            </div>
            {#if saveError}<p role="status" class="mt-2 text-xs text-warn">{saveError}</p>{/if}
          </div>
        {/if}
      </div>
    </div>
{/if}

<style>
  .annotation-editor:global(.file-drop-target-active) {outline:2px solid var(--color-accent);outline-offset:-2px}
  .comment-images {display:grid;gap:4px;margin:0 0 8px;padding:0;list-style:none}
  .comment-images li {display:flex;align-items:center;gap:8px;padding:3px 5px;border-radius:4px;background:var(--color-canvas)}
  .comment-images li span {flex:1;min-width:0;font-size:12px;overflow-wrap:anywhere}
  .comment-images img {width:36px;height:32px;object-fit:contain}
  .comment-layer { grid-area:1 / 1; position:relative; overflow:hidden; pointer-events:none; }
  .comment-border { position:absolute; border:1px solid rgb(124 92 255 / 55%); border-radius:3px; background:rgb(124 92 255 / 3%); }
  .comment-border.focused { border:2px solid #7c5cff; background:rgb(124 92 255 / 15%); }
  .comment-pin { position:absolute; z-index:1; display:grid; place-items:center; width:22px; height:22px; border:1px solid var(--color-accent); border-radius:50%; background:var(--color-panel); color:var(--color-accent); font-size:12px; font-weight:650; font-variant-numeric:tabular-nums; box-shadow:0 1px 4px #0003; pointer-events:auto; cursor:pointer; }
  .comment-pin:hover, .comment-pin:focus-visible, .comment-pin.focused { background:var(--color-accent); color:var(--color-canvas); }
  .review-issues { max-height:30dvh; overflow-y:auto; }
  .review-issues :global([role="status"]) { padding:4px 12px; overflow-wrap:anywhere; }
</style>
