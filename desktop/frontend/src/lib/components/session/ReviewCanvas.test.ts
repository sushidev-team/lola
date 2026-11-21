import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import './talk.testkit';
import { DaemonService, PreviewService } from '@bindings/desktop';
import ReviewCanvas from './ReviewCanvas.svelte';
import { feedback } from '$lib/feedback.svelte';
import { markLocation, reviewSections } from '$lib/reviewmaterial';
const image = vi.mocked(DaemonService.ReviewPackageImage);
const pkg = {id:'package1',session:'s',screens:[{title:'Settings',before:'before',after:'after',caption:'A clearer form'},{title:'Phone',after:'phone'}]} as never;
beforeEach(()=>{vi.stubGlobal('ResizeObserver',class {observe(){} disconnect(){}});localStorage.clear(); image.mockReset(); image.mockResolvedValue({dataURL:'data:image/png;base64,aGVsbG8='});});
afterEach(()=>{cleanup();vi.unstubAllGlobals();});

it('compares with a keyboard range, switches views, and handles screens with no baseline',async()=>{
  render(ReviewCanvas,{pkg,onComment:()=>true});
  await screen.findByAltText('Settings — Before');
  const range=screen.getByRole('slider',{name:'Before and after comparison'});
  await fireEvent.keyDown(range,{key:'ArrowRight'});
  expect(range).toHaveAttribute('aria-valuenow','55');
  await fireEvent.click(screen.getByRole('button',{name:'Side by side'}));
  expect(screen.queryByRole('slider')).toBeNull();
  expect(screen.getByAltText('Settings — Before')).toBeInTheDocument();
  await fireEvent.click(screen.getByRole('button',{name:'Next'}));
  await screen.findByAltText('Phone — After');
  expect(screen.getByRole('button',{name:'Slider'})).toBeDisabled();
  expect(screen.getByRole('button',{name:'Before'})).toBeDisabled();
});

it('keeps region drafts across reopening, and queues coordinates with the comment',async()=>{
  const onComment=vi.fn().mockReturnValue(false);
  const mounted=render(ReviewCanvas,{pkg,onComment});
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  await fireEvent.input(screen.getByRole('textbox',{name:'Screenshot comment'}),{target:{value:'Align this area'}});
  mounted.unmount();
  render(ReviewCanvas,{pkg,onComment});
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Next comment'}));
  expect(screen.getByRole('textbox',{name:'Screenshot comment'})).toHaveValue('Align this area');
  await fireEvent.click(screen.getByRole('button',{name:'Save'}));
  expect(onComment).toHaveBeenLastCalledWith(0,expect.stringContaining('After screenshot, point from top-left: x=50%, y=50%\nAlign this area'),undefined);
  expect(screen.getByRole('textbox')).toHaveValue('Align this area');
  onComment.mockReturnValue(true);
  await fireEvent.click(screen.getByRole('button',{name:'Save'}));
  expect(screen.queryByRole('textbox')).toBeNull();
  await fireEvent.click(screen.getByRole('button',{name:'Comment 1'}));
  expect(screen.getByRole('textbox')).toHaveValue('Align this area');
  expect(screen.getByRole('button',{name:'Done'})).toBeEnabled();
  expect(screen.getByRole('button',{name:'Comment 1'})).toBeInTheDocument();
});

it('remembers the chosen comparison mode without saving temporary annotation or missing-baseline views',async()=>{
  const mounted=render(ReviewCanvas,{pkg,onComment:()=>true});
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Side by side'}));
  await fireEvent.click(screen.getByRole('button',{name:'Next'}));
  await screen.findByAltText('Phone — After');
  expect(screen.getByRole('button',{name:'After'})).toHaveAttribute('aria-pressed','true');
  await fireEvent.click(screen.getByRole('button',{name:'Previous'}));
  await screen.findByAltText('Settings — Before');
  expect(screen.getByRole('button',{name:'Side by side'})).toHaveAttribute('aria-pressed','true');
  await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
  expect(screen.getByRole('button',{name:'After'})).toHaveAttribute('aria-pressed','true');
  mounted.unmount();
  render(ReviewCanvas,{pkg,onComment:()=>true});
  await screen.findByAltText('Settings — Before');
  expect(screen.getByRole('button',{name:'Side by side'})).toHaveAttribute('aria-pressed','true');
});

it('rejects remote image sources and retries loading',async()=>{
  image.mockResolvedValueOnce({dataURL:'https://example.com/image.png'});
  render(ReviewCanvas,{pkg,onComment:()=>true});
  await screen.findByRole('alert');expect(screen.queryByRole('img')).toBeNull();
  await fireEvent.click(screen.getByRole('button',{name:'Retry image'}));
  await screen.findByAltText('Settings — After');
});

it('keeps headings inside fenced code and all detailed prose',()=>{
  expect(reviewSections('## Summary\nA short summary.\n## Details\n```md\n## literal heading\n```\nRemaining detail.')).toEqual([
    {title:'Summary',body:'A short summary.'},{title:'Details',body:'```md\n## literal heading\n```\nRemaining detail.'},
  ]);
  expect(markLocation({x:.1234,y:.5,w:0,h:0,side:'Before',text:''})).toBe('Before screenshot, point from top-left: x=12.3%, y=50%');
});


it('moves the divider from any point on the image, including a drag starting away from the handle',async()=>{
  render(ReviewCanvas,{pkg,onComment:()=>true});
  const surface=await screen.findByRole('slider',{name:'Before and after comparison'});
  vi.spyOn(surface.parentElement!,'getBoundingClientRect').mockReturnValue({left:100,top:50,width:400,height:300} as DOMRect);
  Object.defineProperty(surface,'setPointerCapture',{value:vi.fn()});
  await fireEvent(surface,new MouseEvent('pointerdown',{bubbles:true,button:0,clientX:140,clientY:80}));
  expect(surface).toHaveAttribute('aria-valuenow','10');
  await fireEvent(surface,new MouseEvent('pointermove',{bubbles:true,clientX:420,clientY:80}));
  expect(surface).toHaveAttribute('aria-valuenow','80');
  await fireEvent(surface,new MouseEvent('pointerup',{bubbles:true}));
  await fireEvent(surface,new MouseEvent('pointermove',{bubbles:true,clientX:200,clientY:80}));
  expect(surface).toHaveAttribute('aria-valuenow','80');
});

it('zooms around the pointer, keeps divider coordinates correct after scrolling, and resets to fit',async()=>{
  render(ReviewCanvas,{pkg,onComment:()=>true});
  const slider=await screen.findByRole('slider');
  const stage=slider.parentElement!, frame=stage.parentElement!;
  const viewport=frame.parentElement!;
  const initialWidth=parseFloat(frame.style.width);
  const rect=()=>({left:100-viewport.scrollLeft,top:50-viewport.scrollTop,width:parseFloat(frame.style.width),height:parseFloat(frame.style.width)*.625} as DOMRect);
  vi.spyOn(frame,'getBoundingClientRect').mockImplementation(rect);
  vi.spyOn(stage,'getBoundingClientRect').mockImplementation(rect);
  const anchor={clientX:340,clientY:200};
  expect(await fireEvent.wheel(viewport,{...anchor,deltaY:20,cancelable:true})).toBe(true);
  expect(screen.getByLabelText('Screenshot zoom')).toHaveValue('100');
  expect(await fireEvent.wheel(viewport,{...anchor,deltaY:-Math.log(2)*100,ctrlKey:true,cancelable:true})).toBe(false);
  expect(screen.getByLabelText('Screenshot zoom')).toHaveValue('200');
  expect(parseFloat(frame.style.width)).toBe(initialWidth*2);
  expect(viewport.scrollLeft).toBeCloseTo(240);
  expect(viewport.scrollTop).toBeCloseTo(150);
  Object.defineProperty(slider,'setPointerCapture',{value:vi.fn()});
  const imageBounds=rect();
  await fireEvent(slider,new MouseEvent('pointerdown',{bubbles:true,button:0,clientX:imageBounds.left+imageBounds.width*.75}));
  await fireEvent(slider,new MouseEvent('pointerup',{bubbles:true}));
  expect(slider).toHaveAttribute('aria-valuenow','75');
  expect(stage.querySelector('.divider')).toHaveStyle({left:'75%'});
  expect(stage.querySelector('.baseline')).toHaveStyle({clipPath:'inset(0 25% 0 0)'});
  await fireEvent.change(screen.getByLabelText('Screenshot zoom'),{target:{value:'400'}});
  expect(screen.getByRole('button',{name:'Zoom in'})).toBeDisabled();
  expect(slider).toHaveAttribute('aria-valuenow','75');
  await fireEvent.keyDown(screen.getByRole('region',{name:'Screenshot review canvas'}),{key:'0'});
  expect(parseFloat(frame.style.width)).toBe(initialWidth);
  expect(viewport.scrollLeft).toBe(0);expect(viewport.scrollTop).toBe(0);
  expect(screen.getByRole('button',{name:'Zoom out'})).toBeDisabled();
});

it('handles WebKit pinch gestures without intercepting ordinary scrolling',async()=>{
  render(ReviewCanvas,{pkg,onComment:()=>true});
  const viewport=(await screen.findByAltText('Settings — After')).closest('.viewport')!;
  const gesture=async(type:string,scale=1)=>{
    const event=new Event(type,{bubbles:true,cancelable:true});
    Object.assign(event,{scale,clientX:300,clientY:200});
    expect(await fireEvent(viewport,event)).toBe(false);
  };
  await gesture('gesturestart');await gesture('gesturechange',1.75);
  expect(screen.getByLabelText('Screenshot zoom')).toHaveValue('175');
  await gesture('gesturechange',2.5);await gesture('gestureend');
  expect(screen.getByLabelText('Screenshot zoom')).toHaveValue('250');
  await fireEvent.click(screen.getByRole('button',{name:'Zoom in'}));
  expect(screen.getByLabelText('Screenshot zoom')).toHaveValue('300');
  await fireEvent.click(screen.getByRole('button',{name:'Zoom out'}));
  expect(screen.getByLabelText('Screenshot zoom')).toHaveValue('200');
});

it('keeps area comments anchored while zooming and scrolling, including an open draft',async()=>{
  const resizeCallbacks:Array<()=>void>=[];
  vi.stubGlobal('ResizeObserver',class {
    constructor(callback:()=>void){resizeCallbacks.push(callback);}
    observe(){} disconnect(){}
  });
  const onComment=vi.fn().mockReturnValue(true);
  render(ReviewCanvas,{pkg,onComment});
  await screen.findByAltText('Settings — After');
  await fireEvent.change(screen.getByLabelText('Screenshot zoom'),{target:{value:'200'}});
  await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
  const surface=screen.getByRole('button',{name:'Place comment on After screenshot'});
  const stage=surface.parentElement!, viewport=stage.closest<HTMLElement>('.viewport')!;
  let bounds={left:-100,top:-50,width:800,height:600};
  vi.spyOn(surface,'getBoundingClientRect').mockImplementation(()=>bounds as DOMRect);
  vi.spyOn(stage,'getBoundingClientRect').mockImplementation(()=>bounds as DOMRect);
  vi.spyOn(stage.closest('.canvas-area')!,'getBoundingClientRect').mockReturnValue({left:0,top:0,width:800,height:600} as DOMRect);
  Object.defineProperty(surface,'setPointerCapture',{value:vi.fn()});
  await fireEvent(surface,new MouseEvent('pointerdown',{bubbles:true,button:0,clientX:100,clientY:100}));
  await fireEvent(surface,new MouseEvent('pointermove',{bubbles:true,clientX:300,clientY:250}));
  await fireEvent(surface,new MouseEvent('pointerup',{bubbles:true,clientX:300,clientY:250}));
  await fireEvent.input(screen.getByRole('textbox'),{target:{value:'Keep this position'}});
  const pin=screen.getByRole('button',{name:'Comment 1'});
  const region=screen.getByRole('button',{name:'Open area comment 1'});
  expect(pin).toHaveStyle({left:'25%',top:'25%'});
  expect(region).toHaveStyle({left:'25%',top:'25%',width:'25%',height:'25%'});
  const dialog=screen.getByRole('dialog');
  Object.defineProperties(dialog,{offsetWidth:{value:300},offsetHeight:{value:200}});
  await fireEvent.click(screen.getByRole('button',{name:'Zoom in'}));
  expect(screen.queryByText('Save changes?')).toBeNull();
  expect(screen.getByRole('textbox')).toHaveValue('Keep this position');
  bounds={left:-100,top:-50,width:1200,height:900};
  resizeCallbacks.at(-1)!();
  await fireEvent.scroll(viewport);
  expect(dialog).toHaveStyle({left:'220px',top:'191px'});
  bounds={...bounds,left:-200,top:-100};
  await fireEvent.scroll(viewport);
  expect(dialog).toHaveStyle({left:'120px',top:'141px'});
  expect(pin).toHaveStyle({left:'25%',top:'25%'});
  expect(region).toHaveStyle({width:'25%',height:'25%'});
  await fireEvent.click(screen.getByRole('button',{name:'Save'}));
  expect(onComment).toHaveBeenCalledWith(0,expect.stringContaining('x=25%, y=25%, width=25%, height=25%\nKeep this position'),undefined);
});


it('edits on the image and closes without losing the draft',async()=>{
  render(ReviewCanvas,{pkg,onComment:()=>true});
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
  expect(screen.queryByRole('complementary',{name:'Screen details'})).toBeNull();
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  const dialog=screen.getByRole('dialog',{name:'Comment 1 · After'});
  expect(dialog.closest('.canvas-area')).not.toBeNull();
  expect(dialog.closest('.info-rail')).toBeNull();
  await fireEvent.input(screen.getByRole('textbox',{name:'Screenshot comment'}),{target:{value:'Keep this draft at the pin'}});
  await fireEvent.keyDown(dialog,{key:'Escape'});
  expect(screen.getByText('Save changes?')).toBeInTheDocument();
  await fireEvent.click(screen.getByRole('button',{name:'Keep editing'}));
  expect(screen.queryByText('Save changes?')).toBeNull();
  expect(screen.getByRole('textbox')).toHaveValue('Keep this draft at the pin');
  await fireEvent.click(screen.getByRole('button',{name:'Save'}));
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(screen.getByRole('button',{name:'Comment 1'})).toHaveFocus();
  await fireEvent.click(screen.getByRole('button',{name:'Comment 1'}));
  expect(screen.getByRole('textbox')).toHaveValue('Keep this draft at the pin');
});

it('asks before saving on click-away, closes unchanged comments, and keeps failed saves open',async()=>{
  const onComment=vi.fn().mockReturnValue(false);
  render(ReviewCanvas,{pkg,onComment});
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  await fireEvent.input(screen.getByRole('textbox',{name:'Screenshot comment'}),{target:{value:'Save when I leave'}});
  await fireEvent.click(screen.getByRole('textbox',{name:'Screenshot comment'}));
  expect(onComment).not.toHaveBeenCalled();
  await fireEvent.click(screen.getByAltText('Settings — After'));
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  expect(screen.getByRole('textbox')).toHaveValue('Save when I leave');
  expect(onComment).not.toHaveBeenCalled();
  expect(screen.getByText('Save changes?')).toBeInTheDocument();
  await fireEvent.click(screen.getByRole('button',{name:'Save'}));
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  onComment.mockReturnValue(true);
  await fireEvent.click(screen.getByRole('button',{name:'Save'}));
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(onComment).toHaveBeenCalledTimes(2);
  await fireEvent.click(screen.getByRole('button',{name:'Comment 1'}));
  expect(screen.getByRole('textbox')).toHaveValue('Save when I leave');
  await fireEvent.click(screen.getByAltText('Settings — After'));
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(onComment).toHaveBeenCalledTimes(2);
});


it('discards unsaved edits while retaining the saved annotation and pending feedback',async()=>{
  const onComment=vi.fn().mockReturnValue(true);
  render(ReviewCanvas,{pkg,onComment});
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  await fireEvent.input(screen.getByRole('textbox'),{target:{value:'Original'}});
  await fireEvent.click(screen.getByRole('button',{name:'Save'}));
  await fireEvent.click(screen.getByRole('button',{name:'Comment 1'}));
  await fireEvent.input(screen.getByRole('textbox'),{target:{value:'Unwanted edit'}});
  await fireEvent.click(screen.getByRole('button',{name:'Close comment'}));
  await fireEvent.click(screen.getByRole('button',{name:'Discard'}));
  expect(screen.queryByRole('dialog')).toBeNull();
  await fireEvent.click(screen.getByRole('button',{name:'Comment 1'}));
  expect(screen.getByRole('textbox')).toHaveValue('Original');
  expect(onComment).toHaveBeenCalledTimes(1);
  await fireEvent.click(screen.getByRole('button',{name:'Close comment'}));
  await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  await fireEvent.input(screen.getByRole('textbox'),{target:{value:'New draft'}});
  await fireEvent.click(screen.getByRole('button',{name:'Close comment'}));
  await fireEvent.click(screen.getByRole('button',{name:'Discard'}));
  expect(screen.queryByRole('button',{name:'Comment 2'})).toBeNull();
  expect(screen.getByRole('button',{name:'Comment 1'})).toBeInTheDocument();
});


it('arms comment tools with C and R, focuses placement, and cancels with V or Escape',async()=>{
  render(ReviewCanvas,{pkg,onComment:()=>true});await screen.findByAltText('Settings — After');
  const canvas=screen.getByRole('region',{name:'Screenshot review canvas'});
  await fireEvent.keyDown(canvas,{key:'c'});
  expect(screen.getByRole('button',{name:'Add pin'})).toHaveAttribute('aria-pressed','true');
  expect(screen.getByRole('button',{name:'Place comment on After screenshot'})).toHaveFocus();
  await fireEvent.keyDown(document.activeElement!,{key:'r'});
  expect(screen.getByRole('button',{name:'Mark area'})).toHaveAttribute('aria-pressed','true');
  await fireEvent.keyDown(document.activeElement!,{key:'Escape'});
  expect(screen.queryByRole('button',{name:'Place comment on After screenshot'})).toBeNull();
  await fireEvent.keyDown(canvas,{key:'r'});
  await fireEvent.keyDown(document.activeElement!,{key:'v'});
  expect(screen.queryByRole('button',{name:'Place comment on After screenshot'})).toBeNull();
});

it.each(['metaKey','ctrlKey'])('saves with %s+Enter, preserving normal typing and failed drafts',async modifier=>{
  const onComment=vi.fn().mockReturnValue(false);
  render(ReviewCanvas,{pkg,onComment});await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  const field=screen.getByRole('textbox');
  await fireEvent.keyDown(field,{key:'Enter',[modifier]:true});
  expect(onComment).not.toHaveBeenCalled();
  await fireEvent.input(field,{target:{value:'Keep every line'}});
  for (const key of ['c','r','n','b','1','4','ArrowLeft','ArrowRight','ArrowUp','ArrowDown','Delete','Backspace','Enter','?']) {
    expect(await fireEvent.keyDown(field,{key})).toBe(true);
  }
  expect(field).toHaveValue('Keep every line');
  expect(onComment).not.toHaveBeenCalled();
  await fireEvent.keyDown(field,{key:'Enter',[modifier]:true,isComposing:true});
  await fireEvent.keyDown(field,{key:'Enter',[modifier]:true,repeat:true});
  expect(onComment).not.toHaveBeenCalled();
  await fireEvent.keyDown(field,{key:'Enter',[modifier]:true});
  expect(onComment).toHaveBeenCalledTimes(1);
  expect(screen.getByRole('textbox')).toHaveValue('Keep every line');
  onComment.mockReturnValue(true);
  await fireEvent.keyDown(field,{key:'Enter',[modifier]:true});
  expect(onComment).toHaveBeenCalledTimes(2);
  expect(screen.queryByRole('textbox')).toBeNull();
});

it('navigates images and comparison modes, zooms, and exposes shortcut help without stealing slider keys',async()=>{
  const toggle=vi.fn();
  render(ReviewCanvas,{pkg,onComment:()=>true,onToggleFeedback:toggle});await screen.findByAltText('Settings — After');
  const canvas=screen.getByRole('region',{name:'Screenshot review canvas'});
  const slider=screen.getByRole('slider');
  await fireEvent.keyDown(slider,{key:'ArrowRight'});
  expect(slider).toHaveAttribute('aria-valuenow','55');
  expect(screen.getByAltText('Settings — After')).toBeInTheDocument();
  for (const [key,label] of [['2','Side by side'],['3','Before'],['4','After'],['1','Slider']]) {
    await fireEvent.keyDown(canvas,{key});
    expect(screen.getByRole('button',{name:label})).toHaveAttribute('aria-pressed','true');
  }
  for (const [key,value] of [['+','150'],['=','200'],['-','150'],['0','100']]) {
    await fireEvent.keyDown(canvas,{key});expect(screen.getByLabelText('Screenshot zoom')).toHaveValue(value);
  }
  await fireEvent.keyDown(canvas,{key:'b'});expect(toggle).toHaveBeenCalledOnce();
  await fireEvent.keyDown(canvas,{key:'?'});
  expect(screen.getByRole('complementary',{name:'Screenshot shortcuts'})).toBeInTheDocument();
  await fireEvent.keyDown(canvas,{key:'Escape'});
  expect(screen.queryByRole('complementary',{name:'Screenshot shortcuts'})).toBeNull();
  await fireEvent.keyDown(canvas,{key:'ArrowRight'});
  expect(screen.getByRole('slider')).toHaveAttribute('aria-valuenow','60');
  await fireEvent.keyDown(canvas,{key:'ArrowLeft'});
  expect(screen.getByRole('slider')).toHaveAttribute('aria-valuenow','55');
  await fireEvent.keyDown(canvas,{key:'ArrowDown'});await screen.findByAltText('Phone — After');
  await fireEvent.keyDown(canvas,{key:'1'});
  expect(screen.getByRole('button',{name:'After'})).toHaveAttribute('aria-pressed','true');
  await fireEvent.keyDown(canvas,{key:'ArrowDown'});expect(screen.getByAltText('Phone — After')).toBeInTheDocument();
  await fireEvent.keyDown(canvas,{key:'ArrowLeft'});expect(screen.getByAltText('Phone — After')).toBeInTheDocument();
  await fireEvent.keyDown(canvas,{key:'ArrowUp'});await screen.findByAltText('Settings — After');
});

it('ignores platform chords and composition, and prevents shortcuts from losing unsaved edits',async()=>{
  const onComment=vi.fn().mockReturnValue(true);
  render(ReviewCanvas,{pkg,onComment});await screen.findByAltText('Settings — After');
  const canvas=screen.getByRole('region',{name:'Screenshot review canvas'});
  for(const event of [{key:'c',metaKey:true},{key:'r',ctrlKey:true},{key:'c',altKey:true},{key:'c',isComposing:true},{key:'c',repeat:true}]) {
    expect(await fireEvent.keyDown(canvas,event)).toBe(true);
  }
  expect(screen.queryByRole('button',{name:'Place comment on After screenshot'})).toBeNull();
  await fireEvent.keyDown(canvas,{key:'c'});
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  await fireEvent.input(screen.getByRole('textbox'),{target:{value:'Unsaved'}});
  await fireEvent.keyDown(screen.getByRole('button',{name:'Close comment'}),{key:'ArrowDown'});
  expect(screen.getByText('Save changes?')).toBeInTheDocument();
  expect(screen.getByAltText('Settings — After')).toBeInTheDocument();
  expect(onComment).not.toHaveBeenCalled();
});

it('cycles saved comments and deletes a focused pin without intercepting text deletion',async()=>{
  const remove=vi.fn();render(ReviewCanvas,{pkg,onComment:()=>true,onDeleteComment:remove});await screen.findByAltText('Settings — After');
  const canvas=screen.getByRole('region',{name:'Screenshot review canvas'});
  for (const text of ['First','Second']) {
    await fireEvent.keyDown(canvas,{key:'c'});
    await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
    await fireEvent.input(screen.getByRole('textbox'),{target:{value:text}});
    await fireEvent.keyDown(screen.getByRole('textbox'),{key:'Enter',metaKey:true});
  }
  await fireEvent.keyDown(canvas,{key:'n'});expect(screen.getByRole('textbox')).toHaveValue('First');
  await fireEvent.keyDown(screen.getByRole('textbox'),{key:'Escape'});
  await fireEvent.keyDown(canvas,{key:'N',shiftKey:true});expect(screen.getByRole('textbox')).toHaveValue('Second');
  await fireEvent.keyDown(screen.getByRole('textbox'),{key:'Delete'});expect(remove).not.toHaveBeenCalled();
  await fireEvent.keyDown(screen.getByRole('textbox'),{key:'Escape'});
  await fireEvent.keyDown(screen.getByRole('button',{name:'Comment 2'}),{key:'Delete'});
  expect(remove).toHaveBeenCalledWith(0,expect.stringContaining('Second'));
  expect(screen.queryByRole('button',{name:'Comment 2'})).toBeNull();
});


it.each(['Backspace','Delete'])('deletes the selected comment with Command+%s while editing, leaving other fields alone',async key=>{
  const remove=vi.fn();render(ReviewCanvas,{pkg,onComment:()=>true,onDeleteComment:remove});
  await screen.findByAltText('Settings — After');
  const canvas=screen.getByRole('region',{name:'Screenshot review canvas'});
  expect(await fireEvent.keyDown(canvas,{key,metaKey:true})).toBe(true);
  await fireEvent.click(screen.getByRole('button',{name:'Add pin'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  await fireEvent.input(screen.getByRole('textbox'),{target:{value:'Selected comment'}});
  await fireEvent.keyDown(screen.getByRole('textbox'),{key:'Enter',metaKey:true});
  await fireEvent.click(screen.getByRole('button',{name:'Comment 1'}));
  const field=screen.getByRole('textbox');
  const unrelated=document.createElement('textarea');canvas.append(unrelated);
  expect(await fireEvent.keyDown(unrelated,{key,metaKey:true})).toBe(true);
  unrelated.remove();
  await fireEvent.keyDown(field,{key,metaKey:true,repeat:true});
  await fireEvent.keyDown(field,{key,metaKey:true,isComposing:true});
  expect(remove).not.toHaveBeenCalled();
  expect(await fireEvent.keyDown(field,{key,metaKey:true})).toBe(false);
  expect(remove).toHaveBeenCalledWith(0,expect.stringContaining('Selected comment'));
  expect(screen.queryByRole('textbox')).toBeNull();
  expect(screen.queryByRole('button',{name:'Comment 1'})).toBeNull();
});

it('places arrows and ellipses from the keyboard without a comment, and adds words on a double click',async()=>{
  const onComment=vi.fn().mockReturnValue(true);
  render(ReviewCanvas,{pkg,onComment});
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Draw arrow'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  expect(screen.queryByRole('textbox',{name:'Screenshot comment'})).toBeNull();
  expect(onComment).toHaveBeenLastCalledWith(0,expect.stringContaining('After screenshot, arrow from top-left: x=40%, y=50% to x=60%, y=50%\nMarked without a comment.'),undefined);
  await fireEvent.click(screen.getByRole('button',{name:'Draw ellipse'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  expect(onComment).toHaveBeenLastCalledWith(0,expect.stringContaining('After screenshot, ellipse from top-left: x=50%, y=50%, width=20%, height=20%\nMarked without a comment.'),undefined);
  expect(screen.queryByRole('button',{name:/^Comment \d/})).toBeNull();
  const sent=onComment.mock.calls.at(-1)![1];
  await fireEvent.dblClick(screen.getByRole('button',{name:'Open ellipse comment'}));
  await fireEvent.input(screen.getByRole('textbox',{name:'Screenshot comment'}),{target:{value:'Too round'}});
  await fireEvent.click(screen.getByRole('button',{name:'Save'}));
  expect(onComment).toHaveBeenLastCalledWith(0,expect.stringContaining('ellipse from top-left: x=50%, y=50%, width=20%, height=20%\nToo round'),sent);
});

it('arms a drawing tool from its shortcut and refuses freehand without a pointer',async()=>{
  render(ReviewCanvas,{pkg,onComment:()=>true});
  await screen.findByAltText('Settings — After');
  const section=screen.getByRole('region',{name:'Screenshot review canvas'});
  await fireEvent.keyDown(section,{key:'l'});
  expect(screen.getByRole('button',{name:'Draw line'})).toHaveAttribute('aria-pressed','true');
  await fireEvent.keyDown(section,{key:'p'});
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  expect(screen.getByText(/Freehand drawing needs a pointer/)).toHaveAttribute('role','status');
});

it('ends an arrow where the pointer is released, even without a final move',async()=>{
  const onComment=vi.fn().mockReturnValue(true);
  render(ReviewCanvas,{pkg,onComment});
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Draw arrow'}));
  const surface=screen.getByRole('button',{name:'Place comment on After screenshot'});
  surface.getBoundingClientRect=()=>({left:0,top:0,width:100,height:100,right:100,bottom:100,x:0,y:0,toJSON(){}}) as DOMRect;
  surface.setPointerCapture=()=>{};
  const at=(type:string,x:number,y:number)=>{const e=new MouseEvent(type,{clientX:x,clientY:y,button:0,bubbles:true,cancelable:true});Object.defineProperty(e,'pointerId',{value:1});return e;};
  await fireEvent(surface,at('pointerdown',10,20));
  await fireEvent(surface,at('pointermove',40,20));
  await fireEvent(surface,at('pointerup',60,20));
  expect(onComment).toHaveBeenLastCalledWith(0,expect.stringContaining('arrow from top-left: x=10%, y=20% to x=60%, y=20%'),undefined);
});

it('locks a line to the nearer axis while Shift is held',async()=>{
  const onComment=vi.fn().mockReturnValue(true);
  render(ReviewCanvas,{pkg,onComment});
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Draw line'}));
  const surface=screen.getByRole('button',{name:'Place comment on After screenshot'});
  surface.getBoundingClientRect=()=>({left:0,top:0,width:100,height:100,right:100,bottom:100,x:0,y:0,toJSON(){}}) as DOMRect;
  surface.setPointerCapture=()=>{};
  const at=(type:string,x:number,y:number,shiftKey=false)=>{const e=new MouseEvent(type,{clientX:x,clientY:y,button:0,shiftKey,bubbles:true,cancelable:true});Object.defineProperty(e,'pointerId',{value:1});return e;};
  await fireEvent(surface,at('pointerdown',10,20));
  await fireEvent(surface,at('pointermove',60,28,true));
  await fireEvent(surface,at('pointerup',60,28,true));
  expect(onComment).toHaveBeenLastCalledWith(0,expect.stringContaining('line from top-left: x=10%, y=20% to x=60%, y=20%'),undefined);
});

it('opens the comment of a saved drawing by double click while a drawing tool is still armed',async()=>{
  render(ReviewCanvas,{pkg,onComment:()=>true});
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Draw arrow'}));
  const surface=screen.getByRole('button',{name:'Place comment on After screenshot'});
  await fireEvent.click(surface);
  expect(screen.queryByRole('textbox',{name:'Screenshot comment'})).toBeNull();
  await fireEvent.dblClick(surface);
  expect(screen.getByRole('textbox',{name:'Screenshot comment'})).toBeInTheDocument();
});
it.each(['s', 's~web'])('opens the matching live page inline for a screenshot from %s', async session => {
  const url = 'http://localhost:3000/login';
  const origin = 'http://preview.localhost:5555';
  const open = vi.mocked(PreviewService.Open).mockReset().mockResolvedValue(origin + '/login');
  const steps = [{ do: 'fill', selector: '#email', value: 'review@example.test' }, { do: 'goto', path: '/settings' }];
  const marks = [{ selector: '.legal' }];
  render(ReviewCanvas, { pkg: { ...pkg as object, session,
    screens: [{ title: 'Settings', after: 'after', url }],
    pages: [{ title: 'Settings panels', url, steps, marks }],
  } as never, onComment: () => true });
  expect(screen.getByRole('status', {name:'Page 1 of 2'})).toBeInTheDocument();
  expect(open).toHaveBeenCalledExactlyOnceWith('s', url);
  await fireEvent.click(await screen.findByRole('button', { name: /Open live/ }));
  const frame = await screen.findByTitle('Preview of localhost:3000/login') as HTMLIFrameElement;
  expect(PreviewService.Open).toHaveBeenCalledWith('s', url);
  expect(image).toHaveBeenCalledWith(session, 'package1', 'after');
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(screen.getByRole('status', {name:'Page 2 of 2'})).toBeInTheDocument();
  const post = vi.spyOn(frame.contentWindow!, 'postMessage');
  const ready = (sender: string) => window.dispatchEvent(new MessageEvent('message', {
    source: frame.contentWindow, origin: sender, data: { source: 'lola-preview', type: 'ready', url: origin + '/login' },
  }));
  ready('http://other.localhost');
  expect(post).not.toHaveBeenCalled();
  ready(origin);
  expect(post).toHaveBeenCalledWith(expect.objectContaining({ spec: expect.objectContaining({ steps, marks }) }), origin);
  await fireEvent.click(screen.getByRole('button', { name: 'Previous' }));
  expect(await screen.findByAltText('Settings — After')).toBeInTheDocument();
  await fireEvent.click(screen.getByRole('button', { name: 'Next' }));
  expect(screen.getByTitle('Preview of localhost:3000/login')).toBe(frame);
  expect(open).toHaveBeenCalledTimes(1);
});


it('restores numbered live comments, updates the selected draft in place and clears removed markers', async () => {
  const session = 'badge-session', url = 'http://localhost:3000/settings';
  vi.mocked(PreviewService.Open).mockResolvedValue(url);
  feedback.addElement(session, { url, selector: '#first', note: 'Spacing' });
  feedback.addElement(session, { url, selector: '#second', note: 'Alignment' });
  try {
    render(ReviewCanvas, { pkg: { ...pkg as object, session, screens: [], pages: [{ title: 'Settings', url }] } as never, onComment: () => true });
    const frame = await screen.findByTitle('Preview of localhost:3000/settings') as HTMLIFrameElement;
    const post = vi.spyOn(frame.contentWindow!, 'postMessage');
    const announce = (data: object) => fireEvent(window, new MessageEvent('message', {
      source: frame.contentWindow, origin: new URL(url).origin, data: { source: 'lola-preview', ...data },
    }));
    await announce({ type: 'ready', documentId: 'test', url });
    const geometry = async () => {
      const anchors = post.mock.calls.map(([m]) => m).findLast(m => m.type === 'comment-anchors' && m.anchors.length);
      await announce({ type: 'comment-bounds', revision: anchors.revision, rects: anchors.anchors.map((a: { number: number }) => ({ number: a.number, x: 20, y: 40 * a.number, w: 70, h: 30 })) });
    };
    await geometry();
    await fireEvent.click(screen.getByRole('button', { name: 'Live comment 2' }));
    const field = screen.getByRole('textbox', { name: 'Element comment' });
    expect(field).toHaveValue('Alignment');
    await fireEvent.input(field, { target: { value: 'Align left' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    expect(feedback.elementsFor(session).map(e => e.note)).toEqual(['Spacing', 'Align left']);
    await geometry();
    expect(screen.getByRole('button', { name: 'Live comment 2' })).toHaveAttribute('title', 'Align left');
    await fireEvent.click(screen.getByRole('button', { name: 'Live comment 2' }));
    await fireEvent.click(screen.getByRole('button', { name: 'Delete comment' }));
    expect(feedback.elementsFor(session).map(e => e.note)).toEqual(['Spacing']);
    await geometry();
    expect(screen.queryByRole('button', { name: 'Live comment 2' })).toBeNull();
    expect(document.querySelectorAll('.comment-border')).toHaveLength(1);
    await fireEvent.click(screen.getByRole('button', { name: 'Live comment 1' }));
    await fireEvent.keyDown(screen.getByRole('textbox', { name: 'Element comment' }), { key: 'Backspace', metaKey: true });
    expect(feedback.elementsFor(session)).toHaveLength(0);
    expect(screen.queryByRole('button', { name: 'Live comment 1' })).toBeNull();
  } finally { feedback.clear(session); }
});

it('starts at the prepared device size and resizes the same live document', async () => {
  const url = 'http://localhost:3000/settings';
  const open = vi.mocked(PreviewService.Open).mockReset().mockResolvedValue(url);
  render(ReviewCanvas, { pkg: { ...pkg as object, screens: [], pages: [{ title: 'Settings', url, device: 'tablet' }] } as never, onComment: () => true });
  const frame = await screen.findByTitle('Preview of localhost:3000/settings') as HTMLIFrameElement;
  const post = vi.spyOn(frame.contentWindow!, 'postMessage');
  expect(screen.getByRole('button', { name: 'Tablet', pressed: true })).toBeVisible();
  for (const [name, width] of [['Mobile', '390px'], ['Full width', '100%'], ['Tablet', '820px']]) {
    await fireEvent.click(screen.getByRole('button', { name }));
    expect(screen.getByRole('button', { name, pressed: true })).toBeVisible();
    expect(frame.style.width).toBe(width);
    expect(screen.getByTitle(frame.title)).toBe(frame);
    expect(frame.src).toBe(url);
  }
  expect(open).toHaveBeenCalledTimes(1);
  expect(post).not.toHaveBeenCalled();
});

it('returns from a shared live page to the screenshot that opened it in the chosen mode', async () => {
  const url = 'http://localhost:3000/settings';
  vi.mocked(PreviewService.Open).mockResolvedValue('http://preview.localhost:5555/settings');
  render(ReviewCanvas, { pkg: { id:'paired', session:'s', pages:[{title:'Settings',url}],
    screens:['Profile','Legal'].map(title => ({title, url, before:'before', after:'after'})) } as never,
    onComment:() => true });
  await fireEvent.click(screen.getByRole('button', {name:'Next'}));
  await fireEvent.click(screen.getByRole('button', {name:'Open live'}));
  expect(screen.getByRole('button', {name:'Open live'})).toHaveAttribute('aria-pressed','true');
  await fireEvent.keyDown(screen.getByRole('button', {name:'Before'}), {key:'3'});
  expect(await screen.findByAltText('Legal — Before')).toBeInTheDocument();
  expect(screen.getByRole('button', {name:'Before'})).toHaveAttribute('aria-pressed','true');
});

it('dismisses an instruction on hover per review item and keeps an explicitly reopened note readable', async () => {
  render(ReviewCanvas, {pkg, onComment:() => true});
  const note = screen.getByRole('complementary', {name:'Review instruction'});
  const toggle = screen.getByRole('button', {name:'Review instruction'});
  expect(note).toHaveTextContent('A clearer form');
  const moveMouse = (element: HTMLElement) => fireEvent(element, Object.assign(new Event('pointermove', {bubbles:true}), {pointerType:'mouse'}));
  await moveMouse(note);
  expect(toggle).toHaveAttribute('aria-expanded','false');
  await fireEvent.click(screen.getByRole('button', {name:'Next'}));
  expect(screen.queryByRole('button', {name:'Review instruction'})).toBeNull();
  await fireEvent.click(screen.getByRole('button', {name:'Previous'}));
  const reopened = screen.getByRole('button', {name:'Review instruction'});
  expect(reopened).toHaveAttribute('aria-expanded','false');
  await fireEvent.click(reopened);
  const restored = screen.getByRole('complementary', {name:'Review instruction'});
  await moveMouse(restored);
  expect(restored).toBeVisible();
  await fireEvent.keyDown(reopened, {key:'i'});
  expect(restored).not.toBeVisible();
});
