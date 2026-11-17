<script lang="ts">
  // DEBUG harness for the Capacitor bridge guard. A sandboxed `data:` frame
  // posts a plugin call straight to WebKit's `bridge` handler; the guard must
  // drop it, so the device log shows `bridge-guard dropped` and never
  // `To Native -> LolaTransport haptic`.
  const probe =
    `webkit.messageHandlers.bridge.postMessage(` +
    `{type:"message",pluginId:"LolaTransport",methodName:"haptic",options:{kind:"error"},callbackId:"x"})`;
  const doc = `<!doctype html><script>try{${probe}}catch(e){}<\/script>`;
  const src = `data:text/html;charset=utf-8,${encodeURIComponent(doc)}`;
</script>

<main class="p-4 text-sm">
  <h1>Bridge guard</h1>
  <p>Check the device log.</p>
  <iframe title="Untrusted frame" sandbox="allow-scripts" {src} class="h-0 w-0 border-0"></iframe>
</main>
