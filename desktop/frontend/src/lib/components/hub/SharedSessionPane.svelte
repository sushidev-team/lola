<script lang="ts">
  // A teammate's session in the app's own terminal: the same LiveTerminal as a local
  // pane, streamed through the hub to the owner's Mac (TermService routes the
  // `share-<id>` name to the remote backend). The header says whose session it is, what
  // you may do in it, and who else is attached; the owner's daemon is the source of all
  // three, and the owner decides whether the keystrokes land.
  //
  // It is always mounted and hidden while nothing is attached: TerminalCell holds the
  // only {#if}{#key} that mounts a LiveTerminal (see its note), so the parent never
  // toggles one.
  import { onDestroy, untrack } from "svelte";
  import { Events } from "@wailsio/runtime";
  import { TermService } from "@bindings/desktop";
  import type { SharedSession } from "@bindings/internal/protocol";
  import Button from "../Button.svelte";
  import Icon from "../Icon.svelte";
  import TerminalCell from "../session/TerminalCell.svelte";
  import { share } from "$lib/share.svelte";
  import { LEVELS, attachedLine, initials, levelOf, shareStreamName } from "$lib/sharing";
  import { tooltip } from "$lib/tooltip";

  const id = $derived(share.attachedId);
  const name = $derived(id ? shareStreamName(id) : "");
  const offer = $derived(share.offers.find((o) => o.id === id));

  /** The owner's latest roster row for this stream (pushed, or read until the first one lands). */
  let info = $state<SharedSession | null>(null);

  // Depends on the stream name only: `info` is written here and read through untrack, or the
  // effect would re-run (and reset it) every time a roster row arrived.
  $effect(() => {
    const n = name;
    untrack(() => (info = null));
    if (!n) return;
    const off = Events.On(`share:${n}`, (e: { data: SharedSession }) => {
      info = e.data;
    });
    // The stream attaches inside the terminal a moment after it mounts, so the
    // first read retries a few times instead of failing the pane.
    let tries = 0;
    const read = () => {
      if (untrack(() => info) || ++tries > 12) return clearInterval(timer);
      TermService.ShareInfo(n)
        .then((r) => {
          if (!info) info = r;
        })
        .catch(() => {});
    };
    const timer = setInterval(read, 1000);
    read();
    return () => {
      off();
      clearInterval(timer);
    };
  });
  onDestroy(() => share.detach());

  const level = $derived(levelOf(info?.level ?? offer?.level));
  const levelRow = $derived(LEVELS.find((l) => l.id === level) ?? LEVELS[0]);
  const viewers = $derived(info?.attached ?? []);
  const title = $derived(info?.title || offer?.title || "Shared session");
  const owner = $derived(offer ? (offer.ownerMember ? `${offer.ownerMember} · ${offer.ownerName}` : offer.ownerName) : "");
</script>

<div class="grid h-full min-h-0 grid-rows-[auto_minmax(0,1fr)] {id ? '' : 'hidden'}" data-shared-pane={id ? "open" : "closed"}>
  <div class="flex min-w-0 items-center gap-3 border-b border-edge px-4 py-2">
    <Button icon size="sm" aria-label="Back to the team" onclick={() => share.detach()}><Icon name="arrow-left" size={14} /></Button>
    <span class="min-w-0 truncate font-medium text-ink" data-shared-title>{title}</span>
    {#if owner}<span class="min-w-0 truncate text-sm text-faint">{owner}</span>{/if}
    <span class="ml-auto flex shrink-0 items-center gap-3">
      <span
        class="inline-flex items-center gap-1 text-sm {level === 'type' ? 'text-warn' : 'text-faint'}"
        use:tooltip={{ text: levelRow.hint }}
        role="img"
        aria-label={levelRow.hint}
        data-shared-level={level}
      ><Icon name={levelRow.icon} size={14} /></span>
      {#if viewers.length > 0}
        <span class="flex items-center -space-x-1.5" role="list" aria-label={`Attached: ${attachedLine(viewers)}`} data-shared-viewers>
          {#each viewers as v (v.name + v.since)}
            <span
              role="listitem"
              class="grid size-6 place-items-center rounded-full border border-panel text-[11px] {v.me ? 'bg-accent-fill text-accent-ink' : 'bg-pill-grey text-pill-grey-fg'}"
              use:tooltip={{ text: v.me ? `${v.name} (you)` : v.name }}
            >{initials(v.name)}</span>
          {/each}
        </span>
      {/if}
    </span>
  </div>
  <TerminalCell {name} autofocus onExit={() => share.detach()} onLeave={() => share.detach()} />
</div>
