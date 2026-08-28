import { vitePreprocess } from "@sveltejs/vite-plugin-svelte";

// Required, not optional: without a preprocessor the compiler rejects every
// `<script lang="ts">` block, and every shared component in
// desktop/frontend/src/lib has one. Mirrors desktop/frontend/svelte.config.js.
export default {
  preprocess: vitePreprocess(),
};
