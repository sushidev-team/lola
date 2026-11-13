<script lang="ts">
  // The story: one rail with the agent beside it and the person across from it, the agent's log and
  // lola's record merged on the client so no private block can reach anything an agent reads. See
  // turns.ts for what becomes a row, and .cv-* in app.css for the three layouts of one DOM.
  //
  // It does not depend on the composer and the composer does not depend on it, so typing cannot
  // lag behind a long story. Scrolling is held by hand, because WKWebView has no overflow-anchor:
  // a ResizeObserver keeps the tail in view while the reader is there, and the first sign they
  // are reading elsewhere (a scroll up, a selection) lets go.
  //
  // While focused it keeps the keys it uses. Every other key goes on to the app's handler, which
  // ignores what has no meaning in a session, so a focused story does not make a session deaf.
  import { onMount, tick, untrack } from "svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { talk } from "$lib/talk.svelte";
  import { feed, FEED_SCAN_POLL_MS } from "$lib/feed.svelte";
  import { agentName, dockCards, matchesFilter } from "$lib/ssh";
  import { agoPhrase } from "$lib/time";
  import { buildStory, catchUp, findInStory, isFolded, lastLifeIndex, locate, turnParts, turnAsk, type StoryRow } from "$lib/turns";
  import Button from "../Button.svelte";
  import Chevron from "../Chevron.svelte";
  import TimelineTurn from "./TimelineTurn.svelte";

  let { sessionId }: { sessionId: string } = $props();

  /** Within this of the bottom, the reader is at the tail. */
  const STICK_PX = 60;
  /** The rows stay mounted this far past the screen. */
  const MOUNT_MARGIN = "200% 0px";
  /** Characters of 12px mono in a pixel, near enough to decide how many names fit on a run line. */
  const PX_PER_CHAR = 7.3;
  /** Verb, gaps, status and chevron take this much of a tool row. */
  const ROW_CHROME_PX = 180;
  /** No agent row is wider than this, however wide the window. */
  const AGENT_COLUMN_PX = 720;

  /** The deliveries ⌘J counts as a session that needs looking at. See queue.ts. */
  const REGRESSED = new Set(["ci_failed", "changes_requested", "merge_conflict"]);

  const view = $derived(ssh.view(sessionId));
  const f = $derived(feed.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const agent = $derived(agentName(session?.agent));
  const talkState = $derived(talk.state(sessionId));
  // A parked agent can still answer a call. A resting or dead one never will, so it reads no result.
  const alive = $derived(["working", "starting", "waiting_input"].includes(session?.agentState ?? ""));
  const story = $derived(buildStory({ feed: f.items, items: view.items, filter: view.filter, scope: view.scope, alive, opened: view.openedFolds, standing: f.standing }));

  // --- the feed --------------------------------------------------------------
  const feedSeq = $derived(talkState?.feedSeq);
  const feedEpoch = $derived(talkState?.feedEpoch);
  $effect(() => {
    const seq = feedSeq;
    const epoch = feedEpoch;
    untrack(() => f.sync(seq, epoch));
  });
  const scanning = $derived(f.scanning);
  $effect(() => {
    if (!scanning) return;
    const t = setInterval(() => void f.fetch(), FEED_SCAN_POLL_MS);
    return () => clearInterval(t);
  });

  // --- what mounts -----------------------------------------------------------
  const audit = $derived(view.filter !== "everything");
  const lifeAt = $derived(audit ? -1 : lastLifeIndex(story.turns));
  const lifeOpen = $derived(lifeAt > 0 && f.expanded.has(`life-${story.turns[lifeAt].id}`));
  const firstShown = $derived(lifeAt > 0 && !lifeOpen ? lifeAt : 0);
  const earlierAgo = $derived.by(() => {
    if (firstShown <= 0) return "";
    const rows = story.turns[firstShown - 1].rows;
    return agoPhrase(rows[rows.length - 1].at);
  });
  const shownTurns = $derived(story.turns.slice(firstShown));
  // A jump is on its way to these, so they mount whatever the scroll position says.
  let pinnedTurns = $state<Set<string>>(new Set());

  function folded(id: string, index: number): boolean {
    return !audit && !f.standing.has(`turn-${id}`) && isFolded(index, story.turns.length, f.expanded, id);
  }

  // --- the rows the keyboard walks ----------------------------------------------
  function walkable(): StoryRow[] {
    const out: StoryRow[] = [];
    story.turns.forEach((t, i) => {
      if (i < firstShown || folded(t.id, i)) return;
      out.push(...t.rows);
    });
    return out;
  }

  // Item ids are the daemon's or the log's, but a selector built from one is still escaped.
  function safeId(id: string): string {
    return typeof CSS !== "undefined" && CSS.escape ? CSS.escape(id) : id;
  }
  function rowSelector(id: string): string {
    return `[data-row="${safeId(id)}"]`;
  }

  // --- since you looked ------------------------------------------------------
  const readKey = $derived(`lola.read.${sessionId}`);
  function readStamp(key: string): number {
    try {
      return Number(localStorage.getItem(key) ?? 0) || 0;
    } catch {
      return 0;
    }
  }
  // Fixed for this visit: the line marks where you stopped, not where you are.
  const stamp = untrack(() => readStamp(`lola.read.${sessionId}`));
  const caught = $derived(catchUp(story, stamp));
  let sincePill = $state(false);
  let sinceAsked = false;

  function covered(): boolean {
    return !scroller || !!scroller.closest("[inert]") || document.visibilityState === "hidden";
  }

  /** The tail was on screen, so everything up to it has been seen. */
  function markRead() {
    if (covered() || !stuck || f.scanning) return;
    let newest = 0;
    for (const t of story.turns) for (const r of t.rows) if (r.at > newest) newest = r.at;
    if (!newest) return;
    try {
      localStorage.setItem(readKey, String(newest));
    } catch {
      /* a full store costs this one line next time */
    }
  }

  // --- scroll ----------------------------------------------------------------
  let scroller = $state<HTMLDivElement | null>(null);
  let content = $state<HTMLDivElement | null>(null);
  let stuck = true;
  let selecting = false;
  let lastTop = 0;
  let newCount = $state(0);
  let lastRows = -1;
  let back = $state<number | null>(null);
  let notAtTail = $state(false);
  let chars = $state(72);
  // The turn whose ask has scrolled off while its rows still fill the window.
  let headTurn = $state("");
  const headSource = $derived(story.turns.find(t => t.id === headTurn)?.rows.find(r => r.kind === "origin" || r.kind === "you" || r.kind === "terminal" || r.kind === "moment"));
  const headLabel = $derived(headSource?.kind === "origin" && headSource.source === "lola" ? "Lola instruction" : headSource?.kind === "moment" ? "Session event" : headSource?.kind === "origin" ? "Background context" : "Your request");
  const headAsk = $derived(headTurn ? turnAsk(story.turns.find((t) => t.id === headTurn) ?? { rows: [] }) : "");
  let anchor: { row: Element; top: number } | null = null;
  // Opening details holds the control where it was, rather than snapping it to the top.
  let opening: HTMLElement | null = null;
  let openingTop = 0;
  function releaseOpening() { opening = null; }
  function scrollOpening() {
    if (!opening?.isConnected || !scroller) return;
    const top = openingTop;
    scroller.scrollTop += opening.getBoundingClientRect().top - top;
    lastTop = scroller.scrollTop;
    notAtTail = !atBottom();
    anchor = { row: opening, top };
  }
  function captureExpansion(event: MouseEvent) {
    releaseOpening();
    const target = event.target;
    if (!(target instanceof Element) || target.closest('a')) return;
    const control = target.closest<HTMLElement>('button[aria-expanded], summary');
    if (!control || control.matches(':disabled')) return;
    const expands = control.matches('summary')
      ? !(control.parentElement as HTMLDetailsElement).open
      : control.getAttribute('aria-expanded') === 'false';
    if (!expands) return;
    opening = control;
    openingTop = control.getBoundingClientRect().top;
    stuck = false;
    anchor = null;
    void tick().then(scrollOpening);
  }
  // New rows fade once. A row seen before never replays it, whether it remounts or refetches.
  const knownRows = new Set<string>();
  let firstPaint = true;

  function atBottom(): boolean {
    const el = scroller;
    return !el || el.scrollHeight - el.scrollTop - el.clientHeight <= STICK_PX;
  }
  function toBottom() {
    releaseOpening();
    if (scroller) {
      scroller.scrollTop = scroller.scrollHeight;
      lastTop = scroller.scrollTop;
    }
    stuck = true;
    notAtTail = false;
    newCount = 0;
    markRead();
  }
  function onScroll() {
    const el = scroller;
    if (!el) return;
    if (el.scrollTop < lastTop - 1 && !atBottom()) stuck = false;
    if (atBottom()) {
      if (!selecting && !opening) stuck = true;
      newCount = 0;
      back = null;
    }
    notAtTail = !atBottom();
    lastTop = el.scrollTop;
    if (stuck) markRead();
    measureTop();
  }
  // What sits at the top of the window. Plain measuring in the scroll event, with no observer of its
  // own: walking back from the newest turn stops at the first one that has started by the top edge.
  function measureTop() {
    const el = scroller;
    if (!el || !content) return;
    const top = el.getBoundingClientRect().top;
    const turns = content.querySelectorAll<HTMLElement>("[data-turn]");
    let nextHead = "";
    let row: Element | null = null;
    for (let i = turns.length - 1; i >= 0; i--) {
      const r = turns[i].getBoundingClientRect();
      if (r.top > top) continue;
      if (r.bottom > top) {
        // What opened the turn is its first row, whichever kind that is.
        const opener = turns[i].querySelector("[data-row]");
        if (opener && opener.getBoundingClientRect().bottom <= top) nextHead = turns[i].dataset.turn ?? "";
        for (const c of turns[i].querySelectorAll("[data-row]")) {
          if (c.getBoundingClientRect().bottom > top) {
            row = c;
            break;
          }
        }
      }
      break;
    }
    headTurn = nextHead;
    anchor = row ? { row, top: row.getBoundingClientRect().top } : null;
  }
  /**
   * Holds a reader above the tail in place, since WKWebView has no overflow-anchor: a narration beat
   * folding away above the window moves everything under it, and this pays that back.
   */
  function holdAnchor() {
    const el = scroller;
    if (!el || !anchor?.row.isConnected || stuck || selecting) return;
    const delta = anchor.row.getBoundingClientRect().top - anchor.top;
    if (Math.abs(delta) < 1) return;
    el.scrollTop += delta;
    lastTop = el.scrollTop;
  }
  function toHead() {
    const el = scroller?.querySelector(`[data-turn="${safeId(headTurn)}"] [data-row]`);
    stuck = false;
    el?.scrollIntoView?.({ block: "start" });
    if (scroller) lastTop = scroller.scrollTop;
    notAtTail = !atBottom();
  }
  // The same gesture that scrolls up lets go, before the scroll event can arrive.
  function onWheel(e: WheelEvent) {
    releaseOpening();
    if (e.deltaY < 0) stuck = false;
  }
  $effect(() => {
    const el = content;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => {
      const lastTurn = Array.from(el.querySelectorAll<HTMLElement>("[data-turn]")).at(-1);
      const end = lastTurn?.querySelector<HTMLElement>(".cv-turn-end");
      const stop = end ? Math.max(16, el.getBoundingClientRect().bottom - end.getBoundingClientRect().top + 8) : 24;
      el.style.setProperty("--cv-rail-end", `${stop}px`);
      if (scroller?.clientWidth) chars = Math.max(24, Math.floor((Math.min(el.clientWidth - 48, AGENT_COLUMN_PX) - ROW_CHROME_PX) / PX_PER_CHAR));
      // The dock sits outside the scroller, and has to give up the same width to keep the rail straight.
      if (scroller) scroller.closest<HTMLElement>("[data-conversation]")?.style.setProperty("--cv-sb", `${scroller.offsetWidth - scroller.clientWidth}px`);
      if (opening) scrollOpening();
      else if (stuck && !selecting) toBottom();
      else holdAnchor();
    });
    ro.observe(el);
    return () => ro.disconnect();
  });
  $effect(() => {
    if (!view.sending) return;
    untrack(() => {
      stuck = true;
      releaseOpening();
      void tick().then(toBottom);
    });
  });
  // Rows arrived. Follow if the reader is at the tail, otherwise count them for the pill.
  $effect(() => {
    const n = story.rowCount;
    const you = story.lastYou;
    untrack(() => {
      const added = lastRows < 0 ? 0 : n - lastRows;
      lastRows = n;
      if (stuck && !selecting) void tick().then(toBottom);
      else if (added > 0) newCount += added;
      for (const t of story.turns) for (const r of t.rows) knownRows.add(r.id);
      // Both sources have answered, so whatever arrives after this is actually new.
      if (f.loaded && view.loaded) firstPaint = false;
      if (you !== lastSent) {
        // Sending returns to the bottom, and the first answer of a visit is not a send.
        if (lastSent !== null) {
          selecting = false;
          void tick().then(toBottom);
        }
        lastSent = you;
      }
      if (caught && !sinceAsked && !f.scanning && f.loaded) {
        sinceAsked = true;
        void tick().then(() => {
          const el = scroller?.querySelector(rowSelector(caught?.rowId ?? ""));
          const top = scroller?.getBoundingClientRect().top ?? 0;
          if (el && el.getBoundingClientRect().top < top) sincePill = true;
        });
      }
    });
  });
  let lastSent: string | null = null;
  function fresh(id: string): boolean {
    return !firstPaint && !knownRows.has(id);
  }

  // A selection is someone's work: the page must not move out from under it.
  onMount(() => {
    const onSel = () => {
      const sel = window.getSelection?.();
      const inside = !!sel && !sel.isCollapsed && !!scroller && !!sel.anchorNode && scroller.contains(sel.anchorNode);
      selecting = inside;
      if (!inside && atBottom()) stuck = true;
    };
    document.addEventListener("selectionchange", onSel);
    return () => {
      document.removeEventListener("selectionchange", onSel);
      markRead();
    };
  });

  // One observer for every turn, because a story can be long.
  const near = new Map<Element, (near: boolean) => void>();
  let io: IntersectionObserver | undefined;
  function observeTurn(el: HTMLElement, cb: (isNear: boolean) => void): () => void {
    if (typeof IntersectionObserver === "undefined" || !scroller) return () => {};
    if (!io) {
      io = new IntersectionObserver((entries) => {
        for (const e of entries) near.get(e.target)?.(e.isIntersecting);
      }, { root: scroller, rootMargin: MOUNT_MARGIN });
    }
    near.set(el, cb);
    io.observe(el);
    return () => {
      near.delete(el);
      io?.unobserve(el);
    };
  }

  // Older pages arrive above the reader, so their height is paid back to the scroll position.
  async function older() {
    const before = scroller?.scrollHeight ?? 0;
    const top = scroller?.scrollTop ?? 0;
    await f.older();
    await tick();
    if (scroller) {
      scroller.scrollTop = top + scroller.scrollHeight - before;
      lastTop = scroller.scrollTop;
    }
  }

  // --- digging ---------------------------------------------------------------
  $effect(() => {
    const j = view.jump;
    if (!j || j.n === view.seen.jump) return;
    view.seen.jump = j.n;
    untrack(() => void landOn(j.block, j.expand !== false));
  });

  /** Makes a row reachable: its earlier life, its folded turn, its plate fold or its run line. */
  function reveal(id: string): StoryRow | undefined {
    const at = locate(story, id);
    if (!at) return undefined;
    const t = story.turns[at.turn];
    if (at.turn < firstShown && lifeAt > 0) f.expanded.add(`life-${story.turns[lifeAt].id}`);
    if (folded(t.id, at.turn)) f.expanded.add(`turn-${t.id}`);
    pinnedTurns = new Set([t.id]);
    if (at.row.kind === "platefold") view.openedFolds.add(at.row.id);
    if (at.row.kind === "run" && at.row.id !== id) f.expanded.add(at.row.id);
    return at.row;
  }

  let landing = 0;
  async function landOn(target: string, expand = true) {
    const request = ++landing;
    const isItem = target.startsWith("item:");
    let id = isItem ? target.slice(5) : target;
    const item = view.items.find((i) => (isItem ? i.id === id : i.block?.id === id));
    if (item) {
      id = item.id;
      if (!matchesFilter(item, view.filter)) view.filter = "everything";
    }
    await tick();
    if (request !== landing) return;
    if (!expand) {
      const at = locate(story, id);
      if (!at) return;
      const turn = story.turns[at.turn];
      pinnedTurns = new Set([turn.id]);
      if (back == null) back = scroller?.scrollTop ?? null;
      stuck = false;
      await tick();
      if (request !== landing) return;
      // Land on the nearest visible container, leaving every disclosure untouched.
      const turnEl = scroller?.querySelector(`[data-turn="${safeId(turn.id)}"]`);
      const work = turnParts(turn.rows, 0, !(alive && at.turn === story.turns.length - 1), f.standing)
        .find(p => p.kind === 'work' && p.rows.some(row => row.id === at.row.id));
      const el = at.turn < firstShown ? scroller?.querySelector('[data-earlier-life]')
        : turnEl?.querySelector('[data-turn-fold]')
          ?? turnEl?.querySelector(rowSelector(id))
          ?? turnEl?.querySelector(rowSelector(at.row.id))
          ?? (work?.kind === 'work' ? turnEl?.querySelector(`[data-work="${safeId(work.id)}"]`) : null)
          ?? turnEl;
      el?.scrollIntoView?.({ block: 'center', behavior: 'instant' });
      if (scroller) lastTop = scroller.scrollTop;
      return;
    }
    if (!reveal(id)) return;
    if (back == null) back = scroller?.scrollTop ?? null;
    stuck = false;
    view.cursor = id; // Open work groups and answer sections before locating the target.
    await tick();
    if (request !== landing) return;
    const el = scroller?.querySelector(rowSelector(id));
    // A block lands at the top, because the first thing a dig shows is the exact command.
    el?.scrollIntoView?.({ block: item?.block ? "start" : "center" });
    if (scroller) lastTop = scroller.scrollTop;
    view.cursor = id;
    if (item?.block) {
      const b = item.block.id;
      view.flashBlock = b;
      setTimeout(() => {
        if (view.flashBlock === b) view.flashBlock = "";
      }, 200);
    }
  }

  function goBack() {
    if (scroller && back != null) {
      scroller.scrollTop = back;
      lastTop = back;
    }
    back = null;
    stuck = atBottom();
    notAtTail = !stuck;
  }

  function toCatchUp() {
    sincePill = false;
    if (caught) void landOn(`item:${caught.rowId}`);
  }

  // --- find ------------------------------------------------------------------
  let finding = $state(false);
  let query = $state("");
  let findAt = $state(0);
  let findEl = $state<HTMLInputElement | null>(null);
  const found = $derived(query.trim() ? findInStory(story, query) : []);
  $effect(() => {
    const n = view.findFocus;
    if (n === view.seen.find) return;
    view.seen.find = n;
    openFind();
  });
  function openFind() {
    finding = true;
    void tick().then(() => findEl?.focus());
  }
  function walk(d: number) {
    if (!found.length) return;
    findAt = (findAt + d + found.length) % found.length;
    view.jump = { block: `item:${found[findAt].id}`, n: (view.jump?.n ?? 0) + 1 };
  }

  // --- keyboard --------------------------------------------------------------
  $effect(() => {
    const n = view.streamFocus;
    // Not before the story is here: what this lands on is one of its rows.
    if (n === view.seen.stream || !f.loaded || !view.loaded) return;
    view.seen.stream = n;
    // `t`: a waiting card comes first, because that is what the keyboard is for.
    if (waitingCard()) {
      view.dockFocus++;
      return;
    }
    untrack(() => {
      // A session that came back to you lands on what happened to it: CI, a review, a conflict.
      if (REGRESSED.has(session?.delivery ?? "") && !alive) {
        const moment = walkable().findLast((r) => r.kind === "moment");
        if (moment) moveTo(moment.id);
      }
      if (!view.cursor) view.cursor = walkable().at(-1)?.id ?? "";
      scroller?.focus();
    });
  });

  function moveTo(id: string) {
    view.cursor = id;
    stuck = false;
    void tick().then(() => scroller?.querySelector(rowSelector(id))?.scrollIntoView?.({ block: "nearest" }));
  }

  function move(d: number) {
    const rows = walkable();
    if (!rows.length) return;
    const i = rows.findIndex((r) => r.id === view.cursor);
    const at = i < 0 ? (d > 0 ? 0 : rows.length - 1) : Math.max(0, Math.min(rows.length - 1, i + d));
    moveTo(rows[at].id);
  }

  function cursorRow(): StoryRow | undefined {
    return walkable().find((r) => r.id === view.cursor);
  }

  function onKey(e: KeyboardEvent) {
    const t = e.target as HTMLElement;
    if (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT") return;
    if (e.metaKey || e.ctrlKey || e.altKey || e.key === "Tab") return;
    const cur = cursorRow();
    const blk = cur?.kind === "plate" ? cur.item.block : undefined;
    switch (e.key) {
      case "j":
      case "ArrowDown":
        move(1);
        break;
      case "k":
      case "ArrowUp":
        move(-1);
        break;
      case "g": {
        const first = walkable()[0];
        if (first) moveTo(first.id);
        break;
      }
      case "G": {
        const last = walkable().at(-1);
        if (last) moveTo(last.id);
        break;
      }
      case "Enter":
      case "ArrowRight":
        if (t.tagName === "BUTTON" || t.tagName === "A" || t.tagName === "SUMMARY") return;
        if (cur?.kind === "platefold") view.openedFolds.add(cur.id);
        else if (blk) view.openBlock(blk.id);
        else if (cur?.kind === "tool" || cur?.kind === "run") f.expanded.add(cur.id);
        else return;
        opening = scroller?.querySelector<HTMLElement>(rowSelector(cur!.id)) ?? null;
        openingTop = opening?.getBoundingClientRect().top ?? 0;
        stuck = false;
        void tick().then(scrollOpening);
        break;
      case "ArrowLeft":
        if (blk) view.expanded.delete(blk.id);
        else if (cur?.kind === "tool" || cur?.kind === "run") f.expanded.delete(cur.id);
        else return;
        break;
      case "s":
        if (!blk || !view.expanded.has(blk.id)) return;
        {
          const on = view.streams.get(blk.id);
          view.streams.set(blk.id, on === "stderr" ? "stdout" : on === "stdout" ? "stderr" : blk.status === "failed" && blk.stderrBytes ? "stdout" : "stderr");
        }
        break;
      case "a":
        if (!blk) return;
        ssh.askAbout(sessionId, blk.id);
        break;
      case "X":
        if (blk?.status !== "running") return;
        void ssh.stop(sessionId, blk);
        break;
      case "/":
        openFind();
        break;
      case "Escape":
        scroller?.blur();
        break;
      default:
        return;
    }
    e.preventDefault();
    e.stopPropagation();
  }

  /** `[`, `]` and `u` work anywhere in the session, so they do not wait for the story to hold focus. */
  function onWindowKey(e: KeyboardEvent) {
    if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey) return;
    if (e.key !== "[" && e.key !== "]" && e.key !== "u") return;
    if (nav.focusedTerm !== sessionId || covered()) return;
    const t = e.target as HTMLElement | null;
    if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable)) return;
    e.preventDefault();
    if (e.key === "u") toCatchUp();
    else jumpTurn(e.key === "]" ? 1 : -1);
  }

  function jumpTurn(d: 1 | -1) {
    const els = [...(content?.querySelectorAll<HTMLElement>("[data-turn]") ?? [])];
    if (!els.length || !scroller) return;
    const top = scroller.getBoundingClientRect().top;
    let at = els.findIndex((el) => el.getBoundingClientRect().bottom > top + 8);
    if (at < 0) at = els.length - 1;
    const onEdge = Math.abs(els[at].getBoundingClientRect().top - top) < 8;
    const next = d > 0 ? at + 1 : onEdge ? at - 1 : at;
    const el = els[Math.max(0, Math.min(els.length - 1, next))];
    stuck = false;
    el.scrollIntoView?.({ block: "start" });
    lastTop = scroller.scrollTop;
    notAtTail = !atBottom();
  }

  // Clicking the story without selecting anything hands the keyboard back to the composer.
  function clickStory(e: MouseEvent) {
    const t = e.target as HTMLElement | null;
    if (!t || t.closest("button,a,input,textarea,select,summary,[role='button']")) return;
    const sel = window.getSelection?.();
    if (sel && !sel.isCollapsed) return;
    view.composerFocus++;
  }

  // --- what waits ------------------------------------------------------------
  function waitingCard(): boolean {
    return !!talk.state(sessionId)?.prompt || dockCards(view.connections, view.items, view.active()?.id ?? "").length > 0;
  }
  const waiting = $derived(waitingCard());

  function answerPill() {
    toBottom();
    view.dockFocus++;
  }

  // --- states ----------------------------------------------------------------
  // Nothing has happened yet but the brief it was given.
  const noLog = $derived(f.loaded && f.health === "empty" && !f.items.length && view.filter === "everything" && view.items.every((i) => i.human?.origin === "initial"));
  const reading = $derived(noLog && alive);
  const codexNote = $derived(session?.agent === "codex" && f.items.length > 0);

  function emptyLine(): string {
    if (view.filter === "facts") return "No commands yet.";
    if (view.filter === "findings") return `${agent} has not posted any findings or theories yet.`;
    if (noLog || f.scanning || f.health === "unreadable" || f.health === "unsupported") return "";
    const conn = view.active();
    if (conn?.wheel === "human") return "";
    if (conn?.state === "connected") {
      return session?.agentState === "idle" || session?.agentState === "waiting_input"
        ? `Connected. ${agent} is waiting for you to tell it what to look at.`
        : `Connected. ${agent} has not run anything yet.`;
    }
    return "";
  }

  // Lifecycle only: output and tool rows are never read out. Generation never moves focus.
  const announce = $derived.by(() => {
    if (waiting) return "Something is waiting for you.";
    const state = session?.agentState;
    if (state === "working" || state === "starting") return `${agent} is working.`;
    for (let i = story.turns.length - 1; i >= 0; i--) {
      const r = story.turns[i].receipt;
      if (r?.kind === "receipt") return r.stopped ? "Stopped." : `${agent} finished a turn.`;
    }
    return "";
  });
  const showHost = $derived(view.scope === "");
</script>

<svelte:window onkeydown={onWindowKey} />

<div class="cv-story relative grid min-h-0 grid-rows-[minmax(0,1fr)] overflow-hidden">
  <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
  <div
    bind:this={scroller}
    role="log"
    aria-label="Conversation"
    aria-live="off"
    tabindex="0"
    onscroll={onScroll}
    onwheel={onWheel}
    onpointerdowncapture={releaseOpening}
    onkeydowncapture={releaseOpening}
    onclickcapture={captureExpansion}
    onkeydown={onKey}
    onclick={clickStory}
    class="grid min-h-0 grid-cols-[minmax(0,1fr)] grid-rows-[minmax(0,1fr)_auto] overflow-y-scroll outline-none"
    data-timeline
  >
    <!-- A short story sits on the action zone: what the agent said stays beside what you act on,
         and the room above reads as history. This row collapses to nothing once the story
         overflows, so following the tail is unchanged. A grid and not flex with an auto margin,
         because WebKit does not stretch a flex child in a column. -->
    <div aria-hidden="true"></div>
    <!-- One line for the whole story, inside the column's padding, so a folded or off-screen turn does
         not break it and no width needs a mode of its own. -->
    <div
      bind:this={content}
      class="cv-band cv-stream relative pt-4 pb-4 {story.rowCount > 0 ? 'before:absolute before:top-4 before:bottom-0 before:left-2 before:w-px before:bg-edge/70' : ''}"
      data-rail={story.rowCount > 0 ? "" : undefined}
      data-stream-column
    >
      {#if !view.loaded && !f.loaded}
        <div class="grid gap-2" aria-hidden="true">
          {#each [0, 1, 2] as i (i)}<div class="h-6 rounded-md bg-sel/40"></div>{/each}
        </div>
      {:else if view.predates && !f.items.length}
        <div class="grid justify-items-start gap-2 py-2 text-faint">
          <p>This daemon is older than this app. Restart it to see this conversation.</p>
          <Button variant="secondary" size="sm" onclick={() => ssh.restartDaemon()}>Restart daemon</Button>
        </div>
      {:else if view.error && !story.rowCount}
        <div class="grid justify-items-start gap-2 py-2 text-faint">
          <p>Could not load this timeline. Lola keeps recording it.</p>
          <Button variant="secondary" size="sm" onclick={() => view.fetch()}>Try again</Button>
        </div>
      {/if}

      {#if f.scanning}
        <p class="mb-4 text-sm text-faint" data-feed-scanning>Reading this conversation</p>
      {/if}
      {#if noLog && session?.title}
        <h2 class="mb-4 text-lg text-ink" data-no-log>{session.title}</h2>
      {/if}
      {#if codexNote}
        <p class="mb-4 text-sm text-faint">Codex reports commands when they finish.</p>
      {/if}
      {#if f.more && !audit}
        <div class="mb-4">
          <Button size="xs" loading={f.loadingOlder} onclick={older}>Earlier in this conversation</Button>
        </div>
      {/if}
      {#if firstShown > 0}
        <!-- The row is the control, as in ToolRow. -->
        <button
          type="button"
          class="mb-8 grid w-full grid-cols-[auto_minmax(0,1fr)] items-center gap-x-3 rounded-sm text-left text-sm text-faint hover:text-ink"
          aria-expanded={false}
          onclick={() => f.expanded.add(`life-${story.turns[lifeAt].id}`)}
          data-earlier-life
        >
          <span class="inline-flex items-center gap-1.5">Earlier session, {earlierAgo} <Chevron /></span>
          <span class="h-px bg-edge/60" aria-hidden="true"></span>
        </button>
      {/if}

      {#each shownTurns as turn, i (turn.id)}
        {@const index = i + firstShown}
        <TimelineTurn
          {sessionId}
          {turn}
          folded={folded(turn.id, index)}
          afterFolded={i > 0 && folded(shownTurns[i - 1].id, index - 1)}
          pinned={index === story.turns.length - 1 || pinnedTurns.has(turn.id)}
          live={alive && index === story.turns.length - 1}
          latest={index === story.turns.length - 1}
          {chars}
          {showHost}
          cursor={view.cursor}
          catchUpRow={caught?.rowId ?? ""}
          catchUpWords={caught?.words ?? ""}
          {fresh}
          observe={observeTurn}
        />
      {/each}

      {#each [...view.outgoing, ...(view.sending ? [view.sending] : [])].filter(sent => (!view.scope || !sent.conn || view.scope === sent.conn) && !view.items.some(item => item.id === sent.item)) as sent}
        <div class="cv-turn relative mt-8" data-sending-message>
          <div class="cv-person">
            <div class="cv-speaker"><strong>You</strong></div>
            <div class="cv-user-message text-prose"><span class="selectable whitespace-pre-wrap break-words">{sent.text}</span></div>
            <p class="mt-1 flex items-center gap-2 text-sm text-faint" role="status">
              {#if !sent.item}<span class="size-3 rounded-full border border-current border-t-transparent motion-safe:animate-spin" aria-hidden="true"></span>Sending…
              {:else}{'delivered' in sent && sent.delivered ? 'Sent' : 'Queued'}{/if}
            </p>
          </div>
        </div>
      {/each}

      {#if story.rowCount === 0 && !view.sending && !view.outgoing.length && emptyLine()}
        <p class="text-faint">{emptyLine()}</p>
      {/if}
      {#if reading && store.alive}
        <p class="mt-4 text-sm text-faint" data-feed-reading>{agent} is reading the task</p>
      {/if}
    </div>
  </div>

  <!-- Always mounted, and only ever a lifecycle line. Its own region, so the log can stay quiet. -->
  <div class="sr-only" role="status">{announce}</div>

  {#if headAsk && !finding}
    <!-- The question a long answer has scrolled away from. It comes and goes only with the scroll. The
         row is the control, as in ToolRow. -->
    <button type="button" class="cv-context absolute inset-x-0 top-0 z-10 text-left" title={headAsk} onclick={toHead} data-turn-head>
      <span class="cv-band cv-context-inner"><span class="cv-context-source">{headLabel}</span><span class="min-w-0 truncate">{headSource?.kind === "origin" && headSource.label ? headSource.label : headAsk}</span><span class="cv-context-action">Back to message <Chevron /></span></span>
    </button>
  {/if}

  {#if finding}
    <div class="absolute top-2 right-6 z-10 flex items-center gap-2 rounded-md border border-edge/60 bg-panel px-2 py-1 shadow-md" data-find>
      <input
        bind:this={findEl}
        class="w-44 rounded-sm bg-transparent px-1 text-sm text-ink outline-none placeholder:text-placeholder"
        placeholder="Find in this conversation"
        aria-label="Find in this conversation"
        bind:value={query}
        oninput={() => (findAt = 0)}
        onkeydown={(e) => {
          e.stopPropagation();
          if (e.key === "Enter" || e.key === "ArrowDown") {
            e.preventDefault();
            walk(1);
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            walk(-1);
          } else if (e.key === "Escape") {
            e.preventDefault();
            finding = false;
            query = "";
            scroller?.focus();
          }
        }}
      />
      {#if query.trim()}
        {#if found.length}
          <span class="num text-sm text-faint">{findAt + 1} of {found.length}</span>
        {:else}
          <span class="text-sm text-faint">Nothing here matches. Output has its own find.</span>
        {/if}
      {/if}
    </div>
  {/if}

  <!-- Pills above the composer. They say what waits, and digging must not lose your place. -->
  {#if back != null || sincePill || (notAtTail && (waiting || newCount > 0))}
    <div class="pointer-events-none absolute inset-x-0 bottom-2 flex justify-center gap-2" data-pills>
      {#if back != null}
        <span class="pointer-events-auto"><Button variant="secondary" size="xs" class="bg-panel! shadow-md" onclick={goBack}>← Back to where you were</Button></span>
      {/if}
      {#if sincePill}
        <span class="pointer-events-auto"><Button variant="secondary" size="xs" class="bg-panel! shadow-md" onclick={toCatchUp}>Since you looked <span aria-hidden="true">↑</span></Button></span>
      {/if}
      {#if notAtTail && waiting}
        <span class="pointer-events-auto"><Button variant="secondary" size="xs" class="bg-panel! shadow-md" onclick={answerPill}>Waiting for you</Button></span>
      {:else if notAtTail && newCount > 0}
        <span class="pointer-events-auto"><Button variant="secondary" size="xs" class="bg-panel! shadow-md" onclick={toBottom}><span class="num">{newCount}</span> new</Button></span>
      {/if}
    </div>
  {/if}
</div>
