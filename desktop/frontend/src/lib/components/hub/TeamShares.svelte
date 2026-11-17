<script lang="ts">
  // Team session sharing on the Team view: the sessions teammates offer this Mac,
  // and the teammates this Mac knows. Compact on purpose (icons carry the state, words
  // only where they decide something). The daemon applies every rule; a refusal is shown
  // in its words.
  //
  // A pin is two separate decisions, each an icon button: "may attach to my sessions"
  // and "I will attach to theirs". Pinning shows the fingerprint to compare with the one
  // on their Mac; the daemon re-reads their key at that moment and refuses a changed one.
  import { onMount, untrack } from "svelte";
  import Button from "../Button.svelte";
  import Checkbox from "../Checkbox.svelte";
  import Icon from "../Icon.svelte";
  import { share } from "$lib/share.svelte";
  import { LEVELS, attachBlock, groupOffers, levelOf } from "$lib/sharing";
  import { tooltip } from "$lib/tooltip";
  import type { ShareMemberInfo } from "@bindings/internal/protocol";

  onMount(() => share.watch());

  const groups = $derived(groupOffers(share.offers));

  /** The teammate and direction being pinned, waiting for the person's comparison. */
  let pinning = $state<{ id: string; allow: boolean; trust: boolean; fingerprint: string } | null>(null);
  let matches = $state(false);

  function startPin(m: ShareMemberInfo, dir: "allow" | "trust") {
    matches = false;
    changed = false;
    share.error = "";
    pinning = { id: m.deviceId, allow: dir === "allow", trust: dir === "trust", fingerprint: m.fingerprint };
  }

  // A refresh that changes the fingerprint under an open comparison voids it: the box is
  // cleared and the person compares the new one (what is sent is always the compared one).
  let changed = $state(false);
  $effect(() => {
    const p = pinning;
    if (!p) return;
    const now = share.members.find((x) => x.deviceId === p.id)?.fingerprint;
    if (now !== undefined && now !== p.fingerprint)
      untrack(() => {
        p.fingerprint = now;
        matches = false;
        changed = true;
      });
  });

  async function confirmPin(m: ShareMemberInfo) {
    if (!pinning || !matches || pinning.fingerprint !== m.fingerprint) return;
    // The fingerprint the person compared, not whatever a refresh has put there since.
    const ok = await share.trust(m.deviceId, pinning.fingerprint, { allow: pinning.allow, trust: pinning.trust });
    if (ok) pinning = null;
  }

  function levelIcon(l: string) {
    return LEVELS.find((x) => x.id === levelOf(l)) ?? LEVELS[0];
  }
</script>

<section class="min-w-0 rounded-xl border border-edge" aria-labelledby="team-shares" data-team-shares>
  <header class="flex items-center gap-3 border-b border-edge px-4 py-3">
    <h2 id="team-shares" class="text-base font-medium text-ink">Shared sessions <span class="num ml-1 text-faint">{share.offers.length}</span></h2>
  </header>

  {#if share.error}<p role="alert" class="px-4 py-2 text-sm text-warn" data-share-error>{share.error}</p>{/if}
  {#if share.offersOffline}<p class="px-4 py-2 text-sm text-faint">The hub could not be reached.</p>{/if}

  {#if groups.length === 0}
    <p class="px-4 py-3 text-sm text-faint" data-shares-empty>Nothing is shared with you yet.</p>
  {:else}
    <div class="grid divide-y divide-edge/40">
      {#each groups as g (g.owner)}
        <div class="grid min-w-0 gap-1 px-4 py-2.5" data-share-owner={g.owner}>
          <span class="flex min-w-0 items-center gap-2">
            <span class="h-2 w-2 shrink-0 rounded-full {g.online ? 'bg-good' : 'border border-faint'}" role="img" aria-label={g.online ? "Online" : "Offline"}></span>
            <span class="min-w-0 truncate text-ink">{g.ownerName}</span>
          </span>
          <ul class="grid gap-0.5 pl-4">
            {#each g.offers as o (o.id)}
              {@const block = attachBlock(o)}
              {@const lv = levelIcon(o.level)}
              <li class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto_auto] items-center gap-2" data-offer={o.id}>
                <span class="min-w-0 truncate text-ink-2">{o.title}</span>
                <span class="text-faint" use:tooltip={{ text: lv.hint }} role="img" aria-label={lv.hint}><Icon name={lv.icon} size={13} /></span>
                <Button
                  icon
                  size="xs"
                  disabled={!!block}
                  aria-label={block ? `${o.title}: ${block}` : `Attach to ${o.title}`}
                  title={block || `Attach to ${o.title}`}
                  onclick={() => share.attach(o.id)}
                  data-attach
                ><Icon name="plug" size={14} /></Button>
              </li>
            {/each}
          </ul>
        </div>
      {/each}
    </div>
  {/if}
</section>

<section class="min-w-0 rounded-xl border border-edge" aria-labelledby="team-teammates" data-team-teammates>
  <header class="flex items-center gap-3 border-b border-edge px-4 py-3">
    <h2 id="team-teammates" class="text-base font-medium text-ink">Teammates <span class="num ml-1 text-faint">{share.members.length}</span></h2>
  </header>
  {#if share.membersOffline}<p class="px-4 py-2 text-sm text-faint">The hub could not be reached. Only teammates you already pinned are shown.</p>{/if}
  {#if share.members.length === 0}
    <p class="px-4 py-3 text-sm text-faint" data-teammates-empty>No teammate has opened lola with sharing yet.</p>
  {:else}
    <ul class="divide-y divide-edge/40">
      {#each share.members as m (m.deviceId)}
        <li class="grid min-w-0 gap-1.5 px-4 py-2.5" data-teammate={m.deviceId}>
          <span class="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-2">
            <span class="grid min-w-0">
              <span class="min-w-0 truncate text-ink">{m.name}</span>
              <span class="selectable truncate font-mono text-sm text-faint" data-fingerprint>{m.fingerprint}</span>
            </span>
            <span class="flex items-center gap-0.5">
              <Button
                icon
                size="xs"
                selected={m.allowed}
                aria-pressed={m.allowed}
                aria-label={m.allowed ? `${m.name} may attach to your sessions` : `Allow ${m.name} to attach to your sessions`}
                title={m.allowed ? "May attach to your sessions. Press to stop." : "Allow to attach to your sessions"}
                disabled={!!share.busy[m.deviceId]}
                onclick={() => (m.allowed ? void share.untrust(m.deviceId, { allow: true }) : startPin(m, "allow"))}
                data-allow
              ><Icon name="user-check" size={14} /></Button>
              <Button
                icon
                size="xs"
                selected={m.trusted}
                aria-pressed={m.trusted}
                aria-label={m.trusted ? `You trust ${m.name}'s sessions` : `Trust ${m.name}'s sessions`}
                title={m.trusted ? "You can attach to their sessions. Press to stop." : "Trust their sessions so you can attach"}
                disabled={!!share.busy[m.deviceId]}
                onclick={() => (m.trusted ? void share.untrust(m.deviceId, { trust: true }) : startPin(m, "trust"))}
                data-trust
              ><Icon name="plug" size={14} /></Button>
              {#if m.allowed || m.trusted}
                <Button icon size="xs" variant="danger" aria-label={`Remove ${m.name}`} title="Remove in both directions" disabled={!!share.busy[m.deviceId]} onclick={() => void share.untrust(m.deviceId)} data-remove><Icon name="user-minus" size={14} /></Button>
              {/if}
            </span>
          </span>
          {#if pinning?.id === m.deviceId}
            <div class="grid gap-2 rounded-md border border-edge px-3 py-2" data-pin-confirm>
              <p class="text-sm text-ink-2">Check that this matches the fingerprint {m.name.split("·")[1]?.trim() || "their Mac"} shows in lola.</p>
              <p class="selectable break-all font-mono text-ink" data-pin-fingerprint>{m.fingerprint}</p>
              {#if changed}<p role="alert" class="text-sm text-warn" data-pin-changed>The fingerprint changed while you were comparing. Compare the new one.</p>{/if}
              <label class="flex items-center gap-2 text-sm text-ink-2"><Checkbox bind:checked={matches} /> It matches</label>
              <span class="flex gap-2">
                <Button size="sm" variant="primary" disabled={!matches} loading={!!share.busy[m.deviceId]} onclick={() => void confirmPin(m)} data-pin-confirm-button>{pinning.allow ? "Allow" : "Trust"}</Button>
                <Button size="sm" onclick={() => (pinning = null)}>Cancel</Button>
              </span>
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>
