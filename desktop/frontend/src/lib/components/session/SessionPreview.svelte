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
  import { flushSync, onMount, tick, untrack, type Snippet } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { isImagePath } from "$lib/composerpaste";
  import { PreviewService, TalkService } from "@bindings/desktop";
  import { ssh } from "$lib/ssh.svelte";
  import { store } from "$lib/store.svelte";
  import { confirm } from "$lib/confirm.svelte";
  import { annotationCaption, elementForAgent, feedback, type ElementDraft } from "$lib/feedback.svelte";
  import { annotationSnapshot } from "$lib/annotationsnapshot";
  import "./annotation-editor.css";
  import { devUrlLabel, loginSite } from "$lib/devurl";
  import { DEFAULT_VIEWPORT, PICKER_SOURCE, VIEWPORTS, devPageURL, previewLayout, parsePick, preview, previewIcon, type PickedElement, type PreviewRequest, type OpenPreviewTab, type ViewportId } from "$lib/preview.svelte";
  import { labelFontSize, type Point, type PreviewMark, type PreviewTool, type Rect } from "$lib/previewmark";
  import PreviewDrawing from "./PreviewDrawing.svelte";
  import DrawnMark from "./PreviewMark.svelte";
  import Chevron from "../Chevron.svelte";
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
  const commentId = $props.id();
  type CommentImage = { id: number; path?: string; thumb?: string; error?: string };
  let images = $state.raw<CommentImage[]>([]);
  let imageSeq = 0, commentSeq = $state(0);
  const dropId = $derived("preview-comment-" + commentId + "-" + commentSeq);
  const imagePaths = $derived(images.flatMap(im => im.path ? [im.path] : []));
  let mark = $state.raw<PreviewMark>();
  const editingLabel = $derived(!!picked && mark?.kind === "text");
  let label = $state("");
  let labelMeasure = $state<HTMLSpanElement>();
  let labelInput = $state<HTMLTextAreaElement>();
  let labelWidth = $state(0), labelHeight = $state(0);
  let labelRoom = $state({ w: Infinity, h: Infinity });
  // Fit the editor at its placement point once. Scrolling the site then moves
  // the editor and its anchor together, including past the viewport edges.
  const labelShift = $derived({ x: Math.max(0, labelWidth - labelRoom.w), y: Math.max(0, labelHeight - labelRoom.h) });
  const readyToSave = $derived((mark?.kind === "text" ? !!label.trim() : !!mark || !!note.trim() || images.length > 0) && images.every(im => im.path));
  let editingDraft = $state.raw<ElementDraft>();
  const modified = $derived((mark?.kind === "text" && label.trim() !== (editingDraft?.mark?.label ?? "")) || note.trim() !== (editingDraft?.note ?? "") ||
    images.length !== (editingDraft?.images?.length ?? 0) || images.some((im, i) => im.path !== editingDraft?.images?.[i]));
  let editor = $state<HTMLDivElement>();
  let stage = $state<HTMLDivElement>();
  let stageWidth = $state(0), stageHeight = $state(0);
  let confirmDiscard = $state(false);
  let saveError = $state("");
  let tool = $state<PreviewTool>("comment");
  const tools = [
    { id: "area", key: "r", label: "Draw rectangle", icon: "square" },
    { id: "arrow", key: "a", label: "Draw arrow", icon: "arrow-up-right" },
    { id: "pen", key: "p", label: "Draw freehand", icon: "pencil" },
    { id: "text", key: "t", label: "Place text label", icon: "type" },
  ] as const;
  const shortcutKeys = ["c", ...tools.map(t => t.key)];
  function toolForKey(key: unknown): PreviewTool | undefined {
    return key === "c" ? "comment" : tools.find(t => t.key === key)?.id;
  }
  let pendingMark = $state.raw<PreviewMark>();
  let capturing = $state.raw<PreviewMark>();
  let targets = $state.raw<string[]>([]);
  let moreTargets = $state(false);
  let selectedDraft = $state.raw<ElementDraft>();
  let editSeq = 0;
  let pendingEdit = $state.raw<{ id: number; previous: ElementDraft; mark: PreviewMark }>();
  $effect(() => {
    if (!pendingEdit || capturing) return;
    const timer = setTimeout(() => {
      pendingEdit = undefined;
      store.setFlash("The page didn’t answer. Try moving the annotation again.", "warn");
    }, 3000);
    return () => clearTimeout(timer);
  });
  function inspect(point: Point, drawn?: PreviewMark, previous?: ElementDraft) {
    if (!frame?.contentWindow || !commentAvailable || pendingMark || pendingEdit || (!previous && !picking)) return;
    if (previous && drawn) pendingEdit = { id: ++editSeq, previous, mark: drawn };
    else pendingMark = drawn;
    frame?.contentWindow?.postMessage({
      target: PICKER_SOURCE, documentId, type: "pick-at", ...point,
      start: drawn?.kind === "arrow" ? { x: drawn.x + drawn.points[0], y: drawn.y + drawn.points[1] } : undefined,
      editId: pendingEdit?.id, label: drawn?.kind === "text",
      region: drawn?.kind === "area" || drawn?.kind === "pen",
      rect: drawn && { x: drawn.x, y: drawn.y, w: drawn.w, h: drawn.h },
    }, frameOrigin);
  }
  function changeMark(draft: ElementDraft, moved: PreviewMark) {
    const tip = moved.kind === "arrow" ? { x: moved.points[2], y: moved.points[3] } : { x: 0, y: 0 };
    inspect({ x: moved.x + tip.x, y: moved.y + tip.y }, moved, draft);
  }
  function hover(point: Point) {
    frame?.contentWindow?.postMessage({target: PICKER_SOURCE, documentId, type: "hover-at", ...point}, frameOrigin);
  }
  type CommentRect = Rect & { number: number; originX?: number; originY?: number };
  let bounds = $state<Rect>();
  const drafts = $derived(req ? feedback.elementsFor(req.sessionId) : []);
  const batchCount = $derived(drafts.length + (picked && !editingDraft && readyToSave ? 1 : 0));
  let commentRects = $state.raw<CommentRect[]>([]);
  const draftRect = $derived(editingDraft ? commentRects.find(r => drafts[r.number - 1] === editingDraft) : undefined);
  const labelPosition = $derived(draftRect ? { x: draftRect.originX ?? draftRect.x, y: draftRect.originY ?? draftRect.y } : bounds);
  // Older labels stored only document coordinates. When their original point
  // comes into view, resolve it through the same placement path as a drag.
  function bindLegacyLabel() {
    if (!req || picking || picked || pendingMark || pendingEdit || feedback.isSending(req.sessionId)) return;
    for (const r of commentRects) {
      const draft = drafts[r.number - 1];
      if (draft?.selector !== ":root" || draft.mark?.kind !== "text" || !draft.mark.page) continue;
      if (r.originX !== r.x || r.originY !== r.y) continue;
      changeMark(draft, { ...draft.mark, x: r.x, y: r.y });
      break;
    }
  }
  let revision = 0;
  let trackedNumbers = new Set<number>();
  let position = $state({ left: 12, top: 12 });
  $effect(() => {
    const rect = draftRect ?? bounds;
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
  let trackedDocument = "";
  let measuredDrafts: ElementDraft[] = [];
  $effect(() => {
    const iframe = frame, win = iframe?.contentWindow, origin = frameOrigin, doc = documentId;
    if (!req || !active || !win || !origin || !doc) {
      commentRects = [];
      trackedDocument = "";
      return;
    }
    // Keep unchanged marks mounted during remeasurement, including a focused resize handle.
    const previousRects = untrack(() => commentRects);
    commentRects = doc === trackedDocument ? previousRects.filter(r => measuredDrafts[r.number - 1] === drafts[r.number - 1]) : [];
    measuredDrafts = [...drafts];
    trackedDocument = doc;
    const devOrigin = new URL(req.url).origin;
    const anchors = drafts.flatMap((draft, i) => {
      if (!draft.url || (draft.reviewKey && draft.reviewKey !== resumeKey)) return [];
      try {
        const url = new URL(draft.url);
        return url.origin === devOrigin ? [{ number: i + 1, path: url.pathname + url.search + url.hash, selector: draft.selector,
          area: draft.mark && { label: draft.mark.kind === "text", x: draft.mark.x, y: draft.mark.y, w: draft.mark.w, h: draft.mark.h } }] : [];
      } catch { return []; }
    });
    if (!anchors.length) return;
    trackedNumbers = new Set(anchors.map(a => a.number));
    const message = { target: PICKER_SOURCE, type: "comment-anchors", documentId: doc, revision: ++revision };
    win.postMessage({ ...message, anchors }, origin);
    return () => {
      revision++;
      trackedNumbers.clear();
      if (iframe?.isConnected) win.postMessage({ ...message, anchors: [] }, origin);
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
  let exitHint = $state<"clear" | "close" | null>(null);
  const hasFeedback = $derived(canComment && !!(drafts.length || picked || pendingMark || pendingEdit));
  const exitWindowMs = 3000;
  let exitDeadline = 0;
  let exitTimer: ReturnType<typeof setTimeout>;
  let returnFocus: HTMLIFrameElement | null = null;

  function clearExitHint() {
    clearTimeout(exitTimer);
    exitDeadline = 0;
    exitHint = null;
    if (returnFocus && document.activeElement === surface) returnFocus.focus({ preventScroll: true });
    returnFocus = null;
  }

  function pointerDown(e: PointerEvent) {
    clearExitHint();
    const hit = e.target instanceof Element && e.target.closest("[data-preview-mark]");
    if (!hit || !surface?.contains(hit)) selectedDraft = undefined;
  }

  function showExitHint() {
    clearTimeout(exitTimer);
    exitDeadline = performance.now() + exitWindowMs;
    exitHint = hasFeedback ? "clear" : "close";
    exitTimer = setTimeout(clearExitHint, exitWindowMs);
  }

  function onPreviewKey(e: KeyboardEvent) {
    if (!active || !req || confirm.request || e.defaultPrevented || e.isComposing) return;
    if (e.key !== "Escape" || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) {
      if (exitHint) clearExitHint();
      return;
    }
    e.preventDefault();
    if (e.repeat) return;
    if (hasFeedback) {
      if (exitHint === "clear" && performance.now() < exitDeadline) clearAnnotations();
      else showExitHint();
    } else if (!inlinePage) {
      if (exitHint === "close" && performance.now() < exitDeadline) close();
      else showExitHint();
    }
  }


  function feedbackKey(e: KeyboardEvent) {
    if (exitHint && e.key !== "Escape") clearExitHint();
    if (!hasFeedback || loginsOpen || e.key !== "Escape" || e.isComposing || e.altKey || e.metaKey || e.ctrlKey || e.shiftKey) return;
    onPreviewKey(e);
    e.stopPropagation();
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
    selectedDraft = undefined;
    pendingEdit = undefined;
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
    pendingMark = undefined;
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
    pendingMark = undefined;
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
    if (!on) pendingMark = undefined;
    if (src) frame?.contentWindow?.postMessage({ target: PICKER_SOURCE, type: "pick", on, tool }, frameOrigin);
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
      if (signal.aborted || autoAttempted || !choice?.name || choice.origin !== loginSite(request!.url)) return;
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

  const toolsAvailable = $derived(active && pickerReady && !preparing && canComment && !loginsOpen && !confirm.request);
  const commentAvailable = $derived(toolsAvailable && !picked);
  const canChooseTool = $derived(toolsAvailable && !pendingMark && !pendingEdit && (!picked || readyToSave || !modified));
  $effect(() => {
    if (documentId && frame?.contentWindow) frame.contentWindow.postMessage({
      target: PICKER_SOURCE, type: "comment-shortcut", documentId, enabled: commentAvailable, keys: shortcutKeys,
    }, frameOrigin);
  });
  export function annotationKeyShortcut(e: KeyboardEvent) {
    if (!commentAvailable || e.defaultPrevented || e.isComposing || e.keyCode === 229 || e.repeat ||
        e.altKey || e.ctrlKey || e.metaKey || e.shiftKey || e.composedPath().some(el =>
          el instanceof HTMLElement && (el.isContentEditable || el.matches('input, textarea, select, [contenteditable]:not([contenteditable="false"]), [role="textbox"], [role="combobox"], [role="searchbox"]')))) return;
    if (e.key === "Backspace" || e.key === "Delete") {
      const draft = selectedDraft;
      if (!req || !draft || !drafts.includes(draft) || feedback.isSending(req.sessionId)) return;
      e.preventDefault();
      e.stopPropagation();
      selectedDraft = undefined;
      if (pendingEdit?.previous === draft) pendingEdit = undefined;
      surface?.focus({ preventScroll: true });
      feedback.removeElement(req.sessionId, draft);
      return;
    }
    const next = toolForKey(e.key);
    if (!next) return;
    e.preventDefault();
    e.stopPropagation();
    togglePick(next);
  }

  function togglePick(next: PreviewTool = "comment") {
    if (!canChooseTool) return;
    const on = !!picked || !picking || next !== tool;
    if (picked) {
      if (!readyToSave) finishComment(false);
      else if (!saveComment(false)) return;
    }
    selectedDraft = undefined;
    tool = next;
    pendingMark = undefined;
    tell(on);
  }

  onMount(() => {
    const onMessage = async (e: MessageEvent) => {
      // Only the framed page, and only a well-formed pick, ever reaches the form.
      if (!frame || e.source !== frame.contentWindow || !src || e.origin !== frameOrigin) return;
      const d = e.data as { source?: string; type?: string; id?: number; documentId?: string; picking?: boolean; url?: string; found?: unknown; waited?: unknown; i?: unknown; failed?: { index?: unknown; step?: unknown; why?: unknown } | null } | null;
      if (d?.source === PICKER_SOURCE && d.type === "page-loading" && documentId && d.documentId === documentId && typeof e.data.loading === "boolean") {
        loadingPage = e.data.loading;
      }
      if (d?.source === PICKER_SOURCE && d.type === "comment-shortcut" && d.documentId === documentId && commentAvailable) {
        const next = toolForKey(e.data.key ?? "c");
        if (next) togglePick(next);
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
      if (active && d?.source === PICKER_SOURCE && d.type === "annotation-deselect" && d.documentId === documentId) {
        selectedDraft = undefined;
        clearExitHint();
        return;
      }
      if (active && d?.source === PICKER_SOURCE && d.type === "comment-bounds" && e.data.revision === revision && Array.isArray(e.data.rects)) {
        commentRects = e.data.rects.slice(0, 10).filter((r: CommentRect) =>
          r && trackedNumbers.has(r.number) && [r.x, r.y, r.w, r.h].every(Number.isFinite) && r.w > 0 && r.h > 0 && (r.originX === undefined || Number.isFinite(r.originX)) && (r.originY === undefined || Number.isFinite(r.originY)));
        bindLegacyLabel();
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
        pendingMark = undefined;
        pendingEdit = undefined;
        selectedDraft = undefined;
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
      if (d?.source === PICKER_SOURCE && active && (!inlinePage || hasFeedback) && d.type === "escape" && document.activeElement === frame) {
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
      const editId = e.data?.editId;
      const editing = editId !== undefined ? pendingEdit : undefined;
      if (editId !== undefined && (!editing || editId !== editing.id || d?.documentId !== documentId)) return;
      if (p && (editing || picking) && active && canComment && !picked) {
        if (capturing || (d?.documentId && d.documentId !== documentId)) return;
        const drawn = editing?.mark ?? pendingMark;
        const r = e.data.rect;
        if (!drawn) {
          openEditor(p);
          return;
        }
        let arrow: PreviewMark["arrow"];
        const evidence = e.data.arrow;
        if (drawn.kind === "arrow" && typeof evidence?.tail === "string" && typeof evidence.tip === "string") {
          arrow = { tail: evidence.tail.slice(0, 650), tip: evidence.tip.slice(0, 650) };
          if (evidence.embedded === true && req) {
            const doc = documentId, request = req, root = session?.attachmentRoot || session?.worktree || "";
            const current = () => active && frame?.isConnected && doc === documentId && req === request &&
              drawn === (editing ? pendingEdit?.mark : pendingMark);
            try {
              flushSync(() => { capturing = drawn; });
              const jpg = await annotationSnapshot(frame, drawn, layout.scale);
              if (current()) arrow.snapshot = await PreviewService.SaveAnnotationImage(root, jpg);
            } catch {
              if (current()) store.setFlash("Couldn’t attach the marked preview. The arrow still includes its endpoints.", "warn");
            } finally {
              capturing = undefined;
            }
            if (!current()) {
              if (pendingMark === drawn) pendingMark = undefined;
              if (editing && pendingEdit === editing) pendingEdit = undefined;
              return;
            }
          }
        }
        pendingEdit = undefined;
        pendingMark = undefined;
        if (!req || !r || !Number.isFinite(r.x) || !Number.isFinite(r.y)) return;
        const placed = { ...drawn, arrow, ...(drawn.kind === "text" && { page: undefined }), x: drawn.x - r.x, y: drawn.y - r.y };
        const enclosed = Array.isArray(e.data.targets)
          ? e.data.targets.slice(0, 16).filter((t: unknown) => typeof t === "string").map((t: string) => t.slice(0, 256)) : [];
        if (editing) {
          const old = editing.previous;
          const anchor = elementDraft(p);
          if (!drafts.includes(old) || old.url !== anchor.url) return;
          const updated = { ...old, selector: anchor.selector, html: anchor.html, text: anchor.text, mark: placed, targets: enclosed, moreTargets: e.data.moreTargets === true };
          const number = drafts.indexOf(old) + 1;
          measuredDrafts[number - 1] = updated;
          commentRects = commentRects.map(box => box.number === number ? { number, x: drawn.x, y: drawn.y, w: drawn.w, h: drawn.h } : box);
          feedback.addElement(req.sessionId, updated, old);
          if (selectedDraft === old) selectedDraft = updated;
        } else if (tool === "comment" || drawn.kind === "text") {
          openEditor(p, undefined, placed, drawn);
          targets = enclosed;
          moreTargets = e.data.moreTargets === true;
        } else {
          try {
            feedback.addElement(req.sessionId, { ...elementDraft(p), mark: placed, targets: enclosed, moreTargets: e.data.moreTargets === true });
            tell(true); // The iframe disarms after resolving a pick. Keep this drawing tool active.
          } catch (err) { store.setFlash(String(err), "bad"); }
        }
      }
    };
    window.addEventListener("message", onMessage);
    if (!inlinePage) window.addEventListener("keydown", onPreviewKey);
    else window.addEventListener("focus", checkSession);
    window.addEventListener("pointerdown", pointerDown, true);
    return () => {
      window.removeEventListener("message", onMessage);
      window.removeEventListener("keydown", onPreviewKey);
      window.removeEventListener("focus", checkSession);
      window.removeEventListener("pointerdown", pointerDown, true);
      clearExitHint();
    };
  });

  async function attachImage(file?: string) {
    if (!picked || editingLabel || !active || !canComment) return;
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
    if (!picked || editingLabel || !active || !canComment) return;
    const target = dropId;
    return Events.On("files:dropped", e => {
      if (e.data?.target !== target || target !== dropId) return;
      for (const path of e.data.files ?? []) {
        if (isImagePath(path)) void attachImage(path);
      }
    });
  });
  function openEditor(p: PickedElement, draft?: ElementDraft, drawing = draft?.mark, placement?: Rect) {
    label = draft?.mark?.label ?? "";
    commentSeq++;
    images = (draft?.images ?? []).map(path => ({ id: ++imageSeq, path, thumb: req && ssh.view(req.sessionId).sentThumbs.get(path) }));
    picked = p;
    editingDraft = draft;
    bounds = placement;
    mark = drawing;
    if (frame && drawing?.kind === "text") {
      const anchor = labelPosition;
      labelRoom = { w: Math.max(drawing.w, frame.clientWidth - (anchor?.x ?? 0)),
        h: Math.max(drawing.h, frame.clientHeight - (anchor?.y ?? 0)) };
    }
    targets = draft?.targets ?? [];
    moreTargets = !!draft?.moreTargets;
    confirmDiscard = false;
    saveError = "";
    note = draft?.note ?? "";
    void tick().then(() => {
      if (editingLabel) {
        labelInput?.focus({ preventScroll: true });
        if (draft) labelInput?.select();
      } else editor?.querySelector("textarea")?.focus({ preventScroll: true });
    });
  }

  function reopenAnnotation(draft: ElementDraft) {
    if (picked || !canComment) return;
    if (picking) tell(false);
    openEditor({ url: draft.url ?? "", selector: draft.selector, html: draft.html ?? "", text: draft.text ?? "", tag: "" }, draft);
  }

  function finishComment(restoreFocus = true) {
    tell(false);
    picked = null;
    images = [];
    editingDraft = undefined;
    note = "";
    if (restoreFocus) void tick().then(() => surface?.querySelector<HTMLButtonElement>('[aria-label="Comment on element"]')?.focus({ preventScroll: true }));
  }

  function closeComment() {
    if (modified) confirmDiscard = true;
    else finishComment();
  }

  function deleteComment() {
    if (!req || !canComment) return;
    if (editingDraft) feedback.removeElement(req.sessionId, editingDraft);
    finishComment();
  }

  function commentKey(e: KeyboardEvent) {
    if (e.isComposing || e.repeat) return;
    if (e.metaKey && !e.ctrlKey && !e.altKey && !e.shiftKey && (e.key === "Backspace" || e.key === "Delete")) {
      e.preventDefault(); e.stopPropagation();
      deleteComment();
      return;
    }
    if (e.key === "Escape" || (e.key === "Enter" && (e.metaKey || e.ctrlKey))) {
      e.preventDefault(); e.stopPropagation();
      if (e.key === "Escape") closeComment();
      else saveComment();
    }
  }

  function elementDraft(p: PickedElement): ElementDraft {
    return { url: devPageURL(p.url, req!.url), selector: p.selector, html: p.html, text: p.text, note: "", reviewKey: resumeKey };
  }
  function commitLabel() {
    if (!editingLabel) return true;
    if (label.trim()) return saveComment(false);
    finishComment(false);
    return true;
  }
  function saveComment(restoreFocus = true) {
    if (!req || !picked || !canComment || !readyToSave) return false;
    const sessionId = req.sessionId;
    if (mark?.kind === "text" && label !== label.trim()) flushSync(() => { label = label.trim(); });
    const savedMark = mark?.kind === "text" ? { ...mark, label: label.trim(), x: mark.x - labelShift.x, y: mark.y - labelShift.y,
      w: labelMeasure?.offsetWidth || mark.w, h: labelMeasure?.offsetHeight || mark.h } : mark;
    const element = { ...elementDraft(picked), note: note.trim(), ...(savedMark && { mark: savedMark, targets, moreTargets }), ...(imagePaths.length ? { images: imagePaths } : {}) };
    try {
      elementForAgent(element); // Validate attachment paths before accepting the draft.
      if (modified || !editingDraft) feedback.addElement(sessionId, element, editingDraft);
      for (const image of images) {
        if (image.path && image.thumb) ssh.view(sessionId).sentThumbs.set(image.path, image.thumb);
      }
      finishComment(restoreFocus);
      return true;
    } catch (e) {
      saveError = String(e);
      return false;
    }
  }

  function confirmClear() {
    if (!req) return;
    clearExitHint();
    const sessionId = req.sessionId;
    const count = drafts.length + (picked && !editingDraft ? 1 : 0);
    confirm.ask({
      title: "Clear preview feedback?",
      body: `Discard ${count === 1 ? "the unsent annotation" : `all ${count} unsent annotations`} and any attached comments for this session.`,
      confirmLabel: "Clear feedback",
      onConfirm: () => { if (req?.sessionId === sessionId) clearAnnotations(); },
    });
  }
  function clearAnnotations() {
    if (!req || !canComment || feedback.isSending(req.sessionId)) return;
    clearExitHint();
    selectedDraft = undefined;
    pendingEdit = undefined;
    picking = false;
    finishComment();
    feedback.clearElements(req.sessionId);
  }
  function sendAnnotations() {
    if (!req || !canComment || pendingEdit || pendingMark || feedback.isSending(req.sessionId)) return;
    if (picked && !saveComment(false)) return;
    if (picking) tell(false);
    void feedback.send(req.sessionId, feedback.elementsFor(req.sessionId));
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
    <div bind:this={surface} onkeydowncapture={feedbackKey} onkeydown={annotationKeyShortcut} tabindex="-1" class="relative grid h-full min-h-0 grid-rows-[auto_minmax(0,1fr)_auto] outline-none" data-session-preview>
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
            {#if picking && commentAvailable}
              {#key tool}<PreviewDrawing {tool} style={layout.style} scale={layout.scale} place={inspect} {hover} cancel={() => tell(false)} />{/key}
            {/if}
            {#if capturing}
              <div class="comment-layer" style={layout.style}><DrawnMark mark={capturing} rect={capturing}/></div>
            {/if}
            {#if (commentRects.length || (picked && mark && bounds)) && !preparing}
              <div class="comment-layer" class:drawing-active={picking && (tool === "comment" || tool === "text")} style={layout.style} style:opacity={capturing ? 0 : undefined}>
                {#if editingLabel && mark && labelPosition}
                  <div class="label-entry" style:font-size={(mark.fontSize ?? labelFontSize) + "px"}
                    style:left={labelPosition.x - labelShift.x + "px"}
                    style:top={labelPosition.y - labelShift.y + "px"}>
                    <span class="preview-lettering label-measure" aria-hidden="true"
                      bind:this={labelMeasure} bind:offsetWidth={labelWidth} bind:offsetHeight={labelHeight}>{label || "Text"}</span>
                    <textarea class="preview-lettering" bind:this={labelInput} bind:value={label} rows="1" aria-label="Text label" placeholder="Text" maxlength="300"
                      onblur={() => commitLabel()} onkeydown={e => {
                        if (e.isComposing) return;
                        if (e.metaKey && (e.key === "Delete" || e.key === "Backspace")) {
                          commentKey(e);
                          return;
                        }
                        if (e.key === "Enter" || e.key === "Escape") {
                          e.preventDefault(); e.stopPropagation();
                          if (e.key === "Escape") finishComment(); else commitLabel();
                        }
                      }}></textarea>
                    {#if saveError}<span class="label-error" role="alert">{saveError}</span>{/if}
                  </div>
                {:else if picked && mark && bounds && !editingDraft}<DrawnMark {mark} rect={bounds}/>{/if}
                {#each commentRects as r,i (r.number)}
                  {@const draft = drafts[r.number - 1]}
                  {@const moved = pendingEdit?.previous === draft ? pendingEdit?.mark : undefined}
                  {@const slot = commentRects.slice(0, i).filter(other => Math.abs(other.x - r.x) < 24 && Math.abs(other.y - r.y) < 24).length}
                  {#if draft && !(editingLabel && draft === editingDraft)}
                    {#if draft.mark}
                      <DrawnMark mark={moved ?? draft.mark}
                        rect={moved ?? {...r, x:r.originX ?? r.x, y:r.originY ?? r.y}} visible={moved ? undefined : r} number={r.number}
                        scale={layout.scale} selected={draft === selectedDraft && !picked}
                        select={() => selectedDraft = draft}
                        change={commentAvailable && !pendingEdit && !feedback.isSending(req.sessionId) ? moved => changeMark(draft, moved) : undefined}
                        edit={canComment && !picked ? () => reopenAnnotation(draft) : undefined}/>
                    {:else}
                    <div class="comment-border" class:focused={draft === editingDraft} style:left={`${r.x}px`} style:top={`${r.y}px`} style:width={`${r.w}px`} style:height={`${r.h}px`}></div>
                    {/if}
                    {#if draft.mark?.kind !== "text"}
                      <button class="comment-pin" class:uncommented={!!draft.mark && !draft.note && !draft.images?.length} class:focused={draft === editingDraft} style:left={`clamp(0px, ${r.x + 4 + slot * 24}px, 100% - 22px)`} style:top={`clamp(0px, ${r.y - (draft.mark ? 22 : 11)}px, 100% - 22px)`} onfocus={() => selectedDraft = draft} aria-keyshortcuts="Backspace Delete" aria-label={`Live comment ${r.number}`} aria-expanded={draft === editingDraft} title={annotationCaption(draft)} disabled={!canComment || !!picked || picking} onclick={() => reopenAnnotation(draft)}>{r.number}</button>
                    {/if}
                  {/if}
                {/each}
              </div>
            {/if}
            {#if preparing}<div class="absolute inset-0 grid place-items-center bg-canvas" role="status"><span class="text-sm text-faint">Preparing live view…</span></div>{/if}
          {:else}
            <p class="text-sm text-faint">Starting preview…</p>
          {/if}
        </div>
        <div class="preview-actions" style:visibility={capturing ? "hidden" : undefined}>
        {#if picking && commentAvailable}
          <div class="tool-hint" role="status">
            {tool === "comment" ? "Click an element or drag a rectangle" : tool === "area" ? "Drag to draw a rectangle · Double-click a shape to comment" : tool === "arrow" ? "Drag to draw an arrow · Double-click a shape to comment" : tool === "pen" ? "Drag to draw · Double-click a shape to comment" : "Click to place text · Double-click a label to edit"}
          </div>
        {/if}
        <div class="review-dock live-tools" role="group" aria-label="Live page tools">
          <div role="group" aria-label="Viewport size" class="mr-0.5 flex items-center border-r border-edge pr-1">
            {#each VIEWPORTS as preset (preset.id)}
              {@const label = preset.id === "desktop" ? "Full width" : preset.label}
              <Button icon size="sm" selected={viewport === preset.id} aria-pressed={viewport === preset.id} aria-label={label} title={preset.width ? `${label} · ${preset.width} × ${preset.height}` : label} onclick={() => { viewport = preset.id; onPresetChange?.(); }}>
                <Icon name={viewportIcons[preset.id]} size={15} />
              </Button>
            {/each}
          </div>
          <Button size="sm" selected={picking && tool === "comment"} disabled={!canChooseTool} aria-label="Comment on element" aria-keyshortcuts="c" title="Click an element or drag a rectangle · C" onclick={() => togglePick()}>
            <Icon name="square-mouse-pointer" size={15} /><span>Comment</span>
          </Button>
          {#each tools as t}
            <Button icon size="sm" selected={picking && tool === t.id} disabled={!canChooseTool} aria-label={t.label} aria-keyshortcuts={t.key} title={`${t.label} · ${t.key.toUpperCase()}`} onclick={() => togglePick(t.id)}><Icon name={t.icon} size={15}/></Button>
          {/each}
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
        {#if (drafts.length || picked) && canComment}
          <div class="batch-actions">
          <Button size="sm" variant="bare" disabled={feedback.isSending(req.sessionId)} title="Clear this session’s preview feedback" onclick={confirmClear}>Clear all</Button>
          <Button size="md" variant="bare" class="preview-send" disabled={!batchCount || !!pendingEdit || !!pendingMark || (!!picked && !readyToSave)} loading={feedback.isSending(req.sessionId)}
            title="Send all preview annotations for this session" onclick={sendAnnotations}>
            <Icon name="send" size={14}/>{batchCount ? `Send feedback (${batchCount})` : "Send feedback"}
          </Button>
          </div>
        {/if}
        </div>
        {#if exitHint}
          <div role="status" class="ssh-fade-in pointer-events-none absolute inset-0 z-10 grid place-items-center bg-black/35 p-4 motion-reduce:animate-none!">
            <p class="rounded-xl border border-edge bg-panel px-6 py-4 text-center text-xl leading-loose text-balance text-ink shadow-xl">
              Press <kbd class="rounded-md border border-edge bg-canvas px-2 py-1 font-mono text-lg">Esc</kbd> again to {exitHint === "clear" ? "clear preview feedback" : "close preview"}
            </p>
          </div>
        {/if}
        {#if picked && !editingLabel}
          <div class="annotation-editor" id={dropId} data-file-drop-target onpaste={pasteComment} bind:this={editor} role="dialog" aria-label="Element comment" tabindex="-1" style:left={`${position.left}px`} style:top={`${position.top}px`} onkeydown={commentKey}>
            <div class="editor-heading">
              <label for={commentId}>Comment{editingDraft && drafts.includes(editingDraft) ? ` ${drafts.indexOf(editingDraft) + 1}` : ""}<span class="editor-side">Live</span></label>
              <div class="editor-controls">
                {#if inlinePage || editingDraft}
                  <Button icon size="xs" variant="danger" disabled={!canComment} aria-label={mark ? "Delete annotation" : "Delete comment"} title={mark ? "Delete annotation · ⌘Backspace" : "Delete comment · ⌘Backspace"} aria-keyshortcuts="Meta+Backspace Meta+Delete" onclick={deleteComment}><Icon name="trash-2" size={14} /></Button>
                {/if}
                <Button icon size="xs" aria-label="Close comment" title="Close comment" onclick={closeComment}><Icon name="x" size={14} /></Button>
              </div>
            </div>
            {#if mark?.kind === "area"}
              {#if targets.length}
                <details class="region-targets"><summary><Chevron class="disclosure-chevron" />{targets.length}{moreTargets ? "+" : ""} enclosed {targets.length === 1 ? "element" : "elements"}</summary>
                  <ul>{#each targets as target}<li>{target}</li>{/each}</ul>
                </details>
              {:else}<p class="region-targets">No fully enclosed elements</p>{/if}
            {/if}
            <textarea id={commentId} rows="3" maxlength="2000" placeholder="What should change? Paste or drop images here." aria-label="Element comment" bind:value={note}></textarea>
            {#if images.length}
              <ul class="comment-images" aria-label="Attached images">
                {#each images as im, i (im.id)}
                  <li>
                    {#if im.thumb}<img src={im.thumb} alt={"Image " + (i + 1)}/>{/if}
                    <span class:text-warn={!!im.error} role="status">{im.error || (im.path ? "Image " + (i + 1) : "Preparing image " + (i + 1) + "…")}</span>
                    <Button icon size="xs" aria-label={"Remove image " + (i + 1)} onclick={() => images = images.filter(item => item.id !== im.id)}><Icon name="x" size={12}/></Button>
                  </li>
                {/each}
              </ul>
            {/if}
            {#if confirmDiscard}<p class="save-question">Save changes?</p>{/if}
            <div class="editor-actions">
              {#if confirmDiscard}
                <Button size="xs" onclick={() => confirmDiscard = false}>Keep editing</Button>
                <Button size="xs" variant="danger" onclick={() => finishComment()}>Discard</Button>
              {/if}
              <Button variant="primary" size="sm" disabled={!readyToSave || !canComment} aria-keyshortcuts="Meta+Enter Control+Enter" onclick={() => saveComment()}><Icon name="check" size={14} />{editingDraft && !modified ? "Done" : "Save"}</Button>
            </div>
            {#if saveError}<p role="status" class="mt-2 text-xs text-warn">{saveError}</p>{/if}
          </div>
        {/if}
      </div>
    </div>
{/if}

<style>
  .label-entry { position:absolute; z-index:3; width:max-content; max-width:min(18em, 100%); pointer-events:none; }
  .label-entry .preview-lettering { font-size:inherit; }
  .label-measure { visibility:hidden; max-width:100%; }
  .label-entry textarea { position:absolute; inset:0; width:100%; height:100%; resize:none; overflow:hidden; pointer-events:auto; outline:2px solid var(--preview-mark-color); outline-offset:2px; }
  .label-entry textarea::placeholder { color:inherit; opacity:1; }
  .label-error { position:absolute; top:100%; background:var(--color-panel); color:var(--color-warn); font-size:12px; }

  .region-targets { margin:0 0 8px; color:var(--color-faint); font-size:12px; }
  .region-targets summary { display:flex; align-items:center; gap:4px; cursor:pointer; list-style:none; }
  .region-targets summary::-webkit-details-marker { display:none; }
  .region-targets ul { max-height:6rem; overflow:auto; margin:4px 0; padding-left:16px; overflow-wrap:anywhere; }

  .preview-actions { position:absolute; z-index:4; inset:auto 10px 10px; display:flex; flex-wrap:wrap; align-items:flex-end; justify-content:center; gap:6px; pointer-events:none; }
  .tool-hint { position:absolute; bottom:calc(100% + 6px); max-width:100%; border-radius:5px; padding:3px 8px; background:var(--color-panel); color:var(--color-ink-2); font-size:12px; box-shadow:0 1px 5px #0003; }
  .comment-pin:disabled, .comment-pin.uncommented:not(:focus-visible) { pointer-events:none; }
  .comment-pin.uncommented:not(:focus-visible) { opacity:0; }
  .preview-actions .live-tools { position:static; transform:none; margin-inline:auto; max-width:100%; pointer-events:auto; }
  .batch-actions { display:flex; align-items:center; gap:4px; margin-left:auto; padding:3px; border:1px solid var(--color-edge); border-radius:9px; background:var(--color-panel); pointer-events:auto; }
  .preview-actions :global(.preview-send) { pointer-events:auto; background:var(--color-good); color:var(--color-on-good, var(--color-canvas)); box-shadow:0 2px 8px #0003; }
  .preview-actions :global(.preview-send:enabled:hover) { filter:brightness(.93); }

  .annotation-editor:global(.file-drop-target-active) {outline:2px solid var(--color-accent);outline-offset:-2px}
  .comment-images {display:grid;gap:4px;margin:0 0 8px;padding:0;list-style:none}
  .comment-images li {display:flex;align-items:center;gap:8px;padding:3px 5px;border-radius:4px;background:var(--color-canvas)}
  .comment-images li span {flex:1;min-width:0;font-size:12px;overflow-wrap:anywhere}
  .comment-images img {width:36px;height:32px;object-fit:contain}
  .comment-layer { z-index:3; grid-area:1 / 1; position:relative; overflow:hidden; pointer-events:none; }
  .comment-border { position:absolute; border:1px solid rgb(124 92 255 / 55%); border-radius:3px; background:rgb(124 92 255 / 3%); }
  .comment-border.focused { border:2px solid #7c5cff; background:rgb(124 92 255 / 15%); }
  .comment-pin { position:absolute; z-index:1; display:grid; place-items:center; width:22px; height:22px; border:1px solid var(--color-accent); border-radius:50%; background:var(--color-panel); color:var(--color-accent); font-size:12px; font-weight:650; font-variant-numeric:tabular-nums; box-shadow:0 1px 4px #0003; pointer-events:auto; cursor:pointer; }
  .comment-pin:hover, .comment-pin:focus-visible, .comment-pin.focused { background:var(--color-accent); color:var(--color-canvas); }
  .review-issues { max-height:30dvh; overflow-y:auto; }
  .review-issues :global([role="status"]) { padding:4px 12px; overflow-wrap:anywhere; }
</style>
