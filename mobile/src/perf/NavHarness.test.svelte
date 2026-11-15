<script lang="ts">
  // The App outlet, reduced to what the navigation budget needs: the Inbox stays
  // mounted under a pushed Session screen, which mounts and unmounts with the route.
  import Inbox from "../screens/Inbox.svelte";
  import Session from "../screens/session/Session.svelte";
  import { router } from "../lib/router.svelte";

  const over = $derived(router.stack.length > 1 ? router.top : null);
</script>

<Inbox />
{#if over?.name === "session"}
  <Session />
{/if}
