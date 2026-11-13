import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import './talk.testkit';
import { DaemonService } from '@bindings/desktop';
import ReviewCapture from './ReviewCapture.svelte';
const pkg={id:'a'.repeat(64),session:'worker',head:'b'.repeat(40),base:'c'.repeat(40),repo:'acme/app',pr:1,title:'Review',body:'Changes',version:1,screens:[{title:'Settings',after:'d'.repeat(64),url:'http://localhost:3000/settings'}],artifacts:[],createdAt:''};
const capture=vi.mocked(DaemonService.ReviewPackageCapture);
beforeEach(()=>{localStorage.clear();capture.mockReset();});
afterEach(cleanup);
it('captures explicitly into an exact package and returns its new version',async()=>{
 const done=vi.fn();const next={...pkg,id:'e'.repeat(64)};capture.mockResolvedValue(next);
 render(ReviewCapture,{pkg,initialURL:'http://localhost:3000',onCaptured:done,onClose:vi.fn()});
 await waitFor(()=>expect(screen.getByLabelText('Page URL')).toHaveFocus());
 expect(capture).not.toHaveBeenCalled();
 await fireEvent.input(screen.getByLabelText('Screenshot title'),{target:{value:'New screen'}});
 await fireEvent.submit(screen.getByRole('button',{name:'Capture & save'}).closest('form')!);
 await waitFor(()=>expect(done).toHaveBeenCalledWith(next,1));
 expect(capture).toHaveBeenCalledWith('worker',{package:pkg.id,screen:-1,url:'http://localhost:3000',title:'New screen',selector:'',width:1440,height:900,channel:'chrome'});
});
it('retains input after capture failure and never sends automatically',async()=>{
 capture.mockRejectedValue(new Error('Playwright unavailable'));
 const props={pkg,onCaptured:vi.fn(),onClose:vi.fn()};const view=render(ReviewCapture,props);
 await fireEvent.input(screen.getByLabelText('Page URL'),{target:{value:'http://localhost:4000'}});
 await fireEvent.submit(screen.getByRole('button',{name:'Capture & save'}).closest('form')!);
 expect(await screen.findByRole('alert')).toHaveTextContent('Playwright unavailable');
 expect(props.onCaptured).not.toHaveBeenCalled();view.unmount();render(ReviewCapture,props);
 await waitFor(()=>expect(screen.getByLabelText('Page URL')).toHaveValue('http://localhost:4000'));expect(capture).toHaveBeenCalledTimes(1);
});
it('ignores an old completion when package selection changes',async()=>{
 let resolve!:(value:typeof pkg)=>void;capture.mockImplementationOnce(()=>new Promise(r=>resolve=r) as never);
 const done=vi.fn();const view=render(ReviewCapture,{pkg,initialURL:pkg.screens[0].url,onCaptured:done,onClose:vi.fn()});
 await fireEvent.submit(screen.getByRole('button',{name:'Capture & save'}).closest('form')!);
 await view.rerender({pkg:{...pkg,id:'f'.repeat(64)},onCaptured:done,onClose:vi.fn()});resolve(pkg);
 await Promise.resolve();await Promise.resolve();expect(done).not.toHaveBeenCalled();
});
it('updates the selected screenshot instead of exceeding the four-screen limit',async()=>{
 const four={...pkg,screens:Array.from({length:4},(_,i)=>({...pkg.screens[0],title:`Screen ${i}`}))};
 capture.mockResolvedValue(four);render(ReviewCapture,{pkg:four,initialScreen:2,onCaptured:vi.fn(),onClose:vi.fn()});
 await waitFor(()=>expect(screen.getByLabelText('Screenshot title')).toHaveValue('Screen 2'));
 await fireEvent.submit(screen.getByRole('button',{name:'Capture & save'}).closest('form')!);expect(capture.mock.calls[0][1].screen).toBe(2);
});
