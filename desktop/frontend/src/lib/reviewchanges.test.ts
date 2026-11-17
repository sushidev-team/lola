import { expect, it } from 'vitest';
import type { ReviewPackage } from '@bindings/internal/protocol';
import { reviewChanges, reviewFreshness } from './reviewchanges';
const pkg = (over={}) => ({id:'old',session:'s',repo:'acme/app',pr:1,base:'base',head:'head',title:'Settings',body:'## Changes\nOld behavior\n## Risks\nMail delay',screens:[{title:'Settings',after:'image',caption:'Old caption'}],...over}) as ReviewPackage;

it('compares retained content at the same head, including removed sections and changed capture metadata',()=>{
  const delta=reviewChanges(pkg(),pkg({body:'## Changes\nNew behavior\n## Checks\nTests pass',screens:[{title:'Settings',after:'image',caption:'New caption'}]}));
  expect(delta.headChanged).toBe(false);
  expect(delta.sections.map(s=>s.after?.title ?? s.before?.title)).toEqual(['Changes','Checks','Risks']);
  expect(delta.sections[2].after).toBeUndefined();
  expect(delta.screens).toHaveLength(1);
  expect(delta.unchangedScreens).toBe(0);
});
it('does not call identical evidence changed just because a package has a new identity',()=>{
  const delta=reviewChanges(pkg(),pkg({id:'new',head:'next'}));
  expect(delta.headChanged).toBe(true);
  expect(delta.sections).toEqual([]);expect(delta.screens).toEqual([]);expect(delta.unchangedScreens).toBe(1);
});
it('keeps repeated headings and screens separate',()=>{
  const before=pkg({body:'## Change\nFirst\n## Change\nSecond'});
  const after=pkg({body:'## Change\nFirst\n## Change\nRevised'});
  expect(reviewChanges(before,after).sections).toEqual([{before:{title:'Change',body:'Second'},after:{title:'Change',body:'Revised'}}]);
});
it('requires base, head and PR identity to establish freshness',()=>{
  const facts={repo:'acme/app',number:1,head:'head',base:'base'};
  expect(reviewFreshness(pkg(),facts)).toBe('current');
  expect(reviewFreshness(pkg(),{...facts,base:'other'})).toBe('stale');
  expect(reviewFreshness(pkg(),{...facts,number:2})).toBe('stale');
  expect(reviewFreshness(pkg(),{...facts,base:undefined})).toBe('unknown');
});
