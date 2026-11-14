import {expect,it} from 'vitest';
import {gitErrorInfo} from './giterror';
it.each(['remote branch changed since it was fetched', 'git push: exit status 1: To github.com:acme/repo.git\n ! [rejected] (delete) -> topic (stale info)'])('explains remote deletion refused for %s', error => {
 expect(gitErrorInfo(error, 'delete-branch')).toMatchObject({title:'Remote branch changed',kind:'warn'});
 expect(gitErrorInfo(error, 'delete-branch').description).toContain('Fetch');
});
it.each([
 ['RuntimeError: repository action failed: git push: exit status 1: ! [rejected] main -> main (non-fast-forward)','Push blocked','pull'],
 ['branch has diverged; check it out and merge or rebase the upstream explicitly','Branches have diverged','merge'],
 ['fatal: Authentication failed for remote','Git sign-in required',undefined],
 ['fatal: Could not resolve host: github.com','Remote unavailable',undefined],
 ['remote: error: GH013: Repository rule violations found','Push blocked by repository rules',undefined],
 ['Automatic merge failed; fix conflicts and then commit the result.','Conflicts need attention',undefined],
 ['commit or stash this checkout\'s changes before switching or integrating branches','Save your local changes first',undefined],
 ['branch changed since it was selected; refresh and try again','Repository changed','refresh'],
])('explains %s', (error,title,recovery)=>{
 const info=gitErrorInfo(error,'push');
 expect(info.title).toBe(title);
 expect(info.recovery).toBe(recovery);
});
it('keeps unknown errors useful without displaying an unbounded stderr dump',()=>{
 const info=gitErrorInfo('RuntimeError: repository action failed: '+ 'Unexpected failure '.repeat(40)+'\nhint: more output','push');
 expect(info.title).toBe('Push failed');expect(info.description.length).toBeLessThanOrEqual(160);
 expect(info.description).not.toMatch(/RuntimeError|hint:/);
});
it('offers force recovery only for unmerged worktree branch removal',()=>{
 const error='RuntimeError: branch is not fully merged; remove only the worktree to keep its branch';
 expect(gitErrorInfo(error,'remove-worktree-branch')).toMatchObject({title:'Branch is not fully merged',kind:'warn',recovery:'force-remove'});
 expect(gitErrorInfo(error,'delete-branch').recovery).toBe('force-delete');
 expect(gitErrorInfo('worktree changed since it was selected','remove-worktree-branch').recovery).toBe('refresh');
});
it.each(['git worktree: exit status 255: failed to delete checkout: Directory not empty', 'commit or stash this checkout\'s changes before switching or integrating branches', 'worktree contains modified or untracked files'])('offers force recovery for worktree removal: %s', error => {
 expect(gitErrorInfo(error,'remove-worktree').recovery).toBe('force-worktree');
 expect(gitErrorInfo(error,'remove-worktree-branch').recovery).toBe('force-worktree');
 expect(gitErrorInfo(error,'checkout').recovery).not.toBe('force-worktree');
});
