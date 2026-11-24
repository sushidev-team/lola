<script lang="ts" module>
  import type { ImageAttachment } from "@bindings/desktop/models";
  import type { PickSource } from "lola-transport";
  import { native } from "@mobile/native";
  import { WireError } from "@mobile/wire";
  import { UploadPickedImage } from "@mobile/wailsshim/services/talkimage";
  import { router } from "@mobile/lib/router.svelte";

  // The composer's image picker is this sheet. `requestImage` opens it and
  // resolves with the attachment the person made, or null when the sheet closed
  // without one; at most one request is pending, and a newer one cancels the
  // older.
  let pending: ((a: ImageAttachment | null) => void) | null = null;

  export function requestImage(): Promise<ImageAttachment | null> {
    settleImage(null);
    return new Promise((resolve) => {
      pending = resolve;
      router.open("attach");
    });
  }

  export function settleImage(a: ImageAttachment | null): void {
    const done = pending;
    pending = null;
    done?.(a);
  }

  export async function pickAndUpload(session: string, source: PickSource): Promise<ImageAttachment | null> {
    if (!native) throw new WireError("No camera here.");
    const r = await native.pickImage({ source });
    if (r.cancelled) return null;
    return UploadPickedImage(session, r.mime, r.data);
  }
</script>

<script lang="ts">
  import { onDestroy } from "svelte";
  import { TalkService } from "@bindings/desktop";
  import { store } from "$lib/store.svelte";
  import Sheet from "@mobile/lib/components/Sheet.svelte";
  import ListGroup from "@mobile/lib/components/ListGroup.svelte";
  import ListRow from "@mobile/lib/components/ListRow.svelte";
  import { copy } from "@mobile/lib/copy";

  // Camera, Photos, Paste (spec §5.3). A pick or a paste uploads to the session
  // the sheet was opened from; whatever happens, closing the sheet settles the
  // composer's pending request.

  const id = $derived(router.top.name === "session" ? router.top.id : "");
  let busy = $state(false);
  let error = $state("");

  async function run(load: () => Promise<ImageAttachment | null>): Promise<void> {
    if (busy) return;
    busy = true;
    error = "";
    try {
      const got = await load();
      if (got) {
        settleImage(got);
        router.close();
      }
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  const pick = (source: PickSource) => run(() => pickAndUpload(id, source));
  const paste = () => {
    const session = store.sessionById(id);
    return run(() => TalkService.AttachClipboardImage(session?.attachmentRoot || session?.worktree || ""));
  };

  onDestroy(() => settleImage(null));
</script>

<Sheet title={copy.attach.title} label={copy.attach.title} detent="fit" done={false} cancel="Cancel" onclose={() => router.close()}>
  <ListGroup>
    <ListRow label={copy.attach.camera} icon="camera" disabled={busy} onclick={() => pick("camera")} />
    <ListRow label={copy.attach.photos} icon="image" disabled={busy} onclick={() => pick("library")} />
    <ListRow label={copy.attach.paste} icon="clipboard" disabled={busy} onclick={paste} />
  </ListGroup>
  {#if error}<p class="px-gutter pt-2 text-subhead text-subtext" role="alert">{error}</p>{/if}
</Sheet>
