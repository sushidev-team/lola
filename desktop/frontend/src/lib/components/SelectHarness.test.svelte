<script lang="ts">
  // Test harness: <Select> takes its <option>s as a children snippet, which a
  // test cannot pass through render()'s props. Fixed options, drivable props.
  import Select from "./Select.svelte";

  let { value = "b", disabled = false }: { value?: string; disabled?: boolean } = $props();
</script>

<Select aria-label="Flavor" {value} {disabled}>
  <option value="a">Alpha</option>
  <option value="b">Beta</option>
</Select>
