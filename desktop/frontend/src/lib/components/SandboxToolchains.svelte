<script lang="ts">
  // The toolchains a sandboxed project's container adds to Lola's image
  // ([[project]].sandbox_toolchains / sandbox_packages). Empty means "what the
  // repository's files ask for", so the chips start from the detected set; the
  // first toggle turns that into an explicit list, and Reset goes back to
  // detection. Lola builds the image on the next sandboxed start.
  import Button from "$lib/components/Button.svelte";
  import HelpText from "$lib/components/HelpText.svelte";
  import { ConfigService } from "@bindings/desktop";
  import type { ToolchainInfoDTO } from "@bindings/desktop/models";

  let {
    path = "",
    toolchains = [],
    packages = [],
    onChange,
  }: {
    path?: string;
    toolchains?: string[];
    packages?: string[];
    onChange: (toolchains: string[], packages: string[]) => void;
  } = $props();

  const NAMES: Record<string, string> = { node: "Node", php: "PHP", python: "Python", go: "Go", rust: "Rust", ruby: "Ruby" };
  const FALLBACK_KINDS = ["node", "php", "python", "go", "rust", "ruby"];

  let info = $state<ToolchainInfoDTO | null>(null);
  let pkgDraft = $state<string | null>(null);
  const id = $props.id();

  $effect(() => {
    const p = path;
    let stale = false;
    (async () => {
      try {
        const r = await ConfigService.DetectToolchains(p);
        if (!stale) info = r;
      } catch {
        if (!stale) info = null;
      }
    })();
    return () => {
      stale = true;
    };
  });

  const kinds = $derived(info?.kinds?.length ? info.kinds : FALLBACK_KINDS);
  const detected = $derived((info?.detected ?? []).map((t) => (t.version ? `${t.kind}@${t.version}` : t.kind)));
  const auto = $derived(toolchains.length === 0);
  const none = $derived(toolchains.length === 1 && toolchains[0] === "none");
  const active = $derived(auto ? detected : none ? [] : toolchains);

  const kindOf = (e: string) => e.split("@")[0];
  const entryFor = (kind: string) => active.find((e) => kindOf(e) === kind);
  const label = (kind: string) => {
    const e = entryFor(kind);
    const v = e?.split("@")[1];
    return v ? `${NAMES[kind] ?? kind} ${v}` : (NAMES[kind] ?? kind);
  };

  function toggle(kind: string) {
    const on = !!entryFor(kind);
    let next = on ? active.filter((e) => kindOf(e) !== kind) : [...active, detected.find((e) => kindOf(e) === kind) ?? kind];
    // Back to "detected" when the choice matches it; "none" when nothing is left.
    const same = next.length === detected.length && next.every((e) => detected.includes(e));
    if (same) next = [];
    else if (next.length === 0) next = ["none"];
    onChange(next, packages);
  }

  function setPackages(text: string) {
    pkgDraft = text;
    onChange(toolchains, text.split(/[\s,]+/).filter(Boolean));
  }
</script>

<div class="grid gap-2" data-sandbox-toolchains>
  <div class="flex items-center gap-1.5 text-ink">
    <span id={`${id}-label`}>Toolchains</span>
    <HelpText label="Toolchains" detail="Lola adds these to its image and builds it once on the next sandboxed start. Package caches are kept per project." />
  </div>
  <div class="flex flex-wrap gap-1.5" role="group" aria-labelledby={`${id}-label`}>
    {#each kinds as kind (kind)}
      {@const on = !!entryFor(kind)}
      <Button size="xs" variant={on ? "primary" : "secondary"} aria-pressed={on} onclick={() => toggle(kind)}>{label(kind)}</Button>
    {/each}
  </div>
  <p class="flex flex-wrap items-center gap-2 text-sm text-faint">
    {#if auto}
      {detected.length || info?.extras?.length ? `Detected from the repository${info?.extras?.length ? ` · ${info.extras.join(", ")}` : ""}` : "Nothing beyond Lola’s image detected"}
    {:else}
      <span>Chosen by hand</span>
      <Button size="xs" onclick={() => onChange([], packages)}>Use detected</Button>
    {/if}
  </p>
  <label class="grid gap-1 text-sm text-faint">
    Extra packages
    <input class="w-full rounded border border-edge bg-canvas px-2 py-1.5 font-mono text-ink outline-none focus:border-accent placeholder:text-placeholder"
      placeholder="ffmpeg libvips-dev" spellcheck="false" autocomplete="off"
      value={pkgDraft ?? packages.join(" ")}
      oninput={(e) => setPackages(e.currentTarget.value)} />
  </label>
</div>
