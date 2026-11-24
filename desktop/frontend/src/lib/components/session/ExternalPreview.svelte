<script lang="ts">
  import { onMount } from "svelte";
  import { previewIcon, type OpenPreviewTab } from "$lib/preview.svelte";
  import Button from "../Button.svelte";
  import PreviewLoadBar from "./PreviewLoadBar.svelte";

  let { tab, active, onNewTab }: { tab: OpenPreviewTab; active: boolean; onNewTab: (url: string) => void } = $props();
  let surface: HTMLDivElement;
  let error = $state("");
  let percent = $state<number | null>(null);
  type Bridge = { postMessage: (data: Record<string, unknown>) => void };
  let bridge = $state.raw<Bridge>();
  function send(action: string, args: Record<string, unknown> = {}) {
    bridge?.postMessage({ id: tab.id, action, ...args });
  }
  export function navigatePage(url: string) { error = ""; send("load", { url }); }
  export function reloadPage() { error = ""; send("reload"); }
  export function backInFrame() { error = ""; send("back"); return !!bridge; }

  onMount(() => {
    bridge = (window as Window & { webkit?: { messageHandlers?: { lolaExternalPreview?: Bridge } } }).webkit?.messageHandlers?.lolaExternalPreview;
    function receive(event: Event) {
      const data = (event as CustomEvent).detail;
      if (data?.id !== tab.id) return;
      if (data.type === "progress") {
        percent = data.loading && Number.isFinite(data.fraction) ? Math.round(Math.min(1, Math.max(0, data.fraction)) * 100) : null;
      } else if (data.type === "location") {
        if (data.url) {
          if (new URL(data.url).origin !== new URL(tab.currentURL).origin) tab.favicon = undefined;
          tab.currentURL = data.url;
        }
        tab.documentTitle = data.title;
        tab.canGoBackInFrame = data.back;
      } else if (data.type === "favicon" && data.url === tab.currentURL) {
        tab.favicon = previewIcon(data.icon, data.url);
      } else if (data.type === "new-tab" && active) onNewTab(data.url);
      else if (data.type === "error") {
        percent = null;
        error = data.message;
      }
    }
    const close = () => send("close");
    window.addEventListener("lola-external-preview", receive);
    window.addEventListener("pagehide", close);
    navigatePage(tab.request.url);
    if (!bridge) error = "External websites open in the Lola desktop app.";
    return () => {
      window.removeEventListener("lola-external-preview", receive);
      window.removeEventListener("pagehide", close);
      close();
    };
  });

  $effect(() => {
    if (!bridge || !active || error) return;
    function place() {
      const { x, y, width, height } = surface.getBoundingClientRect();
      send("place", { x, y, width, height, viewport: window.innerWidth });
    }
    const observer = new ResizeObserver(place);
    observer.observe(surface);
    window.addEventListener("resize", place);
    place();
    return () => {
      observer.disconnect();
      window.removeEventListener("resize", place);
      send("hide");
    };
  });
</script>

<div class="relative h-full w-full">
  <!-- The native view sits above the DOM. Keep its frame below the loading bar. -->
  <div bind:this={surface} class="absolute inset-x-0 top-[3px] bottom-0"></div>
  {#if active && !error && percent !== null}<PreviewLoadBar value={percent} />{/if}
  {#if error}
    <div class="relative grid h-full place-content-center justify-items-center gap-2">
      <p role="alert" class="text-sm text-faint">{error}</p>
      {#if bridge}<Button size="sm" onclick={() => navigatePage(tab.currentURL)}>Retry</Button>{/if}
    </div>
  {/if}
</div>
