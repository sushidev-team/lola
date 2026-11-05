<script lang="ts">
  // Agent text, as text. A bare id naming a real block becomes the same plate an
  // evidence link uses, an http(s) URL opens through the daemon, and nothing else
  // is interactive. There is no {@html} anywhere near this: it is a claim, written
  // by something that reads untrusted remote output.
  import { store } from "$lib/store.svelte";
  import { ssh } from "$lib/ssh.svelte";
  import { tokenize } from "$lib/ssh";
  import SSHEvidence from "./SSHEvidence.svelte";

  let { sessionId, text, class: cls = "" }: { sessionId: string; text: string | undefined; class?: string } = $props();

  const view = $derived(ssh.view(sessionId));
  const tokens = $derived(tokenize(text ?? "", (id) => !!view.block(id)));
</script>

<span class="selectable whitespace-pre-wrap break-words {cls}"
  >{#each tokens as t, i (i)}{#if t.kind === "url"}<a
        class="text-accent-ink underline underline-offset-2"
        href={t.text}
        onclick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          void store.openURL(t.text);
        }}>{t.text}</a
      >{:else if t.kind === "block"}<SSHEvidence {sessionId} block={t.text} verified={true} />{:else}{t.text}{/if}{/each}</span
>
