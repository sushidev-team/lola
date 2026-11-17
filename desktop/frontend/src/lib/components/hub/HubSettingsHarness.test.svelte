<script lang="ts">
  // Holds the settings values the way SettingsForm does, so a test can see what
  // an edit wrote, what was reported saved, and whether drafts are unsaved.
  import HubSettings from "./HubSettings.svelte";
  import type { TeamPolicyDTO } from "@bindings/desktop/models";

  let { initial, policy = null, onIdentityChange = () => {}, onOpenTab = () => {}, sink }: {
    initial: Record<string, unknown>;
    policy?: TeamPolicyDTO | null;
    onIdentityChange?: () => void;
    onOpenTab?: (tab: string) => void;
    sink: { values: Record<string, unknown>; dirty: boolean; saved: Record<string, unknown>[] };
  } = $props();
  // svelte-ignore state_referenced_locally
  let values = $state({ ...initial });
  $effect(() => { sink.values = $state.snapshot(values); });
</script>

<HubSettings values={values as never} {policy} {onIdentityChange} {onOpenTab}
  onChange={(p) => Object.assign(values, p)}
  onSaved={(p) => { sink.saved.push({ ...p }); Object.assign(values, p); }}
  onDirty={(d) => (sink.dirty = d)} />
