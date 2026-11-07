import { expect, it } from 'vitest';
import type { PRReadinessData } from '@bindings/internal/protocol';
import { reviewConfidence, reviewOverview, reviewItems, plainReviewText } from './reviewoverview';
const pkg = {head:'abc',repo:'acme/app',pr:42};
const facts = {head:'abc',repo:'acme/app',number:42,checks:'pass',review:'APPROVED',mergeable:'MERGEABLE',threads:[]} as unknown as PRReadinessData;

it('scores only independently observed signals for the exact reviewed revision',()=>{
  expect(reviewConfidence(pkg,facts).score).toBe(100);
  expect(reviewConfidence(pkg,{...facts,review:'',checks:'none'}).score).toBe(60);
  const failed=reviewConfidence(pkg,{...facts,checks:'fail',mergeable:'CONFLICTING'});
  expect(failed.score).toBe(60);
  expect(failed.signals.filter(s=>s.failed)).toHaveLength(2);
  expect(reviewConfidence(pkg,{...facts,threads:null}).score).toBe(80);
  expect(reviewConfidence(pkg).score).toBeNull();
  for(const changed of [{head:'other'},{repo:'other/app'},{number:43},{head:''}]) {
    const result=reviewConfidence(pkg,{...facts,...changed});
    expect(result.score).toBeNull();
    expect(result.signals.some(s=>s.confirmed)).toBe(false);
  }
});

it('turns legacy agent write-ups into compact feature rows without mixing unverified risks into verification',()=>{
  const result=reviewOverview({title:'feat(auth): verify email',body:'## What changed\n- Users confirm their address before entering. `User` implements the mail flow.\n- Existing accounts keep access.\n## How it was verified\n- `php artisan test`: 357 passed.\n  - Nine verification scenarios.\n## Remaining risks and unverified behavior\n- Email delivery was not checked in production.\n## Details\nKept verbatim.'});
  expect(result.title).toBe('Verify email');
  expect(plainReviewText('Use `email_verified_at` and **User**.')).toBe('Use email_verified_at and User.');
  expect(result.summary).toBe('Users confirm their address before entering.');
  expect(result.changes).toHaveLength(2);
  expect(result.changes[0].body).toContain('`User` implements');
  expect(result.verification).toHaveLength(1);
  expect(result.verification[0].body).toContain('Nine verification scenarios');
  expect(result.risks).toHaveLength(1);
  expect(result.details[0].body).toBe('Kept verbatim.');
});

it('preserves long summaries, nested bullets and fenced examples behind compact labels',()=>{
  const summary='First sentence. '+ 'More detail. '.repeat(30);
  const result=reviewOverview({title:'Review',body:'## Summary\n'+summary});
  expect(result.summary).toBe('First sentence.');
  expect(result.details[0].body).toBe(summary.trim());
  const items=reviewItems('- Parent item\n  - Nested detail\n```md\n- Not a feature\n```\n- Next item');
  expect(items).toHaveLength(2);
  expect(items[0].body).toContain('- Not a feature');
  expect(reviewItems('x'.repeat(400))[0].title.length).toBeLessThanOrEqual(140);
});
