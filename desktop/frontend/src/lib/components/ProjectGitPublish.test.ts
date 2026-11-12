import {afterEach, expect, it, vi} from 'vitest';
import {cleanup, fireEvent, render, screen, waitFor} from '@testing-library/svelte';
import ProjectGitPublish from './ProjectGitPublish.svelte';
import {ProjectGitState} from '$lib/projectgit.svelte';
vi.mock('@bindings/desktop',()=>({ProjectGitService:{}}));
afterEach(()=>{cleanup();vi.restoreAllMocks();});
function setup(remotes=['origin','backup']) {
 const git=new ProjectGitState('demo','/repo');
 git.snapshot={path:'/repo',branch:'feature/local',head:'a'.repeat(40),upstream:'',refs:[],commits:[],worktrees:[],files:[],hasMore:false,signing:{enabled:false,format:'',key:''},remotes:remotes.map(name=>({name,host:'',owner:'',repo:'',avatarUrl:''}))};
 const branch={name:'feature/local',fullName:'refs/heads/feature/local',sha:'a'.repeat(40),remote:false,current:true};
 const action=vi.spyOn(git,'branchAction').mockResolvedValue('/repo');const close=vi.fn();
 render(ProjectGitPublish,{git,branch,onClose:close});return {git,action,close,branch};
}
it('defaults to origin and publishes only the explicitly chosen destination',async()=>{
 const {action,close,branch}=setup();
 const name=screen.getByRole('textbox',{name:'Remote branch'});
 await waitFor(()=>expect(name).toHaveFocus());expect(name).toHaveValue('feature/local');expect(action).not.toHaveBeenCalled();
 await fireEvent.change(screen.getByLabelText('Remote',{exact:true}),{target:{value:'backup'}});
 await fireEvent.input(name,{target:{value:'review/remote-name'}});await fireEvent.submit(screen.getByRole('form',{name:'Publish branch'}));
 expect(action).toHaveBeenCalledWith({action:'publish',source:branch.fullName,sourceSHA:branch.sha,target:'',targetSHA:'',remote:'backup',remoteBranch:'review/remote-name'},'',200);
 await waitFor(()=>expect(close).toHaveBeenCalledOnce());
});
it('keeps edited destination on rejection, blocks busy cancellation, and supports Escape',async()=>{
 const {git,action,close}=setup();action.mockImplementation(async()=>{git.error='Remote rejected this push';return null;});
 await fireEvent.input(screen.getByLabelText('Remote branch'),{target:{value:'keep/name'}});
 await fireEvent.submit(screen.getByRole('form',{name:'Publish branch'}));
 expect(await screen.findByRole('alert')).toHaveTextContent('Remote rejected this push');expect(screen.getByLabelText('Remote branch')).toHaveValue('keep/name');expect(close).not.toHaveBeenCalled();
 git.busy='publish';await fireEvent.keyDown(window,{key:'Escape'});expect(close).not.toHaveBeenCalled();
 git.busy='';await fireEvent.keyDown(window,{key:'Escape'});expect(close).toHaveBeenCalledOnce();
});
it('explains missing remotes and prevents submission',async()=>{
 const {action}=setup([]);expect(screen.getByRole('button',{name:'Publish branch'})).toBeDisabled();
 expect(screen.getByText(/Add a Git remote/)).toBeInTheDocument();await fireEvent.submit(screen.getByRole('form',{name:'Publish branch'}));expect(action).not.toHaveBeenCalled();
});

it('configures an existing remote tracking branch without publishing commits',async()=>{
 const git=new ProjectGitState('demo','/repo');
 const branch={name:'local',fullName:'refs/heads/local',sha:'a'.repeat(40),remote:false,current:true};
 const target={name:'origin/review',fullName:'refs/remotes/origin/review',sha:'b'.repeat(40),remote:true,current:false};
 git.snapshot={path:'/repo',branch:'local',head:branch.sha,upstream:'',refs:[branch,target],commits:[],worktrees:[],files:[],hasMore:false,signing:{enabled:false,format:'',key:''},remotes:[{name:'origin',host:'',owner:'',repo:'',avatarUrl:''}]};
 const action=vi.spyOn(git,'branchAction').mockResolvedValue('/repo');const onClose=vi.fn();
 render(ProjectGitPublish,{git,branch,destination:{remote:'origin',name:'review',trackingOnly:true},onClose});
 expect(screen.getByRole('combobox',{name:'Remote branch'})).toHaveValue('review');expect(screen.queryByText(/Future pulls and pushes/)).toBeNull();expect(action).not.toHaveBeenCalled();
 await fireEvent.submit(screen.getByRole('form',{name:'Set upstream'}));
 expect(action).toHaveBeenCalledWith({action:'set-upstream',source:branch.fullName,sourceSHA:branch.sha,target:target.fullName,targetSHA:target.sha,remote:'origin',remoteBranch:'review'},'',200);
 await waitFor(()=>expect(onClose).toHaveBeenCalledOnce());
});
