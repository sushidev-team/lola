<script lang="ts">
  // Desktop commands first. Control-specific navigation stays with its control.
  import { nav } from "$lib/nav.svelte";
  import Modal from "$lib/components/Modal.svelte";

  const groups: { title: string; keys: [string, string][] }[] = [
    { title: "Navigation", keys: [
      ["⌘⇧G", "Git workspace / previous view"],
      ["T", "Team chores"],
      ["⌘K", "Search projects, sessions and actions"],
      ["⌘B", "Show or hide sidebar"],
      ["⌘⇧E", "Focus sessions sidebar"],
      ["⌘J / ⌘⇧J", "Next / previous waiting session"],
      ["⌘/", "Keyboard shortcuts"],
    ] },
    { title: "Session", keys: [
      ["⌘N", "New session"],
      ["⌘⇧N", "New temporary shell"],
      ["⌘L", "Focus conversation composer"],
      ["⌘\\", "Terminal / conversation"],
      ["⌘⇧D", "Show or hide changes"],
      ["⌘T / ⌘W", "New shell / close active tab"],
      ["⌘.", "Stop the current turn"],
      ["⌘⇧K", "Remove session…"],
      ["⌘⇧O", "Open pull request in browser"],
      ["⌘⇧R", "Run configured review"],
    ] },
    { title: "Project Git", keys: [
      ["⌘F", "Search commit history"],
      ["⌘ + click", "Select commits to compare"],
      ["← / →", "Resize focused column divider"],
      ["↑ / ↓", "Resize focused section divider"],
      ["Esc", "Back to the previous view"],
    ] },
    { title: "Focused controls", keys: [
      ["↑ / ↓", "Navigate sessions or timeline"],
      ["Home / End", "First / last session"],
      ["Enter", "Open selected item"],
      ["Esc", "Close dialog or move focus back"],
      ["Ctrl-Q", "Release terminal keyboard focus"],
      ["⌥ + arrows", "Move projects or folders"],
    ] },
    { title: "Host sessions", keys: [
      ["⌘⇧H", "New SSH session"],
      ["⌘⇧L", "Take or return control"],
      ["⌘⇧A", "Review next approval"],
    ] },
  ];
</script>

<Modal title="Keyboard shortcuts" onClose={() => nav.closeOverlay()} width="760px">
  <div class="grid gap-x-8 gap-y-6 sm:grid-cols-2">
    {#each [groups.slice(0, 2), groups.slice(2)] as column}
      <div class="flex flex-col gap-6">
      {#each column as g (g.title)}
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
    {/each}
  </div>

  <p class="copy mt-6 border-t border-edge/60 pt-4 text-sm text-faint">
    ⌘ commands work from text fields and terminals. Focused controls use their own keys.
    Use Ctrl instead of ⌘ on Windows and Linux.
  </p>
</Modal>
