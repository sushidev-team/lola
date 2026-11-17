<script lang="ts">
  import { onMount } from "svelte";
  import { router } from "@mobile/lib/router.svelte";
  import { confirmUnpair } from "@mobile/lib/settings.svelte";

  // Unpairing is a confirmation, never a sheet: this entry only hands the
  // question to the shared dialog and closes itself.
  onMount(() => {
    confirmUnpair();
    router.close();
  });
</script>
