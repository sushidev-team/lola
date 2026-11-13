import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { freshSession, store, talkSvc } from './talk.testkit';
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
const pkg = (over={}) => ({version:1,id:'c'.repeat(64),session:id,repo:'acme/app',pr:42,title:'A clearer settings screen',body:'## Changes\nSpacing now follows the form rhythm.',base:'a'.repeat(40),head,createdAt:'2026-11-07T10:00:00Z',screens:[{title:'Settings',after:'d'.repeat(64),caption:'Aligned labels',viewport:'1440 × 900'}],artifacts:[],...over});
const status = (over={}) => ({repo:'acme/app',number:42,url:'https://github.com/acme/app/pull/42',state:'OPEN',draft:false,mergeable:'MERGEABLE',mergeState:'CLEAN',review:'APPROVED',checks:'pass',head,base:'a'.repeat(40),checksHead:head,threads:[],mergeMethod:'squash',mergeBlocked:'',...over});
let id:string;
beforeEach(() => {
  vi.stubGlobal('ResizeObserver',class {observe(){} disconnect(){}});
  localStorage.clear(); nav.openOverlay('pr-review');
  read.mockReset();image.mockReset();facts.mockReset();send.mockReset();
  vi.mocked(DaemonService.ReviewPackageChecks).mockReset().mockResolvedValue({checks:[]});
  vi.mocked(DaemonService.ReviewPackageMark).mockReset();
  talkSvc.Say.mockReset();
  id=freshSession({prNumber:42,prUrl:'https://github.com/acme/app/pull/42'});
  read.mockResolvedValue({packages:[pkg()],reviewTracking:true});image.mockResolvedValue({dataURL:'data:image/png;base64,aGVsbG8='});facts.mockResolvedValue(status());
  vi.spyOn(store,'refresh').mockResolvedValue(undefined);
});
afterEach(() => {cleanup();feedback.clear(id);nav.closeOverlay();vi.restoreAllMocks();vi.unstubAllGlobals();});
async function openScreens() { await fireEvent.click(await screen.findByRole('tab',{name:'Screenshots'})); }

it('shows material before GitHub completes and shares the pending status request with PR actions',async()=>{
  let finish!:(value:ReturnType<typeof status>)=>void;
  facts.mockImplementationOnce(()=>new Promise(resolve=>finish=resolve) as never);
  render(ReviewWorkspace,{sessionId:id});
  await screen.findByRole('heading',{name:'A clearer settings screen'});
  expect(screen.queryByRole('status',{name:'Loading review'})).toBeNull();
  expect(screen.getByRole('status',{name:'Loading confidence'})).toBeInTheDocument();
  expect(screen.getByRole('status',{name:'Loading PR status'})).toBeInTheDocument();
  expect(screen.queryByText('Not verified',{exact:true})).toBeNull();
  expect(screen.queryByRole('button',{name:'Squash and merge'})).toBeNull();
  expect(facts).toHaveBeenCalledTimes(1);
  vi.mocked(DaemonService.ReviewPackageMark).mockResolvedValue({package:pkg().id,at:'2026-11-08T12:00:00Z'});
  await fireEvent.click(screen.getByRole('button',{name:'Mark reviewed'}));
  await screen.findByRole('button',{name:'Reviewed'});
  finish(status());
  await screen.findByText('Awaiting self-assessment');
  expect(await screen.findByRole('button',{name:'Squash and merge'})).toBeEnabled();
  expect(screen.queryByRole('status',{name:'Loading confidence'})).toBeNull();
  expect(screen.queryByRole('status',{name:'Loading PR status'})).toBeNull();
});

it('shows a skeleton while material loads without flashing the empty state',async()=>{
  let finish!:(value:{packages:ReturnType<typeof pkg>[]})=>void;
  read.mockImplementationOnce(()=>new Promise(resolve=>finish=resolve) as never);
  render(ReviewWorkspace,{sessionId:id});
  expect(await screen.findByRole('status',{name:'Loading review'})).toBeInTheDocument();
  expect(screen.queryByText('No review material yet')).toBeNull();
  finish({packages:[pkg()]});
  await screen.findByRole('heading',{name:'A clearer settings screen'});
  expect(screen.queryByRole('status',{name:'Loading review'})).toBeNull();
});

it('shows material errors without waiting for GitHub and removes the skeleton',async()=>{
  let finish!:(value:ReturnType<typeof status>)=>void;
  facts.mockImplementationOnce(()=>new Promise(resolve=>finish=resolve) as never);
  read.mockRejectedValueOnce(new Error('Material unavailable'));
  render(ReviewWorkspace,{sessionId:id});
  expect(await screen.findByRole('alert')).toHaveTextContent('Material unavailable');
  expect(screen.queryByRole('status',{name:'Loading review'})).toBeNull();
  finish(status());
});

it('marks only the inspected package, retains the baseline on refresh, and compares updated material',async()=>{
  const mark=vi.mocked(DaemonService.ReviewPackageMark);
  const reviewed={package:pkg().id,at:'2026-11-08T12:00:00Z'};
  mark.mockResolvedValue(reviewed);
  render(ReviewWorkspace,{sessionId:id});
  await fireEvent.click(await screen.findByRole('button',{name:'Mark reviewed'}));
  expect(mark).toHaveBeenCalledWith(id,pkg().id,'');
  expect(await screen.findByRole('button',{name:'Reviewed'})).toBeDisabled();
  const next=pkg({id:'new-package',body:'## Changes\nCorrected spacing.\n## Risks\nNew risk.'});
  read.mockResolvedValue({packages:[next,pkg()],reviewed:{[id]:reviewed},reviewTracking:true});
  await fireEvent.click(screen.getByRole('button',{name:'Refresh review'}));
  await fireEvent.click(await screen.findByRole('button',{name:'View latest package'}));
  await fireEvent.click(await screen.findByRole('button',{name:'See changes since review'}));
  expect(await screen.findByRole('heading',{name:'Since your last review'})).toBeInTheDocument();
  expect(screen.getByText('Same code revision; the published review material changed.')).toBeInTheDocument();
  expect(screen.getByText('Corrected spacing.')).toBeInTheDocument();
  expect(mark).toHaveBeenCalledTimes(1);expect(send).not.toHaveBeenCalled();
});

it('keeps the inspected package unreviewed when saving fails and supports retry',async()=>{
  const mark=vi.mocked(DaemonService.ReviewPackageMark);
  mark.mockRejectedValueOnce(new Error('disk full'));
  render(ReviewWorkspace,{sessionId:id});
  await fireEvent.click(await screen.findByRole('button',{name:'Mark reviewed'}));
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not save your review');
  mark.mockResolvedValueOnce({package:pkg().id,at:'2026-11-08T12:00:00Z'});
  await fireEvent.click(screen.getByRole('button',{name:'Mark reviewed'}));
  expect(await screen.findByRole('button',{name:'Reviewed'})).toBeDisabled();
});

it('does not offer review tracking on older daemons and anchors comments to the selected section',async()=>{
  read.mockResolvedValue({packages:[pkg({body:'## Changes\nSpacing.\n## Risks\nSmall screens.'})]});
  render(ReviewWorkspace,{sessionId:id});
  await screen.findByRole('heading',{name:'A clearer settings screen'});
  expect(screen.queryByRole('button',{name:'Mark reviewed'})).toBeNull();
  await fireEvent.change(screen.getByLabelText('Comment section'),{target:{value:'Section 2: Risks'}});
  await fireEvent.input(screen.getByRole('textbox',{name:'Review comment'}),{target:{value:'Check the small screen.'}});
  await fireEvent.click(screen.getByRole('button',{name:'Add to feedback'}));
  expect(feedback.noteFor(id)).toContain('Section: Section 2: Risks');
  expect(feedback.noteFor(id)).toContain(`Review package ${pkg().id}`);
});

it('does not carry a delayed review write into another session',async()=>{
  let finish!:(value:{package:string;at:string})=>void;
  vi.mocked(DaemonService.ReviewPackageMark).mockImplementationOnce(()=>new Promise(resolve=>finish=resolve) as never);
  const mounted=render(ReviewWorkspace,{sessionId:id});
  await fireEvent.click(await screen.findByRole('button',{name:'Mark reviewed'}));
  await mounted.rerender({sessionId:'other'});
  await waitFor(()=>expect(read).toHaveBeenLastCalledWith('other'));
  finish({package:pkg().id,at:'2026-11-08T12:00:00Z'});
  expect(await screen.findByRole('button',{name:'Mark reviewed'})).toBeEnabled();
});

it('keeps retained stack-member review baselines separate after session cleanup',async()=>{
  store.sessions=[];
  const web=pkg({session:`${id}~web`,id:'web-current'});
  const old=pkg({session:`${id}~web`,id:'web-reviewed',body:'Older web write-up'});
  const api=pkg({session:`${id}~api`,id:'api-current',repo:'acme/api',pr:43});
  read.mockResolvedValue({packages:[web,api,old],reviewTracking:true,reviewed:{[web.session]:{package:old.id,at:'2026-11-08T12:00:00Z'}}});
  vi.mocked(DaemonService.ReviewPackageMark).mockResolvedValue({package:web.id,at:'2026-11-08T13:00:00Z'});
  render(ReviewWorkspace,{sessionId:id});
  await screen.findByRole('tab',{name:'Since review'});
  await fireEvent.change(screen.getByLabelText('Review version'),{target:{value:api.id}});
  expect(screen.queryByRole('tab',{name:'Since review'})).toBeNull();
  await fireEvent.change(screen.getByLabelText('Review version'),{target:{value:web.id}});
  await fireEvent.click(screen.getByRole('button',{name:'Mark reviewed'}));
  expect(DaemonService.ReviewPackageMark).toHaveBeenCalledWith(web.session,web.id,old.id);
});

it('requests review material through the conversation without sending draft feedback or duplicating clicks',async()=>{
  read.mockResolvedValue({packages:[]});
  feedback.setNote(id,'Keep this unfinished comment');
  let finish!:(value:unknown)=>void;
  talkSvc.Say.mockImplementationOnce(()=>new Promise(resolve=>finish=resolve));
  render(ReviewWorkspace,{sessionId:id});
  const button=await screen.findByRole('button',{name:'Ask agent to prepare review'});
  await fireEvent.click(button);
  expect(button).toBeDisabled();
  await fireEvent.click(button);
  expect(talkSvc.Say).toHaveBeenCalledTimes(1);
  expect(talkSvc.Say).toHaveBeenCalledWith({session:id,text:expect.stringContaining('lola review-package publish --help')});
  finish({item:'h',delivered:true});
  await screen.findByText(/Request sent/);
  expect(screen.getByRole('button',{name:'Review requested'})).toBeDisabled();
  expect(feedback.noteFor(id)).toBe('Keep this unfinished comment');
  expect(send).not.toHaveBeenCalled();
});

it('allows retry on request failure and explains queued requests',async()=>{
  read.mockResolvedValue({packages:[]});
  talkSvc.Say.mockRejectedValueOnce(new Error('offline'));
  render(ReviewWorkspace,{sessionId:id});
  await fireEvent.click(await screen.findByRole('button',{name:'Ask agent to prepare review'}));
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not send the request');
  talkSvc.Say.mockResolvedValueOnce({item:'h',delivered:false,why:'Answer the permission prompt first.'});
  await fireEvent.click(screen.getByRole('button',{name:'Retry request'}));
  await screen.findByText('Request queued. Answer the permission prompt first.');
});

it('keeps request results scoped to the selected stack member',async()=>{
  read.mockResolvedValue({packages:[]});
  freshSession({id,stack:true,focusApp:'api',apps:[{name:'api',label:'API',number:42},{name:'web',label:'Web',number:43}]});
  let finish!:(value:unknown)=>void;
  talkSvc.Say.mockImplementationOnce(()=>new Promise(resolve=>finish=resolve));
  render(ReviewWorkspace,{sessionId:id});
  await fireEvent.click(await screen.findByRole('button',{name:'Ask agent to prepare review'}));
  expect(talkSvc.Say).toHaveBeenCalledWith({session:id,text:expect.stringContaining('--app "api"')});
  await fireEvent.change(screen.getByLabelText('Review repository'),{target:{value:'web'}});
  finish({item:'h',delivered:true});
  expect(await screen.findByRole('button',{name:'Ask agent to prepare review'})).toBeEnabled();
  expect(screen.queryByText(/Request sent/)).toBeNull();
});

it('does not offer a review request for an ended session',async()=>{
  read.mockResolvedValue({packages:[]});store.sessions=[];
  render(ReviewWorkspace,{sessionId:id});
  await screen.findByText('This session ended without publishing review material.');
  expect(screen.queryByRole('button',{name:'Ask agent to prepare review'})).toBeNull();
});

it('shows retained material, missing baseline, and truthful revision freshness',async()=>{
  render(ReviewWorkspace,{sessionId:id});
  await screen.findByRole('heading',{name:'A clearer settings screen'});
  expect(screen.queryByText('Matches current PR',{selector:'[role=status]'})).toBeNull();
  await openScreens();
  expect(screen.getByRole('button',{name:'Before'})).toBeDisabled();
  expect(await screen.findByAltText('Settings — After')).toBeInTheDocument();
  await fireEvent.change(screen.getByLabelText('Screenshot zoom'),{target:{value:'150'}});
  expect(screen.getByLabelText('Screenshot zoom')).toHaveValue('150');
  facts.mockResolvedValue(status({head:'e'.repeat(40)}));
  await fireEvent.click(screen.getByRole('button',{name:'Refresh review'}));
  await fireEvent.click(screen.getByRole('tab',{name:'Overview'}));
  await screen.findByText(/Outdated material/);
});

it('batches revision and screenshot context, preserves failed sends, then clears accepted feedback',async()=>{
  render(ReviewWorkspace,{sessionId:id});await openScreens();
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  await fireEvent.input(screen.getByRole('textbox',{name:'Screenshot comment'}),{target:{value:'Move the save button closer.'}});
  await fireEvent.click(screen.getByRole('button',{name:'Save'}));
  const note=feedback.noteFor(id);
  expect(note).toContain(`Review package ${pkg().id}`);expect(note).toContain(`Head: ${head}`);expect(note).toContain('Section: Screen 1: Settings');
  await fireEvent.click(screen.getByRole('button',{name:/Checks & feedback/}));
  send.mockRejectedValueOnce(new Error('offline'));
  await fireEvent.click(screen.getByRole('button',{name:'Send feedback to agent'}));
  await screen.findByText(/Feedback was not accepted/);expect(feedback.noteFor(id)).toBe(note);
  send.mockResolvedValue({delivered:false,queued:true} as never);
  await fireEvent.click(screen.getByRole('button',{name:'Send feedback to agent'}));
  await screen.findByText(/Feedback accepted/);expect(feedback.noteFor(id)).toBe('');
  expect(send).toHaveBeenLastCalledWith({session:id,comments:[],note});
});

it('keeps unfinished material comments across closing and reopening',async()=>{
  const mounted=render(ReviewWorkspace,{sessionId:id});await openScreens();
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  await fireEvent.input(screen.getByRole('textbox',{name:'Screenshot comment'}),{target:{value:'Still reviewing this'}});
  mounted.unmount();render(ReviewWorkspace,{sessionId:id});await openScreens();
  await fireEvent.click(await screen.findByRole('button',{name:'Next comment'}));
  expect(screen.getByRole('textbox',{name:'Screenshot comment'})).toHaveValue('Still reviewing this');
});

it('retains saved annotations across reopening and updates their pending feedback instead of duplicating it',async()=>{
  const mounted=render(ReviewWorkspace,{sessionId:id});await openScreens();
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  await fireEvent.input(screen.getByRole('textbox',{name:'Screenshot comment'}),{target:{value:'Original annotation'}});
  await fireEvent.click(screen.getByRole('button',{name:'Save'}));
  expect(screen.queryByRole('textbox',{name:'Screenshot comment'})).toBeNull();
  expect(screen.getByRole('button',{name:'Comment 1'})).toBeInTheDocument();
  mounted.unmount();render(ReviewWorkspace,{sessionId:id});await openScreens();
  await fireEvent.click(await screen.findByRole('button',{name:'Next comment'}));
  expect(screen.getByRole('textbox',{name:'Screenshot comment'})).toHaveValue('Original annotation');
  await fireEvent.input(screen.getByRole('textbox',{name:'Screenshot comment'}),{target:{value:'Updated annotation'}});
  await fireEvent.click(screen.getByRole('button',{name:'Save'}));
  expect(feedback.noteFor(id)).toContain('Updated annotation');
  expect(feedback.noteFor(id)).not.toContain('Original annotation');
  expect(feedback.noteFor(id).match(/Annotation /g)).toHaveLength(1);
  await fireEvent.click(screen.getByRole('button',{name:/Checks & feedback/}));
  send.mockResolvedValue({delivered:false,queued:true} as never);
  await fireEvent.click(screen.getByRole('button',{name:'Send feedback to agent'}));
  await screen.findByText(/Feedback accepted/);
  expect(feedback.noteFor(id)).toBe('');
  await fireEvent.click(screen.getByRole('button',{name:'Comment 1'}));
  expect(screen.getByRole('textbox',{name:'Screenshot comment'})).toHaveValue('Updated annotation');
  await fireEvent.input(screen.getByRole('textbox',{name:'Screenshot comment'}),{target:{value:'Follow-up after sending'}});
  await fireEvent.click(screen.getByRole('button',{name:'Save'}));
  expect(feedback.noteFor(id)).toContain('Follow-up after sending');
  expect(feedback.noteFor(id).match(/Annotation /g)).toHaveLength(1);
});

it('keeps the inspected revision when a newer package arrives',async()=>{
  render(ReviewWorkspace,{sessionId:id});await openScreens();
  read.mockResolvedValue({packages:[pkg({id:'f'.repeat(64),title:'Corrected layout'}),pkg()]});
  await fireEvent.click(screen.getByRole('button',{name:'Refresh review'}));
  await fireEvent.click(screen.getByRole('tab',{name:'Overview'}));
  await screen.findByText(/A newer review package is available/);
  expect(screen.getByRole('heading',{name:'A clearer settings screen'})).toBeInTheDocument();
  await fireEvent.click(screen.getByRole('button',{name:'View latest package'}));
  await screen.findByRole('heading',{name:'Corrected layout'});
});

it('does not call unknown freshness current, and allows retry on old daemon errors',async()=>{
  read.mockRejectedValueOnce(new Error('unknown cmd "reviewPackages"'));facts.mockRejectedValue(new Error('offline'));
  render(ReviewWorkspace,{sessionId:id});await screen.findByRole('alert');
  await fireEvent.click(screen.getByRole('button',{name:'Refresh review'}));
  await screen.findByText('Not verified',{exact:true});
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
  render(ReviewWorkspace,{sessionId:id});await openScreens();
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
  await openScreens();
  expect(await screen.findByAltText('Settings — After')).toBeInTheDocument();
  expect(screen.queryByRole('tab',{name:'Code changes'})).toBeNull();
  expect(screen.queryByRole('button',{name:'Add to feedback'})).toBeNull();
  await fireEvent.click(screen.getByRole('button',{name:/Checks & feedback/}));
  expect(screen.getByText(/Session ended/)).toBeInTheDocument();
});


it('saves and sends material beyond the old shared 4000-character limit',async()=>{
  feedback.setNote(id,'x'.repeat(3900));
  render(ReviewWorkspace,{sessionId:id});await openScreens();
  await screen.findByAltText('Settings — After');
  for (const text of ['First screenshot comment','Second screenshot comment','Third screenshot comment']) {
    await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
    await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
    await fireEvent.input(screen.getByRole('textbox',{name:'Screenshot comment'}),{target:{value:text}});
    await fireEvent.click(screen.getByRole('button',{name:'Save'}));
    expect(screen.queryByRole('textbox',{name:'Screenshot comment'})).toBeNull();
    expect(feedback.noteFor(id)).toContain(text);
  }
  const note=feedback.noteFor(id);
  expect(note.length).toBeGreaterThan(4000);
  expect(send).not.toHaveBeenCalled();
  await fireEvent.click(screen.getByRole('button',{name:/Checks & feedback/}));
  send.mockRejectedValueOnce(new Error('Queue unavailable'));
  await fireEvent.click(screen.getByRole('button',{name:'Send feedback to agent'}));
  await screen.findByText(/Feedback was not accepted/);
  expect(feedback.noteFor(id)).toBe(note);
  send.mockResolvedValue({delivered:false,queued:true} as never);
  await fireEvent.click(screen.getByRole('button',{name:'Send feedback to agent'}));
  await screen.findByText(/Feedback accepted/);
  expect(send).toHaveBeenLastCalledWith(expect.objectContaining({note}));
  expect(feedback.noteFor(id)).toBe('');
});

it('deletes saved and unsaved annotations without removing other pending feedback',async()=>{
  feedback.setNote(id,'Keep other feedback');
  render(ReviewWorkspace,{sessionId:id});await openScreens();
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  await fireEvent.input(screen.getByRole('textbox'),{target:{value:'Remove this annotation'}});
  await fireEvent.click(screen.getByRole('button',{name:'Save'}));
  await fireEvent.click(screen.getByRole('button',{name:'Comment 1'}));
  await fireEvent.click(screen.getByRole('button',{name:'Delete comment'}));
  expect(screen.queryByRole('dialog',{name:'Comment 1 · After'})).toBeNull();
  expect(screen.queryByRole('button',{name:'Comment 1'})).toBeNull();
  expect(feedback.noteFor(id)).toBe('Keep other feedback');
  await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  await fireEvent.input(screen.getByRole('textbox'),{target:{value:'Unfinished'}});
  await fireEvent.click(screen.getByRole('button',{name:'Delete comment'}));
  expect(screen.queryByRole('button',{name:'Comment 1'})).toBeNull();
  expect(feedback.noteFor(id)).toBe('Keep other feedback');
  expect(send).not.toHaveBeenCalled();
});


it('routes screenshot shortcuts from review chrome and stops routing when another tab is selected',async()=>{
  render(ReviewWorkspace,{sessionId:id});await openScreens();await screen.findByAltText('Settings — After');
  const tab=screen.getByRole('tab',{name:'Screenshots'});
  await fireEvent.keyDown(tab,{key:'b'});expect(screen.getByRole('complementary',{name:'Review actions'})).toBeInTheDocument();
  await fireEvent.keyDown(tab,{key:'c'});expect(screen.getByRole('button',{name:'Add pin'})).toHaveAttribute('aria-pressed','true');
  await fireEvent.keyDown(screen.getByRole('button',{name:'Place comment on After screenshot'}),{key:'Escape'});
  await fireEvent.click(screen.getByRole('tab',{name:'Overview'}));
  await fireEvent.keyDown(screen.getByRole('tab',{name:'Overview'}),{key:'c'});
  expect(screen.queryByRole('button',{name:'Add pin'})).toBeNull();
});


it('opens the selected overview thumbnail directly in the screenshot canvas',async()=>{
  read.mockResolvedValue({packages:[pkg({screens:[{title:'Desktop',after:'desktop'},{title:'Phone',after:'phone'}]})]});
  render(ReviewWorkspace,{sessionId:id});
  await fireEvent.click(await screen.findByRole('button',{name:'Review screenshot: Phone'}));
  expect(await screen.findByAltText('Phone — After')).toBeInTheDocument();
  expect(screen.getByRole('tab',{name:'Screenshots'})).toHaveAttribute('aria-selected','true');
});


it('incorporates checks and feedback into Overview without opening a side panel',async()=>{
  feedback.setNote(id,'Review this carefully');
  render(ReviewWorkspace,{sessionId:id});
  await screen.findByRole('heading',{name:'A clearer settings screen'});
  expect(screen.queryByRole('complementary',{name:'Review actions'})).toBeNull();
  expect(await screen.findByRole('button',{name:'Squash and merge'})).toBeInTheDocument();
  expect(screen.getByRole('button',{name:'Send feedback to agent'})).toBeEnabled();
  await fireEvent.input(screen.getByRole('textbox',{name:'Review comment'}),{target:{value:'Check the error state'}});
  await fireEvent.click(screen.getByRole('button',{name:'Add to feedback'}));
  expect(feedback.noteFor(id)).toContain('Check the error state');
  expect(send).not.toHaveBeenCalled();
});
it('requests explicit risk estimates without sending pending review feedback',async()=>{
  feedback.setNote(id,'Keep pending feedback');
  talkSvc.Say.mockResolvedValue({item:'h',delivered:true});
  render(ReviewWorkspace,{sessionId:id});
  await fireEvent.click(await screen.findByRole('button',{name:'Ask agent to assess risks'}));
  await screen.findByRole('button',{name:'Assessment requested'});
  expect(talkSvc.Say).toHaveBeenCalledWith({session:id,text:expect.stringContaining('Likelihood: low|medium|high|unknown')});
  expect(talkSvc.Say).toHaveBeenCalledWith({session:id,text:expect.stringContaining(`Assess risks in review package ${pkg().id}`)});
  expect(feedback.noteFor(id)).toBe('Keep pending feedback');
  expect(send).not.toHaveBeenCalled();
});

it('distinguishes same-head publications by time with the commit as secondary text',async()=>{
  read.mockResolvedValue({packages:[pkg({id:'new',createdAt:'2026-11-08T10:30:45Z'}),pkg({id:'old',createdAt:'2026-11-08T09:20:15Z'})]});
  render(ReviewWorkspace,{sessionId:id});
  const versions=await screen.findByLabelText('Review version');
  expect(versions.parentElement?.querySelector('.select-value')).toHaveTextContent(/^Latest$/);
  await fireEvent.click(versions);
  const options=screen.getByRole('listbox',{name:'Review version'}).querySelectorAll('[role=option]');
  expect(options).toHaveLength(2);
  expect(options[0]).toHaveTextContent('30:45');
  expect(options[1]).toHaveTextContent('20:15');
  expect(options[0].querySelector('small')).toHaveTextContent('Commit bbbbbbbb');
  await fireEvent.click(options[1]);
  expect(versions).toHaveValue('old');
  expect(versions.parentElement?.querySelector('.select-value')).toHaveTextContent('20:15');
  expect(versions.parentElement?.querySelector('.select-value small')).toBeNull();
});

it('requests a revision-bound self-assessment without sending draft review feedback',async()=>{
  feedback.setNote(id,'Keep my unsent comments');
  talkSvc.Say.mockResolvedValueOnce({item:'h',delivered:true});
  render(ReviewWorkspace,{sessionId:id});
  await fireEvent.click(await screen.findByRole('button',{name:'About confidence'}));
  await fireEvent.click(await screen.findByRole('button',{name:'Ask agent for self-assessment'}));
  expect(await screen.findByRole('button',{name:'Self-assessment requested'})).toBeDisabled();
  expect(talkSvc.Say).toHaveBeenCalledWith({session:id,text:expect.stringContaining(`Add an explicit self-assessment to review package ${pkg().id}`)});
  expect(talkSvc.Say).toHaveBeenCalledWith({session:id,text:expect.stringContaining('Score: N/100')});
  expect(talkSvc.Say).toHaveBeenCalledWith({session:id,text:expect.stringContaining('Do not lower it solely because human approval is pending')});
  expect(feedback.noteFor(id)).toBe('Keep my unsent comments');
  expect(send).not.toHaveBeenCalled();
});

it('opens capture from review and selects the saved screenshot in its new version',async()=>{
 const next=pkg({id:'e'.repeat(64),screens:[...pkg().screens,{title:'Captured page',after:'f'.repeat(64)}]});
 vi.mocked(DaemonService.ReviewPackageCapture).mockResolvedValue(next);render(ReviewWorkspace,{sessionId:id});
 const button=await screen.findByRole('button',{name:'Capture screenshot'});
 await waitFor(()=>expect(button).toBeEnabled());await fireEvent.click(button);
 await fireEvent.input(screen.getByLabelText('Page URL'),{target:{value:'http://localhost:3000'}});
 await fireEvent.submit(screen.getByRole('button',{name:'Capture & save'}).closest('form')!);
 await waitFor(()=>expect(screen.getByRole('tab',{name:'Screenshots'})).toHaveAttribute('aria-selected','true'));
 expect(screen.getByRole('heading',{name:'Captured page'})).toBeInTheDocument();
 expect(screen.queryByRole('region',{name:'Capture screenshot'})).toBeNull();
});
it('keeps capture unavailable for stale review material',async()=>{
 facts.mockResolvedValue(status({head:'9'.repeat(40)}));render(ReviewWorkspace,{sessionId:id});
 await screen.findByText('Older revision');expect(screen.getByRole('button',{name:'Capture screenshot'})).toBeDisabled();
});
