import {describe, expect, it} from 'vitest';
import {conflictBlocks, mergedText, selectedLines} from './conflictmerge';

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
