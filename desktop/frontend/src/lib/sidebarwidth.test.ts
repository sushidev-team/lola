import { expect, it } from 'vitest';
import { sidebarWidth } from './sidebarwidth';
it('bounds saved sizes and protects the conversation width', () => {
  expect(sidebarWidth(320, 1400)).toBe(320);
  expect(sidebarWidth(900, 1400)).toBe(480);
  expect(sidebarWidth(10, 1400)).toBe(240);
  expect(sidebarWidth(480, 1000)).toBe(440);
  expect(sidebarWidth(480, 800)).toBe(240);
  expect(sidebarWidth(NaN, 1400)).toBe(320);
});
