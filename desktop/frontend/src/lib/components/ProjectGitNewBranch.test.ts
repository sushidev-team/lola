import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import ProjectGitNewBranch from './ProjectGitNewBranch.svelte';
import { ProjectGitState } from '$lib/projectgit.svelte';
vi.mock('@bindings/desktop', () => ({ ProjectGitService: {} }));
afterEach(() => { cleanup(); vi.restoreAllMocks(); });
const head = 'a'.repeat(40), remote = 'b'.repeat(40);
function setup() {
  const git = new ProjectGitState('project', '/tree');
  const create = vi.spyOn(git, 'createBranch').mockResolvedValue('/tree');
  const onClose = vi.fn(), onCreated = vi.fn();
  const view = render(ProjectGitNewBranch, { git, start:{sha:head,label:'main'}, refs:[{name:'origin/feature',fullName:'refs/remotes/origin/feature',sha:remote,remote:true,current:false}], historyRef:'refs/heads/main', limit:400, onClose, onCreated });
  return { git, create, onClose, onCreated, view };
}
it('focuses the name and creates only on submission at the displayed pinned commit', async () => {
  const { create, onCreated } = setup();
  const name = screen.getByRole('textbox', {name:'Branch name'});
  await waitFor(() => expect(name).toHaveFocus());
  expect(screen.getByRole('button', {name:'Create branch'})).toBeDisabled();
  expect(create).not.toHaveBeenCalled();
  await fireEvent.input(name, {target:{value:'feature/new'}});
  await fireEvent.submit(name.closest('form')!);
  expect(create).toHaveBeenCalledWith('feature/new', head, true, 'refs/heads/main', 400);
  await waitFor(() => expect(onCreated).toHaveBeenCalledWith('/tree', true));
});
it('chooses a remote starting point and can create without switching', async () => {
  const { create, onCreated } = setup();
  await fireEvent.input(screen.getByRole('textbox', {name:'Branch name'}), {target:{value:'feature/from-remote'}});
  await fireEvent.change(screen.getByLabelText('Starting point'), {target:{value:'refs/remotes/origin/feature'}});
  await fireEvent.click(screen.getByRole('checkbox', {name:'Switch to new branch'}));
  await fireEvent.submit(screen.getByRole('textbox', {name:'Branch name'}).closest('form')!);
  expect(create).toHaveBeenCalledWith('feature/from-remote', remote, false, 'refs/heads/main', 400);
  await waitFor(() => expect(onCreated).toHaveBeenCalledWith('/tree', false));
});
it('keeps the draft after a Git error and allows correction and retry', async () => {
  const { git, create, onCreated } = setup();
  create.mockImplementationOnce(async () => { git.error='Branch already exists'; return null; });
  const name = screen.getByRole('textbox', {name:'Branch name'});
  await fireEvent.input(name, {target:{value:'feature/existing'}});
  await fireEvent.submit(name.closest('form')!);
  expect(await screen.findByRole('alert')).toHaveTextContent('Branch already exists');
  expect(name).toHaveValue('feature/existing'); expect(onCreated).not.toHaveBeenCalled();
  await fireEvent.input(name, {target:{value:'feature/available'}});
  await fireEvent.submit(name.closest('form')!);
  await waitFor(() => expect(onCreated).toHaveBeenCalledWith('/tree', true));
});
it('closes on Escape, blocks cancellation while busy, and ignores results after unmount', async () => {
  const { git, create, onClose, onCreated, view } = setup();
  const name = screen.getByRole('textbox', {name:'Branch name'});
  await fireEvent.keyDown(name, {key:'Escape'}); expect(onClose).toHaveBeenCalledOnce();
  git.busy='create-branch'; await fireEvent.keyDown(name, {key:'Escape'}); expect(onClose).toHaveBeenCalledOnce();
  git.busy=''; let resolve!: (value:string) => void;
  create.mockImplementationOnce(() => new Promise(r => resolve=r));
  await fireEvent.input(name, {target:{value:'feature/new'}}); await fireEvent.submit(name.closest('form')!);
  view.unmount(); resolve('/tree'); await Promise.resolve();
  expect(onCreated).not.toHaveBeenCalled();
});
