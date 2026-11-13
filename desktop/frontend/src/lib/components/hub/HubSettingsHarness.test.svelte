<script lang="ts">
  // Holds the settings values the way SettingsForm does, so a test can see what
  // an edit wrote.
  import HubSettings from "./HubSettings.svelte";
  import type { TeamPolicyDTO } from "@bindings/desktop/models";

  let { initial, policy = null, onIdentityChange = () => {}, sink }: {
    initial: Record<string, unknown>;
    policy?: TeamPolicyDTO | null;
    onIdentityChange?: () => void;
    sink: { values: Record<string, unknown> };
  } = $props();
  // svelte-ignore state_referenced_locally
  let values = $state({ ...initial });
  $effect(() => { sink.values = $state.snapshot(values); });
</script>

<HubSettings values={values as never} {policy} {onIdentityChange} onChange={(p) => Object.assign(values, p)} />
