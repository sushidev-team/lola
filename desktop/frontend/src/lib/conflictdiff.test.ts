import {expect,it} from 'vitest';
import {compareConflict,fileComparison,visibleSource,fileResult} from './conflictdiff';
import {conflictBlocks} from './conflictmerge';

function file(left:string,right:string) {
 const text='outside before\n<<<<<<< Current\n'+left+'=======\n'+right+'>>>>>>> Incoming\noutside after\n';
 return fileComparison(text,conflictBlocks(text));
}

it('opens on a lockfile deletion despite thousands of shared lines with different indentation',()=>{
  const shared=Array.from({length:2000},(_,i)=>`"pkg-${i}": "1",\n`);
  const left=shared.map(line=>'    '+line), right=shared.map(line=>'  '+line);
  left.splice(1500,0,'    "name": "nori-app",\n');
  const diff=compareConflict(left.join(''),right.join(''));
  expect(diff.whitespace).toBe(true);
  expect(diff.ranges).toEqual([{left:1500,right:1500,removed:1,added:0}]);
  const model=file(left.join(''),right.join(''));
  const start=model.changes.find(change=>!change.optional)!.row-2;
  const current=visibleSource(model,start,5,false),incoming=visibleSource(model,start,5,true);
  expect(current.map(row=>row.changed)).toEqual([false,false,true,false,false]);
  expect(current[2].text).toBe(left[1500]);
  expect(incoming[2].gap).toBe(true);
  expect(current[3].text.trim()).toBe(incoming[3].text.trim());
});

it('can inspect exact whitespace and line endings',()=>{
  const left=' a\r\n',right='  a\n';
  expect(compareConflict(left,right)).toMatchObject({whitespace:true,ranges:[]});
  expect(compareConflict(left,right,true).ranges).toEqual([{left:0,right:0,removed:1,added:1}]);
});

it('pages a large addition without dropping lines or moving the next shared row',()=>{
  const lines=Array.from({length:205},(_,i)=>i+'\n');
  const model=file('before\nafter\n','before\n'+lines.join('')+'after\n');
  const incoming=[],current=[];
  for(let row=0;row<model.length;row+=29){
    incoming.push(...visibleSource(model,row,29,true)); current.push(...visibleSource(model,row,29,false));
  }
  expect(incoming.filter(row=>row.changed).map(row=>row.text)).toEqual(lines);
  expect(current.map(row=>!!row.gap)).toEqual(incoming.map(row=>row.changed));
  expect(current[0]).toMatchObject({text:'outside before\n',block:-1});
  expect(current.at(-1)).toMatchObject({text:'outside after\n',block:-1});
});

it('indexes changes in later conflict blocks after intervening file context',()=>{
  const text='hello\n<<<<<<< a\nx\n=======\ny\n>>>>>>> b\nmid\n<<<<<<< a\n1\n=======\n2\n>>>>>>> b\nbye';
  const model=fileComparison(text,conflictBlocks(text));
  expect(model.changes.map(change=>change.row)).toEqual([1,3]);
  expect(visibleSource(model,2,2,true).map(r=>[r.text,r.block])).toEqual([['mid\n',-1],['2\n',1]]);
  expect(visibleSource(model,4,2,false)[0].text).toBe('bye');
});

it('resolves individual changes in file order across marker boundaries',()=>{
  const text='<<<<<<< A\na\nshared\nb\n=======\nA\nshared\nB\n>>>>>>> B\nbetween\n<<<<<<< A\nc\n=======\nC\n>>>>>>> B\n';
  const model=fileComparison(text,conflictBlocks(text));
  expect(model.changes.map(change=>change.current)).toEqual(['a\n','b\n','c\n']);
  expect(fileResult(model,['A\n','b\n','C\n'])).toBe('A\nshared\nb\nbetween\nC\n');
});
