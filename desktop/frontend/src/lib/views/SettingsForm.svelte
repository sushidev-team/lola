<script lang="ts">
  import { onMount } from "svelte";
  import Modal from "$lib/components/Modal.svelte";
  import { store } from "$lib/store.svelte";
  import { nav } from "$lib/nav.svelte";
  import { ConfigService } from "@bindings/desktop";
  import type { SettingsDTO } from "@bindings/desktop/models";

  let dto = $state<SettingsDTO | null>(null);
  let loading = $state(true);
  let loadError = $state("");
  let saving = $state(false);

  const AGENTS = ["claude", "codex", "opencode"];

  const inputCls =
    "w-full rounded border border-edge bg-canvas px-2 py-1 text-xs text-ink outline-none focus:border-accent";
  const rowCls = "grid grid-cols-[11rem_1fr] items-center gap-3";

  onMount(async () => {
    try {
      dto = await ConfigService.GetSettings();
    } catch (err) {
      loadError = String(err);
      store.setFlash(String(err), "bad");
    } finally {
      loading = false;
    }
  });

  async function save() {
    if (!dto) return;
    saving = true;
    try {
      await ConfigService.SaveSettings(dto);
      store.setFlash("settings saved", "good");
      nav.closeOverlay();
    } catch (err) {
      store.setFlash(String(err), "bad");
    } finally {
      saving = false;
    }
  }
</script>

{#snippet head(label: string)}
  <h3 class="mb-2 text-[10px] font-semibold tracking-wider text-faint uppercase">{label}</h3>
{/snippet}

<Modal title="settings" onClose={() => nav.closeOverlay()} width="620px">
  {#if loading}
    <div class="py-10 text-center text-xs text-faint">loading settings…</div>
  {:else if loadError}
    <div class="py-10 text-center text-xs text-bad">{loadError}</div>
  {:else if dto}
    <div class="space-y-5 text-xs">
      <!-- Defaults ------------------------------------------------------- -->
      <section>
        {@render head("Defaults")}
        <div class="space-y-2">
          <label class={rowCls}>
            <span class="text-faint">Global cap</span>
            <input class={inputCls} type="number" min="0" bind:value={dto.globalCap} />
          </label>
          <label class={rowCls}>
            <span class="text-faint">Concurrency cap</span>
            <input class={inputCls} type="number" min="0" bind:value={dto.concurrencyCap} />
          </label>
          <label class={rowCls}>
            <span class="text-faint">Poll interval</span>
            <input class={inputCls} type="text" placeholder="60s" bind:value={dto.pollInterval} />
          </label>
          <div class={rowCls}>
            <span class="text-faint">Agent</span>
            <div class="inline-flex divide-x divide-edge overflow-hidden rounded border border-edge">
              {#each AGENTS as a (a)}
                <button
                  type="button"
                  class="px-3 py-1 {dto.agent === a ? 'bg-accent/20 text-accent' : 'text-faint hover:text-ink'}"
                  onclick={() => (dto!.agent = a)}>{a}</button
                >
              {/each}
            </div>
          </div>
        </div>
      </section>

      <!-- Notify --------------------------------------------------------- -->
      <section class="border-t border-edge/40 pt-4">
        {@render head("Notify")}
        <div class="space-y-2">
          <label class="flex cursor-pointer items-center gap-2">
            <input type="checkbox" class="accent-accent" bind:checked={dto.notifyDesktop} />
            <span>Desktop notifications</span>
          </label>
          <label class={rowCls}>
            <span class="text-faint">Slack webhook env</span>
            <div>
              <input class={inputCls} type="text" placeholder="LOLA_SLACK_WEBHOOK" bind:value={dto.slackWebhookEnv} />
              <p class="mt-1 text-[10px] text-faint">Slack webhook env VAR NAME — never the URL.</p>
            </div>
          </label>
        </div>
      </section>

      <!-- Brain ---------------------------------------------------------- -->
      <section class="border-t border-edge/40 pt-4">
        {@render head("Brain")}
        <div class="space-y-2">
          <label class="flex cursor-pointer items-center gap-2">
            <input type="checkbox" class="accent-accent" bind:checked={dto.brainEnabled} />
            <span>Enabled</span>
          </label>
          <label class={rowCls}>
            <span class="text-faint">Model</span>
            <input class={inputCls} type="text" placeholder="claude-…" bind:value={dto.brainModel} />
          </label>
          <label class={rowCls}>
            <span class="text-faint">Timeout (s)</span>
            <input class={inputCls} type="number" min="0" bind:value={dto.brainTimeout} />
          </label>
          <div class="flex flex-wrap gap-x-6 gap-y-2 pt-1">
            <label class="flex cursor-pointer items-center gap-2">
              <input type="checkbox" class="accent-accent" bind:checked={dto.brainSummarizeEscalation} />
              <span>Summarize on escalation</span>
            </label>
            <label class="flex cursor-pointer items-center gap-2">
              <input type="checkbox" class="accent-accent" bind:checked={dto.brainSummarizeApproved} />
              <span>Summarize on approved</span>
            </label>
          </div>
        </div>
      </section>

      <!-- CodeRabbit review ---------------------------------------------- -->
      <section class="border-t border-edge/40 pt-4">
        {@render head("CodeRabbit review")}
        <div class="space-y-2">
          <label class="flex cursor-pointer items-center gap-2">
            <input type="checkbox" class="accent-accent" bind:checked={dto.reviewEnabled} />
            <span>Enabled</span>
          </label>
          <label class={rowCls}>
            <span class="text-faint">Command</span>
            <input class={inputCls} type="text" placeholder="coderabbit review" bind:value={dto.reviewCommand} />
          </label>
          <label class={rowCls}>
            <span class="text-faint">Timeout (s)</span>
            <input class={inputCls} type="number" min="0" bind:value={dto.reviewTimeout} />
          </label>
          <div class="flex flex-wrap gap-x-6 gap-y-2 pt-1">
            <label class="flex cursor-pointer items-center gap-2">
              <input type="checkbox" class="accent-accent" bind:checked={dto.reviewOnPrOpen} />
              <span>On PR open</span>
            </label>
            <label class="flex cursor-pointer items-center gap-2">
              <input type="checkbox" class="accent-accent" bind:checked={dto.reviewSendToAgent} />
              <span>Send to agent</span>
            </label>
            <label class="flex cursor-pointer items-center gap-2">
              <input type="checkbox" class="accent-accent" bind:checked={dto.reviewCommentOnLinear} />
              <span>Comment on Linear</span>
            </label>
          </div>
        </div>
      </section>

      <!-- CodeRabbit watch ----------------------------------------------- -->
      <section class="border-t border-edge/40 pt-4">
        {@render head("CodeRabbit watch")}
        <div class="space-y-2">
          <label class="flex cursor-pointer items-center gap-2">
            <input type="checkbox" class="accent-accent" bind:checked={dto.crEnabled} />
            <span>Enabled</span>
          </label>
          <label class={rowCls}>
            <span class="text-faint">Author</span>
            <input class={inputCls} type="text" placeholder="coderabbitai[bot]" bind:value={dto.crAuthor} />
          </label>
          <div class="flex flex-wrap gap-x-6 gap-y-2 pt-1">
            <label class="flex cursor-pointer items-center gap-2">
              <input type="checkbox" class="accent-accent" bind:checked={dto.crNotify} />
              <span>Notify</span>
            </label>
            <label class="flex cursor-pointer items-center gap-2">
              <input type="checkbox" class="accent-accent" bind:checked={dto.crSendToAgent} />
              <span>Send to agent</span>
            </label>
            <label class="flex cursor-pointer items-center gap-2">
              <input type="checkbox" class="accent-accent" bind:checked={dto.crCommentOnLinear} />
              <span>Comment on Linear</span>
            </label>
          </div>
        </div>
      </section>
    </div>
  {/if}

  {#snippet footer()}
    <div class="flex items-center justify-end gap-2">
      <button class="rounded px-3 py-1 text-xs text-faint hover:text-ink" onclick={() => nav.closeOverlay()}
        >cancel</button
      >
      <button
        class="rounded bg-accent/20 px-3 py-1 text-xs text-accent hover:bg-accent/30 disabled:opacity-40"
        onclick={save}
        disabled={saving || loading || !dto}>{saving ? "saving…" : "save"}</button
      >
    </div>
  {/snippet}
</Modal>
