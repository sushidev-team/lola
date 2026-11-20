<script lang="ts">
  import { copy } from "@mobile/lib/copy";
  import { router } from "@mobile/lib/router.svelte";
  import SegmentBar from "./SegmentBar.svelte";

  let { project, value }: { project: string; value: "sessions" | "git" | "graph" } = $props();
  const segments = [
    { id: "sessions", label: copy.projects.sessions },
    { id: "git", label: copy.git.segments.changes },
    { id: "graph", label: copy.git.graph },
  ] as const;

  function select(id: typeof value) {
    if (id === value) return;
    router.replace({ name: "tab", tab: "projects", project, ...(id === "sessions" ? {} : { pick: id }) });
  }
</script>

<div class="shrink-0 px-4 pt-1 pb-3">
  <SegmentBar {segments} {value} onchange={select} label={copy.projects.title} />
</div>

<style>
  div :global(.segment-track) { min-height: 44px; }
</style>
