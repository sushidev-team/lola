import type { ReviewPackage } from '@bindings/internal/protocol';
import { reviewSections } from './reviewmaterial';

// Pair repeated titles by occurrence, never infer that different screens depict
// the same page. Image hashes compare retained bytes, not capture provenance.
function pair<T extends {title:string}>(before:T[], after:T[]) {
  const remaining = before.map((value,index) => ({value,index}));
  const rows: {before?:T;after?:T}[] = [];
  for (const value of after) {
    const index = remaining.findIndex(row => row.value.title === value.title);
    rows.push({before:index < 0 ? undefined : remaining.splice(index,1)[0].value,after:value});
  }
  rows.push(...remaining.map(row => ({before:row.value})));
  return rows;
}

export function reviewChanges(before:ReviewPackage, after:ReviewPackage) {
  const sections = pair(reviewSections(before.body),reviewSections(after.body));
  const screens = pair(before.screens ?? [],after.screens ?? []);
  const changed = <T>(row:{before?:T;after?:T}) => JSON.stringify(row.before) !== JSON.stringify(row.after);
  return {
    sections:sections.filter(changed), screens:screens.filter(changed),
    unchangedScreens:screens.filter(row => !changed(row)).length,
    headChanged:before.head !== after.head, baseChanged:before.base !== after.base,
    titleChanged:before.title !== after.title,
  };
}

export function reviewFreshness(pkg:ReviewPackage, facts?:{repo:string;number:number;head:string;base?:string}) {
  if (!facts?.head || !facts.base) return 'unknown';
  return pkg.repo === facts.repo && (!pkg.pr || pkg.pr === facts.number) && pkg.head === facts.head && pkg.base === facts.base ? 'current' : 'stale';
}
