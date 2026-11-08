import { expect, it } from 'vitest';
import type { PRReadinessData } from '@bindings/internal/protocol';
import { reviewConfidence, reviewOverview, reviewItems, plainReviewText, compactReviewLabel } from './reviewoverview';
const pkg = {head:'abc',base:'def',repo:'acme/app',pr:42};
const facts = {head:'abc',base:'def',checksHead:'abc',repo:'acme/app',number:42,checks:'pass',review:'APPROVED',mergeable:'MERGEABLE',threads:[]} as unknown as PRReadinessData;

it('invalidates changed bases and refuses CI credit without the exact checked commit',()=>{
  expect(reviewConfidence(pkg,{...facts,base:'changed'}).score).toBeNull();
  expect(reviewConfidence(pkg,{...facts,base:undefined}).score).toBeNull();
  for (const checksHead of [undefined,'old']) {
    const result=reviewConfidence(pkg,{...facts,checksHead});
    expect(result.score).toBe(80);
    expect(result.signals[1].value).toBe('Revision unconfirmed');
  }
});

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

it('uses short author labels and preserves legacy detail behind outcome summaries',()=>{
  expect(compactReviewLabel('- **Keep existing account access** — Migration 2026_11_07_200000 backfills data.')).toBe('Keep existing account access');
  const body='The migration 2026_11_07_200000_mark_existing_users_as_verified backfills email_verified_at from created_at, so existing accounts keep access.';
  expect(compactReviewLabel(body)).toBe('Existing accounts keep access');
  expect(compactReviewLabel("The whole workspace route group now uses `['auth','verified']`.")).toBe('Workspace route group');
  expect(compactReviewLabel('Registration now lands on a new step at `/email/verify`.')).toBe('Registration lands on new step');
  expect(reviewOverview({title:'Update',body:'## Changes\n- '+body}).changes[0].body).toContain('2026_11_07');
});
it('orders explicit risk estimates without inferring ratings from alarming prose',()=>{
  const result=reviewOverview({title:'Review',body:'## Risks\n- **Minor styling** — Likelihood: low; Impact: low. Basis: cosmetic only.\n- **Unassessed security issue** — Catastrophic critical failure is possible.\n- **Mail delivery** — Likelihood: medium; Impact: high. Outcome: users cannot sign in.\n- **Deployment lockout** — Likelihood: high; Impact: high.'});
  expect(result.risks.map(r=>r.title)).toEqual(['Deployment lockout','Users cannot sign in','Unassessed security issue','Minor styling']);
  expect(result.risks[2]).toMatchObject({likelihood:'unknown',impact:'unknown'});
  expect(result.risks[1].body).toContain('users cannot sign in');
  expect(reviewOverview({title:'Review',body:'## Risks\nNo known risks.'})).toMatchObject({riskReportProvided:true,risks:[]});
  expect(reviewOverview({title:'Review',body:''}).riskReportProvided).toBe(false);
});

it('reads new behavior from legacy after fields and uses consequences as risk headings',()=>{
  const result=reviewOverview({title:'Email verification',body:'## Changes\n- **Require email confirmation before opening the workspace**\n  Before: New accounts entered immediately.\n  After: New accounts confirm their email first.\n  Details: Middleware protects the route group.\n## Risks\n- **Mail delivery**\n  Likelihood: unknown; Impact: high\n  Outcome: New users cannot finish registration.\n  Basis: Production email delivery has not been verified.\n- Existing accounts: Users could be locked out if backfill is skipped.'});
  expect(result.changes[0]).toMatchObject({title:'Require email confirmation before opening the workspace',description:'New accounts confirm their email first.'});
  expect(result.changes[0].body).toContain('Middleware');
  expect(result.risks[0].title).toBe('New users cannot finish registration');
  expect(result.risks[1].title).toBe('Users could be locked out if backfill is skipped');
  const legacy=reviewOverview({title:'Legacy',body:'## Changes\n- Add email verification.'});
  expect(legacy.changes[0]).toMatchObject({description:''});
});

it('extracts user-facing sub-features and a concise assessment step',()=>{
  const result=reviewOverview({title:'Review',body:'## Changes\n- **Confirm email before entering**\n  New: Enter the workspace after confirming your address.\n  Includes:\n    - See which address received the link.\n    - Request another confirmation link.\n  Details: User implements MustVerifyEmail.\n## Risks\n- **New users cannot finish registration**\n  Likelihood: unknown; Impact: high\n  Outcome: Mail delivery failure blocks registration.\n  Assess: Send a confirmation email using production mail settings.\n  Basis: Only test mailers were exercised.'});
  expect(result.changes[0]).toMatchObject({description:'Enter the workspace after confirming your address.',parts:['See which address received the link.','Request another confirmation link.']});
  expect(result.risks[0]).toMatchObject({description:'Mail delivery failure blocks registration.',assess:'Send a confirmation email using production mail settings.'});
});
