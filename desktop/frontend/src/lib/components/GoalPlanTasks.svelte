<script lang="ts">
  import type { Output, Label } from "@bindings/internal/goalplan";
  let { output, labels = [] }: { output: Output; labels?: Label[] | null } = $props();
</script>

{#if output.unknowns?.length}
  <div class="text-sm"><h5 class="font-medium text-warn">Open questions</h5><ul class="mt-1 list-disc pl-5 text-faint">{#each output.unknowns as item}<li class="selectable break-words">{item}</li>{/each}</ul></div>
{/if}
<ol class="divide-y divide-edge">
  {#each output.tasks ?? [] as task, i}
    <li class="py-3 text-sm">
      <h5 class="break-words font-medium text-ink">{i + 1}. {task.title}</h5>
      {#if task.description}<p class="selectable mt-1 whitespace-pre-wrap break-words text-faint">{task.description}</p>{/if}
      <p class="mt-1 break-words text-faint">Repositories: {task.apps?.join(", ") || "Project repository"}</p>
      <p class="break-words text-faint">Covers criteria: {(task.covers ?? []).join(", ")}{task.blocked_by?.length ? ` · Depends on tasks: ${task.blocked_by.join(", ")}` : ""}</p>
      {#if task.label_ids?.length}<p class="break-words text-faint">Labels: {task.label_ids.map(id => labels?.find(label => label.id === id)?.name ?? id).join(", ")}</p>{/if}
      <ul class="mt-1 list-disc pl-5 text-faint">{#each task.acceptance ?? [] as item}<li class="selectable break-words">{item}</li>{/each}</ul>
      {#each Object.entries(task.paths ?? {}) as [member, paths]}<p class="mt-1 break-words text-faint">{member}: {paths?.length ? paths.join(", ") : "Read-only context"}</p>{/each}
    </li>
  {/each}
</ol>
