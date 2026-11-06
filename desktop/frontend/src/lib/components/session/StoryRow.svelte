<script lang="ts">
  // One row of the story, by what it is. Voice comes from container and type: a plate means lola
  // witnessed it, a card means you can act on it, and everything else sits on the ground, with only
  // a neutral mark on the turn's rail beside it. Only a store message gets the you fill, because only
  // those are the human's words on lola's own record. Everything from the log is the agent's claim.
  import { store } from "$lib/store.svelte";
  import { ssh, sshClock } from "$lib/ssh.svelte";
  import { talk } from "$lib/talk.svelte";
  import { feed } from "$lib/feed.svelte";
  import { agentName, droveFor, maybeAnsweredInPane, transcriptLabel, formatMs, plural, type SSHElement as SSHElementData } from "$lib/ssh";
  import { clampsRow, fitNames, type StoryRow, type TurnFiles } from "$lib/turns";
  import { answerShaped } from "$lib/answerblocks";
  import { youAttachments, type YouPart } from "$lib/composerpaste";
  import Button from "../Button.svelte";
  import Chevron from "../Chevron.svelte";
  import Disclosure from "../Disclosure.svelte";
  import SSHBlock from "../ssh/SSHBlock.svelte";
  import SSHElement from "../ssh/SSHElement.svelte";
  import { disclosureMotion } from "$lib/disclosureMotion";
  import SSHRich from "../ssh/SSHRich.svelte";
  import ToolRow from "./ToolRow.svelte";
  import WorkRun from "./WorkRun.svelte";
  import Receipt from "./Receipt.svelte";
  import SystemMessage from "./SystemMessage.svelte";
  import Moment from "./Moment.svelte";
  import WidgetCard from "./WidgetCard.svelte";
  import AttachGlyph from "./AttachGlyph.svelte";

  let {
    sessionId,
    row,
    chars = 72,
    showHost = false,
    readonly = false,
    nested = false,
    role = "answer",
    latest = false,
    files,
  }: {
    sessionId: string;
    row: StoryRow;
    chars?: number;
    showHost?: boolean;
    readonly?: boolean;
    /** Inside an opened line, off the rail: a tool row is a child there rather than a dig line of its own. */
    nested?: boolean;
    /** What a prose row is doing in its turn. Colour only: its size comes from its text. */
    role?: "answer" | "narration";
    /** The newest turn's receipt keeps its actions out, rather than behind a hover. */
    latest?: boolean;
    /** What the turn's edits touched, for its receipt. */
    files?: TurnFiles;
  } = $props();

  const view = $derived(ssh.view(sessionId));
  const session = $derived(store.sessionById(sessionId));
  const agent = $derived(agentName(session?.agent));
  const noFeed = $derived(talk.state(sessionId)?.caps?.feed === false);
  const f = $derived(feed.view(sessionId));
</script>

{#snippet clamped(id: string, text: string, cls: string, lines: 6 | 12 = 12, parts?: YouPart[])}
  <!-- A pasted wall folds so it cannot be most of the story. Its head stays readable. Not `block`
       beside the clamp: the clamp needs its own display, and block could win it. -->
  {@const long = clampsRow(text, lines)}
  {@const open = !long || readonly || f.expanded.has(`more-${id}`)}
  <span class="copy selectable break-words whitespace-pre-wrap {cls} {open ? 'block' : lines === 6 ? 'line-clamp-6' : 'line-clamp-12'}">{#if parts}{#each parts as p, i (i)}{#if p.n}<span class="num text-sm text-faint">({p.n})</span>{:else}{p.text}{/if}{/each}{:else}{text}{/if}</span>
  {#if long && !readonly}
    <Button size="xs" class="mt-0.5 -ml-1.5" aria-expanded={open} onclick={() => (open ? f.expanded.delete(`more-${id}`) : f.expanded.add(`more-${id}`))}>
      <Chevron {open} /> {open ? "Show less" : "Show all"}
    </Button>
  {/if}
{/snippet}

{#if row.kind === "tool"}
  <ToolRow {sessionId} {row} {chars} {readonly} nested />
{:else if row.kind === "run"}
  <WorkRun {sessionId} {row} {chars} {readonly} {nested} />
{:else if row.kind === "prose"}
  <span class="sr-only">{agent} says</span>
  <!-- Colour and rail mark change at the stop, and the geometry does not. An answer's body reads a
       step below its lead, and a beat short enough to be one line carries its role's colour alone. -->
  {#if role === "narration"}
    <aside class="cv-progress-note" aria-label="Agent commentary">
      <svg class="cv-note-icon" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 3.5h12v9H9l-4 3v-3H4zM7 7h6M7 9.5h4" /></svg>
      <div class="min-w-0 selectable text-prose text-ink-2" data-voice="agent" data-role="narration"><SSHRich {sessionId} text={row.text} quiet conversation /></div>
    </aside>
  {:else}
  <div class="selectable {answerShaped(row.text) ? 'text-ink-2' : 'text-ink'} text-prose" data-voice="agent" data-role={role}><SSHRich {sessionId} text={row.text} conversation compact={role === "answer"} reveal={view.cursor === row.id} /></div>
  {/if}
  {#if row.truncated}
    <p class="mt-1 flex items-center gap-2 text-sm text-faint">
      This message is longer than lola keeps.
      <Button size="xs" onclick={() => ssh.showAgentPane(sessionId)}>Terminal</Button>
    </p>
  {/if}
{:else if row.kind === "you"}
  {@const h = row.item.human}
  {#if h}
    {@const sent = youAttachments(h.text)}
    <span class="sr-only">You said</span>
    <div class="cv-user-message text-prose" data-voice="you">
      {#if sent.images.length}
        <div class="flex flex-wrap gap-2 pb-2" data-you-images>
          {#each sent.images as im (im.n)}
            {@const thumb = view.sentThumbs.get(im.path)}
            <div class="relative h-8 w-11" title={im.path} data-you-image={im.n}>
              {#if thumb}
                <img src={thumb} alt="Image {im.n}" class="h-8 w-11 rounded-sm border border-edge object-cover" />
              {:else}
                <span class="grid h-8 w-11 place-items-center rounded-sm border border-edge text-faint"><AttachGlyph kind="image" class="size-5" /></span>
              {/if}
              <span class="num absolute -top-1.5 -right-1.5 grid size-4 place-items-center rounded-full bg-ink-2 text-[11px] text-canvas" aria-hidden="true">{im.n}</span>
            </div>
          {/each}
        </div>
      {/if}
      {@render clamped(row.id, sent.text, h.recalled ? "text-faint line-through" : "text-ink", 12, sent.parts)}
      {#if h.transcript}
        <Disclosure label={transcriptLabel(view.items, row.item)} class="-ml-1.5">
          <pre class="selectable mt-1 ml-1.5 max-h-60 overflow-auto rounded-sm bg-canvas px-2 py-1.5 font-mono text-sm whitespace-pre-wrap text-ink">{h.transcript}</pre>
        </Disclosure>
      {/if}
    </div>
    {#if h.recalled}
      <p class="mt-1 text-sm text-faint">Recalled</p>
    {:else if h.returned}
      <p class="mt-1 flex items-center justify-start gap-2 text-sm text-faint">
        Returned to {agent}'s prompt
        <Button size="xs" onclick={() => ssh.showAgentPane(sessionId)}>Terminal</Button>
      </p>
    {/if}
    {#if noFeed && maybeAnsweredInPane(view.items, row.item, session?.agentState, sshClock.now)}
      <div class="mt-1 flex justify-start"><Moment text="{agent} may have answered in its own terminal."><Button size="xs" onclick={() => ssh.showAgentPane(sessionId)}>Show</Button></Moment></div>
    {/if}
  {/if}
{:else if row.kind === "terminal"}
  <!-- On the person's side, because a person typed it, but only the log says so: its label stays and
       it gets a side line where a message on lola's record gets a fill. -->
  <div class="cv-user-message">
    <span class="sr-only">In terminal</span>
    <div class="text-prose" data-voice="terminal">{@render clamped(row.id, row.text, "text-ink-2", 6)}</div>
  </div>
{:else if row.kind === "origin"}
  {#if row.source === "lola"}
    <SystemMessage text={row.text} label={row.label} legacy reveal={view.cursor === row.id} />
  {:else}
    <details class="cv-background-event" use:disclosureMotion open={view.cursor === row.id}>
      <summary><Chevron class="cv-disclosure-chevron" /><span>Background event</span><span class="cv-event-preview">{row.text.replace(/^Background task finished · /, "")}</span></summary>
      <div class="selectable">{row.text}</div>
    </details>
  {/if}
{:else if row.kind === "notice"}
  <p class="selectable text-sm break-words whitespace-pre-wrap text-faint" data-voice="notice" title={row.text}>{row.text}</p>
{:else if row.kind === "plate"}
  {#if row.item.block}
    <span class="sr-only">Lola recorded</span>
    <SSHBlock {sessionId} block={row.item.block} filter={view.filter} {showHost} />
  {/if}
{:else if row.kind === "platefold"}
  {@const fit = fitNames(row.names, Math.max(16, chars - 28))}
  <!-- A plate that opens: a card-shaped row. It still names what ran. -->
  <button
    type="button"
    data-plate
    class="grid w-full grid-cols-[minmax(0,1fr)_auto_16px] items-baseline gap-x-2 rounded-md border border-edge/60 bg-panel px-3 py-2 text-left font-mono text-sm transition-colors hover:bg-sel/30"
    title={row.names.join("\n")}
    onclick={() => view.openedFolds.add(row.id)}
  >
    <span class="flex min-w-0 items-baseline gap-[1ch] text-ink"><span class="min-w-0 truncate">{fit.shown.join(", ")}</span>{#if fit.rest > 0}<span class="num shrink-0 text-faint">+{fit.rest}</span>{/if}</span>
    <span class="num whitespace-nowrap"><span class="text-good">all exit 0</span><span class="text-faint"> · {plural(row.items.length, "command")} · {formatMs(row.totalMs)}</span></span>
    <span class="grid place-items-center self-center text-faint"><Chevron /></span>
  </button>
{:else if row.kind === "element"}
  {#if row.item.element}<SSHElement {sessionId} element={row.item.element} record />{/if}
{:else if row.kind === "widget"}
  {#if row.item.element}<WidgetCard {sessionId} element={row.item.element as SSHElementData} />{/if}
{:else if row.kind === "moment"}
  {#if row.item.system?.kind === "handoff" && row.text.startsWith("lola ")}
    <SystemMessage text={row.text} label={row.text.replace(/^lola /, "")} reveal={view.cursor === row.id} />
  {:else}
    <Moment text={row.text} />
  {/if}
  {#if row.item.system?.kind === "handed_back"}
    <p class="mt-0.5 text-sm text-faint">{droveFor(view.items, row.item)}</p>
  {/if}
  {#if row.item.system?.note}
    <div class="mt-1 w-fit max-w-full rounded-md bg-sel/45 px-3 py-2 text-prose text-ink-2" data-voice="you"><span class="sr-only">You said</span><span class="copy selectable">{row.item.system.note}</span></div>
  {/if}
{:else if row.kind === "divider"}
  <div class="grid grid-cols-[auto_minmax(0,1fr)] items-center gap-x-3 text-sm text-faint" data-divider>
    <span>{row.label}</span>
    <span class="h-px bg-edge/60" aria-hidden="true"></span>
  </div>
{:else if row.kind === "receipt"}
  <Receipt {sessionId} words={row.words} seq={row.seq} stopped={row.stopped} title={row.title} clock={row.clock} {latest} {files} />
{/if}
