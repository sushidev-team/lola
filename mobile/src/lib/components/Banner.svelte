<script lang="ts" module>
  import { copy, fill } from "@mobile/lib/copy";
  import type { Diagnosis } from "@mobile/lib/diagnose";

  /** info: quiet; warn: a notice (development link, version); bad: the Mac's identity changed. */
  export type BannerTone = "info" | "warn" | "bad";
  /** The leading 18px glyph: wifi-off (offline, unreachable), shield-x (key changed), refresh-cw (racing). */
  export type BannerGlyph = "wifi-off" | "key-round" | "shield-x" | "refresh-cw";

  /** What the connection's state says in the banner slot, or null when it says nothing. */
  export interface BannerSpec {
    id: string;
    tone: BannerTone;
    body: string;
    glyph: BannerGlyph;
    /** Label of the one action, when there is one. */
    action?: string;
    /** What the action does; the host owns routing. */
    intent?: "pair" | "details";
  }

  /**
   * The banner a diagnosis earns (spec §5.1, §5.5). A pairing failure
   * (`not_paired`) is not a banner: it routes to Connect. `idle` and `ok` say
   * nothing.
   */
  export function connectionBanner(d: Diagnosis, mac: string): BannerSpec | null {
    switch (d.kind) {
      case "key_changed":
        return {
          id: "key_changed",
          tone: "bad",
          glyph: "shield-x",
          body: fill(copy.banner.keyChanged.body, { mac }),
          intent: "pair",
        };
      case "racing":
        return { id: "racing", tone: "info", glyph: "refresh-cw", body: copy.banner.racing.body, intent: "details" };
      case "offline":
        return { id: "offline", tone: "info", glyph: "wifi-off", body: copy.banner.offline.body, intent: "details" };
      case "unreachable":
        return {
          id: "unreachable",
          tone: "info",
          glyph: "wifi-off",
          body: fill(copy.banner.unreachable.body, { mac }),
          action: copy.banner.unreachable.action,
          intent: "details",
        };
      case "version":
      case "failed":
        return { id: d.kind, tone: "warn", glyph: "wifi-off", body: d.detail, intent: "details" };
      default:
        return null;
    }
  }
</script>

<script lang="ts">
  import Icon from "$lib/components/Icon.svelte";

  // A 44 pt row under the large title and above the first section (spec §6.4):
  // an 18 px glyph, one line, and (when it has somewhere to go) the whole row is
  // the button. A changed key is one row ("Pair Mars again" + chevron, no separate
  // action); an action, when a banner has one, is for assistive technology only. Radius 10, 12 px padding, inside
  // the 16 px gutter.
  //
  // Invariants: it slides in from the top in 200 ms, or fades under reduced
  // motion (CSS only, so a banner that mounts never waits on script). A banner
  // that is the first thing on a screen (`inset`) pays the status-bar inset itself.

  let {
    tone = "info",
    glyph,
    body,
    action,
    onaction,
    inset = false,
    role = "status",
  }: {
    tone?: BannerTone;
    glyph?: BannerGlyph;
    body: string;
    action?: string;
    onaction?: () => void;
    inset?: boolean;
    role?: "status" | "alert";
  } = $props();

  const mark = $derived<BannerGlyph>(glyph ?? (tone === "info" ? "wifi-off" : "shield-x"));
  const ground = $derived(
    tone === "bad"
      ? "bg-pill-broken-soft text-pill-broken-soft-fg"
      : tone === "warn"
        ? "bg-pill-urgent-soft text-pill-urgent-soft-fg"
        : "bg-edge-soft text-subtext",
  );
</script>

<div class="banner shrink-0 px-4 py-1 {inset ? 'pt-[max(4px,env(safe-area-inset-top,0px))]' : ''}" {role}>
  {#if onaction}
    <button
      type="button"
      class="flex h-11 w-full touch-manipulation items-center gap-2 rounded-[10px] p-3 text-left text-subhead active:opacity-70 {ground}"
      onclick={() => onaction?.()}
    >
      <Icon name={mark} size={18} />
      <span class="min-w-0 flex-1 truncate">{body}</span>
      {#if action}<span class="sr-only">{action}</span>{/if}
      <Icon name="chevron-right" size={16} class="shrink-0 opacity-70" />
    </button>
  {:else}
    <div class="flex h-11 w-full items-center gap-2 rounded-[10px] p-3 text-subhead {ground}">
      <Icon name={mark} size={18} />
      <span class="min-w-0 flex-1 truncate">{body}</span>
    </div>
  {/if}
</div>

<style>
  .banner {
    animation: banner-in 200ms ease-out;
  }
  @keyframes banner-in {
    from {
      transform: translateY(-100%);
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .banner {
      animation: banner-fade 200ms ease-out;
    }
    @keyframes banner-fade {
      from {
        opacity: 0;
      }
    }
  }
</style>
