import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import './talk.testkit';
import { DaemonService } from '@bindings/desktop';
import ReviewOverview from './ReviewOverview.svelte';
import { setTouchSurface } from '$lib/touchsurface';
import { preview } from '$lib/preview.svelte';
const pkg={id:'p',session:'s',head:'abc',base:'def',repo:'acme/app',pr:42,title:'feat(auth): verify email',createdAt:'2026-11-07',body:'## Agent confidence\nScore: 80/100\nBasis: Tests passed; mail is unverified.\n## Summary\nConfirm your email before entering.\n## Changes\n- Add an email verification step.\n- Preserve access for existing accounts.\n## Verification\n- Unit tests passed.\n## Risks\n- Mail delivery needs checking.',screens:[{title:'Registration',before:'before',after:'after'},{title:'Phone',after:'phone'}]} as never;
const facts={head:'abc',base:'def',checksHead:'abc',repo:'acme/app',number:42,checks:'pass',review:'',mergeable:'MERGEABLE',threads:[]} as never;
beforeEach(()=>{vi.mocked(DaemonService.ReviewPackageImage).mockResolvedValue({dataURL:'data:image/png;base64,aGVsbG8='});});
afterEach(cleanup);
it('shows an evidence score, compact rows, and actionable screenshot previews',async()=>{
  const onScreen=vi.fn(),onAssessRisks=vi.fn();
  render(ReviewOverview,{pkg,facts,sessionId:'s',freshness:'current',onScreen,onAssessRisks});
  expect(screen.getByRole('heading',{name:'Verify email'})).toBeInTheDocument();
  expect(screen.getByText('Confirm your email before entering.')).toBeInTheDocument();
  expect(screen.getByText('94',{exact:false,selector:'.score'})).toBeInTheDocument();
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
  expect(screen.getByText('Not verified',{exact:true})).toBeInTheDocument();
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
  expect(screen.getByRole('heading',{name:'Next step'})).toBeInTheDocument();
  expect(screen.getByText('Check production email delivery.',{selector:'.risk-detail p'})).toBeInTheDocument();
});

it('keeps the breakdown behind the info button and supports keyboard and outside dismissal',async()=>{
  render(ReviewOverview,{pkg,facts,sessionId:'s',freshness:'current',onScreen:()=>{}});
  const info=screen.getByRole('button',{name:'About confidence'});
  expect(screen.queryByText('Observed results')).toBeNull();
  expect(info).toHaveAttribute('aria-expanded','false');
  await fireEvent.click(info);
  expect(screen.getByRole('dialog',{name:'About confidence'})).toHaveFocus();
  expect(screen.getByText('Observed results')).toBeVisible();
  expect(screen.getByText('Tests passed; mail is unverified.')).toBeVisible();
  await fireEvent.keyDown(screen.getByRole('dialog',{name:'About confidence'}),{key:'Escape'});
  expect(screen.queryByRole('dialog',{name:'About confidence'})).toBeNull();
  expect(info).toHaveFocus();
  await fireEvent.click(info);
  await fireEvent.pointerDown(document.body);
  expect(screen.queryByRole('dialog',{name:'About confidence'})).toBeNull();
});

it('on a phone, only changes with supporting parts expand and risk ratings are labelled inline', async () => {
  setTouchSurface({});
  try {
    const body = '## Changes\n- **Update reminders**\n  New: Customers see the due date.\n  Includes:\n  - Dark mode\n- **Remove the old template**\n  New: Reminders share a layout.\n## Risks\n- **Long names**\n  Outcome: Names may wrap.\n  Likelihood: low; Impact: medium.\n  Assess: Send a preview.';
    const { container } = render(ReviewOverview, { pkg: { ...pkg as object, body } as never, facts, sessionId: 's', freshness: 'current', onScreen: () => {} });
    const change = screen.getByText('Update reminders').closest('summary')!;
    expect(change).toBeInTheDocument();
    expect(screen.getByText('Remove the old template').closest('details')).toBeNull();
    expect(container.querySelector('.feature-icon')).toBeNull();
    await fireEvent.click(change);
    expect(screen.getByText('Dark mode')).toBeVisible();
    expect(screen.getByText('Likelihood')).toBeVisible();
    expect(screen.getByText('Impact')).toBeVisible();
    expect(screen.getByText('Send a preview.')).toBeVisible();
  } finally {
    setTouchSurface(null);
  }
});
it('offers the live pages the agent named and opens one in the preview',async()=>{
  render(ReviewOverview,{pkg:{...pkg as object,pages:[{title:'Settings page',url:'http://localhost:3000/settings',caption:'Spacing fix'}]} as never,facts,sessionId:'s',freshness:'current',onScreen:()=>{}});
  expect(screen.getByText('Spacing fix')).toBeInTheDocument();
  await fireEvent.click(screen.getByRole('button',{name:'Open live page: Settings page'}));
  expect(preview.request).toEqual({sessionId:'s',url:'http://localhost:3000/settings'});
  preview.close();
});
it('opens every live page as a tab in one click', async () => {
  const pages = [{title:'Home',url:'http://localhost:3000/'},{title:'Settings',url:'http://localhost:3000/settings'}];
  render(ReviewOverview,{pkg:{...pkg as object,pages} as never,facts,sessionId:'s',freshness:'current',onScreen:()=>{}});
  await fireEvent.click(screen.getByRole('button',{name:/Open all as tabs/}));
  expect(preview.tabs.map(t=>t.title)).toEqual(['Home','Settings']);
  preview.close();
});
it('on a phone lists only pages whose server is forwarded and opens the forward', async () => {
  const { store } = await import('$lib/store.svelte');
  store.sessions = [{id:'s',devForwards:[{url:'http://192.168.1.5:40000',from:'127.0.0.1:3000'}]}] as never;
  const open = vi.spyOn(store,'openURL').mockResolvedValue(undefined);
  setTouchSurface({});
  try {
    const pages = [{title:'Home',url:'http://localhost:3000/home'},{title:'Other',url:'http://localhost:9999/'}];
    render(ReviewOverview,{pkg:{...pkg as object,pages} as never,facts,sessionId:'s',freshness:'current',onScreen:()=>{}});
    expect(screen.queryByRole('button',{name:'Open live page: Other'})).toBeNull();
    expect(screen.queryByRole('button',{name:/Open all as tabs/})).toBeNull();
    await fireEvent.click(screen.getByRole('button',{name:'Open live page: Home'}));
    expect(open).toHaveBeenCalledWith('http://192.168.1.5:40000/home');
  } finally { setTouchSurface(null); store.sessions = []; open.mockRestore(); }
});
