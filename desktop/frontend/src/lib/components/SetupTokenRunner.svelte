<script lang="ts">
  // "Log in with Claude": runs `claude setup-token` in the desktop process
  // (SandboxLoginService) and shows its screen here, so the user finishes the
  // browser login and the token lands in the keychain without being copied.
  //
  // The token never reaches this page. The backend sends whole repaints of a
  // screen it has already checked, and replaces everything with a notice once
  // a token shows up. This component only draws frames and forwards keys.
  import { onDestroy, tick } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import { WebLinksAddon } from "@xterm/addon-web-links";
  import { SandboxLoginService } from "@bindings/desktop";
  import Button from "$lib/components/Button.svelte";
  import HelpText from "$lib/components/HelpText.svelte";
  import { store } from "$lib/store.svelte";
  import { TERM_FONT, appearance } from "$lib/theme-runtime.svelte";

  let { onStored }: { onStored?: () => void } = $props();

  type Frame = { run: number; data: string };
  type Done = { run: number; stored: boolean; cancelled: boolean; error: string };

  let phase = $state<"idle" | "starting" | "running" | "stored">("idle");
  let err = $state("");
  let host = $state<HTMLDivElement>();
  let term: Terminal | undefined;
  let fit: FitAddon | undefined;
  let ro: ResizeObserver | undefined;
  let offs: (() => void)[] = [];
  let run = 0;
  // Events that arrive before Start returns the run id: the newest frame and
  // an end, kept per run and replayed once the id is known.
  let early = new Map<number, { frame?: string; stored?: boolean; done?: Done }>();

  function payload<T>(e: unknown): T {
    return (e as { data: T }).data;
  }

  function teardown() {
    offs.forEach((off) => off());
    offs = [];
    ro?.disconnect();
    ro = undefined;
    term?.dispose();
    term = undefined;
    fit = undefined;
    early = new Map();
  }

  function onFrame(f: Frame) {
    if (run === 0) early.set(f.run, { ...early.get(f.run), frame: f.data });
    else if (f.run === run) term?.write(f.data);
  }

  function onStoredEvent(d: Done) {
    if (run === 0) early.set(d.run, { ...early.get(d.run), stored: true });
    else if (d.run === run) finishStored();
  }

  function onDone(d: Done) {
    if (run === 0) early.set(d.run, { ...early.get(d.run), done: d });
    else if (d.run === run) finish(d);
  }

  function finishStored() {
    if (phase === "stored") return;
    run = 0;
    phase = "stored";
    teardown();
    onStored?.();
  }

  function finish(d: Done) {
    if (d.stored) return finishStored();
    run = 0;
    teardown();
    phase = "idle";
    err = d.cancelled ? "" : d.error || "The login ended without a token.";
  }

  async function start() {
    err = "";
    phase = "starting";
    await tick();
    if (!host) return;
    term = new Terminal({
      ...TERM_FONT,
      scrollback: 200,
      cursorBlink: false,
      theme: appearance.term,
    });
    fit = new FitAddon();
    term.loadAddon(fit);
    // Same opener as every lola terminal: the daemon opens http(s) only.
    term.loadAddon(new WebLinksAddon((_e, uri) => void store.openURL(uri)));
    term.open(host);
    fit.fit();
    term.onData((d) => {
      if (run) void SandboxLoginService.Write(run, d);
    });
    term.onResize(({ cols, rows }) => {
      if (run) void SandboxLoginService.Resize(run, cols, rows);
    });
    ro = new ResizeObserver(() => fit?.fit());
    ro.observe(host);
    offs = [
      Events.On("sandboxlogin:frame", (e: unknown) => onFrame(payload<Frame>(e))),
      Events.On("sandboxlogin:stored", (e: unknown) => onStoredEvent(payload<Done>(e))),
      Events.On("sandboxlogin:done", (e: unknown) => onDone(payload<Done>(e))),
    ];
    try {
      const id = await SandboxLoginService.Start(term.cols, term.rows);
      if (phase !== "starting") {
        // Cancelled or unmounted while starting: end the run we just made.
        void SandboxLoginService.Cancel(id);
        return;
      }
      run = id;
      phase = "running";
      term.focus();
      const pending = early.get(id);
      early = new Map();
      if (pending?.frame) term.write(pending.frame);
      if (pending?.stored) finishStored();
      else if (pending?.done) finish(pending.done);
    } catch (e) {
      teardown();
      phase = "idle";
      err = String(e);
    }
  }

  function cancel() {
    const id = run;
    run = 0;
    teardown();
    phase = "idle";
    if (id) void SandboxLoginService.Cancel(id);
  }

  onDestroy(() => {
    if (run) void SandboxLoginService.Cancel(run);
    run = 0;
    phase = "idle";
    teardown();
  });

  $effect(() => {
    const theme = appearance.term;
    if (term) term.options.theme = theme;
  });
</script>

<div class="grid gap-2">
  {#if phase === "idle" || phase === "stored"}
    <span class="flex flex-wrap items-center gap-2">
      <Button variant="primary" onclick={start}>Log in with Claude</Button>
      <HelpText label="Log in with Claude" detail="Runs claude setup-token here. Finish the login in your browser; Lola stores the token in the macOS keychain and never shows it." />
      {#if phase === "stored"}<span role="status" class="text-sm text-good">Logged in. Token stored.</span>{/if}
    </span>
    {#if err}<p role="alert" class="text-sm text-bad">{err}</p>{/if}
  {:else}
    <span class="flex flex-wrap items-center gap-2 text-sm text-ink">
      <span role="status">A browser window opens. Finish the login there.</span>
      <Button size="xs" variant="secondary" onclick={cancel}>Cancel</Button>
    </span>
    <div
      bind:this={host}
      class="h-56 overflow-hidden rounded border border-edge bg-panel p-1"
      aria-label="Claude login terminal"
    ></div>
  {/if}
</div>
