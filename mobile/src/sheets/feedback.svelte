<script lang="ts">
  import { feedback } from "$lib/feedback.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import { copy, fill } from "@mobile/lib/copy";
  import { haptic } from "@mobile/lib/haptics";
  import { router } from "@mobile/lib/router.svelte";

  // The queued line comments of this session and a note (spec §5.3). Send ships
  // the batch as one message; the daemon queues it until the agent rests, and a
  // refused send keeps every draft. Send is the sheet's own action; the title
  // carries the count. A typed note makes the sheet dirty (dismissal asks). The note is the one place the general note is written.

  const id = $derived(router.top.name === "session" ? router.top.id : "");
  const drafts = $derived(feedback.draftsFor(id));
  const sending = $derived(feedback.isSending(id));
  const n = $derived(feedback.count(id));
  const title = $derived(n === 1 ? copy.feedback.titleOne : fill(copy.feedback.titleMany, { n }));
  const note = $derived(feedback.noteFor(id));
  const basename = (path: string): string => path.slice(path.lastIndexOf("/") + 1);

  async function send(): Promise<void> {
    if (await feedback.send(id)) {
      haptic("sent");
      router.close();
    }
  }

  // The note grows from 44 to 120 pt with its text, then scrolls.
  function grow(node: HTMLTextAreaElement, _value: string) {
    const fit = () => {
      node.style.height = "auto";
      node.style.height = `${Math.min(Math.max(node.scrollHeight, 44), 120)}px`;
    };
    fit();
    return { update: fit };
  }
</script>

<Sheet
  label={copy.feedback.title}
  title={title}
  detent="fit"
  action={{ label: copy.feedback.send, onclick: send, disabled: n === 0 || sending }}
  dirty={note.trim() !== ""}
  onclose={() => router.close()}
>
  <div class="flex min-w-0 flex-col pb-2">
    <ListGroup>
      {#each drafts as d (d.key)}
        <div class="flex min-w-0 items-center gap-1 px-4 py-2" data-list-row data-comment={d.key}>
          <div class="min-w-0 flex-1">
            <p class="truncate font-mono text-footnote text-faint">{basename(d.path)}:{d.line}{d.endLine > d.line ? `–${d.endLine}` : ""}</p>
            <p class="text-body">{d.body}</p>
          </div>
          <button
            type="button"
            class="grid size-11 shrink-0 touch-manipulation place-items-center text-faint active:opacity-60"
            aria-label="Remove comment"
            onclick={() => feedback.remove(id, d.key)}
          >
            <Icon name="circle-x" size={20} />
          </button>
        </div>
      {/each}
    </ListGroup>
    <ListGroup>
      <textarea
        class="note block w-full resize-none bg-transparent px-4 py-3 text-body text-ink"
        aria-label="Note"
        rows="1"
        placeholder={copy.feedback.placeholder}
        value={note}
        use:grow={note}
        oninput={(e) => feedback.setNote(id, e.currentTarget.value)}
      ></textarea>
    </ListGroup>
  </div>
</Sheet>

<style>
  .note { min-height: 44px; max-height: 120px; border: 0; outline: none; }
</style>
