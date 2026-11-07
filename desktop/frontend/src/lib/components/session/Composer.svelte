<script lang="ts">
  import GlyphIcon from "$lib/components/GlyphIcon.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import type { FootState } from "./RailFoot.svelte";
  // One field, and the key decides where it goes: ↩ speaks to the agent and ⌘↩ runs on a host.
  // Tapping ⌘ twice swaps them for a stretch of typing commands. The worst mistake is a sentence
  // for the agent executing on a host, so running always shows as a different field, in grayscale
  // too: the tier owns the ring and the ground, never the accent, plus mono, a `$`, the tier's colour
  // down its side and a rule above the footer. Run and Ask must not share the brightest shape on screen.
  //
  // Holding ⌘ shows what the chord would do before anything happens. With no host none of this
  // exists, because there is nowhere for a command to go. All of it is read from the field's own
  // key events, so these keys mean nothing unless the field has focus.
  //
  // ↩ never interrupts. A human message goes to the agent's empty composer immediately;
  // the agent decides when to act on it. A blocked composer keeps the message queued.
  // Stopping is a separate act and refuses clicks briefly after any send, because a send
  // can change what is under the cursor.
  //
  // Which row of the footer applies is decided in composer.ts, and what a line starting with `/`
  // or `!` means in slash.ts. This file only draws the answer and carries it out.
  import { tick } from "svelte";
  import { attachmentTone, attachmentToken, expandPastes, fieldSegments, foldsPaste, isImagePath, textSize, tokenAtCaret, tokensIn, type Attachment } from "$lib/composerpaste";
  import { TalkService } from "@bindings/desktop";
  import { store } from "$lib/store.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import { talk } from "$lib/talk.svelte";
  import {
    SSH_MAX_SAY_BYTES,
    SSH_REMOTE_OUTLIVES,
    agentName,
    blockElapsedMs,
    byteLength,
    clockTime,
    formatBytes,
    formatMs,
    hostName,
    isEnded,
    isTerminal,
    needsTerminal,
    runRoute,
    tierOf,
  } from "$lib/ssh";
  import {
    AGENT_DEST,
    RUN_NEEDS_HOST,
    RUN_PICK_HOST,
    composerState,
    footerFor,
    isLiveHost,
    placeholderFor,
    queuedItems,
    runTarget,
    targetLabel,
    tierOneCount,
    trayCards,
    type FooterButton,
  } from "$lib/composer";
  import { BANG_NOTE, isBang, slashEnter, slashRows, unknownSlashNote } from "$lib/slash";
  import { elapsedWords } from "$lib/sessionview";
  import { toMillis } from "$lib/time";
  import Button from "../Button.svelte";
  import MenuItem from "../MenuItem.svelte";
  import SlashList from "./SlashList.svelte";
  import AttachGlyph from "./AttachGlyph.svelte";

  let { sessionId, railState = $bindable() }: { sessionId: string; /** What the footer says, for the rail to end in. */ railState?: FootState } = $props();

  const HOLD_MS = 180;
  const TAP_MS = 250;
  const DOUBLE_TAP_MS = 400;
  /** Stop, Stop & send and Revive refuse clicks this long after any send. */
  const SEND_GUARD_MS = 800;
  /** The size counter comes in at this share of the cap. */
  const COUNTER_AT = 0.75;
  const AGENT_KINDS = ["claude", "codex", "opencode"] as const;

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const talkData = $derived(talk.state(sessionId));
  const agent = $derived(agentName(session?.agent));
  const queued = $derived(queuedItems(view.items));

  const target = $derived(runTarget(view.connections, view.scope));
  const conn = $derived(target.conn);
  const canRun = $derived(conn?.state === "connected");
  const route = $derived(runRoute(conn, agent));
  const rail = $derived(({ production: "bg-bad", staging: "bg-warn", dev: "bg-ink/40" } as const)[tierOf(conn?.tier)]);
  // Literal strings, since Tailwind reads source text. The ring only says "focused" in neutral ink:
  // focus is on the field most of the time, so an accent ring would be the longest saturated line
  // on screen in every state.
  const runBox = $derived(
    ({
      production: "border-bad focus-within:border-bad bg-bad/5",
      staging: "border-warn focus-within:border-warn bg-warn/5",
      dev: "border-ink/40 focus-within:border-ink/40 bg-canvas",
    } as const)[tierOf(conn?.tier)],
  );
  const runRule = $derived(({ production: "border-bad/25", staging: "border-warn/25", dev: "border-ink/25" } as const)[tierOf(conn?.tier)]);

  let held = $state(false); // ⌘ held on the field: show what ⌘↩ would do
  let holdTimer: ReturnType<typeof setTimeout> | undefined;
  let lastTap = 0; // when ⌘ was last tapped alone
  let tapDown = 0; // when ⌘ went down, 0 once another key has made it a chord
  const runsByDefault = $derived(view.runDefault && !!conn);
  const running = $derived(canRun && runsByDefault !== (held && !!conn));

  // The swap names the destination, so the field shows that destination's draft. Holding ⌘ is
  // only a preview, and what it would run is the text already there.
  const text = $derived(runsByDefault && conn ? view.commandDraft(conn.id) : view.agentDraft);
  function setText(v: string) {
    if (runsByDefault && conn) view.setCommandDraft(conn.id, v);
    else view.agentDraft = v;
  }
  const dest = $derived(runsByDefault && conn ? conn.id : AGENT_DEST);

  // What goes, with paste tokens filled in. Only a draft for the agent holds tokens.
  const whole = $derived(runsByDefault ? text : expandPastes(text, view.pastes));
  const bytes = $derived(byteLength(whole));
  const pasted = $derived(runsByDefault ? [] : tokensIn(text, view.pastes));
  // The last long paste, for as long as nothing has touched the field since: the same text pasted
  // again straight after shows it as plain text.
  let lastPaste: { text: string; n: number; field: string } | null = null;

  // A textarea cannot style part of its text, so with a reference in it the field's text is drawn
  // by a layer behind it, cut the same way, and the textarea keeps the caret and the selection. Only
  // then: a plain field stays a plain field. The layer keeps the reference's own glyphs, clear, so the
  // two cannot drift, and the disc sits over them inside that width, or the field's edge would cut it.
  // A digit is centred by trimming its box to the digit, because line metrics differ between WebKits:
  // the packaged app drew it a pixel low where the test browser did not.
  let mirror = $state<HTMLElement | null>(null);
  const segments = $derived(pasted.length ? fieldSegments(text, view.pastes) : []);
  function syncMirror() {
    if (mirror && field) mirror.scrollTop = field.scrollTop;
  }
  $effect(() => {
    text;
    void tick().then(syncMirror);
  });

  function placeCaret(at: number) {
    void tick().then(() => field?.setSelectionRange(at, at));
  }
  // Reserve the reference at the caret immediately, before any asynchronous preparation.
  function attach(item: Attachment, clip: string) {
    const el = field;
    if (!el) return;
    if (view.pastes.size === 0) view.pasteSeq = 0;
    const n = ++view.pasteSeq;
    view.pastes.set(n, item);
    const made = attachmentToken(n);
    const from = el.selectionStart ?? el.value.length;
    const next = el.value.slice(0, from) + made + el.value.slice(el.selectionEnd ?? from);
    setText(next);
    placeCaret(from + made.length);
    lastPaste = clip ? { text: clip, n, field: next } : null;
    return n;
  }
  // Removing the tile removes its reference, and the other way round, so neither can be left alone.
  function removeAttachment(n: number) {
    const t = tokensIn(text, view.pastes).find((x) => x.n === n);
    view.pastes.delete(n);
    if (t) setText(text.slice(0, t.from) + text.slice(t.to));
    field?.focus();
  }
  async function prepareImage(load: () => Promise<{ path: string; thumb: string }>, clip = "") {
    const targetView = view;
    const targetDest = dest;
    const pending: Attachment = { kind: "image", value: "", loading: true };
    const n = attach(pending, clip);
    if (n === undefined) return;
    const stillAttached = () => {
      const draft = targetDest === AGENT_DEST ? targetView.agentDraft : targetView.commandDraft(targetDest);
      return targetView.pastes.get(n) === pending && tokensIn(draft, targetView.pastes).some(token => token.n === n);
    };
    try {
      const got = await load();
      // Removed, replaced or switched to a different draft: never move the caret or reinsert it.
      if (!stillAttached()) return;
      targetView.pastes.set(n, { kind: "image", value: got.path, thumb: got.thumb });
    } catch (err) {
      if (!stillAttached()) return;
      const error = err instanceof Error ? err.message : String(err);
      targetView.pastes.set(n, { kind: "image", value: clip, error });
      targetView.setFlash(`Could not attach image: ${error}. Remove it and paste again to retry.`, "text-warn");
    }
  }
  function attachClipboardImage() {
    const worktree = session?.worktree ?? "";
    return prepareImage(() => TalkService.AttachClipboardImage(worktree));
  }
  function attachImageFile(clip: string) {
    const worktree = session?.worktree ?? "";
    return prepareImage(() => TalkService.AttachImageFile(worktree, clip.trim()), clip);
  }
  function onPaste(e: ClipboardEvent) {
    const clip = e.clipboardData?.getData("text/plain") ?? "";
    const el = e.currentTarget as HTMLTextAreaElement;
    if (running) return;
    // No text on the clipboard: an image, if anything. WebKit may not hand one over, so the app reads it.
    if (!clip) {
      e.preventDefault();
      void attachClipboardImage();
      return;
    }
    const again = lastPaste && lastPaste.text === clip && lastPaste.field === el.value ? lastPaste : null;
    lastPaste = null;
    if (again) {
      const t = tokensIn(el.value, view.pastes).find((x) => x.n === again.n);
      if (t) {
        e.preventDefault();
        view.pastes.delete(again.n);
        setText(el.value.slice(0, t.from) + clip + el.value.slice(t.to));
        placeCaret(t.from + clip.length);
        return;
      }
    }
    if (isImagePath(clip)) {
      e.preventDefault();
      void attachImageFile(clip);
      return;
    }
    if (!foldsPaste(clip)) return;
    e.preventDefault();
    attach({ kind: "text", value: clip }, clip);
  }
  /** Backspace or Delete against a token takes the whole of it, since half of one means nothing. */
  function deleteToken(e: KeyboardEvent): boolean {
    if (running || (e.key !== "Backspace" && e.key !== "Delete") || !field) return false;
    if (field.selectionStart !== field.selectionEnd) return false;
    const caret = field.selectionStart ?? 0;
    const t = tokenAtCaret(field.value, caret, e.key === "Delete", view.pastes);
    if (!t) return false;
    e.preventDefault();
    view.pastes.delete(t.n);
    setText(field.value.slice(0, t.from) + field.value.slice(t.to));
    placeCaret(t.from);
    return true;
  }
  const showCounter = $derived(bytes >= SSH_MAX_SAY_BYTES * COUNTER_AT);
  const over = $derived(bytes > SSH_MAX_SAY_BYTES);
  const hasText = $derived(!!text.trim());
  const attachmentsPreparing = $derived(pasted.some(t => view.pastes.get(t.n)?.loading));
  const attachmentFailed = $derived(pasted.some(t => !!view.pastes.get(t.n)?.error));
  const attachmentsBlocked = $derived(attachmentsPreparing || attachmentFailed);

  const cards = $derived(
    trayCards({
      connections: view.connections,
      items: view.items,
      activeConn: view.activeConn,
      answered: view.answeredPrompts,
      agentPrompt: talkData?.prompt ?? null,
    }),
  );
  const composerNow = $derived(
    composerState({
      alive: store.alive,
      talk: talkData,
      agentState: session?.agentState,
      inputReason: session?.inputReason,
      cardWaiting: tierOneCount(cards) > 0,
      held: queued.length,
    }),
  );
  const canStop = $derived(!!talkData?.caps?.stop && !session?.goalId);
  const driven = $derived(view.connections.find((c) => c.wheel === "human" && !isEnded(c) && c.state !== "denied" && (!view.scope || c.id === view.scope)));
  const quotaWindow = $derived.by(() => {
    const q = store.status?.usage?.quotas?.find((x) => x.agent === (session?.agent || "claude"));
    return [...(q?.windows ?? [])].sort((a, b) => b.usedPercent - a.usedPercent)[0];
  });
  const footer = $derived(
    footerFor({
      state: composerNow,
      agent,
      hasText,
      over,
      canStop,
      canSend: !!talkData?.caps?.send,
      held: queued.length,
      resets: quotaWindow?.resetsAt ? clockTime(quotaWindow.resetsAt) : "",
      running,
      runsByDefault,
      target: targetLabel(conn),
      pick: target.why === "pick",
      driving: driven ? hostName(driven) : undefined,
    }),
  );
  const working = $derived(footer.clock === true);
  const clock = $derived(working ? elapsedWords(sshClock.now - toMillis(session?.agentStateSince as unknown as string)) : "");
  // Run mode's prompt is about the field, not about the agent, so it stays in here.
  $effect(() => {
    railState = footer.mono ? undefined : { glyph: footer.glyph, glyphCls: footer.glyphCls, text: footer.text, textCls: footer.textCls, clock };
  });
  const placeholder = $derived(running && conn ? `Command to run on ${hostName(conn)}` : placeholderFor(composerNow, agent, !!driven));

  // In flight on a host you drive, with their own Stop: Stop ⌘. stops a turn, not these.
  const inFlight = $derived(view.items.filter((i) => i.block?.status === "running" && i.block?.origin !== "terminal" && i.block?.conn === driven?.id));
  // A command in your shell that printed nothing, or stopped mid-line, may be asking for something
  // lola did not recognize. A question it did recognize has its own card.
  const waitingShell = $derived(
    !!conn &&
      !conn.prompt &&
      view.items.some((i) => i.block?.conn === conn.id && i.block?.origin === "terminal" && i.block?.status === "running" && (conn.termOpenLine || !i.block?.stdoutBytes)),
  );

  let field = $state<HTMLTextAreaElement | null>(null);
  let busy = $state(false);
  let walked = $state(-1); // position in this destination's history; -1 is the draft
  let warned = $state(""); // the /word lola already warned about
  let picked = $state(0);
  let dismissed = $state(""); // the text whose list Esc closed
  let note = $state("");
  let lastSend = 0;

  const slashOn = $derived(!running && !runsByDefault);
  const rows = $derived(slashOn && text !== dismissed ? slashRows(text, session?.agent) : []);
  const pickedRow = $derived(rows[Math.min(picked, rows.length - 1)]?.name ?? "");
  const unknownNote = $derived(warned && text.trimStart().startsWith(warned) ? unknownSlashNote(warned, agent) : "");
  const bangNote = $derived(slashOn && isBang(text) ? BANG_NOTE : "");

  $effect(() => {
    const n = view.composerFocus;
    if (n === view.seen.composer) return;
    view.seen.composer = n;
    void tick().then(() => {
      field?.focus();
      const end = field?.value.length ?? 0;
      field?.setSelectionRange(end, end);
    });
  });

  // It grows to its cap, 40% of the column, then scrolls.
  $effect(() => {
    text;
    running;
    const el = field;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${el.scrollHeight}px`;
  });

  // Your command came back from the shell as a block: open it, as with any you ran.
  $effect(() => {
    if (!view.expectShellBlock) return;
    const it = [...view.items].reverse().find((i) => i.block?.origin === "terminal" && i.block?.actor === "human" && (!conn || i.block?.conn === conn.id));
    if (!it?.block || isTerminal(it.block.status)) return;
    view.expectShellBlock = false;
    view.openBlock(it.block.id);
  });

  function flash(line: string) {
    note = line;
  }

  /** A click on something a send may just have changed. */
  function guarded(): boolean {
    return Date.now() - lastSend < SEND_GUARD_MS;
  }

  // What is left of a field after a send. `text` is a derived of the current draft, so it has to
  // be captured before the wait: a person can go on typing while a send is on its way, and a swap
  // can change the field. Only what actually went is taken out.
  function unsent(now: string, sent: string): string {
    if (now === sent) return "";
    return now.startsWith(sent) ? now.slice(sent.length).trimStart() : now;
  }

  async function ask(stopFirst = false) {
    const raw = text;
    const from = dest;
    const value = whole.replace(/\s+$/, "");
    if (!value.trim() || busy || view.sending || over || attachmentsBlocked) return;
    note = "";
    // Stop & send asks the same question. A screen command typed mid-turn is still a screen command.
    const next = slashEnter(value, session?.agent, pickedRow, warned);
    if (next.act === "fill") {
      setText(next.text);
      picked = 0;
      return;
    }
    if (next.act === "terminal") {
      setText("");
      ssh.showAgentTerminal(sessionId);
      return;
    }
    if (next.act === "warn") {
      warned = next.word;
      return;
    }
    busy = true;
    lastSend = Date.now();
    const sendingView = view;
    const attachments = tokensIn(raw, sendingView.pastes);
    // Clear immediately; a slower daemon must not make Send look unresponsive.
    if (from === AGENT_DEST) sendingView.agentDraft = "";
    else sendingView.setCommandDraft(from, "");
    for (const t of attachments) {
      const item = sendingView.pastes.get(t.n);
      if (item?.kind === "image" && item.thumb) sendingView.sentThumbs.set(item.value, item.thumb);
    }
    if (await ssh.speak(sessionId, value, stopFirst)) {
      sendingView.pushHistory(AGENT_DEST, value);
      if (from === AGENT_DEST) {
        for (const t of attachments) if (!sendingView.agentDraft.includes(raw.slice(t.from, t.to))) sendingView.pastes.delete(t.n);
      }
      walked = -1;
      warned = "";
    } else {
      // Preserve anything typed during the request, alongside the unsent message.
      const draft = from === AGENT_DEST ? sendingView.agentDraft : sendingView.commandDraft(from);
      const restored = draft ? `${raw}\n${draft}` : raw;
      if (from === AGENT_DEST) sendingView.agentDraft = restored;
      else sendingView.setCommandDraft(from, restored);
    }
    busy = false;
  }

  async function run() {
    const raw = text;
    const from = dest;
    const value = raw.trim();
    if (!conn) {
      flash(target.why === "pick" ? RUN_PICK_HOST : RUN_NEEDS_HOST);
      return;
    }
    if (!value || busy) return;
    const host = hostName(conn);
    if (!canRun) {
      view.setFlash(`Not connected, so nothing can run on ${host}.`, "text-warn");
      return;
    }
    const interactive = needsTerminal(value);
    if (interactive && !route.inShell) {
      view.setFlash("This looks interactive, and your shell cannot take it right now.", "text-warn");
      return;
    }
    busy = true;
    lastSend = Date.now();
    const on = conn.id;
    if (await ssh.runCommand(sessionId, on, value)) {
      if (from === AGENT_DEST) view.agentDraft = unsent(view.agentDraft, raw);
      else view.setCommandDraft(from, unsent(view.commandDraft(from), raw));
      walked = -1;
      if (interactive) ssh.showHostTerminal(sessionId, on);
    }
    busy = false;
  }

  function onPrimary(b: FooterButton) {
    switch (b.act) {
      case "send":
      case "queue":
      case "ask":
        void ask();
        break;
      case "sendHeld":
        ssh.sendHeld(sessionId);
        break;
      case "run":
        void run();
        break;
      case "stopSend":
        if (!guarded()) void ask(true);
        break;
      case "revive":
        if (!guarded()) store.revive(sessionId);
        break;
    }
  }

  function stop() {
    if (guarded()) return;
    talk
      .stop(sessionId)
      .then(() => talk.refresh(sessionId))
      .catch((err) => flash(String(err)));
  }

  // ↑ on an empty field takes the newest queued message back first, because that is what was just
  // typed, and only then walks what has already gone.
  async function upFromEmpty(): Promise<boolean> {
    if (dest !== AGENT_DEST) return false;
    const it = queued[queued.length - 1];
    if (!it?.human) return false;
    if (await ssh.recall(sessionId, it.id)) view.agentDraft = it.human.text;
    return true;
  }

  function historyStep(step: number) {
    const h = view.historyFor(dest);
    if (!h.length) return;
    const from = walked < 0 ? h.length : walked;
    const next = from + step;
    if (next < 0) return;
    if (next >= h.length) {
      walked = -1;
      setText("");
      return;
    }
    walked = next;
    setText(h[next]);
  }

  function insertLine() {
    const el = field;
    if (!el) return;
    const at = el.selectionStart ?? el.value.length;
    const to = el.selectionEnd ?? at;
    setText(`${el.value.slice(0, at)}\n${el.value.slice(to)}`);
    void tick().then(() => el.setSelectionRange(at + 1, at + 1));
  }

  function onKey(e: KeyboardEvent) {
    if (e.isComposing) return;
    if (deleteToken(e)) return;
    // As in Claude Code and Codex. On a Mac this chord is otherwise a page down in a text field.
    if (e.key === "v" && e.ctrlKey && !e.metaKey && !e.altKey && !running) {
      e.preventDefault();
      void attachClipboardImage();
      return;
    }
    if (e.key === "Meta") {
      if (!e.repeat) metaDown();
      return;
    }
    tapDown = 0; // ⌘C and friends are chords, not taps
    if (rows.length) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        picked = (Math.min(picked, rows.length - 1) + (e.key === "ArrowDown" ? 1 : rows.length - 1)) % rows.length;
        return;
      }
      if (e.key === "Tab" && !e.shiftKey) {
        e.preventDefault();
        setText(pickedRow);
        picked = 0;
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        dismissed = text;
        return;
      }
    }
    if (e.key === "Enter") {
      if (e.shiftKey) return; // the textarea's own newline
      if (e.altKey) {
        e.preventDefault();
        insertLine();
        return;
      }
      e.preventDefault();
      release();
      if ((e.metaKey || e.ctrlKey) !== runsByDefault) void run();
      else if (footer.primary.act === "sendHeld") ssh.sendHeld(sessionId);
      else void ask();
    } else if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      field?.blur(); // leaves the field and keeps the draft
    } else if (e.key === "Tab" && !e.shiftKey && !text && cards.length) {
      // The tray precedes the field in the DOM, so a plain Tab would walk on to the footer.
      e.preventDefault();
      view.dockFocus++;
    } else if (e.key === "ArrowUp" && !text && walked < 0) {
      e.preventDefault();
      void upFromEmpty().then((took) => {
        if (!took) historyStep(-1);
      });
    } else if (walked >= 0 && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
      e.preventDefault();
      historyStep(e.key === "ArrowUp" ? -1 : 1);
    }
  }

  // After a beat, so ⌘C or a double tap does not flash the other look. Nothing at all with no host.
  function metaDown() {
    if (!conn) return;
    tapDown = Date.now();
    clearTimeout(holdTimer);
    holdTimer = setTimeout(() => (held = true), HOLD_MS);
  }
  function onKeyUp(e: KeyboardEvent) {
    if (e.key !== "Meta") return;
    release();
    if (!conn) return;
    const now = Date.now();
    if (!tapDown || now - tapDown > TAP_MS) {
      lastTap = 0;
      return;
    }
    tapDown = 0;
    if (now - lastTap > DOUBLE_TAP_MS) {
      lastTap = now;
      return;
    }
    lastTap = 0;
    swap();
  }
  // Text typed in the wrong mode comes along, as long as it would not land on something else.
  function swap() {
    if (!conn) return;
    const toRun = !view.runDefault;
    // A command must be readable, so a token going to the host arrives as its text.
    const from = toRun ? whole : text;
    const there = toRun ? view.commandDraft(conn.id) : view.agentDraft;
    view.runDefault = toRun;
    if (from && !there) {
      if (toRun) {
        view.agentDraft = "";
        view.setCommandDraft(conn.id, from);
      } else {
        view.setCommandDraft(conn.id, "");
        view.agentDraft = from;
      }
    }
  }
  function release() {
    clearTimeout(holdTimer);
    held = false;
  }

  // --- the footer's left --------------------------------------------------------

  const stopNote = $derived.by(() => {
    const live = view.connections.find((c) => isLiveHost(c) && (c.runningBlocks ?? 0) > 0);
    const base = "Stop this turn. The session stays open.";
    return live ? `${base} Commands already running on ${hostName(live)} keep running. Stop them on their row.` : base;
  });
  const otherAgents = $derived(AGENT_KINDS.filter((k) => k !== (session?.agent || "claude")));
  let switching = $state<{ x: number; y: number } | null>(null);
  function openSwitch(e: MouseEvent) {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
    switching = { x: r.left, y: r.top - 4 };
  }
</script>

{#snippet keys(b: FooterButton)}
  {b.label}{#if b.keys}{" "}<span class="num opacity-70" aria-hidden="true">{b.keys}</span>{/if}
{/snippet}

<div class="grid gap-2">
  {#if inFlight.length}
    <div class="grid text-sm" data-driving>
      {#each inFlight as it, i (it.id)}
        {#if it.block}
          {@const b = it.block}
          <div class="grid h-6 min-w-0 grid-cols-[56px_minmax(0,1fr)_auto_auto] items-center gap-x-2">
            <span class="text-faint">{i === 0 ? "Running" : ""}</span>
            <span class="selectable truncate font-mono text-ink">{b.command}</span>
            <span class="num text-faint">{formatMs(blockElapsedMs(b, sshClock.now))}</span>
            <Button variant="danger" size="xs" class="h-5!" title={SSH_REMOTE_OUTLIVES} onclick={() => ssh.stop(sessionId, b)}>Stop</Button>
          </div>
        {/if}
      {/each}
    </div>
  {/if}
  {#if waitingShell}
    <div class="flex items-center gap-2 text-sm text-faint" data-shell-waiting>
      <span class="min-w-0">Waiting for input?</span>
      <Button size="xs" class="h-5!" onclick={() => conn && ssh.showHostTerminal(sessionId, conn.id)}>Open terminal</Button>
    </div>
  {/if}

  <div
    class="cv-composer relative overflow-hidden border transition-colors {running ? runBox : 'border-edge bg-panel'}"
    data-composer={running ? "run" : "ask"}
    data-filled={view.widgetFill ? "" : undefined}
  >
    {#if running}
      <span class="absolute inset-y-0 left-0 w-[3px] {rail}" aria-hidden="true"></span>
    {:else if view.widgetFill}
      <span class="absolute inset-y-0 left-0 w-0.5 bg-magenta" aria-hidden="true" data-fill-rail></span>
    {/if}
    {#if view.widgetFill}
      <div class="flex items-center gap-1 px-3 pt-2 text-sm text-faint" data-fill-chip>
        <span class="min-w-0 truncate">Filled by {view.widgetFill.name}</span>
        <span aria-hidden="true">·</span>
        <Button size="xs" class="h-5!" onclick={() => { view.agentDraft = ""; view.composerFocus++; }}>Clear</Button>
      </div>
    {/if}
    <!-- The same box in both looks: holding ⌘ must not move the story above it. -->
    {#if pasted.length}
      <ul class="flex flex-wrap gap-2 px-3 pt-2.5" aria-label="Attached to this message" data-attachments>
        {#each pasted as t (t.n)}
          {@const item = view.pastes.get(t.n)}
          {#if item}
            <li class="group relative size-12" data-attachment={item.kind} aria-busy={item.loading ? true : undefined}>
              {#if item.loading}
                <span class="grid size-12 place-items-center rounded-md border border-dashed border-edge bg-canvas text-faint" role="status" aria-label="Preparing image {t.n}">
                  <span class="size-5 rounded-full border-2 border-current border-t-transparent motion-safe:animate-spin" aria-hidden="true"></span>
                </span>
              {:else if item.error}
                <span class="grid size-12 place-items-center rounded-md border border-warn bg-canvas text-warn" role="img" aria-label="Image {t.n} failed to attach" title={item.error}>
                  <Icon name="triangle-alert" class="size-5" />
                </span>
              {:else if item.kind === "image" && item.thumb}
                <img src={item.thumb} alt="Attachment {t.n}" class="size-12 rounded-md border border-edge object-cover" />
              {:else}
                {@const said = item.kind === "image" ? "Image" : textSize(item.value)}
                <span class="grid size-12 place-items-center rounded-md border border-edge bg-canvas text-faint" title={said} role="img" aria-label="{said}, attachment {t.n}">
                  <AttachGlyph kind={item.kind} class="size-5" />
                </span>
              {/if}
              <span class="num absolute -top-1.5 -right-1.5 grid size-[18px] place-items-center rounded-full text-[11px] font-semibold leading-none text-canvas {attachmentTone(t.n).badge}" aria-hidden="true"><span class="block [text-box:trim-both_cap_alphabetic]">{t.n}</span></span>
              <!-- Hand-rolled on purpose: a mark sitting on a corner of a tile, smaller than any Button. -->
              <button
                type="button"
                class="absolute -top-1.5 -left-1.5 hidden size-[18px] place-items-center rounded-full border border-edge bg-panel text-xs text-faint group-hover:grid group-focus-within:grid hover:text-ink focus-visible:grid"
                aria-label="Remove attachment {t.n}"
                onclick={() => removeAttachment(t.n)}
              ><Icon name="x" size={14} /></button>
            </li>
          {/if}
        {/each}
      </ul>
    {/if}
    <div class="cv-composer-field flex items-start" data-composer-field>
      {#if running}<span class="text-prose pr-1.5 font-mono text-ink-2" aria-hidden="true">$</span>{/if}
      <div class="grid min-w-0 flex-1 grid-cols-[minmax(0,1fr)]">
        {#if segments.length}
          <div bind:this={mirror} class="text-prose pointer-events-none max-h-[min(160px,30cqh)] min-w-0 overflow-hidden break-words whitespace-pre-wrap text-ink" style="grid-area:1/1" aria-hidden="true" data-field-mirror>
            {#each segments as seg, i (i)}{#if seg.n}{@const tone = attachmentTone(seg.n)}<span class="relative"><span class="text-transparent">{seg.text}</span><span class="absolute top-1/2 left-1/2 grid size-[15px] -translate-x-1/2 -translate-y-1/2 place-items-center rounded-full text-[10px] font-semibold leading-none {tone.tint} {tone.text}" data-field-ref={seg.n}><span class="block [text-box:trim-both_cap_alphabetic]">{seg.n}</span></span></span>{:else}{seg.text}{/if}{/each}{"\u200b"}
          </div>
        {/if}
        <textarea
          bind:this={field}
          value={text}
          rows="1"
          spellcheck={!running}
          style="grid-area:1/1"
          onscroll={syncMirror}
          class="text-prose max-h-[min(160px,30cqh)] min-w-0 resize-none overflow-y-auto bg-transparent {segments.length ? 'text-transparent' : 'text-ink'} outline-none placeholder:text-placeholder focus-visible:outline-none! {running ? 'font-mono' : 'caret-accent'}"
          {placeholder}
          aria-label={placeholder}
          oninput={(e) => {
            setText(e.currentTarget.value);
            walked = -1;
            picked = 0;
            note = "";
          }}
          onpaste={onPaste}
          onkeydown={onKey}
          onkeyup={onKeyUp}
          onblur={release}
        ></textarea>
      </div>
    </div>

    {#if rows.length}<SlashList {rows} selected={Math.min(picked, rows.length - 1)} onpick={(name) => { setText(name); picked = 0; field?.focus(); }} />{/if}

    {#if view.flash}<p class="px-3 pt-1 text-sm {view.flashCls}" role="status">{view.flash}</p>
    {:else if attachmentsPreparing}<p class="px-3 pt-1 text-sm text-faint" role="status">Preparing image… You can keep typing.</p>
    {:else if note}<p class="px-3 pt-1 text-sm text-faint" role="status" data-composer-note>{note}</p>
    {:else if unknownNote}<p class="px-3 pt-1 text-sm text-warn" role="status" data-composer-note>{unknownNote}</p>
    {:else if bangNote}<p class="px-3 pt-1 text-sm text-faint" data-composer-note>{bangNote}</p>{/if}
    {#if showCounter}
      <p class="num px-3 pt-1 text-right text-sm {over ? 'text-warn' : 'text-faint'}" data-composer-counter>{formatBytes(bytes)} of {formatBytes(SSH_MAX_SAY_BYTES)}</p>
    {/if}

    <!-- Three groups at most, and each keeps its place, so Stop and the primary never move. The rule
         is always there and only coloured in Run, so holding ⌘ cannot move the box. -->
    <div class="cv-composer-actions grid grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-2 border-t {running ? runRule : 'border-transparent'}" data-composer-actions data-footer-state={footer.state}>
      <div class="flex min-w-0 items-center gap-1.5 text-sm" data-footer-left>
        {#if footer.glyph}<span class="w-3.5 shrink-0 {footer.glyphCls} {footer.pulse ? 'ssh-pulse motion-reduce:animate-none' : ''}" aria-hidden="true" data-footer-glyph><GlyphIcon glyph={footer.glyph} /></span>{/if}
        <span class="selectable min-w-0 truncate {footer.textCls} {footer.mono ? 'font-mono' : ''}" data-run-prompt={footer.mono ? "" : undefined} data-footer-text title={composerNow === "unproven" ? talkData?.caps?.why : undefined}>{footer.text}{#if clock}{" "}<span class="num inline-block min-w-[6ch]">{clock}</span>{/if}</span>
        {#each footer.left as act (act)}
          {#if act === "stop"}
            <Button variant="danger" size="xs" class="h-5!" title={stopNote} onclick={stop} data-footer-stop><span aria-hidden="true"><Icon name="square" size={14} /></span> Stop <span class="text-faint">⌘.</span></Button>
          {:else if act === "terminal"}
            <!-- `pointer-events` and opacity: the way out must keep working while the daemon is down. -->
            <Button size="xs" class="pointer-events-auto! h-5! opacity-100!" onclick={() => ssh.showAgentTerminal(sessionId)}>Terminal <span class="text-faint">⌘\</span></Button>
          {:else if act === "sendThem"}
            <Button variant="secondary" size="xs" class="h-5!" onclick={() => ssh.sendHeld(sessionId)}>Send them</Button>
          {:else if act === "handBack" && driven}
            <Button variant="secondary" size="xs" class="h-5!" onclick={() => ssh.openHandback(sessionId, driven.id)}>Hand back</Button>
          {:else if act === "switchAgent"}
            <Button variant="secondary" size="xs" class="h-5!" aria-haspopup="menu" aria-expanded={!!switching} onclick={openSwitch}>Switch agent…</Button>
          {/if}
        {/each}
      </div>
      <div class="flex items-center gap-1">
        {#if footer.hint && composerNow !== "card" && composerNow !== "modal"}<span class="truncate text-sm text-faint" data-footer-hint>{footer.hint}</span>
        {:else if !running && composerNow === "resting"}<span class="cv-compose-hint" data-footer-hint><kbd>⇧ ↵</kbd> new line</span>{/if}
        {#each footer.secondary as b (b.act)}
          <Button variant="secondary" size="sm" disabled={b.disabled || (busy && b.act !== "revive") || (attachmentsBlocked && ["send", "queue", "stopSend"].includes(b.act))} title={b.act === "run" ? (canRun ? route.note : `Not connected, so nothing can run on ${hostName(conn)}.`) : undefined} onclick={() => onPrimary(b)} data-footer-secondary={b.act}>
            {@render keys(b)}
          </Button>
        {/each}
      </div>
      <Button variant={footer.primary.variant} size="md" class="cv-composer-send justify-center!" loading={busy || !!view.sending} disabled={footer.primary.disabled || (busy && footer.primary.act !== "revive") || (attachmentsBlocked && ["send", "queue", "stopSend"].includes(footer.primary.act))} title={footer.primary.act === "run" ? route.note : `${footer.primary.label} ${footer.primary.keys ?? ''}`} onclick={() => onPrimary(footer.primary)} data-footer-primary={footer.primary.act}>
        {#if footer.primary.act === 'send' || footer.primary.act === 'queue'}
          <span class="sr-only">{footer.primary.label}</span>
          <Icon name="arrow-up" class="size-4" />
        {:else}{@render keys(footer.primary)}{/if}
      </Button>
    </div>
  </div>
</div>

{#if switching}
  <div class="fixed inset-0 z-40" role="presentation" onclick={() => (switching = null)}></div>
  <div class="fixed z-50 w-48 -translate-y-full rounded-lg border border-edge bg-panel p-1 shadow-xl" style="left:{switching.x}px;top:{switching.y}px" role="menu">
    {#each otherAgents as kind (kind)}
      <MenuItem icon="⇄" onclick={() => { switching = null; store.askSwitchAgent(sessionId, kind); }}>Switch to {kind}</MenuItem>
    {/each}
  </div>
{/if}
