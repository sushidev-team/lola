import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { freshSession, store } from './talk.testkit';
import { DaemonService } from '@bindings/desktop';
import { feedback } from '$lib/feedback.svelte';
import { nav } from '$lib/nav.svelte';
import ReviewWorkspace from './ReviewWorkspace.svelte';
import ReviewScreenshot from './ReviewScreenshot.svelte';

const read = vi.mocked(DaemonService.ReviewPackages);
const image = vi.mocked(DaemonService.ReviewPackageImage);
const facts = vi.mocked(DaemonService.PRReadiness);
const send = vi.mocked(DaemonService.SendFeedback);
const head = 'b'.repeat(40);
const pkg = (over={}) => ({version:1,id:'c'.repeat(64),session:'s',repo:'acme/app',pr:42,title:'A clearer settings screen',body:'## Changes\nSpacing now follows the form rhythm.',base:'a'.repeat(40),head,createdAt:'2026-11-07T10:00:00Z',screens:[{title:'Settings',after:'d'.repeat(64),caption:'Aligned labels',viewport:'1440 × 900'}],artifacts:[],...over});
const status = (over={}) => ({repo:'acme/app',number:42,url:'https://github.com/acme/app/pull/42',state:'OPEN',draft:false,mergeable:'MERGEABLE',mergeState:'CLEAN',review:'APPROVED',checks:'pass',head,threads:[],mergeMethod:'squash',mergeBlocked:'',...over});
let id:string;
beforeEach(() => {
  localStorage.clear(); nav.openOverlay('pr-review');
  read.mockReset();image.mockReset();facts.mockReset();send.mockReset();
  read.mockResolvedValue({packages:[pkg()]});image.mockResolvedValue({dataURL:'data:image/png;base64,aGVsbG8='});facts.mockResolvedValue(status());
  id=freshSession({prNumber:42,prUrl:'https://github.com/acme/app/pull/42'});
  vi.spyOn(store,'refresh').mockResolvedValue(undefined);
});
afterEach(() => {cleanup();feedback.clear(id);nav.closeOverlay();vi.restoreAllMocks();});

it('shows retained material, missing baseline, and truthful revision freshness',async()=>{
  render(ReviewWorkspace,{sessionId:id});
  await screen.findByRole('heading',{name:'A clearer settings screen'});
  expect(screen.getByText(/Package head matches/)).toBeInTheDocument();
  expect(screen.getByText('Before capture unavailable')).toBeInTheDocument();
  expect(await screen.findByAltText('Settings — After')).toBeInTheDocument();
  await fireEvent.click(screen.getByRole('button',{name:'Full size'}));
  expect(screen.getByRole('button',{name:'Fit image'})).toHaveAttribute('aria-pressed','true');
  facts.mockResolvedValue(status({head:'e'.repeat(40)}));
  await fireEvent.click(screen.getByRole('button',{name:'Refresh review'}));
  await screen.findByText(/Outdated material/);
});

it('batches revision and screenshot context, preserves failed sends, then clears accepted feedback',async()=>{
  render(ReviewWorkspace,{sessionId:id});await screen.findByText('Aligned labels');
  await fireEvent.click(screen.getByRole('button',{name:'Comment on screen'}));
  await fireEvent.input(screen.getByRole('textbox',{name:'Review comment'}),{target:{value:'Move the save button closer.'}});
  await fireEvent.click(screen.getByRole('button',{name:'Add to feedback'}));
  const note=feedback.noteFor(id);
  expect(note).toContain(`Review package ${pkg().id}`);expect(note).toContain(`Head: ${head}`);expect(note).toContain('Section: Screen 1: Settings');
  send.mockRejectedValueOnce(new Error('offline'));
  await fireEvent.click(screen.getByRole('button',{name:'Send feedback to agent'}));
  await screen.findByText(/Feedback was not accepted/);expect(feedback.noteFor(id)).toBe(note);
  send.mockResolvedValue({delivered:false,queued:true} as never);
  await fireEvent.click(screen.getByRole('button',{name:'Send feedback to agent'}));
  await screen.findByText(/Feedback accepted/);expect(feedback.noteFor(id)).toBe('');
  expect(send).toHaveBeenLastCalledWith({session:id,comments:[],note});
});

it('keeps unfinished material comments across closing and reopening',async()=>{
  const mounted=render(ReviewWorkspace,{sessionId:id});await screen.findByText('Aligned labels');
  await fireEvent.click(screen.getByRole('button',{name:'Comment on screen'}));
  await fireEvent.input(screen.getByRole('textbox',{name:'Review comment'}),{target:{value:'Still reviewing this'}});
  mounted.unmount();render(ReviewWorkspace,{sessionId:id});await screen.findByText('Aligned labels');
  expect(screen.getByRole('textbox',{name:'Review comment'})).toHaveValue('Still reviewing this');
  expect(screen.getByLabelText('Comment on')).toHaveValue('Screen 1: Settings');
});

it('keeps the inspected revision when a newer package arrives',async()=>{
  render(ReviewWorkspace,{sessionId:id});await screen.findByText('Aligned labels');
  read.mockResolvedValue({packages:[pkg({id:'f'.repeat(64),title:'Corrected layout'}),pkg()]});
  await fireEvent.click(screen.getByRole('button',{name:'Refresh review'}));
  await screen.findByText(/A newer review package is available/);
  expect(screen.getByRole('heading',{name:'A clearer settings screen'})).toBeInTheDocument();
  await fireEvent.click(screen.getByRole('button',{name:'View latest package'}));
  await screen.findByRole('heading',{name:'Corrected layout'});
});

it('does not call unknown freshness current, and allows retry on old daemon errors',async()=>{
  read.mockRejectedValueOnce(new Error('unknown cmd "reviewPackages"'));facts.mockRejectedValue(new Error('offline'));
  render(ReviewWorkspace,{sessionId:id});await screen.findByRole('alert');
  await fireEvent.click(screen.getByRole('button',{name:'Refresh review'}));
  await screen.findByText(/Freshness unknown/);
  expect(screen.queryByText(/Package head matches/)).toBeNull();
});

it('drops late package reads after switching sessions',async()=>{
  let release!:(value:{packages:ReturnType<typeof pkg>[]})=>void;
  read.mockImplementationOnce(()=>new Promise(resolve=>release=resolve) as never);
  const mounted=render(ReviewWorkspace,{sessionId:id});
  await waitFor(()=>expect(read).toHaveBeenCalled());
  read.mockResolvedValue({packages:[pkg({title:'Other session'})]});
  await mounted.rerender({sessionId:'other'});
  await screen.findByRole('heading',{name:'Other session'});
  release({packages:[pkg()]});
  await waitFor(()=>expect(screen.queryByRole('heading',{name:'A clearer settings screen'})).toBeNull());
});

it('pins stack readiness and material to the selected member',async()=>{
  freshSession({id,stack:true,focusApp:'api',apps:[{name:'api',label:'API',number:42,url:''},{name:'web',label:'Web',number:43,url:''}]});
  render(ReviewWorkspace,{sessionId:id});await screen.findByText('Aligned labels');
  expect(read).toHaveBeenCalledWith(`${id}~api`);
  await fireEvent.change(screen.getByLabelText('Review repository'),{target:{value:'web'}});
  await waitFor(()=>expect(read).toHaveBeenLastCalledWith(`${id}~web`));
  await waitFor(()=>expect(facts).toHaveBeenLastCalledWith(`${id}~web`,43));
});

it('does not render a broken image or accept non-image URLs',async()=>{
  image.mockResolvedValueOnce({dataURL:'https://example.com/leak'});
  render(ReviewScreenshot,{sessionId:id,packageId:'p',artifact:'a',label:'After'});
  await screen.findByText(/Could not load screenshot/);expect(screen.queryByRole('img')).toBeNull();
  await fireEvent.click(screen.getByRole('button',{name:'Retry image'}));
  expect(await screen.findByAltText('After')).toBeInTheDocument();
});

it('keeps retained material readable after the session is removed without offering live actions',async()=>{
  store.sessions=[];
  render(ReviewWorkspace,{sessionId:id});
  await screen.findByRole('heading',{name:'A clearer settings screen'});
  expect(await screen.findByAltText('Settings — After')).toBeInTheDocument();
  expect(screen.queryByRole('tab',{name:'Code changes'})).toBeNull();
  expect(screen.queryByRole('button',{name:'Add to feedback'})).toBeNull();
  expect(screen.getByText(/This session has ended/)).toBeInTheDocument();
});
