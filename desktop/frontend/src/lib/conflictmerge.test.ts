import {describe, expect, it} from 'vitest';
import {conflictBlocks, mergedText, mergeLockfile, parseConflictChoices, selectedLines} from './conflictmerge';

describe('merge editor', () => {
  it('resolves multiple blocks independently with custom markers, diff3 and CRLF', () => {
    const source = ['one','<<<<<<<< local','a','|||||||| base','old','========','b','>>>>>>>> stash','between','<<<<<<< A','=======','added','>>>>>>> B','end'].join('\r\n');
    const blocks = conflictBlocks(source);
    expect(blocks).toHaveLength(2);
    expect(blocks[0]).toMatchObject({line:2,current:'a\r\n',base:'old\r\n',incoming:'b\r\n',incomingLine:7});
    expect(mergedText(source,blocks,[null,null])).toBe(source);
    expect(mergedText(source,blocks,['b\r\n',''])).toBe('one\r\nb\r\nbetween\r\nend');
  });
  it('keeps incomplete markers available for manual editing', () => {
    expect(conflictBlocks('prefix\n<<<<<<< A\nunclosed')).toEqual([]);
  });
  it('adds complete selected lines without accidentally including the next line', () => {
    expect(selectedLines('abc\ndef\nlast',1,4)).toBe('abc\n');
    expect(selectedLines('abc\ndef\nlast',9,9)).toBe('last');
  });
});

describe('AI conflict replies', () => {
  it('preserves braces and backticks in merged text inside a formatted reply', () => {
    const choices = {'2':{merged:'const value = `{${name}}`;\n'}};
    const reply = ['Suggested resolution:', '```JSON', JSON.stringify(choices), '```', 'Ready for review.'].join('\n');
    expect(parseConflictChoices(reply)).toEqual(choices);
  });
  it.each(['[{"0":"current"}]', '{"0":{"merged":"ok"}', '{"0":"current"}\n{"0":"incoming"}', 'no decisions'])('rejects incomplete or ambiguous replies: %s', reply => {
    expect(parseConflictChoices(reply)).toBeNull();
  });
});

it('reconciles independent lockfile updates and deletions and defers competing changes', () => {
  const lock = (packages:Record<string,unknown>) => JSON.stringify({lockfileVersion:3,packages})+'\n';
  const base = lock({'':{name:'app'}, dep:{version:'1'}});
  const current = lock({'':{name:'app'}, dep:{version:'2'}});
  const incoming = lock({'':{}, dep:{version:'1'}});
  const marked = (other:string) => '<<<<<<< Current\n'+current+'=======\n'+other+'>>>>>>> Incoming\n';
  const resolve = (other:string) => mergeLockfile(marked(other),conflictBlocks(marked(other)),base,current,other);
  expect(JSON.parse(resolve(incoming)!)).toEqual(JSON.parse(lock({'':{},dep:{version:'2'}})));
  expect(resolve(lock({'':{},dep:{version:'3'}}))).toBeNull();
});
