import {beforeEach,afterEach,describe,it,expect,vi} from 'vitest';
import {render,screen,fireEvent,waitFor,cleanup} from '@testing-library/svelte';
import ProjectGitPullRequest from './ProjectGitPullRequest.svelte';
import {ProjectGitState} from '$lib/projectgit.svelte';
import type {GitSnapshot} from '$lib/projectgit';
import {store} from '$lib/store.svelte';
import {pullRequestWebURL} from '$lib/gitpullrequest';
const api=vi.hoisted(()=>({PullRequestOptions:vi.fn(),PreviewPullRequest:vi.fn(),CreatePullRequest:vi.fn(),DraftPullRequest:vi.fn(),Snapshot:vi.fn(),Identities:vi.fn(),Stashes:vi.fn()}));
const daemon=vi.hoisted(()=>({OpenURL:vi.fn()}));
vi.mock('@bindings/desktop',async(original)=>({...(await original() as object),ProjectGitService:api,DaemonService:daemon}));
const sha='a'.repeat(40),baseSHA='b'.repeat(40);
const branch={name:'feature/topic',fullName:'refs/heads/feature/topic',sha,remote:false,current:true};
const target={name:'main',fullName:'refs/heads/main',sha:baseSHA,remote:false,current:false};
const snapshot=():GitSnapshot=>({path:'/repo',branch:branch.name,head:sha,upstream:'origin/feature/topic',commits:[{sha,parents:[baseSHA],subject:'feat: improve Git actions',author:'A',email:'',date:'',refs:[]}],refs:[branch,target],remotes:[{name:'origin',host:'github.com',repo:'acme/repo',owner:'acme',avatarUrl:''}],worktrees:[],files:[],hasMore:false,signing:{enabled:false,format:'',key:''}});
const options={sourceRepo:'acme/repo',repositories:['acme/repo','acme/upstream'],baseRepo:'acme/repo',defaultBranch:'main',branches:['main','release'],reviewers:[{value:'reviewer',label:'reviewer'}],assignees:[{value:'owner',label:'owner'}],labels:[{value:'feature',label:'feature'}],notices:[]};
const preview={sourceRepo:'acme/repo',head:'feature/topic',baseSHA,needsPush:false,commits:2,files:3,subject:'feat: improve Git actions'};
let serial=0;
beforeEach(()=>{daemon.OpenURL.mockReset();daemon.OpenURL.mockResolvedValue(undefined);store.dismissFlash();Object.values(api).forEach(f=>f.mockReset());api.PullRequestOptions.mockResolvedValue(options);api.PreviewPullRequest.mockResolvedValue(preview);api.CreatePullRequest.mockResolvedValue({number:42,url:'https://github.com/acme/repo/pull/42',existing:false,warnings:[]});api.Snapshot.mockResolvedValue(snapshot());api.Identities.mockResolvedValue({authors:{}});api.Stashes.mockResolvedValue([]);});
afterEach(cleanup);
async function setup(){const git=new ProjectGitState('pr-'+(++serial),'');git.snapshot=snapshot();const onClose=vi.fn();render(ProjectGitPullRequest,{props:{git,branch,target,onClose}});await waitFor(()=>expect(screen.getByRole('button',{name:'Create PR'})).toBeEnabled());return {git,onClose};}
describe('Create pull request',()=>{
 it('prefills branch direction, submits explicit metadata and reports partial success without retrying creation',async()=>{
  const {git,onClose}=await setup();expect(screen.getByLabelText('Source branch')).toHaveValue(branch.fullName);expect(screen.getByLabelText('Base branch')).toHaveValue('main');
  expect(api.CreatePullRequest).not.toHaveBeenCalled();await fireEvent.input(screen.getByLabelText('Description'),{target:{value:'Concrete behavior'}});
  await fireEvent.focus(screen.getByRole('combobox',{name:'Reviewers'}));await fireEvent.click(screen.getByRole('option',{name:'reviewer'}));
  await fireEvent.focus(screen.getByRole('combobox',{name:'Labels'}));await fireEvent.click(screen.getByRole('option',{name:'feature'}));
  api.CreatePullRequest.mockResolvedValue({number:42,url:'https://github.com/acme/repo/pull/42',existing:false,warnings:['Labels could not be added']});
  await fireEvent.submit(screen.getByRole('form',{name:'Create pull request'}));
  expect(api.CreatePullRequest).toHaveBeenCalledWith(git.project,'',expect.objectContaining({selection:{remote:'origin',sourceRef:branch.fullName,sourceSHA:sha,baseRepo:'acme/repo',base:'main'},title:'feat: improve Git actions',body:'Concrete behavior',reviewers:['reviewer'],labels:['feature'],push:false}));
  await waitFor(()=>expect(onClose).toHaveBeenCalled());expect(store.flash).toMatchObject({text:'Pull request #42 created',kind:'warn',action:{label:'Open pull request'}});expect(store.flash?.description).toContain('acme/repo · feature/topic → main');expect(store.flash?.description).toContain('Labels could not be added');
  await store.flash!.action!.run();expect(daemon.OpenURL).toHaveBeenCalledWith('https://github.com/acme/repo/pull/42');
 });
 it('requires explicit push selection and preserves text after GitHub rejects creation',async()=>{
  const {git,onClose}=await setup();api.PreviewPullRequest.mockResolvedValue({...preview,needsPush:true});await fireEvent.change(screen.getByLabelText('Base branch'),{target:{value:'release'}});
  const create=await screen.findByRole('button',{name:'Push & create PR'});expect(create).toBeDisabled();
  await fireEvent.click(screen.getByRole('checkbox',{name:/Push feature\/topic/}));expect(create).toBeEnabled();
  let fail!:(error:Error)=>void;api.CreatePullRequest.mockReturnValue(new Promise((_,reject)=>fail=reject));await fireEvent.click(create);
  expect(git.busy).toBe('create-pr');await fireEvent.keyDown(screen.getByRole('dialog'),{key:'Escape'});expect(onClose).not.toHaveBeenCalled();
  fail(new Error('GitHub permission denied'));expect(await screen.findByRole('alert')).toHaveTextContent('GitHub permission denied');expect(screen.getByLabelText('Title')).toHaveValue('feat: improve Git actions');expect(screen.getByRole('button',{name:'Push & create PR'})).toBeEnabled();
 });
 it('does not overwrite a human edit with a late AI response',async()=>{
  await setup();let finish!:(text:string)=>void;api.DraftPullRequest.mockReturnValue(new Promise(resolve=>finish=resolve));
  await fireEvent.click(screen.getByRole('button',{name:'AI draft'}));await fireEvent.input(screen.getByLabelText('Title'),{target:{value:'Manual title'}});finish('feat: generated\n\nGenerated body');
  expect(await screen.findByRole('alert')).toHaveTextContent('Your draft or branches changed');expect(screen.getByLabelText('Title')).toHaveValue('Manual title');expect(api.CreatePullRequest).not.toHaveBeenCalled();
 });
 it('ignores an old branch preview and does not request reviewers for a draft',async()=>{
  await setup();let old!:(value:typeof preview)=>void;api.PreviewPullRequest.mockReturnValueOnce(new Promise(resolve=>old=resolve));
  await fireEvent.change(screen.getByLabelText('Base branch'),{target:{value:'release'}});api.PreviewPullRequest.mockResolvedValue({...preview,commits:4});await fireEvent.change(screen.getByLabelText('Base branch'),{target:{value:'main'}});await screen.findByText('4 commits · 3 files');old({...preview,needsPush:true});await waitFor(()=>expect(screen.getByText('4 commits · 3 files')).toBeInTheDocument());
  await fireEvent.click(screen.getByRole('checkbox',{name:'Create as draft'}));await fireEvent.click(screen.getByRole('button',{name:'Create draft PR'}));
  expect(api.CreatePullRequest).toHaveBeenCalledWith(expect.anything(),'',expect.objectContaining({draft:true,reviewers:[],selection:expect.objectContaining({base:'main'})}));
 });
 it('orders assignees, reviewers, labels and keeps Cancel left of the two create actions',async()=>{
  await setup();const boxes=['Assignees','Reviewers','Labels'].map(name=>screen.getByRole('combobox',{name}));expect(boxes[0].compareDocumentPosition(boxes[1])&Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();expect(boxes[1].compareDocumentPosition(boxes[2])&Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  const actions=screen.getByRole('button',{name:'Cancel'}).parentElement!;expect([...actions.querySelectorAll('button')].map(b=>b.textContent?.trim())).toEqual(['Cancel','Continue on GitHub','Create PR']);
 });
 it('continues on GitHub with the form pre-filled, and only for a pushed branch',async()=>{
  const {onClose}=await setup();await fireEvent.input(screen.getByLabelText('Description'),{target:{value:'Why & how'}});
  await fireEvent.focus(screen.getByRole('combobox',{name:'Labels'}));await fireEvent.click(screen.getByRole('option',{name:'feature'}));
  await fireEvent.click(screen.getByRole('button',{name:'Continue on GitHub'}));
  expect(daemon.OpenURL).toHaveBeenCalledWith('https://github.com/acme/repo/compare/main...feature/topic?quick_pull=1&title=feat%3A+improve+Git+actions&body=Why+%26+how&labels=feature');
  expect(onClose).toHaveBeenCalled();expect(api.CreatePullRequest).not.toHaveBeenCalled();
 });
 it('disables Continue on GitHub while the branch is unpushed',async()=>{
  await setup();api.PreviewPullRequest.mockResolvedValue({...preview,needsPush:true});await fireEvent.change(screen.getByLabelText('Base branch'),{target:{value:'release'}});
  await screen.findByRole('button',{name:'Push & create PR'});expect(screen.getByRole('button',{name:'Continue on GitHub'})).toBeDisabled();
 });
 it('names a fork head and cuts a description too long for a URL',()=>{
  const url=pullRequestWebURL({baseRepo:'acme/upstream',base:'main',sourceRepo:'me/repo',head:'fix/#1',title:'t',body:'x'.repeat(20000),assignees:['a','b'],labels:[]});
  expect(url.startsWith('https://github.com/acme/upstream/compare/main...me:repo:fix/%231?quick_pull=1&title=t&body=xxx')).toBe(true);expect(url.length).toBeLessThanOrEqual(7000);expect(url).toContain('assignees=a%2Cb');
 });
});
