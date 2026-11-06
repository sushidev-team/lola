<script lang="ts">
  // The end of a turn, in lola's words: a fact, so it may carry numbers. With no checkpoint there
  // is no number and nothing to restore. The actions wait on hover or focus, on the same line, so
  // a story of a hundred turns does not read as a hundred sets of buttons. The newest turn shows
  // them, by opacity only, so nothing shifts. The file list sits under the person's column.
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { checkpoints } from "$lib/checkpoints.svelte";
  import { nav } from "$lib/nav.svelte";
  import { changesOffered } from "$lib/sessionview";
  import { diffStat, type TurnFiles } from "$lib/turns";
  import Button from "../Button.svelte";

  let {
    sessionId,
    words,
    seq = 0,
    stopped = false,
    title = "",
    clock = "",
    latest = false,
    files,
  }: { sessionId: string; words: string; seq?: number; stopped?: boolean; title?: string; clock?: string; latest?: boolean; files?: TurnFiles } = $props();

  const session = $derived(store.sessionById(sessionId));
  const canDiff = $derived(!!session && changesOffered(session));

  // The receipt knows the turn only by its number, so that is what the question names.
  const turn = $derived({ seq, label: `turn ${seq}` });
  // What changed is the part of this line worth a second look. The word order is receiptWords'.
  const said = $derived.by(() => {
    const m = /\+\d+ −\d+/.exec(words);
    return m ? { before: words.slice(0, m.index), diff: m[0], after: words.slice(m.index + m[0].length) } : { before: words, diff: "", after: "" };
  });
</script>

<div class="cv-receipt" data-receipt={stopped ? "stopped" : "done"}>
  <div class="cv-receipt-summary group min-h-6 text-sm text-faint">
    <span class="num" {title}>{said.before}{#if said.diff}<span class="text-ink-2">{said.diff}</span>{/if}{said.after}</span>
    {#if canDiff}
      <span class="flex items-center gap-1 transition-opacity duration-100 {latest ? '' : 'opacity-0 group-hover:opacity-100 focus-within:opacity-100'}" data-receipt-actions>
        {#if session}<Button size="xs" onclick={() => ssh.openChanges(session, seq)}>View changes</Button>{/if}
        {#if seq > 0}
          <Button size="xs" onclick={() => checkpoints.askRestore(sessionId, turn)}>Restore</Button>
          <Button size="xs" onclick={() => checkpoints.askFork(sessionId, turn, (id) => nav.openSession(id))}>Fork</Button>
        {/if}
      </span>
    {:else}
      <span></span>
    {/if}
    {#if clock}<time class="num whitespace-nowrap">{clock}</time>{/if}
  </div>
  {#if files?.files.length}
    <ul class="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 text-sm" data-receipt-files>
      {#each files.files as file (file.path)}
        <li class="col-span-2 grid grid-cols-subgrid"><span class="min-w-0 truncate text-ink-2" title={file.path}>{file.name}</span><span class="num text-right text-faint">{diffStat(file.added, file.removed)}</span></li>
      {/each}
      {#if files.more}<li class="col-span-2 text-right text-faint">and <span class="num">{files.more}</span> more</li>{/if}
    </ul>
  {/if}
</div>
