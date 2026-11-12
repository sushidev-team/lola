import { afterEach, expect, it, vi } from 'vitest';
import { disclosureMotion } from './disclosureMotion';

afterEach(() => { document.body.replaceChildren(); vi.restoreAllMocks(); });

it('toggles only the nearest disclosure when summaries are nested', () => {
  const outer = document.createElement('details');
  outer.open = true;
  outer.innerHTML = '<summary>Remote</summary><details open><summary><span>Origin</span></summary><p>Branches</p></details>';
  document.body.append(outer);
  const inner = outer.querySelector('details')!;
  const parentAction = disclosureMotion(outer), childAction = disclosureMotion(inner);
  inner.querySelector('span')!.click();
  expect(inner.open).toBe(false);
  expect(outer.open).toBe(true);
  outer.querySelector('summary')!.click();
  expect(outer.open).toBe(false);
  parentAction.destroy(); childAction.destroy();
});

it('skips animation for reduced motion and leaves summary links alone', () => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: true })));
  const node = document.createElement('details');
  node.innerHTML = '<summary>Worktrees <a href="#branch">Branch</a></summary><p>Content</p>';
  document.body.append(node);
  const animate = vi.fn(); node.animate = animate;
  const action = disclosureMotion(node);
  node.querySelector('summary')!.click();
  expect(node.open).toBe(true);
  expect(animate).not.toHaveBeenCalled();
  const linkClick = new MouseEvent('click', { bubbles: true, cancelable: true });
  node.querySelector('a')!.dispatchEvent(linkClick);
  expect(linkClick.defaultPrevented).toBe(false);
  expect(node.open).toBe(true);
  action.destroy();
  vi.unstubAllGlobals();
});
