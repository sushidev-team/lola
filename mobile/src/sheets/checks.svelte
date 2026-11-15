<script lang="ts" module>
  import type { PRCheck } from "@bindings/internal/protocol";
  import { checkDuration } from "$lib/prchecks";

  // GitHub's state words a section header does not already say.
  const QUIET = new Set(["SUCCESS", "FAILURE", "ERROR", "IN_PROGRESS", "PENDING", "SKIPPED"]);

  /** "Required · CI · 3m 12s · Queued": the row's second line. */
  export function checkDetail(c: PRCheck, required: string, now?: number): string {
    const state = QUIET.has(c.state) ? "" : c.state.charAt(0) + c.state.slice(1).toLowerCase().replace(/_/g, " ");
    return [c.required ? required : "", c.workflow ?? "", checkDuration(c, now), state].filter((p) => p !== "").join(" · ");
  }
</script>

<script lang="ts">
  import { untrack } from "svelte";
  import { TalkService } from "@bindings/desktop";
  import type { PRReadinessData } from "@bindings/internal/protocol";
  import { checkURL, fixRequest, groupChecks } from "$lib/prchecks";
  import { readPRReadinessRecent } from "$lib/prreadiness";
  import { store } from "$lib/store.svelte";
  import Icon from "$lib/components/Icon.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import ListRow from "@mobile/lib/components/ListRow.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import TouchButton from "@mobile/lib/components/TouchButton.svelte";
  import { copy, fill } from "@mobile/lib/copy";
  import { refusalText } from "@mobile/lib/refusal";
  import { reviewChecks } from "@mobile/lib/reviewchecks.svelte";
  import { router } from "@mobile/lib/router.svelte";
  import { can } from "@mobile/lib/tiers.svelte";

  // Every check on the PR head (prReadiness `checkList`), grouped Failing / Running /
  // Passed / Skipped, then lola's own review gates and the agent's self-reported
  // checks, each labelled for what it is. A tap opens the check's own https page on
  // the phone. The readiness answer is the one the Review segment just read (shared
  // cache), so opening this sheet costs no extra GitHub read on the phone's small
  // external-API budget. An older daemon sends no list: the sheet links to GitHub.
  // "Ask agent to fix" is drawn only when this phone may send to the agent, and sends
  // lola's own sentence naming the failing checks: never a description or log text,
  // which are a third party's words.

  /** How long the Review segment's answer is reused. */
  const REUSE_MS = 60_000;

  const id = $derived(router.top.name === "session" ? router.top.id : "");
  const session = $derived(store.sessionById(id));
  const target = $derived(reviewChecks.targetFor(id, session?.prNumber ?? 0));
  // A primitive key: `target` is a fresh object on every sessions push when it falls
  // back to the session's own PR, and the load must not re-run (one GitHub read) for that.
  const targetKey = $derived(target ? `${target.target}#${target.number}` : "");

  let data = $state<PRReadinessData | null>(null);
  let error = $state(false);
  let notice = $state("");
  let sending = $state(false);

  $effect(() => {
    const key = targetKey;
    const t = untrack(() => target);
    data = null;
    error = false;
    if (!key || !t) return;
    let live = true;
    readPRReadinessRecent(t.target, t.number, REUSE_MS).then(
      (d) => {
        if (live) data = d;
      },
      () => {
        if (live) error = true;
      },
    );
    return () => {
      live = false;
    };
  });

  const list = $derived(data?.checkList);
  const groups = $derived(groupChecks(list));
  const local = $derived(session?.board?.checks ?? []);
  const sha = $derived((data?.checksHead || data?.head || "").slice(0, 7));
  const stale = $derived(!!data?.checksHead && !!data?.head && data.checksHead !== data.head);
  const prUrl = $derived(data?.url || session?.prUrl || "");
  // A stack lane is `<id>~<app>`: the one agent works in several app checkouts, so
  // the sentence names which app the failing checks belong to (lola's words + label).
  const laneApp = $derived.by(() => {
    const at = target?.target.indexOf("~") ?? -1;
    if (!target || at < 0) return "";
    const name = target.target.slice(at + 1);
    const a = (session?.apps ?? []).find((x) => x.name === name);
    return a?.label || a?.name || name;
  });
  const fix = $derived.by(() => {
    const base = fixRequest(groups.failing);
    if (!base || !laneApp) return base;
    return `${fill(copy.checks.fixLane.body, { app: JSON.stringify(laneApp.replace(/\s+/g, " ").trim()), n: String(target?.number ?? 0) })} ${base}`;
  });
  const offersFix = $derived(fix !== "" && can("agentSay") && !!session);

  const sections = $derived(
    [
      { key: "failing", header: copy.checks.failing, rows: groups.failing },
      { key: "running", header: copy.checks.running, rows: groups.running },
      { key: "passed", header: copy.checks.passed, rows: groups.passed },
      { key: "skipped", header: copy.checks.skipped, rows: groups.skipped },
    ].filter((s) => s.rows.length > 0),
  );

  function open(c: PRCheck): void {
    const url = checkURL(c);
    if (url) store.openURL(url);
  }
  function openAll(): void {
    if (prUrl) store.openURL(`${prUrl}/checks`);
  }

  async function askFix(): Promise<void> {
    if (!offersFix || sending) return;
    sending = true;
    notice = "";
    try {
      const r = await TalkService.Say({ session: id, text: fix });
      notice = r.delivered ? copy.checks.sent.body : r.why || copy.checks.queued.body;
    } catch (e) {
      notice = refusalText(e);
    } finally {
      sending = false;
    }
  }
</script>

{#snippet row(c: PRCheck)}
  {@const url = checkURL(c)}
  <ListRow label={c.name} detail={checkDetail(c, copy.checks.required) || undefined} onclick={url ? () => open(c) : undefined}>
    {#snippet trailing()}
      {#if url}<span class="shrink-0 text-faint" aria-hidden="true"><Icon name="external-link" size={14} /></span>{/if}
    {/snippet}
  </ListRow>
{/snippet}

{#snippet footer()}
  <div class="grid gap-2">
    {#if notice}<p role="status" class="text-center text-footnote text-faint">{notice}</p>{/if}
    <TouchButton variant="primary" prominent loading={sending} disabled={sending} onclick={askFix}>{copy.checks.askFix}</TouchButton>
  </div>
{/snippet}

<Sheet label={copy.checks.title} title={copy.checks.title} detent="large" footer={offersFix ? footer : undefined} onclose={() => router.close()}>
  <div class="flex min-w-0 flex-col pb-4" data-checks-sheet>
    {#if sha}<p class="px-8 pb-1 text-footnote text-faint">{fill(copy.checks.commit, { sha })}</p>{/if}
    {#if stale}<p class="px-8 pb-1 text-footnote text-warn" data-checks-stale>{copy.checks.stale.body}</p>{/if}
    {#if !target}
      <p class="px-8 py-4 text-body text-faint">{copy.checks.none.body}</p>
    {:else if error}
      <p class="px-8 py-4 text-body text-faint" role="status">{copy.checks.failed.body}</p>
    {:else if !data}
      <p class="px-8 py-4 text-body text-faint" role="status">{copy.checks.loading.body}</p>
    {:else if !list}
      <ListGroup footer={stale ? undefined : copy.checks.legacy.body}>
        <ListRow label={copy.checks.openGitHub} onclick={openAll} disabled={!prUrl}>
          {#snippet trailing()}<span class="shrink-0 text-faint" aria-hidden="true"><Icon name="external-link" size={14} /></span>{/snippet}
        </ListRow>
      </ListGroup>
    {:else}
      {#if sections.length === 0 && groups.gates.length === 0}
        <p class="px-8 py-4 text-body text-faint">{copy.checks.none.body}</p>
      {/if}
      {#each sections as s (s.key)}
        <div data-checks-section={s.key}>
          <ListGroup header={s.header} count={s.rows.length}>
            {#each s.rows as c, i (`${c.name}#${i}`)}{@render row(c)}{/each}
          </ListGroup>
        </div>
      {/each}
      {#if data.checksTruncated}
        <ListGroup>
          <ListRow label={copy.checks.truncated} onclick={openAll} disabled={!prUrl}>
            {#snippet trailing()}<span class="shrink-0 text-faint" aria-hidden="true"><Icon name="external-link" size={14} /></span>{/snippet}
          </ListRow>
        </ListGroup>
      {/if}
      {#if groups.gates.length}
        <div data-checks-section="gates">
          <ListGroup header={copy.checks.gates} count={groups.gates.length} footer={copy.checks.gatesNote.body}>
            {#each groups.gates as c, i (`${c.name}#${i}`)}{@render row(c)}{/each}
          </ListGroup>
        </div>
      {/if}
    {/if}
    {#if local.length}
      <div data-checks-section="local">
        <ListGroup header={copy.checks.local} count={local.length} footer={copy.checks.localNote.body}>
          {#each local as c, i (`${c.name}#${i}`)}
            <ListRow label={c.name} value={c.state === "pass" ? copy.checks.passed : c.state === "fail" ? copy.checks.failing : copy.checks.running} />
          {/each}
        </ListGroup>
      </div>
    {/if}
  </div>
</Sheet>
