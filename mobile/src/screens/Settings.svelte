<script lang="ts">
  import { appearance, FLAVORS, THEME_IDS, type ThemeId } from "$lib/theme-runtime.svelte";
  import LargeTitle from "@mobile/lib/components/LargeTitle.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import ListRow from "@mobile/lib/components/ListRow.svelte";
  import Switch from "@mobile/lib/components/Switch.svelte";
  import { connection } from "@mobile/lib/connection.svelte";
  import { copy, fill } from "@mobile/lib/copy";
  import { NAME_MAX } from "@mobile/lib/daemonname";
  import { router } from "@mobile/lib/router.svelte";
  import {
    appVersion,
    bootMs,
    pathWord,
    readStatus,
    renameMac,
    settingsPhrases as t,
  } from "@mobile/lib/settings.svelte";

  // This phone's own settings, as inset groups. Nothing here writes the Mac's
  // config.toml. Unpair opens the shared confirm (a bottom action sheet).
  let draft = $state<string | null>(null);
  let boot = $state<number | null>(null);
  let themesOpen = $state(false);

  $effect(() => {
    void readStatus().then((s) => (boot = bootMs(s?.launchedAt)));
  });

  const connectionWord = $derived(pathWord(connection.ready, connection.path));
  const themeName = $derived(FLAVORS[appearance.id]?.label ?? "");
  // A placeholder is not a name: with no Mac name there is nothing to head or rename.
  const isName = (v: string | undefined): v is string => !!v && v !== "—" && v !== "your Mac";
  const macName_ = $derived(isName(connection.label) ? connection.label : "");
  const phoneName = $derived(isName(connection.device?.name) ? connection.device!.name : "");
  const followSystem = $derived(appearance.preference.followSystem);

  async function commit(e?: Event): Promise<void> {
    e?.preventDefault();
    const name = draft;
    draft = null;
    if (name !== null) await renameMac(name);
  }

  function pick(id: ThemeId): void {
    appearance.commitLocal({ ...appearance.snapshot(), theme: id, followSystem: false });
  }

  function setFollow(on: boolean): void {
    appearance.commitLocal({ ...appearance.snapshot(), followSystem: on });
  }
</script>

{#snippet macName()}
  <form class="min-w-0 flex-1" onsubmit={commit}>
    <input
      class="w-full min-w-0 bg-transparent text-right text-body text-subtext outline-none placeholder:text-subtext"
      type="text"
      aria-label={copy.session.menu.rename}
      autocapitalize="words"
      autocorrect="off"
      spellcheck="false"
      enterkeyhint="done"
      maxlength={NAME_MAX}
      placeholder={connection.label}
      value={draft ?? connection.label}
      oninput={(e) => (draft = e.currentTarget.value)}
      onblur={() => void commit()}
    />
  </form>
{/snippet}

{#snippet matchSwitch()}
  <Switch checked={followSystem} onchange={setFollow} label={copy.settings.rows.matchSystem} />
{/snippet}

<div class="flex h-full min-h-0 flex-col bg-canvas">
  <LargeTitle title={copy.settings.title} />

  <div class="grid min-h-0 flex-1 content-start gap-4 overflow-y-auto overscroll-contain pb-4">
    <ListGroup header={macName_ || undefined}>
      {#if macName_}
        <!-- An inline name field, not a drill-in: no chevron. -->
        <ListRow label={copy.settings.rows.name} trailing={macName} />
      {/if}
      {#if phoneName}
        <ListRow label={copy.settings.rows.phone} value={phoneName} />
      {/if}
      <ListRow
        label={copy.settings.rows.connection}
        value={connectionWord}
        chevron
        onclick={() => router.open("connection")}
      />
    </ListGroup>

    <ListGroup header={copy.settings.sections.appearance}>
      <ListRow label={copy.settings.rows.matchSystem} trailing={matchSwitch} />
      <ListRow
        label={copy.settings.rows.theme}
        value={themeName}
        chevron
        onclick={() => (themesOpen = !themesOpen)}
      />
      {#if themesOpen}
        {#each THEME_IDS as id (id)}
          <ListRow label={FLAVORS[id].label} checked={appearance.id === id} onclick={() => pick(id)}>
            {#snippet trailing()}
              <!-- The swatch is drawn in its own flavor's colours, not the live ones. -->
              <span
                class="size-5 shrink-0 rounded-md border"
                style="background:{FLAVORS[id].base};border-color:{FLAVORS[id].surface1}"
                aria-hidden="true"
              >
                <span class="m-[5px] block size-2.5 rounded-full" style="background:{FLAVORS[id].sky}"></span>
              </span>
            {/snippet}
          </ListRow>
        {/each}
      {/if}
    </ListGroup>

    <ListGroup header={copy.settings.rows.about}>
      <ListRow label={t.version} value={appVersion} />
      {#if boot !== null}
        <ListRow label={t.boot} value={`${boot} ms`} />
      {/if}
    </ListGroup>

    <!-- 16 (grid gap) + 16: a 32px gap before the destructive group. -->
    <div class="mt-4">
      <ListGroup>
        <ListRow
          label={macName_ ? fill(copy.settings.rows.unpairMac, { mac: macName_ }) : copy.settings.rows.unpair}
          tone="danger" onclick={() => router.open("unpair")} />
      </ListGroup>
    </div>
  </div>
</div>
