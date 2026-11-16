import { expect, it } from 'vitest';
import { sidebarWidth } from './sidebarwidth';
it('bounds saved sizes and protects the conversation width', () => {
  expect(sidebarWidth(540, 1400)).toBe(540);
  expect(sidebarWidth(900, 1400)).toBe(760);
  expect(sidebarWidth(10, 1400)).toBe(440);
  expect(sidebarWidth(760, 1000)).toBe(440);
  expect(sidebarWidth(760, 800)).toBe(240);
  expect(sidebarWidth(NaN, 1400)).toBe(540);
});
