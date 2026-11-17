import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render } from '@testing-library/svelte';
import GitAvatar from './GitAvatar.svelte';
const png='data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==';
const gif='data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7';
afterEach(cleanup);
describe('Git avatars',()=>{
 it('shows a pending shimmer and replaces initials when a validated cached raster loads',async()=>{
   const view=render(GitAvatar,{name:'Martin Reitschmied',src:png,loading:true});
   expect(view.container.querySelector('.git-avatar')).toHaveClass('avatar-loading');
   const candidate=view.container.querySelector<HTMLImageElement>('.avatar-pending')!;expect(candidate).toHaveAttribute('src',png);
   await fireEvent.load(candidate);
   expect(view.container.querySelector('.avatar-image')).toHaveAttribute('src',png);
   expect(view.container.querySelector('.git-avatar')).not.toHaveClass('avatar-loading');
 });
 it('retains the displayed image while a fresh avatar loads and if that refresh fails',async()=>{
   const view=render(GitAvatar,{name:'Martin',src:png});await fireEvent.load(view.container.querySelector('.avatar-pending')!);
   await view.rerender({src:gif,loading:true});
   expect(view.container.querySelector('.avatar-image')).toHaveAttribute('src',png);
   expect(view.container.querySelector('.avatar-pending')).toHaveAttribute('src',gif);
   expect(view.container.querySelector('.git-avatar')).not.toHaveClass('avatar-loading');
   await fireEvent.error(view.container.querySelector('.avatar-pending')!);
   expect(view.container.querySelector('.avatar-image')).toHaveAttribute('src',png);expect(view.container.querySelector('.avatar-pending')).toBeNull();
 });
 it('updates a displayed image only after the new source finishes loading',async()=>{
   const view=render(GitAvatar,{name:'Martin',src:png});await fireEvent.load(view.container.querySelector('.avatar-pending')!);
   await view.rerender({src:gif});await fireEvent.load(view.container.querySelector('.avatar-pending')!);
   expect(view.container.querySelector('.avatar-image')).toHaveAttribute('src',gif);expect(view.container.querySelector('.avatar-pending')).toBeNull();
 });
 it('stops shimmer after an unknown profile settles or its image fails',async()=>{
   const view=render(GitAvatar,{name:'Martin',loading:true});expect(view.container.querySelector('.git-avatar')).toHaveClass('avatar-loading');
   await view.rerender({loading:false});expect(view.container.querySelector('.git-avatar')).not.toHaveClass('avatar-loading');
   await view.rerender({src:png});await fireEvent.error(view.container.querySelector('.avatar-pending')!);
   expect(view.container.querySelector('.git-avatar')).not.toHaveClass('avatar-loading');expect(view.container).toHaveTextContent('M');
 });
 it.each([
   'data:image/svg+xml;base64,PHN2Zy8+',
   'data:image/png;base64,PHN2Zy8+',
   'data:text/html;base64,PHNjcmlwdC8+',
   'https://evil.test/avatar.png','http://github.com/avatar.png','https://user:secret@github.com/avatar.png',
   'data:image/png;base64,'+'A'.repeat(350000),
 ])('rejects unsupported or oversized sources',src=>{
   const view=render(GitAvatar,{name:'Martin',src});expect(view.container.querySelector('img')).toBeNull();
 });
 it('keeps allowlisted HTTPS fixture sources working',()=>{
   const view=render(GitAvatar,{name:'Martin',src:'https://avatars.githubusercontent.com/u/42?s=40'});
   expect(view.container.querySelector('img')).toHaveAttribute('src','https://avatars.githubusercontent.com/u/42?s=40');
 });
});
