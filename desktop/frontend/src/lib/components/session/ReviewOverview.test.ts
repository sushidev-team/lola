import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import './talk.testkit';
import { DaemonService } from '@bindings/desktop';
import ReviewOverview from './ReviewOverview.svelte';
const pkg={id:'p',session:'s',head:'abc',base:'def',repo:'acme/app',pr:42,title:'feat(auth): verify email',createdAt:'2026-11-07',body:'## Summary\nConfirm your email before entering.\n## Changes\n- Add an email verification step.\n- Preserve access for existing accounts.\n## Verification\n- Unit tests passed.\n## Risks\n- Mail delivery needs checking.',screens:[{title:'Registration',before:'before',after:'after'},{title:'Phone',after:'phone'}]} as never;
const facts={head:'abc',repo:'acme/app',number:42,checks:'pass',review:'',mergeable:'MERGEABLE',threads:[]} as never;
beforeEach(()=>{vi.mocked(DaemonService.ReviewPackageImage).mockResolvedValue({dataURL:'data:image/png;base64,aGVsbG8='});});
afterEach(cleanup);
it('shows an evidence score, compact rows, and actionable screenshot previews',async()=>{
  const onScreen=vi.fn(),onAssessRisks=vi.fn();
  render(ReviewOverview,{pkg,facts,sessionId:'s',freshness:'current',onScreen,onAssessRisks});
  expect(screen.getByRole('heading',{name:'Verify email'})).toBeInTheDocument();
  expect(screen.getByText('Confirm your email before entering.')).toBeInTheDocument();
  expect(screen.getByRole('img',{name:'Evidence coverage: 80 out of 100'})).toBeInTheDocument();
  expect(screen.getByText('Add email verification step',{selector:'summary span'}).closest('details')).not.toHaveAttribute('open');
  await fireEvent.click(screen.getByRole('button',{name:'Review screenshot: Phone'}));
  expect(onScreen).toHaveBeenCalledWith(1);
  expect(screen.queryByRole('heading',{name:'Verification'})).toBeNull();
  expect(screen.getAllByText('Not rated')).toHaveLength(2);
  for (const rating of screen.getAllByText('Not rated')) expect(rating).toBeVisible();
  await fireEvent.click(screen.getByRole('button',{name:'Ask agent to assess risks'}));
  expect(onAssessRisks).toHaveBeenCalledOnce();
});
it('does not score a stale revision and keeps previews actionable if images fail',async()=>{
  vi.mocked(DaemonService.ReviewPackageImage).mockRejectedValue(new Error('Missing image'));
  render(ReviewOverview,{pkg,facts:{...facts as object,head:'different'} as never,sessionId:'s',freshness:'stale',onScreen:()=>{},onAssessRisks:()=>{}});
  expect(screen.getByText('Outdated material')).toBeInTheDocument();
  expect(screen.getByRole('img',{name:'Confidence not scored: revision unconfirmed'})).toBeInTheDocument();
  await screen.findAllByText('Open screenshot');
  expect(screen.getByRole('button',{name:'Review screenshot: Phone'})).toBeEnabled();
});

it('shows feature descriptions with supporting details behind a disclosure',()=>{
  render(ReviewOverview,{pkg:{...pkg as object,body:'## Changes\n- **Confirm email before entering**\n  Before: Immediate workspace access.\n  After: Confirm email first.\n## Risks\n- **Mail delivery** — Outcome: New users cannot finish registration. Likelihood: unknown; Impact: high.'} as never,facts,sessionId:'s',freshness:'current',onScreen:()=>{}});
  expect(screen.queryByText('Before',{exact:true})).toBeNull();
  expect(screen.queryByText('After',{exact:true})).toBeNull();
  expect(screen.getByText('Confirm email first.')).toBeVisible();
  expect(screen.getByText('New users cannot finish registration',{selector:'.risk-title'})).toBeVisible();
});

it('expands sub-features and risk descriptions without showing raw implementation prose',async()=>{
  render(ReviewOverview,{pkg:{...pkg as object,body:'## Changes\n- **Confirm email before entering**\n  New: Confirm your address to open the workspace.\n  Includes:\n    - Request another link.\n    - See the destination address.\n  Details: User implements MustVerifyEmail.\n## Risks\n- **Mail delivery**\n  Outcome: New users cannot finish registration.\n  Assess: Check production email delivery.\n  Likelihood: unknown; Impact: high'} as never,facts,sessionId:'s',freshness:'current',onScreen:()=>{}});
  const feature=screen.getByText('Confirm email before entering',{selector:'.feature-heading > span'}).closest('summary')!;
  await fireEvent.click(feature);
  expect(screen.getByText('Request another link.',{selector:'.feature-parts span'})).toBeInTheDocument();
  expect(screen.queryByText('Source notes')).toBeNull();
  expect(screen.queryByText(/MustVerifyEmail/)).toBeNull();
  await fireEvent.click(screen.getByText('New users cannot finish registration',{selector:'.risk-title'}).closest('summary')!);
  expect(screen.getByRole('heading',{name:'How to assess it'})).toBeInTheDocument();
  expect(screen.getByText('Check production email delivery.',{selector:'.risk-detail p'})).toBeInTheDocument();
});
