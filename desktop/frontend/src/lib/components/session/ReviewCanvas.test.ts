import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import './talk.testkit';
import { DaemonService } from '@bindings/desktop';
import ReviewCanvas from './ReviewCanvas.svelte';
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
  await fireEvent.click(screen.getByRole('button',{name:'Mark area'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  await fireEvent.input(screen.getByRole('textbox',{name:'Screenshot comment'}),{target:{value:'Align this area'}});
  mounted.unmount();
  render(ReviewCanvas,{pkg,onComment});
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Next comment'}));
  expect(screen.getByRole('textbox',{name:'Screenshot comment'})).toHaveValue('Align this area');
  await fireEvent.click(screen.getByRole('button',{name:'Save'}));
  expect(onComment).toHaveBeenLastCalledWith(0,expect.stringContaining('After screenshot, area from top-left: x=50%, y=50%, width=20%, height=20%\nAlign this area'),undefined);
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
  await fireEvent.click(screen.getByRole('button',{name:'Mark area'}));
  const surface=screen.getByRole('button',{name:'Place comment on After screenshot'});
  const stage=surface.parentElement!, viewport=stage.closest<HTMLElement>('.viewport')!;
  let bounds={left:-100,top:-50,width:800,height:600};
  vi.spyOn(surface,'getBoundingClientRect').mockImplementation(()=>bounds as DOMRect);
  vi.spyOn(stage,'getBoundingClientRect').mockImplementation(()=>bounds as DOMRect);
  vi.spyOn(stage.closest('.canvas-area')!,'getBoundingClientRect').mockReturnValue({left:0,top:0,width:800,height:600} as DOMRect);
  Object.defineProperty(surface,'setPointerCapture',{value:vi.fn()});
  await fireEvent(surface,new MouseEvent('pointerdown',{bubbles:true,button:0,clientX:100,clientY:100}));
  await fireEvent(surface,new MouseEvent('pointermove',{bubbles:true,clientX:300,clientY:250}));
  await fireEvent(surface,new MouseEvent('pointerup',{bubbles:true}));
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


it('edits on the image with the info rail hidden, and closes without losing the draft',async()=>{
  render(ReviewCanvas,{pkg,onComment:()=>true});
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Screen details'}));
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
  for (const key of ['c','r','n','i','b','1','4','ArrowLeft','ArrowRight','ArrowUp','ArrowDown','Delete','Backspace','Enter','?']) {
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
  await fireEvent.keyDown(canvas,{key:'i'});
  expect(screen.queryByRole('complementary',{name:'Screen details'})).toBeNull();
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
