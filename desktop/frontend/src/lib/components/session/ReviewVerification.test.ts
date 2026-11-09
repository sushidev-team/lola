import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import './talk.testkit';
import { DaemonService } from '@bindings/desktop';
import ReviewVerification from './ReviewVerification.svelte';

const pkg={id:'a'.repeat(64),session:'worker',head:'b'.repeat(40),base:'c'.repeat(40),repo:'acme/app',pr:1,title:'Review',body:'Changes',version:1,screens:[],artifacts:[],createdAt:''};
const read=vi.mocked(DaemonService.ReviewPackageChecks),run=vi.mocked(DaemonService.ReviewPackageVerify);
const spec={id:'unit',command:'npm test',image:'sha256:'+'d'.repeat(64),timeout_seconds:300,memory_mib:2048};
beforeEach(()=>{localStorage.clear();read.mockReset().mockResolvedValue({checks:[]});run.mockReset();vi.mocked(DaemonService.ReviewPackageRecover).mockReset();});
afterEach(cleanup);
async function prepare(){
  await screen.findByText('No checks yet.');
  await fireEvent.click(screen.getByRole('button',{name:'Set up check'}));
  await waitFor(()=>expect(screen.getByLabelText('Command')).toHaveFocus());
  await fireEvent.click(screen.getByText('Options'));
  await fireEvent.input(screen.getByLabelText('Check name'),{target:{value:spec.id}});
  await fireEvent.input(screen.getByLabelText('Command'),{target:{value:spec.command}});
  await fireEvent.input(screen.getByLabelText('Container image'),{target:{value:spec.image}});
  await fireEvent.submit(screen.getByRole('button',{name:'Review check'}).closest('form')!);
  await waitFor(()=>expect(screen.getByRole('heading',{name:'Review check authorization'})).toHaveFocus());
}
it('requires review and explicit authorization for the exact package',async()=>{
  render(ReviewVerification,{pkg,canRun:true,freshness:'current'});await prepare();expect(run).not.toHaveBeenCalled();
  run.mockResolvedValue({id:'run'} as never);
  await fireEvent.click(screen.getByRole('button',{name:'Authorize and run'}));
  await waitFor(()=>expect(run).toHaveBeenCalledTimes(1));
  expect(run.mock.calls[0][0]).toBe('worker');expect(run.mock.calls[0][1]).toEqual({package:pkg.id,id:expect.stringMatching(/^[a-f0-9]{32}$/),spec});
  await waitFor(()=>expect(screen.getByRole('heading',{name:'Independent verification'})).toHaveFocus());
});
it('returns keyboard focus when editing and closing the check form',async()=>{
  render(ReviewVerification,{pkg,canRun:true});await prepare();
  await fireEvent.click(screen.getByRole('button',{name:'Edit check'}));
  await waitFor(()=>expect(screen.getByLabelText('Command')).toHaveFocus());
  await fireEvent.click(screen.getByRole('button',{name:'Cancel'}));
  await waitFor(()=>expect(screen.getByRole('button',{name:'Set up check'})).toHaveFocus());
});
it('locks an uncertain write through failed readback and recovers the original receipt',async()=>{
  render(ReviewVerification,{pkg,canRun:true});await prepare();run.mockRejectedValue(new Error('connection lost'));
  await fireEvent.click(screen.getByRole('button',{name:'Authorize and run'}));
  await screen.findByRole('alert');expect(screen.getByRole('button',{name:'Authorize and run'})).toBeDisabled();
  read.mockRejectedValueOnce(new Error('offline'));await fireEvent.click(screen.getByRole('button',{name:'Refresh checks'}));
  await screen.findByText('Error: offline');expect(screen.getByRole('button',{name:'Authorize and run'})).toBeDisabled();
  const request=run.mock.calls[0][1];read.mockResolvedValue({checks:[{...request,session:pkg.session,base:pkg.base,head:pkg.head,state:'pass',stopped:true,startedAt:'2026-11-08T10:00:00Z'}]});
  await fireEvent.click(screen.getByRole('button',{name:'Refresh checks'}));await screen.findByText('Passed');expect(run).toHaveBeenCalledTimes(1);expect(screen.queryByRole('button',{name:'Authorize and run'})).toBeNull();
});
it('retains frozen request identity when readback confirms no record',async()=>{
  render(ReviewVerification,{pkg,canRun:true});await prepare();run.mockRejectedValueOnce(new Error('lost'));
  await fireEvent.click(screen.getByRole('button',{name:'Authorize and run'}));await screen.findByRole('alert');
  await fireEvent.click(screen.getByRole('button',{name:'Refresh checks'}));await waitFor(()=>expect(screen.getByRole('button',{name:'Authorize and run'})).toBeEnabled());
  run.mockResolvedValue({} as never);await fireEvent.click(screen.getByRole('button',{name:'Authorize and run'}));expect(run.mock.calls[1][1]).toEqual(run.mock.calls[0][1]);
});
it('never carries old package results across selection changes and labels stale evidence',async()=>{
  let finish!:(data:unknown)=>void;read.mockImplementationOnce(()=>new Promise(resolve=>finish=resolve) as never);
  const view=render(ReviewVerification,{pkg,canRun:false,freshness:'stale'});
  await view.rerender({pkg:{...pkg,id:'e'.repeat(64)},canRun:false,freshness:'stale'});
  await screen.findByText('No checks yet.');
  finish({checks:[{id:'old',spec,state:'pass',stopped:true}]});
  await waitFor(()=>expect(screen.queryByText('Passed')).toBeNull());
  expect(screen.getByText('Checks apply to this version, not the current PR.')).toBeInTheDocument();expect(screen.queryByRole('button',{name:'Set up check'})).toBeNull();
});
