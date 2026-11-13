<script lang="ts">
  // An agent asking to do something. See ChoiceCard for the rules on the press.
  //
  // The title is lola's, and it says only what the pane's shape proves. A command dialog names
  // itself in its first line, so this says "wants to run" only then.
  import type { AgentPrompt } from "@bindings/internal/protocol";
  import type { Snippet } from "svelte";
  import { store } from "$lib/store.svelte";
  import { agentName } from "$lib/ssh";
  import ChoiceCard from "./ChoiceCard.svelte";

  let { sessionId, prompt, narrow = false, pager }: { sessionId: string; prompt: AgentPrompt; narrow?: boolean; pager?: Snippet } = $props();

  const agent = $derived(agentName(store.sessionById(sessionId)?.agent));
  const asksToRun = $derived(/command/i.test(prompt.lines?.[0] ?? ""));
  const title = $derived(asksToRun ? `${agent} wants to run` : `${agent} wants permission`);
</script>

<ChoiceCard {sessionId} {prompt} {title} {narrow} {pager} note={(l) => (l === "No" ? "Ends this turn" : "")} />
