<script lang="ts" module>
  import type { PRReadinessData } from "@bindings/internal/protocol";

  export const METHODS = ["squash", "merge", "rebase"] as const;
  export type Method = (typeof METHODS)[number];
  const KEY = "lola.mergeMethod";

  /** The remembered method, or "" when none was chosen or storage is unavailable. */
  export function savedMethod(): Method | "" {
    try {
      const v = localStorage.getItem(KEY);
      return v === "squash" || v === "merge" || v === "rebase" ? v : "";
    } catch {
      return "";
    }
  }

  function remember(m: Method): void {
    try {
      localStorage.setItem(KEY, m);
    } catch {
      /* a per-viewer convenience only */
    }
  }
</script>

<script lang="ts">
  import { DaemonService } from "@bindings/desktop";
  import { confirm } from "$lib/confirm.svelte";
  import { readPRReadiness } from "$lib/prreadiness";
  import { store } from "$lib/store.svelte";
  import SegmentBar from "@mobile/lib/components/SegmentBar.svelte";
  import SessionChips from "@mobile/lib/components/SessionChips.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { copy, fill } from "@mobile/lib/copy";
  import { haptic } from "@mobile/lib/haptics";
  import { router } from "@mobile/lib/router.svelte";

  // The merge sheet (spec §5.3). The method is the one the person last chose on
  // any surface; readiness is read fresh. A PR that is not ready merges only
  // through a second, explicit confirmation, and the daemon still enforces
  // GitHub's own protections.

  const id = $derived(router.top.name === "session" ? router.top.id : "");
  const number = $derived(store.sessionById(id)?.prNumber ?? 0);

  let data = $state<PRReadinessData | null>(null);
  let error = $state("");
  let busy = $state(false);
  let chosen = $state<Method | "">(savedMethod());

  const method = $derived<Method | "">(chosen || (METHODS.find((m) => m === data?.mergeMethod) ?? ""));
  const ready = $derived(!!data && data.mergeBlocked === "");
  const open = $derived(data?.state === "OPEN");
  // The branch it lands on: the PR's base when the readiness read carries it, else the project's default.
  const base = $derived(
    data?.base || store.projects.find((p) => p.name === store.sessionById(id)?.project)?.defaultBranch || "",
  );

  $effect(() => {
    const sid = id;
    const n = number;
    if (!sid || n <= 0) return;
    let live = true;
    readPRReadiness(sid, n)
      .then((d) => live && (data = d))
      .catch((e) => live && (error = String(e instanceof Error ? e.message : e)));
    return () => {
      live = false;
    };
  });

  function choose(m: Method): void {
    chosen = m;
    remember(m);
  }

  async function merge(confirmUnready: boolean): Promise<void> {
    const d = data;
    if (!d || !method || busy) return;
    busy = true;
    error = "";
    try {
      await DaemonService.MergePR(id, number, d.repo, d.head, method, confirmUnready);
      haptic("success");
      void store.refresh();
      router.close();
    } catch (e) {
      error = String(e instanceof Error ? e.message : e);
    } finally {
      busy = false;
    }
  }

  function anyway(): void {
    const d = data;
    if (!d || !method) return;
    confirm.ask({
      title: copy.merge.unready.action,
      body: d.mergeBlocked,
      confirmLabel: copy.merge.unready.action,
      onConfirm: () => void merge(true),
    });
  }
</script>

{#snippet footer()}
  {#if ready}
    <TouchButton variant="primary" prominent loading={busy} disabled={!open || !method} onclick={() => merge(false)}>
      {method ? copy.merge.actions[method] : copy.merge.action}
    </TouchButton>
  {:else}
    <TouchButton variant="danger-solid" prominent loading={busy} disabled={!data || !open || !method} onclick={anyway}>
      {copy.merge.unready.action}
    </TouchButton>
  {/if}
{/snippet}

<Sheet label={copy.merge.title} title={copy.merge.title} detent="fit" cancel="Cancel" done={false} {footer} onclose={() => router.close()}>
  <div class="flex min-w-0 flex-col gap-3 px-4 pb-2">
    <SegmentBar
      segments={METHODS.map((m) => ({ id: m, label: copy.merge.methods[m] }))}
      value={method as Method}
      onchange={choose}
      label="Merge method"
    />
    {#if data}
      {#if ready}
        {#if base}<p class="text-subhead text-subtext">{fill(copy.merge.into, { base })}</p>{/if}
      {:else}
        <p class="text-subhead" style="color: var(--color-bad)" role="status">{copy.merge.unready.body}</p>
      {/if}
    {/if}
    <SessionChips session={store.sessionById(id)} detail />
    {#if error}<p class="text-subhead text-warn" role="alert">{error}</p>{/if}
  </div>
</Sheet>
