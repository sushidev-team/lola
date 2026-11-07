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
  expect(screen.getByText('Before capture unavailable')).toBeInTheDocument();
});

it('keeps region drafts across screen changes, and queues coordinates with the comment',async()=>{
  const onComment=vi.fn().mockReturnValue(false);
  render(ReviewCanvas,{pkg,onComment});
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Mark area'}));
  await fireEvent.click(screen.getByRole('button',{name:'Place comment on After screenshot'}));
  await fireEvent.input(screen.getByRole('textbox',{name:'Screenshot comment'}),{target:{value:'Align this area'}});
  await fireEvent.keyDown(screen.getByRole('dialog'),{key:'Escape'});
  await fireEvent.click(screen.getByRole('button',{name:'Next'}));
  await screen.findByAltText('Phone — After');
  expect(screen.queryByRole('textbox')).toBeNull();
  await fireEvent.click(screen.getByRole('button',{name:'Previous'}));
  await screen.findByAltText('Settings — After');
  await fireEvent.click(screen.getByRole('button',{name:'Next comment'}));
  expect(screen.getByRole('textbox',{name:'Screenshot comment'})).toHaveValue('Align this area');
  await fireEvent.click(screen.getByRole('button',{name:'Add comment to feedback'}));
  expect(onComment).toHaveBeenLastCalledWith(0,expect.stringContaining('After screenshot, area from top-left: x=50%, y=50%, width=20%, height=20%\nAlign this area'),undefined);
  expect(screen.getByRole('textbox')).toHaveValue('Align this area');
  onComment.mockReturnValue(true);
  await fireEvent.click(screen.getByRole('button',{name:'Add comment to feedback'}));
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
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(screen.getByRole('button',{name:'Comment 1'})).toHaveFocus();
  await fireEvent.click(screen.getByRole('button',{name:'Comment 1'}));
  expect(screen.getByRole('textbox',{name:'Screenshot comment'})).toHaveValue('Keep this draft at the pin');
});

it('saves changed text on click-away, closes unchanged comments without resaving, and keeps failed saves open',async()=>{
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
  onComment.mockReturnValue(true);
  await fireEvent.click(screen.getByAltText('Settings — After'));
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(onComment).toHaveBeenCalledTimes(2);
  await fireEvent.click(screen.getByRole('button',{name:'Comment 1'}));
  expect(screen.getByRole('textbox')).toHaveValue('Save when I leave');
  await fireEvent.click(screen.getByAltText('Settings — After'));
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(onComment).toHaveBeenCalledTimes(2);
});
