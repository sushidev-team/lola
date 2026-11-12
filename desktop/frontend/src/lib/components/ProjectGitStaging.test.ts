import { tick } from 'svelte';
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within, waitFor } from '@testing-library/svelte';
import ProjectGitStaging from './ProjectGitStaging.svelte';
import { ProjectGitState } from '$lib/projectgit.svelte';
import { confirm } from '$lib/confirm.svelte';
import type { GitSnapshot } from '$lib/projectgit';
const api = vi.hoisted(() => ({ Snapshot: vi.fn(), Identities: vi.fn(), Commit: vi.fn(), Amend: vi.fn(), LastCommitMessage: vi.fn() }));
vi.mock('@bindings/desktop', () => ({ ProjectGitService: api }));
const head = 'a'.repeat(40);
function snapshot(): GitSnapshot { return { path:'/repo',branch:'main',head,upstream:'origin/main',refs:[],remotes:[],commits:[],worktrees:[],hasMore:false,signing:{enabled:true,format:'ssh',key:''}, files:[{path:'src/app.ts',oldPath:'',index:'M',worktree:' ',untracked:false,conflict:false},{path:'src/new.ts',oldPath:'',index:'?',worktree:'?',untracked:true,conflict:false}] }; }
function setup(files?: GitSnapshot['files']) { const git=new ProjectGitState('lola','');git.snapshot={...snapshot(), ...(files ? {files} : {})}; const oninspect=vi.fn(),onact=vi.fn(),oncommitted=vi.fn();const view=render(ProjectGitStaging,{git,oninspect,onact,oncommitted});return {git,oninspect,onact,oncommitted,view}; }
beforeEach(()=>{localStorage.clear();confirm.cancel();Object.values(api).forEach(f=>f.mockReset());api.Snapshot.mockResolvedValue(snapshot());api.Identities.mockResolvedValue({authors:{}});});
afterEach(()=>{cleanup();vi.useRealTimers();});
describe('Project Git staging',()=>{
 it('keeps filename hierarchy and preserves selection in the file context menu',async()=>{
   const {git,view,oninspect}=setup();const oncontextfile=vi.fn();
   await view.rerender({git,oninspect,onact:vi.fn(),oncommitted:vi.fn(),oncontextfile});
   const row=screen.getByRole('button',{name:'Inspect unstaged src/new.ts'});
   expect(row.querySelector('.directory')?.textContent).toBe('src');expect(row.querySelector('.basename')?.textContent).toBe('/new.ts');
   await fireEvent.contextMenu(row);expect(oncontextfile).toHaveBeenLastCalledWith(expect.objectContaining({path:'src/new.ts'}),false,['src/new.ts'],expect.any(MouseEvent));expect(oninspect).not.toHaveBeenCalled();
   await fireEvent.keyDown(row,{key:'F10',shiftKey:true});expect(oncontextfile).toHaveBeenCalledTimes(2);
 });
 it('keeps staging available but prevents a separate commit during operation recovery',async()=>{
   const {git}=setup();git.snapshot!.operation='cherry-pick';git.subject='existing draft';await tick();
   expect(screen.getByRole('button',{name:'Commit 1 file'})).toBeDisabled();expect(screen.getByRole('checkbox',{name:'Amend last commit'})).toBeDisabled();
   expect(screen.getByRole('button',{name:'Stage src/new.ts'})).toBeEnabled();expect(screen.getByText(/Continue above/)).toBeInTheDocument();
 });
 it('keeps both staging groups visible and maps list and tree actions to exact paths',async()=>{
   const {onact,oninspect}=setup();
   expect(within(screen.getByRole('region',{name:'Staged files'})).getByRole('button',{name:'Inspect staged src/app.ts'})).toBeInTheDocument();
   await fireEvent.click(screen.getByRole('button',{name:'File tree view'}));
   expect(localStorage.getItem('lola.git.files.tree')).toBe('1');
   const folder=screen.getByRole('button',{name:'Unstaged folder src'});
   await fireEvent.click(folder);expect(folder).toHaveAttribute('aria-expanded','false');
   await fireEvent.click(folder);expect(folder).toHaveAttribute('aria-expanded','true');
   await fireEvent.click(screen.getByRole('button',{name:'Inspect unstaged src/new.ts'}));
   expect(oninspect).toHaveBeenCalledWith(expect.objectContaining({path:'src/new.ts'}),false);
   await fireEvent.click(screen.getByRole('button',{name:'Stage src/new.ts'}));expect(onact).toHaveBeenCalledWith('stage',['src/new.ts']);
 });
 it('joins subject and body and retains them when native signing fails',async()=>{
   const {git}=setup();api.Commit.mockRejectedValue(new Error('Signing cancelled'));
   await fireEvent.input(screen.getByLabelText('Commit subject'),{target:{value:'feat: compact Git'}});
   await fireEvent.input(screen.getByLabelText('Commit body'),{target:{value:'Keep staged files visible.\nSupport nested paths.'}});
   await fireEvent.click(screen.getByRole('button',{name:'Commit 1 file'}));
   await waitFor(()=>expect(git.busy).toBe(''));
   expect(api.Commit).toHaveBeenCalledWith('lola','','feat: compact Git\n\nKeep staged files visible.\nSupport nested paths.');
   expect(screen.getByLabelText('Commit body')).toHaveValue('Keep staged files visible.\nSupport nested paths.');
   expect(git.error).toContain('Signing cancelled');
 });
 it('requires a concrete amend confirmation, then pins HEAD and clears a successful draft',async()=>{
   const {git,oncommitted}=setup();
   await fireEvent.input(screen.getByLabelText('Commit subject'),{target:{value:'fix: corrected commit'}});
   await fireEvent.click(screen.getByRole('checkbox',{name:'Amend last commit'}));
   await fireEvent.click(screen.getByRole('button',{name:'Amend 1 file'}));
   expect(api.Amend).not.toHaveBeenCalled();expect(confirm.request?.body).toContain(head.slice(0,8));
   confirm.accept();await waitFor(()=>expect(oncommitted).toHaveBeenCalledOnce());
   expect(api.Amend).toHaveBeenCalledWith('lola','','fix: corrected commit',head);expect(git.message).toBe('');expect(git.amend).toBe(false);
 });
 it('does not amend after the checkout or message changes during confirmation',async()=>{
   const {git}=setup();git.subject='fix: draft';await git.setAmend(true);await tick();
   await fireEvent.click(screen.getByRole('button',{name:'Amend 1 file'}));
   git.snapshot={...snapshot(),head:'b'.repeat(40)};confirm.accept();
   expect(api.Amend).not.toHaveBeenCalled();expect(git.error).toContain('changed');expect(git.subject).toBe('fix: draft');
 });
 it('prefills the full last message for an empty amend draft without overwriting edits',async()=>{
   const {git}=setup();api.LastCommitMessage.mockResolvedValueOnce('fix: previous subject\n\nPrevious body.\n');
   await fireEvent.click(screen.getByRole('checkbox',{name:'Amend last commit'}));
   await waitFor(()=>expect(screen.getByLabelText('Commit body')).toHaveValue('Previous body.'));
   expect(api.LastCommitMessage).toHaveBeenCalledWith('lola','',head);
   expect(screen.getByLabelText('Commit subject')).toHaveValue('fix: previous subject');
   await fireEvent.click(screen.getByRole('checkbox',{name:/Amend last commit/}));
   await fireEvent.input(screen.getByLabelText('Commit subject'),{target:{value:'My revision'}});
   await fireEvent.click(screen.getByRole('checkbox',{name:'Amend last commit'}));
   expect(api.LastCommitMessage).toHaveBeenCalledTimes(1);expect(git.subject).toBe('My revision');
 });
 it('blocks a draft prefilled from the previous HEAD until amend is deliberately selected again',async()=>{
   const {git}=setup();api.LastCommitMessage.mockResolvedValueOnce('fix: previous subject\n\nPrevious body.\n');
   await fireEvent.click(screen.getByRole('checkbox',{name:'Amend last commit'}));
   await waitFor(()=>expect(screen.getByLabelText('Commit body')).toHaveValue('Previous body.'));
   expect(git.amendHead).toBe(head);
   const nextHead='b'.repeat(40);git.snapshot={...snapshot(),head:nextHead};await tick();
   expect(screen.getByRole('alert')).toHaveTextContent('HEAD changed');
   expect(screen.getByRole('button',{name:'Amend 1 file'})).toBeDisabled();
   await fireEvent.submit(screen.getByLabelText('Commit subject').closest('form')!);
   expect(confirm.request).toBeNull();expect(api.Amend).not.toHaveBeenCalled();
   expect(git.amendHead).toBe(head);expect(git.subject).toBe('fix: previous subject');
   await fireEvent.click(screen.getByRole('checkbox',{name:/Amend last commit/}));
   await fireEvent.input(screen.getByLabelText('Commit subject'),{target:{value:'Reviewed replacement message'}});
   await fireEvent.click(screen.getByRole('checkbox',{name:'Amend last commit'}));
   expect(git.amendHead).toBe(nextHead);expect(screen.queryByRole('alert')).not.toBeInTheDocument();
   await fireEvent.click(screen.getByRole('button',{name:'Amend 1 file'}));confirm.accept();
   await waitFor(()=>expect(api.Amend).toHaveBeenCalledWith('lola','','Reviewed replacement message\n\nPrevious body.',nextHead));
 });
 it('keeps checkout drafts isolated when the displayed checkout changes',async()=>{
   const {git,view}=setup();await fireEvent.input(screen.getByLabelText('Commit subject'),{target:{value:'Original checkout'}});
   const other=new ProjectGitState('lola','/repo/tree');other.snapshot=snapshot();
   await view.rerender({git:other});await fireEvent.input(screen.getByLabelText('Commit subject'),{target:{value:'Other checkout'}});
   await view.rerender({git});expect(screen.getByLabelText('Commit subject')).toHaveValue('Original checkout');
 });
 it('selects independent staged and unstaged ranges without staging until an action is clicked',async()=>{
   const files=['a.ts','b.ts','c.ts','d.ts'].map(path=>({path,oldPath:'',index:'M',worktree:'M',untracked:false,conflict:false}));
   const {onact,oninspect}=setup(files);
   const row=(path:string,staged=false)=>screen.getByRole('button',{name:`Inspect ${staged?'staged':'unstaged'} ${path}`});
   await fireEvent.click(row('a.ts'));await fireEvent.click(row('c.ts'),{shiftKey:true});
   for(const path of ['a.ts','b.ts','c.ts'])expect(row(path)).toHaveAttribute('aria-pressed','true');
   expect(row('d.ts')).toHaveAttribute('aria-pressed','false');expect(row('a.ts',true)).toHaveAttribute('aria-pressed','false');
   expect(oninspect).toHaveBeenCalledTimes(1);expect(onact).not.toHaveBeenCalled();
   await fireEvent.click(row('b.ts'),{metaKey:true});expect(row('b.ts')).toHaveAttribute('aria-pressed','false');
   await fireEvent.click(row('d.ts'),{ctrlKey:true});expect(row('d.ts')).toHaveAttribute('aria-pressed','true');
   const action=within(row('d.ts').parentElement!).getByRole('button',{name:'Stage 3 selected files'});
   expect(action).toHaveTextContent('Stage 3 files');await fireEvent.click(action);
   expect(onact).toHaveBeenLastCalledWith('stage',['a.ts','c.ts','d.ts']);
   await fireEvent.click(row('b.ts',true));await fireEvent.click(row('d.ts',true),{shiftKey:true});
   await fireEvent.click(within(row('b.ts',true).parentElement!).getByRole('button',{name:'Unstage 3 selected files'}));
   expect(onact).toHaveBeenLastCalledWith('unstage',['b.ts','c.ts','d.ts']);expect(row('a.ts')).toHaveAttribute('aria-pressed','true');
 });
 it('uses visible tree order for ranges, excluding collapsed folders and revealing selected actions on keyboard focus',async()=>{
   const files=['z.ts','beta/hidden.ts','alpha/c.ts','alpha/a.ts','gamma/d.ts'].map(path=>({path,oldPath:'',index:' ',worktree:'M',untracked:false,conflict:false}));
   const {onact}=setup(files);
   await fireEvent.click(screen.getByRole('button',{name:'File tree view'}));
   await fireEvent.click(screen.getByRole('button',{name:'Unstaged folder beta'}));
   const first=screen.getByRole('button',{name:'Inspect unstaged alpha/c.ts'});
   await fireEvent.click(first);await fireEvent.click(screen.getByRole('button',{name:'Inspect unstaged gamma/d.ts'}),{shiftKey:true});
   const group=screen.getByRole('region',{name:'Unstaged files'});
   await fireEvent.click(within(group).getAllByRole('button',{name:'Stage 3 selected files'})[0]);
   expect(onact).toHaveBeenCalledWith('stage',['alpha/c.ts','alpha/a.ts','gamma/d.ts']);
   first.focus();await fireEvent.keyDown(first,{key:'Escape'});expect(first).toHaveAttribute('aria-pressed','false');
   await fireEvent.keyDown(first,{key:' ',ctrlKey:true});expect(first).toHaveAttribute('aria-pressed','true');
   await fireEvent.keyDown(first,{key:'ArrowDown',shiftKey:true});
   expect(screen.getByRole('button',{name:'Inspect unstaged alpha/a.ts'})).toHaveFocus();
   expect(screen.getByRole('button',{name:'Inspect unstaged alpha/a.ts'})).toHaveAttribute('aria-pressed','true');
   expect(onact).toHaveBeenCalledTimes(1);
 });
 it('prunes disappeared paths after refresh and clears selection when switching worktrees',async()=>{
   const files=['a.ts','b.ts'].map(path=>({path,oldPath:'',index:' ',worktree:'M',untracked:false,conflict:false}));
   const {git,view,onact}=setup(files);
   await fireEvent.click(screen.getByRole('button',{name:'Inspect unstaged a.ts'}));
   await fireEvent.click(screen.getByRole('button',{name:'Inspect unstaged b.ts'}),{metaKey:true});
   git.snapshot={...snapshot(),files:[files[1]]};await tick();
   await fireEvent.click(screen.getByRole('button',{name:'Stage b.ts'}));expect(onact).toHaveBeenLastCalledWith('stage',['b.ts']);
   const other=new ProjectGitState('lola','/other');other.snapshot={...snapshot(),files:[files[1]]};await view.rerender({git:other});
   expect(screen.getByRole('button',{name:'Inspect unstaged b.ts'})).toHaveAttribute('aria-pressed','false');
   expect(screen.queryByRole('button',{name:'Stage 1 selected files'})).not.toBeInTheDocument();
 });
 it('moves signing status into a delayed keyboard tooltip without toggling amend',async()=>{
   vi.useFakeTimers();const {git}=setup();
   const signing=screen.getByRole('button',{name:'SSH signing enabled'});
   expect(screen.queryByText('SSH signing enabled')).not.toBeInTheDocument();expect(signing).not.toHaveAttribute('title');
   signing.focus();await fireEvent.focusIn(signing);await vi.advanceTimersByTimeAsync(499);
   expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();await vi.advanceTimersByTimeAsync(1);
   expect(screen.getByRole('tooltip')).toHaveTextContent('SSH signing enabled');
   await fireEvent.click(signing);expect(git.amend).toBe(false);
 });

});
