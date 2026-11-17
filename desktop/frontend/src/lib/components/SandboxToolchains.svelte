<script lang="ts">
  // What a sandboxed project's container adds to Lola's image: toolchains
  // ([[project]].sandbox_toolchains) and Debian packages (sandbox_packages),
  // in one tag field. No toolchains set means "what the repository's files ask
  // for", so the chips start from the detected set; the first change turns
  // that into an explicit list. Lola builds the image on the next sandboxed
  // start.
  import Button from "$lib/components/Button.svelte";
  import HelpText from "$lib/components/HelpText.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import TagCombobox, { type Tag, type Suggestion } from "$lib/components/TagCombobox.svelte";
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
  const KINDS = ["node", "php", "python", "go", "rust", "ruby"];
  // Packages people commonly need beside a toolchain; anything else can be typed.
  const COMMON = ["ffmpeg", "imagemagick", "libvips-dev", "poppler-utils", "ghostscript", "postgresql-client", "default-mysql-client", "redis-tools", "sqlite3", "chromium", "graphviz", "pandoc"];
  const TOOLCHAIN_RE = /^(node|php|python|go|rust|ruby)(?:@([0-9a-z][0-9a-z._-]*))?$/;
  const PACKAGE_RE = /^[a-z0-9][a-z0-9.+-]*$/;

  let info = $state<ToolchainInfoDTO | null>(null);
  let error = $state("");

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

  const detected = $derived((info?.detected ?? []).map((t) => (t.version ? `${t.kind}@${t.version}` : t.kind)));
  const auto = $derived(toolchains.length === 0);
  const none = $derived(toolchains.length === 1 && toolchains[0] === "none");
  const active = $derived(auto ? detected : none ? [] : toolchains);
  const kindOf = (e: string) => e.split("@")[0];
  const nameOf = (e: string) => {
    const [k, v] = e.split("@");
    return v ? `${NAMES[k] ?? k} ${v}` : (NAMES[k] ?? k);
  };

  const tags = $derived<Tag[]>([
    ...active.map((e) => ({ value: `tc:${e}`, label: nameOf(e) })),
    ...packages.map((p) => ({ value: `pkg:${p}`, label: p, mono: true })),
  ]);
  const suggestions = $derived<Suggestion[]>([
    ...KINDS.filter((k) => !active.some((e) => kindOf(e) === k)).map((k) => ({ value: k, label: NAMES[k], hint: "Toolchain" })),
    ...COMMON.filter((p) => !packages.includes(p)).map((p) => ({ value: p, label: p, hint: "Package" })),
  ]);

  /** An explicit list that equals detection goes back to detection; an empty one is "none". */
  function setToolchains(next: string[]) {
    const same = next.length === detected.length && next.every((e) => detected.includes(e));
    onChange(same ? [] : next.length ? next : ["none"], packages);
  }

  function addText(text: string): boolean {
    error = "";
    // "PHP 8.4" reads as php@8.4.
    const t = text.toLowerCase().replace(/^(\w+)\s+([\d.]+)$/, "$1@$2");
    const tc = TOOLCHAIN_RE.exec(t);
    if (tc) {
      const fromRepo = !tc[2] ? detected.find((e) => kindOf(e) === tc[1]) : undefined;
      setToolchains([...active.filter((e) => kindOf(e) !== tc[1]), fromRepo ?? t]);
      return true;
    }
    if (PACKAGE_RE.test(t)) {
      if (!packages.includes(t)) onChange(toolchains, [...packages, t]);
      return true;
    }
    error = `“${text}” is not a toolchain or a Debian package name.`;
    return false;
  }

  function remove(value: string) {
    error = "";
    if (value.startsWith("tc:")) setToolchains(active.filter((e) => e !== value.slice(3)));
    else onChange(toolchains, packages.filter((p) => p !== value.slice(4)));
  }
</script>

<div class="grid gap-2" data-sandbox-toolchains>
  <div class="flex items-center gap-1.5 text-ink">
    <span>Toolchains and packages</span>
    <HelpText label="Toolchains and packages" detail="Lola adds these to its image and builds it once on the next sandboxed start. Toolchains start from what the repository needs; type any Debian package to add it. Package caches are kept per project." />
    {#if !auto}
      <span class="ml-auto">
        <Button size="xs" icon aria-label="Use the detected toolchains" title="Use the detected toolchains" onclick={() => onChange([], packages)}>
          <Icon name="rotate-ccw" size={12} />
        </Button>
      </span>
    {/if}
  </div>
  <TagCombobox label="Toolchains and packages" {tags} {suggestions} {error} placeholder="Add a toolchain or package…" onAdd={addText} onRemove={remove} />
</div>
