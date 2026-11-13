import { expect, it } from 'vitest';

it('uses drawn disclosure chevrons instead of browser triangles throughout the UI', () => {
  const sources = import.meta.glob<string>(['./components/**/*.svelte', './views/**/*.svelte'], {query:'?raw', import:'default', eager:true});
  const missing: string[] = [];
  for (const [path, source] of Object.entries(sources)) {
    for (const match of source.matchAll(/<summary\b[^>]*>([\s\S]*?)<\/summary>/g)) {
      if (!/<Chevron\b|name=["'](?:chevron-[^"']+|ellipsis)["']/.test(match[1])) missing.push(path);
    }
  }
  expect(missing).toEqual([]);
});
