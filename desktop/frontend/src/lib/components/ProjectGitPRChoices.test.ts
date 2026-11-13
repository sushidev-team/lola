import {afterEach,describe,it,expect} from 'vitest';
import {render,screen,fireEvent,cleanup} from '@testing-library/svelte';
import Choices from './ProjectGitPRChoices.svelte';
afterEach(cleanup);
describe('PR metadata combobox',()=>{
 it('filters, selects multiple values with the keyboard and closes Escape without closing the dialog',async()=>{
  render(Choices,{label:'Reviewers',options:[{value:'ada',label:'Ada'},{value:'grace',label:'Grace'}]});
  const input=screen.getByRole('combobox',{name:'Reviewers'});
  await fireEvent.focus(input);
  await fireEvent.keyDown(input,{key:'Enter'});
  expect(screen.getByRole('option',{name:'Ada'})).toHaveAttribute('aria-selected','true');
  await fireEvent.input(input,{target:{value:'gr'}});
  expect(screen.queryByRole('option',{name:'Ada'})).toBeNull();
  await fireEvent.keyDown(input,{key:'Enter'});
  expect(screen.getByRole('option',{name:'Grace'})).toHaveAttribute('aria-selected','true');
  await fireEvent.keyDown(input,{key:'Escape'});
  expect(screen.queryByRole('listbox')).toBeNull();
  expect(input).toHaveValue('Ada, Grace');
  await fireEvent.keyDown(input,{key:'ArrowDown'});
  await fireEvent.keyDown(input,{key:'Enter'});
  expect(screen.getByRole('option',{name:'Ada'})).toHaveAttribute('aria-selected','false');
  await fireEvent.pointerDown(document.body);
  expect(input).toHaveAttribute('aria-expanded','false');
 });
});
