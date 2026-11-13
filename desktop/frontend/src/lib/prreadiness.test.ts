import { expect, it, vi } from 'vitest';
import type { PRReadinessData } from '@bindings/internal/protocol';
const read = vi.hoisted(()=>vi.fn());
vi.mock('@bindings/desktop',()=>({DaemonService:{PRReadiness:read}}));
import { readPRReadiness } from './prreadiness';

it('shares concurrent reads but fetches again after completion',async()=>{
  let finish!:(value:PRReadinessData)=>void;
  read.mockImplementationOnce(()=>new Promise(resolve=>finish=resolve));
  const first=readPRReadiness('same',42), second=readPRReadiness('same',42);
  expect(first).toBe(second);expect(read).toHaveBeenCalledTimes(1);
  finish({head:'first'} as PRReadinessData);await first;
  read.mockResolvedValueOnce({head:'next'});
  expect(await readPRReadiness('same',42)).toEqual({head:'next'});
  expect(read).toHaveBeenCalledTimes(2);
});
it('does not share responses across sessions or PR numbers',async()=>{
  read.mockResolvedValue({head:'current'});
  const a=readPRReadiness('one',42), b=readPRReadiness('two',42), c=readPRReadiness('one',43);
  expect(a).not.toBe(b);expect(a).not.toBe(c);await Promise.all([a,b,c]);
});
it('removes failed requests so the next refresh can retry',async()=>{
  read.mockRejectedValueOnce(new Error('offline'));
  await expect(readPRReadiness('retry',42)).rejects.toThrow('offline');
  read.mockResolvedValueOnce({head:'recovered'});
  expect(await readPRReadiness('retry',42)).toEqual({head:'recovered'});
});
