<script lang="ts">
  import Checkbox from "$lib/components/Checkbox.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { INSECURE_MIN_KEY_LEN, connection } from "@mobile/lib/connection.svelte";
  import { DEFAULT_REMOTE_PORT, classifyHost, type EndpointDraft } from "@mobile/lib/endpoint";
  import { isPersistent, maskKey } from "@mobile/lib/secretstore";

  // Where the phone is told which Mac to talk to.
  //
  // Four fields, and three of them are public. Host and port are an address; the
  // SPKI pin is a hash of a public key that the daemon prints in its own startup
  // log. Only the access key is a secret, and it is the one field that never
  // touches this component's own persistence — it goes to the Keychain through
  // the native plugin, keyed by endpoint, and comes back only on the way into a
  // connect. See secretstore.ts for why localStorage is not an option for it.
  //
  // THE SCREEN'S REAL JOB IS THE FAILURE MESSAGE. A connection can fail for four
  // reasons that are indistinguishable at the socket and have completely
  // different fixes — wrong key, wrong pin, not on the network, and iOS local
  // network permission denied. The last one is why this screen matters: iOS
  // reports a denied permission as an ordinary unreachable host, asks exactly
  // once, never asks again, and offers no API to check. Without a screen that
  // names it, the user checks their WiFi forever. `diagnose` does the
  // classification; this renders it.

  let { onconnected }: { onconnected: () => void } = $props();

  let draft = $state<EndpointDraft>({ host: "", port: "", spkiPin: "" });
  let key = $state("");
  let remember = $state(true);
  let restored = $state(false);
  let showKey = $state(false);

  const problems = $derived(connection.problems);
  const problemFor = (field: string) => problems.find((p) => p.field === field)?.message ?? "";

  const d = $derived(connection.diagnosis);
  // "closed" without a reason is the state before anything was tried, which must
  // not be reported as a failure — a cold start showing a red banner reads as a
  // broken app.
  const tried = $derived(connection.phase === "closed" && (!!connection.error || !!connection.refusal));

  const kind = $derived(classifyHost(draft.host));

  $effect(() => {
    void (async () => {
      const prev = await connection.restore();
      if (!prev) return;
      draft = prev.draft;
      key = prev.key;
      restored = true;
    })();
  });

  async function submit(e: SubmitEvent) {
    e.preventDefault();
    if (await connection.connect(draft, key, remember)) onconnected();
  }

  const inputCls =
    "w-full rounded border border-edge bg-canvas px-3 py-3 text-base text-ink outline-none " +
    "focus:border-accent placeholder:text-placeholder";
</script>

<!-- `pt-safe-t` / `pb-safe-b` come from app.css's @theme spacing tokens, which is
     the preferred way to pay back an inset here: it is a plain inset with no
     arithmetic, so there is no reason to reach for an inline env(). The screens
     that add to an inset (`calc(env(...) + 0.5rem)`) and AccessoryBar (which
     deliberately does not depend on the shell defining a spacing scale) keep
     the inline form. -->
<div class="flex h-full flex-col overflow-y-auto bg-canvas pt-safe-t pb-safe-b">
  <form class="mx-auto flex w-full max-w-md flex-col gap-4 px-5 py-6" onsubmit={submit}>
    <div class="flex flex-col gap-1">
      <h1 class="text-xl text-ink">Connect to lola</h1>
      <p class="copy text-sm text-faint">
        The phone talks to the daemon over your own network. There is no relay and no cloud
        account: if this phone cannot reach that Mac, nothing here can.
      </p>
    </div>

    <label class="flex flex-col gap-1.5">
      <span class="label text-faint">Mac address</span>
      <input
        class={inputCls}
        type="text"
        inputmode="url"
        autocapitalize="none"
        autocorrect="off"
        spellcheck="false"
        placeholder="192.168.1.5 or marsmac.local"
        bind:value={draft.host}
      />
      {#if problemFor("host")}
        <span class="text-sm text-bad">{problemFor("host")}</span>
      {:else if kind === "loopback"}
        <span class="text-sm text-faint">
          Loopback. This only works through an SSH forward — which is exactly what the M1 daemon
          expects, since it binds to localhost whatever the config says.
        </span>
      {/if}
    </label>

    <label class="flex flex-col gap-1.5">
      <span class="label text-faint">Port</span>
      <input
        class="{inputCls} num"
        type="text"
        inputmode="numeric"
        placeholder={String(DEFAULT_REMOTE_PORT)}
        bind:value={draft.port}
      />
      {#if problemFor("port")}<span class="text-sm text-bad">{problemFor("port")}</span>{/if}
    </label>

    <label class="flex flex-col gap-1.5">
      <span class="label text-faint">SPKI pin</span>
      <input
        class="{inputCls} font-mono text-sm"
        type="text"
        autocapitalize="none"
        autocorrect="off"
        spellcheck="false"
        placeholder="44 characters ending in ="
        bind:value={draft.spkiPin}
      />
      {#if problemFor("pin")}
        <span class="text-sm text-bad">{problemFor("pin")}</span>
      {:else}
        <span class="text-sm text-faint">
          The daemon logs this when it starts: “remote: phone listener up on …, SPKI pin …”. It
          is a hash of a public key, so it is not a secret — but it is what stops this app from
          talking to anything else.
        </span>
      {/if}
    </label>

    <label class="flex flex-col gap-1.5">
      <span class="label text-faint">Access key</span>
      <!-- type=password until asked, because a shared bearer secret typed on a
           phone is typed in public more often than one typed at a desk. The
           reveal is a deliberate action rather than the default. -->
      <input
        class="{inputCls} font-mono text-sm"
        type={showKey ? "text" : "password"}
        autocapitalize="none"
        autocorrect="off"
        spellcheck="false"
        placeholder="LOLA_REMOTE_INSECURE_KEY"
        bind:value={key}
      />
      <div class="flex items-center gap-3">
        {#if problemFor("key")}
          <span class="text-sm text-bad">{problemFor("key")}</span>
        {:else if restored && key}
          <span class="font-mono text-sm text-faint">{maskKey(key)}</span>
        {/if}
        <button
          type="button"
          class="ml-auto shrink-0 py-1 text-sm text-faint underline"
          onclick={() => (showKey = !showKey)}
        >
          {showKey ? "Hide" : "Show"}
        </button>
      </div>
      <span class="text-sm text-faint">
        At least {INSECURE_MIN_KEY_LEN} characters — below that the daemon's listener refuses to
        start at all, which looks exactly like a wrong address.
      </span>
    </label>

    <label class="flex items-center gap-3 py-1">
      <Checkbox bind:checked={remember} />
      <span class="flex flex-col">
        <span class="text-ink">Remember this daemon</span>
        <span class="text-sm text-faint">
          {isPersistent()
            ? "The address is stored on this phone and the key goes to the keychain."
            : "No secure storage in this build, so the key is kept only until the app closes."}
        </span>
      </span>
    </label>

    <TouchButton variant="primary" wide type="submit" loading={connection.busy}>
      {connection.busy ? "Connecting…" : "Connect"}
    </TouchButton>

    {#if tried}
      <!-- The whole point of the screen. `kind` decides the colour: a refusal is
           a fact about a daemon that answered, an unreachable host is not. -->
      <div
        class="flex flex-col gap-1.5 rounded border px-3 py-3
               {d.kind === 'unreachable'
          ? 'border-warn/40 bg-warn/10'
          : 'border-bad/40 bg-bad/10'}"
        role="status"
      >
        <span class="font-medium {d.kind === 'unreachable' ? 'text-warn' : 'text-bad'}">
          {d.title}
        </span>
        <span class="copy text-sm text-ink">{d.detail}</span>
        {#if d.hint}<span class="copy text-sm text-faint">{d.hint}</span>{/if}
      </div>
    {/if}
  </form>
</div>
