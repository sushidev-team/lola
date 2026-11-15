<script lang="ts">
  import FilterSheet from "@mobile/lib/components/FilterSheet.svelte";
  import { inbox } from "@mobile/lib/inbox.svelte";
  import { router, type InboxFilter } from "@mobile/lib/router.svelte";

  // Selecting replaces the Inbox route, which also closes the sheet.

  const top = $derived(router.top);
  const value = $derived<InboxFilter>(top.name === "tab" && top.tab === "inbox" ? (top.filter ?? "") : "");

  function select(filter: InboxFilter): void {
    if (top.name !== "tab" || top.tab !== "inbox") return router.close();
    router.replace(filter === "" ? { name: "tab", tab: "inbox" } : { name: "tab", tab: "inbox", filter });
  }
</script>

<FilterSheet {value} counts={inbox.counts} onselect={select} onclose={() => router.close()} />
