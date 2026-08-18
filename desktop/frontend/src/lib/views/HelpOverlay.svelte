<script lang="ts">
  // The '?' keybinding reference. Mirrors the TUI's help overlay. There is no
  // footer hint strip any more, so this is the ONLY place the full key model is
  // written down — every key listed here must actually be bound in App.svelte's
  // onKey, in the macOS Session menu (the ⌘ chords), or on a specific focused
  // control (⌥↑/⌥↓, which the sidebar row itself handles). Opened from anywhere
  // via '?'; esc / '?' / the ✕ close it (App.svelte + Modal), and the sidebar's
  // utility row has a '?' button for the mouse.
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
      // The ⌘ chords are the macOS Session menu's accelerators (installAppMenu),
      // not bindings in onKey — which is why they also work from inside a focused
      // terminal, where every bare key belongs to the agent.
      title: "Session actions",
      keys: [
        ["s · ⌘T", "new worktree shell"],
        ["D", "run dev here · one session per project"],
        ["< / >", "prev / next terminal tab"],
        ["x · ⌘⇧K", "kill session"],
        ["o · ⌘⇧O", "open PR in browser"],
        ["c · ⌘⇧R", "review · configured provider"],
        ["R", "revive dead session"],
        ["P", "edit session's project"],
      ],
    },
    {
      title: "Global",
      keys: [
        ["b", "show / hide sidebar"],
        // Not an onKey binding: it fires on the focused sidebar row itself, so
        // it is listed under what it moves rather than as a global.
        ["⌥↑ / ⌥↓", "reorder the focused project or group"],
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
    <span class="text-ink">?</span> reveals this list from anywhere. The
    <span class="text-ink">⌘</span> chords live in the <span class="text-ink">Session</span> menu and work even while a
    terminal has the keyboard.
  </p>
</Modal>
