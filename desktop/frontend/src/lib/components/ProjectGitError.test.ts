import {cleanup,fireEvent,render,screen} from '@testing-library/svelte';
import {afterEach,expect,it,vi} from 'vitest';
import ProjectGitError from './ProjectGitError.svelte';
afterEach(cleanup);
it('shows the next action and keeps raw stderr hidden until requested',async()=>{
 const error='RuntimeError: repository action failed: git push: exit status 1:\n! [rejected] main -> main (non-fast-forward)\nhint: Updates were rejected';
 const run=vi.fn(),ondismiss=vi.fn();
 render(ProjectGitError,{error,operation:'push',recovery:{label:'Pull changes',run},ondismiss,onrefresh:vi.fn()});
 expect(screen.getByRole('alert')).toHaveTextContent('Push blocked');
 expect(screen.queryByLabelText('Git error details')).toBeNull();
 await fireEvent.click(screen.getByRole('button',{name:'Pull changes'}));expect(run).toHaveBeenCalledOnce();
 await fireEvent.click(screen.getByRole('button',{name:'Details'}));
 expect(screen.getByLabelText('Git error details').textContent).toBe(error);
 await fireEvent.click(screen.getByRole('button',{name:'Dismiss Git error'}));expect(ondismiss).toHaveBeenCalledOnce();
});
