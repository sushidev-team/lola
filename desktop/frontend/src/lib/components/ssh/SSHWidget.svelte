<script lang="ts">
  // An agent's own page, in a dock panel. The panel draws the chrome that labels it as the
  // agent's, and the one-click gate, so this is only the frame. Agent code is untrusted: the agent reads remote
  // output that anyone can write to. So it runs with `sandbox="allow-scripts"` and nothing
  // else, which gives it an opaque origin, and a policy with no network for what it loads.
  //
  // What the sandbox does not do, handled here:
  //   - It shares this thread, so an endless loop would freeze the window. Hence the one
  //     click per session before any runs, kept in memory so a new run starts clean.
  //   - It can navigate itself, and a new document does not inherit the policy. Nothing here
  //     can stop the request, because that takes a native rule. So everything is arranged for
  //     what comes after: the SDK makes a MessageChannel before the agent's code runs and hands
  //     one port to us in a boot message, and that port is the only way in or out. Data, theme
  //     and clicks all use it, so a new document gets nothing and can fill in nothing. It also
  //     cannot answer a ping, which is how a navigation with no second load event is caught: one
  //     that never finishes, or one before the first load. That closes the frame.
  //   - It could draw something that looks like lola. The frame is always inside our own
  //     chrome, labelled as the agent's, and its buttons can only fill in the composer.
  //   - A message arrives with no user gesture we could verify, since a page can fake any
  //     sign of a click. So a fill never moves focus. A human typing into a prompt card or
  //     the terminal would otherwise lose the rest of a password to the composer, at a moment
  //     the agent can time by posting data.
  //
  // This component must never move in the DOM. WebKit reloads an iframe that does, which
  // would lose the page's state and trip the navigation guard. See DockArea.
  //
  // A widget that fails must not fail silently. What the page says about itself goes to the
  // agent as a framed line, once per revision, so it can fix the page. The human only ever
  // sees lola's own words for the kind of failure, because the page's text is untrusted.
  import { untrack } from "svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { appearance } from "$lib/theme-runtime.svelte";
  import { WIDGET_BOOT_GRACE_MS, WIDGET_FAULTS, WIDGET_PING_MISSES, WIDGET_PING_MS, WIDGET_READY_MS, WIDGET_SANDBOX, widgetDoc, widgetFault, widgetSrc, widgetText, widgetThemeVars, type SSHElement, type WidgetFault } from "$lib/ssh";
  import Button from "../Button.svelte";

  // `active` is false for a tab in the background: it stays alive, and costs less.
  let { sessionId, element, agent, active = true }: { sessionId: string; element: SSHElement; agent: string; active?: boolean } = $props();

  const view = $derived(ssh.view(sessionId));
  const html = $derived(element.html ?? "");
  const data = $derived(element.data);
  const reloads = $derived(view.widgetReloads.get(element.id) ?? 0);

  let frame = $state<HTMLIFrameElement | null>(null);
  let port: MessagePort | null = null;
  let pinger: ReturnType<typeof setInterval> | undefined;
  let bootCheck: ReturnType<typeof setTimeout> | undefined;
  let answered = () => {};
  let loads = 0;
  let closed = $state(false);
  let lastFill = 0;
  let ready = false;
  let fault = $state.raw<WidgetFault | null>(null);
  let told = $state(false);
  let reportedRevision = 0;
  let sentData: string | undefined;
  let seeded: string | undefined;
  let sentTheme = "";

  const readToken = (token: string) => getComputedStyle(document.documentElement).getPropertyValue(token).trim();
  const scheme = () => (getComputedStyle(document.documentElement).colorScheme.includes("light") ? "light" : "dark");
  const themeNow = () => JSON.stringify({ vars: widgetThemeVars(readToken), scheme: scheme() });

  // New HTML is a new page. New data alone is not: it arrives as a message, so a chart
  // keeps its state and an animation keeps moving. So the document reads the data once,
  // without depending on it, and the frame below is keyed on the HTML. Reload is the human
  // asking for the page again, with whatever data is newest by then. The theme is the same
  // story as the data, so a switch reaches a running page without costing it its state.
  const shownDoc = $derived.by(() => {
    const page = html;
    reloads;
    return untrack(() => {
      sentTheme = themeNow();
      return widgetDoc(
        page,
        (seeded = sentData = element.data),
        readToken,
        typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches,
        scheme(),
      );
    });
  });
  $effect.pre(() => {
    html;
    reloads;
    dropPort();
    loads = 0;
    closed = false;
    ready = false;
    fault = null;
    told = false;
    view.widgetFaults.delete(untrack(() => element.id));
  });
  $effect(() => dropPort);

  function dropPort() {
    clearInterval(pinger);
    clearTimeout(bootCheck);
    port?.close();
    port = null;
  }

  // Silence is a failure too: a page that never says it finished is reported, not waited on
  // forever. A hidden window lags its timers, so it looks again rather than guessing.
  $effect(() => {
    if (!frame || closed) return;
    let timer: ReturnType<typeof setTimeout>;
    const check = () => {
      if (ready) return;
      if (document.hidden) timer = setTimeout(check, WIDGET_READY_MS);
      else noteFault({ kind: "stuck", message: "", line: 0 });
    };
    timer = setTimeout(check, WIDGET_READY_MS);
    return () => clearTimeout(timer);
  });

  function noteFault(f: WidgetFault) {
    // The first failure of a page is the useful one: the rest usually follow from it.
    if (fault) return;
    fault = f;
    view.widgetFaults.set(element.id, f.kind);
    const rev = element.revision;
    if (reportedRevision === rev) return;
    reportedRevision = rev;
    void ssh.reportWidget(sessionId, element.id, rev, f).then((yes) => {
      if (fault === f) told = yes;
    });
  }

  function closeFrame() {
    if (closed) return;
    closed = true;
    dropPort();
    fault = null;
    noteFault({ kind: "navigated", message: "", line: 0 });
  }

  function sendData(d: string | undefined) {
    if (!port || d === undefined) return;
    let parsed: unknown = null;
    try {
      parsed = JSON.parse(d);
    } catch {
      return;
    }
    port.postMessage({ lola: "data", data: parsed });
    sentData = d;
  }

  // A tab in the background gets its newest data when it comes forward, not every update
  // on the way, because nobody is looking at the redraws.
  $effect(() => {
    const d = data;
    if (!frame || !active || d === sentData) return;
    // New data is a new chance: if this one is broken too, the page will say so.
    untrack(() => {
      fault = null;
      view.widgetFaults.delete(element.id);
    });
    sendData(d);
  });

  function tellVisible(visible: boolean) {
    port?.postMessage({ lola: "visible", visible });
  }
  $effect(() => {
    const on = active;
    if (frame) untrack(() => tellVisible(on));
  });

  // Reads the id, as paint() sets the tokens in the same tick and an effect runs after it.
  function sendTheme() {
    if (!port) return;
    const now = themeNow();
    if (now === sentTheme) return;
    sentTheme = now;
    port.postMessage({ lola: "theme", ...JSON.parse(now) });
  }
  $effect(() => {
    appearance.id;
    untrack(sendTheme);
  });

  // Only the boot message comes through the window. Whatever else arrives here is either not
  // ours or from a document that was not given a port.
  function onMessage(e: MessageEvent) {
    if (!frame || e.source !== frame.contentWindow || typeof e.data !== "object" || !e.data) return;
    if ((e.data as { lola?: string }).lola !== "boot") return;
    if (port || !e.ports?.[0]) {
      closeFrame();
      return;
    }
    port = e.ports[0];
    port.onmessage = fromPage;
    clearTimeout(bootCheck);
    let waiting = false;
    let missed = 0;
    answered = () => {
      waiting = false;
      missed = 0;
    };
    pinger = setInterval(() => {
      if (document.hidden) return;
      if (waiting && ++missed >= WIDGET_PING_MISSES) {
        closeFrame();
        return;
      }
      waiting = true;
      port?.postMessage({ lola: "ping" });
    }, WIDGET_PING_MS);
  }
  function fromPage(e: MessageEvent) {
    if (typeof e.data !== "object" || !e.data) return;
    const m = e.data as { lola?: string; text?: unknown; kind?: unknown; message?: unknown; line?: unknown };
    if (m.lola === "pong") {
      answered();
    } else if (m.lola === "ready") {
      ready = true;
      sendTheme();
      if (!active) tellVisible(false);
      // Whatever was sent while the page was still loading never reached it.
      else if (data !== seeded) sendData(data);
    } else if (m.lola === "error") {
      const f = widgetFault(m);
      if (f) noteFault(f);
    } else if (m.lola === "ask" || m.lola === "run") {
      // A page nobody can see did not get a click.
      if (!active) return;
      const now = Date.now();
      if (now - lastFill < 1000) return;
      lastFill = now;
      const text = widgetText(m.text);
      if (!text) return;
      fill(text);
    }
  }

  function fill(text: string) {
    if (view.connections.some((c) => c.prompt && !view.answeredPrompts.has(c.prompt.id))) {
      view.setFlash("Widget text not added: answer the question first.", "text-warn");
      return;
    }
    const a = document.activeElement;
    const typing = a instanceof HTMLInputElement || a instanceof HTMLTextAreaElement || (a instanceof HTMLElement && a.isContentEditable);
    if (typing || view.agentDraft.trim()) {
      view.setFlash("Widget text not added: message box is in use.", "text-warn");
      return;
    }
    // A page must not be able to arm a command. A leading space defuses the ! that runs a shell,
    // which was measured. Nobody has measured that for a slash command, and a send trims it
    // anyway, so that one is refused rather than trusted.
    if (text.startsWith("/")) {
      view.setFlash("Widget text not added: it starts with a slash command.", "text-warn");
      return;
    }
    view.fillFromWidget(element.title || "a widget", text.startsWith("!") ? ` ${text}` : text);
    view.runDefault = false;
  }

  function onLoad() {
    loads++;
    if (loads > 1) closeFrame();
    // The SDK spoke before the first load, unless this is not the document we wrote.
    else if (!port) bootCheck = setTimeout(() => !port && closeFrame(), WIDGET_BOOT_GRACE_MS);
  }
</script>

<svelte:window onmessage={onMessage} />

<div class="grid h-full min-h-0 grid-rows-[minmax(0,1fr)_auto] overflow-hidden bg-panel" data-widget={element.id}>
  <div class="grid min-h-0 grid-rows-[minmax(0,1fr)]">
    {#if !closed}
      {#key `${reloads}:${html}`}<iframe
        bind:this={frame}
        title={element.title}
        sandbox={WIDGET_SANDBOX}
        src={widgetSrc(shownDoc)}
        class="block h-full w-full border-0 bg-panel p-2"
        onload={onLoad}
      ></iframe>{/key}
    {/if}
  </div>
  {#if fault}
    <div class="flex min-w-0 items-center gap-2 border-t border-magenta/20 px-3 py-1 text-sm text-warn" data-widget-fault={fault.kind}>
      <span class="min-w-0 flex-1" data-widget-fault-text>{WIDGET_FAULTS[fault.kind]}{told ? `. ${agent} was told.` : ""}</span>
      <Button size="xs" class="h-5!" onclick={() => view.reloadWidget(element.id)}>Reload</Button>
    </div>
  {/if}
</div>
