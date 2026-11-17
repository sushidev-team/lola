<script lang="ts">
  import FilterSheet from "@mobile/lib/components/FilterSheet.svelte";
  import { inbox, inboxView } from "@mobile/lib/inbox.svelte";
  import { router, type InboxFilter } from "@mobile/lib/router.svelte";

  // Selecting a filter replaces the Inbox route, which also closes the sheet.
  // Choosing a grouping is a preference, so it closes the sheet itself.

  const top = $derived(router.top);
  const value = $derived<InboxFilter>(top.name === "tab" && top.tab === "inbox" ? (top.filter ?? "") : "");

  function select(filter: InboxFilter): void {
    if (top.name !== "tab" || top.tab !== "inbox") return router.close();
    router.replace(filter === "" ? { name: "tab", tab: "inbox" } : { name: "tab", tab: "inbox", filter });
  }
</script>

<FilterSheet
  {value}
  counts={inbox.counts}
  group={inboxView.group}
  onselect={select}
  ongroup={(g) => {
    inboxView.set(g);
    router.close();
  }}
  onclose={() => router.close()}
/>
