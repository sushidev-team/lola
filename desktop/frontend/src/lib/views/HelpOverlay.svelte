<script lang="ts">
  // The '?' keybinding reference. Mirrors the TUI's help overlay. There is no
  // footer hint strip any more, so this is the ONLY place the full key model is
  // written down — every key listed here must actually be bound in App.svelte's
  // onKey. Opened from anywhere via '?'; esc / '?' / the ✕ close it (App.svelte
  // + Modal), and the sidebar's utility row has a '?' button for the mouse.
  import { nav } from "$lib/nav.svelte";
  import Modal from "$lib/components/Modal.svelte";

  const groups: { title: string; keys: [string, string][] }[] = [
    {
      title: "Navigate",
      keys: [
        ["j / k · ↑ ↓", "move selection"],
        ["g / G", "first / last"],
        ["Enter", "open live terminal"],
        ["Esc", "back / clear filter / unscope"],
        ["Ctrl-Q", "leave a focused terminal"],
        ["V", "cycle lens · list / board / terminals"],
        ["n / N", "next / prev needs-input"],
      ],
    },
    {
      title: "Session actions",
      keys: [
        ["s", "new worktree shell"],
        ["< / >", "prev / next terminal tab"],
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
        ["b", "show / hide sidebar"],
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
        <h3 class="label mb-2.5 text-faint">{g.title}</h3>
        <ul class="flex flex-col gap-1.5">
          {#each g.keys as [k, d] (k)}
            <li class="flex items-baseline gap-3">
              <kbd
                class="min-w-[7rem] shrink-0 rounded border border-edge bg-canvas px-2 py-0.5 text-center font-mono text-sm text-accent-ink"
                >{k}</kbd
              >
              <span class="text-ink">{d}</span>
            </li>
          {/each}
        </ul>
      </section>
    {/each}
  </div>

  <p class="copy mt-6 border-t border-edge/60 pt-4 text-sm text-faint">
    A double-click (or <span class="text-ink">Enter</span>) opens a session's live terminal fullscreen. Inside it every key
    — <span class="text-ink">Esc</span> included — goes to the agent, so
    <span class="text-ink">Ctrl-Q</span> is the way back out.
    <span class="text-ink">?</span> reveals this list from anywhere.
  </p>
</Modal>
