import { afterEach, expect, it } from 'vitest';
import { cleanup, render } from '@testing-library/svelte';
import SystemMessage from './SystemMessage.svelte';
afterEach(cleanup);
it('does not offer expansion when the event only repeats its summary', () => {
  const { container } = render(SystemMessage, { text: 'lola asked Claude to resolve merge conflicts', label: 'asked Claude to resolve merge conflicts' });
  expect(container.querySelector('details')).toBeNull();
  expect(container.textContent?.match(/resolve merge conflicts/g)).toHaveLength(1);
});
it('keeps additional instructions accessible in a disclosure', () => {
  const { container } = render(SystemMessage, { text: 'Resolve conflicts, then rerun the tests.', label: 'Resolve merge conflicts' });
  expect(container.querySelector('details')).not.toBeNull();
  expect(container.textContent).toContain('then rerun the tests');
});
