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
      title: "Sessions sidebar",
      keys: [
        ["j / k · ↑ ↓", "move selection"],
        ["g / G", "first / last"],
        ["Enter", "open session"],
        ["← / →", "resize the focused sidebar divider"],
        ["Esc", "clear filter / unscope"],
        ["n / N", "next / prev row waiting on you"],
      ],
    },
    {
      title: "In a session",
      keys: [
        ["Esc", "back one step, then focus the sidebar"],
        ["Ctrl-Q", "leave a terminal that has the keyboard"],
        ["n / N", "next / prev session waiting on you"],
        ["t", "move into the timeline"],
        ["j / k · g / G", "timeline: move between rows"],
        ["[ / ]", "timeline: previous or next turn"],
        ["u", "timeline: first thing you have not seen"],
        ["/", "find in the timeline"],
        ["w", "show or hide widgets"],
        ["F", "cycle the filter, with a host"],
        ["< / >", "prev / next cell of the strip"],
      ],
    },
    {
      // The ⌘ chords are the macOS Session menu's accelerators (installAppMenu), not bindings in
      // onKey, which is why they also work from inside a focused terminal, where every bare key
      // belongs to the agent. They act on the open session, else the selection.
      title: "Session actions",
      keys: [
        ["⌘J", "next waiting, wherever it is"],
        ["⌘.", "stop this turn"],
        ["h · ⌘\\", "terminal, and back"],
        ["i · ⌘L", "composer. From a terminal, back to the conversation"],
        ["f · ⌘⇧D", "Changes, and back"],
        ["s · ⌘T", "new shell tab"],
        ["D", "run dev here · one session per project"],
        ["x · ⌘⇧K", "kill session"],
        ["o · ⌘⇧O", "open PR in browser"],
        ["c · ⌘⇧R", "review · configured provider"],
        ["R", "revive dead session"],
        ["P", "edit session's project"],
      ],
    },
    {
      // The ⌘ chords work from inside a terminal. Approval has NO global key: it
      // needs a card holding focus, plus 800ms of arming.
      title: "With a host",
      keys: [
        ["⌘⇧H", "new session on a host"],
        ["W · ⌘⇧L", "take the wheel, or hand it back"],
        ["A · ⌘⇧A", "review the next approval"],
        ["⌥← / ⌥→", "move the focused widget tab"],
        ["j / k · ↑ ↓", "timeline: move between items"],
        ["Enter / → / ←", "timeline: expand, follow, collapse"],
        ["s", "timeline: stdout or stderr"],
        ["a", "timeline: ask about this block"],
        ["X", "timeline: stop the running block"],
        ["Y / N", "focused card: confirm or deny"],
      ],
    },
    {
      title: "Global",
      keys: [
        ["⌘K", "search projects, sessions, and actions"],
        ["⌘N", "new session"],
        ["⌘⇧N", "new temporary shell"],
        ["b", "show / hide sidebar"],
        // Not onKey bindings: they fire on the focused sidebar row itself, so
        // they are listed under what they move rather than as globals.
        ["⌥↑ / ⌥↓", "move the focused project or folder"],
        ["⌥→ / ⌥←", "file the focused project into a folder / out of it"],
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
    A click (or <span class="text-ink">Enter</span> on a sidebar row) opens a session. Inside a terminal every key
    — <span class="text-ink">Esc</span> included — goes to the agent, so
    <span class="text-ink">Ctrl-Q</span> is the way back out.
    <span class="text-ink">?</span> reveals this list from anywhere a field does not have the keyboard. The
    <span class="text-ink">⌘</span> chords live in the <span class="text-ink">Session</span> menu and work even while a
    terminal has the keyboard.
  </p>
</Modal>
