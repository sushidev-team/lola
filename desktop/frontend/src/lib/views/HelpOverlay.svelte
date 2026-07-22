<script lang="ts">
  // The '?' keybinding reference. Mirrors the TUI's help overlay and the trimmed
  // footer: the everyday keys stay in the footer hint, everything else lives here.
  // Opened from anywhere via '?'; esc / '?' / the ✕ close it (App.svelte + Modal).
  import { nav } from "$lib/nav.svelte";
  import Modal from "$lib/components/Modal.svelte";

  const groups: { title: string; keys: [string, string][] }[] = [
    {
      title: "Navigate",
      keys: [
        ["j / k · ↑ ↓", "move selection"],
        ["g / G", "first / last"],
        ["Enter", "open live terminal"],
        ["Esc", "back / minimize"],
        ["V", "cycle lens · list / board / terminals"],
        ["n / N", "next / prev needs-input"],
      ],
    },
    {
      title: "Session actions",
      keys: [
        ["a", "answer (needs input)"],
        ["x", "kill session"],
        ["o", "open PR in browser"],
        ["c", "coderabbit review"],
        ["R", "revive dead session"],
        ["P", "edit session's project"],
      ],
    },
    {
      title: "Global",
      keys: [
        ["p", "projects"],
        ["S", "settings"],
        ["d", "doctor"],
        ["?", "this help"],
      ],
    },
  ];
</script>

<Modal title="Keyboard shortcuts" onClose={() => nav.closeOverlay()} width="660px">
  <div class="grid gap-x-8 gap-y-6 sm:grid-cols-2">
    {#each groups as g (g.title)}
      <section>
        <h3 class="mb-2.5 text-[11px] font-semibold tracking-wider text-faint uppercase">{g.title}</h3>
        <ul class="flex flex-col gap-1.5">
          {#each g.keys as [k, d] (k)}
            <li class="flex items-baseline gap-3 text-xs">
              <kbd
                class="min-w-[7rem] shrink-0 rounded border border-edge bg-canvas px-2 py-0.5 text-center font-mono text-[11px] text-accent-ink"
                >{k}</kbd
              >
              <span class="text-ink">{d}</span>
            </li>
          {/each}
        </ul>
      </section>
    {/each}
  </div>

  <p class="mt-6 border-t border-edge/60 pt-4 text-[11px] leading-relaxed text-faint">
    A double-click (or <span class="text-ink">Enter</span>) opens a session's live terminal fullscreen; inside it, keys
    drive the agent and <span class="text-ink">Esc</span> returns here. The footer always shows the essentials —
    <span class="text-ink">?</span> reveals the rest.
  </p>
</Modal>
