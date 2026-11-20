<script lang="ts">
  // The session's running app, framed through the daemon-side loopback proxy
  // (internal/preview), with viewport presets and an element picker. A picked
  // element plus the human's note goes to the agent as feedback, through the
  // same deferred hand-off gate as diff comments — this component never types
  // into a pane itself.
  import { onMount } from "svelte";
  import { DaemonService, PreviewService } from "@bindings/desktop";
  import { store } from "$lib/store.svelte";
  import { devUrlLabel } from "$lib/devurl";
  import { DEFAULT_VIEWPORT, PICKER_SOURCE, VIEWPORTS, devPageURL, frameStyle, parsePick, preview, type PickedElement, type ViewportId } from "$lib/preview.svelte";
  import Modal from "../Modal.svelte";
  import Button from "../Button.svelte";
  import Icon from "../Icon.svelte";
  import Tabs from "../Tabs.svelte";

  const req = $derived(preview.request);

  let frame = $state<HTMLIFrameElement | null>(null);
  let src = $state("");
  let error = $state("");
  let viewport = $state<ViewportId>(DEFAULT_VIEWPORT);
  let picking = $state(false);
  let picked = $state<PickedElement | null>(null);
  let note = $state("");
  let sending = $state(false);
  let loadedFor = "";

  const vp = $derived(VIEWPORTS.find((v) => v.id === viewport) ?? VIEWPORTS[2]);
  const session = $derived(req ? store.sessions.find((s) => s.id === req.sessionId) : undefined);
  const urls = $derived(session?.devUrls ?? (req ? [req.url] : []));

  async function load(url: string) {
    if (!req) return;
    error = "";
    picked = null;
    picking = false;
    try {
      src = await PreviewService.Open(req.sessionId, url);
    } catch (e) {
      src = "";
      error = String(e);
    }
  }

  $effect(() => {
    if (req && req.url !== loadedFor) {
      loadedFor = req.url;
      void load(req.url);
    }
    if (!req) loadedFor = "";
  });

  function tell(on: boolean) {
    frame?.contentWindow?.postMessage({ target: PICKER_SOURCE, type: "pick", on }, "*");
  }

  function togglePick() {
    picking = !picking;
    if (picking) picked = null;
    tell(picking);
  }

  onMount(() => {
    const onMessage = (e: MessageEvent) => {
      // Only the framed page, and only a well-formed pick, ever reaches the form.
      if (!frame || e.source !== frame.contentWindow) return;
      const d = e.data as { source?: string; type?: string; picking?: boolean } | null;
      if (d?.source === PICKER_SOURCE && d.type === "mode") picking = !!d.picking;
      const p = parsePick(e.data);
      if (p) {
        picked = p;
        note = "";
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
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
    preview.close();
  }
</script>

{#if req}
  <Modal title="Preview" onClose={close} fullscreen>
    <div class="grid h-full min-h-0 grid-rows-[auto_minmax(0,1fr)_auto]" data-session-preview>
      <div class="flex flex-wrap items-center gap-2 border-b border-edge/70 px-4 py-2">
        {#if urls.length > 1}
          <Tabs tabs={urls.map((u) => ({ id: u, label: devUrlLabel(u) }))} active={req.url} onSelect={(u) => preview.open(req.sessionId, u)} quiet />
        {:else}
          <span class="font-mono text-sm text-faint">{devUrlLabel(req.url)}</span>
        {/if}
        <Tabs tabs={VIEWPORTS.map((v) => ({ id: v.id, label: v.label }))} active={viewport} onSelect={(id) => (viewport = id as ViewportId)} quiet />
        <Button size="xs" selected={picking} disabled={!src} title="Click an element in the page to send it to the agent" onclick={togglePick}>
          {picking ? "Picking — click an element" : "Pick element"}
        </Button>
        <Button size="xs" disabled={!src} title="Reload" aria-label="Reload" onclick={() => req && load(req.url)}><Icon name="refresh-cw" size={14} /></Button>
        <Button size="xs" title="Open in the browser" aria-label="Open in the browser" onclick={() => store.openURL(req.url)}><Icon name="external-link" size={14} /></Button>
      </div>

      <div class="grid min-h-0 min-w-0 place-items-center overflow-auto bg-canvas p-3">
        {#if error}
          <p class="text-sm text-bad" role="alert">{error}</p>
        {:else if src}
          <iframe bind:this={frame} {src} title="Preview of {devUrlLabel(req.url)}" class="rounded border border-edge bg-white" style={frameStyle(vp)} sandbox="allow-scripts allow-same-origin allow-forms allow-popups"></iframe>
        {:else}
          <p class="text-sm text-faint">Starting preview…</p>
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
  </Modal>
{/if}
