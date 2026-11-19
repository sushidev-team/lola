import {diffArrays} from 'diff';

import type {ConflictBlock} from './conflictmerge';

export interface SourceLine {text:string; number:number; changed:boolean; block:number; change:number; formatting:boolean; gap?:boolean}
interface Range { left:number; right:number; removed:number; added:number }
export interface ConflictComparison {
  left:string[]; right:string[]; ranges:Range[]; whitespace:boolean;
}

function anchors(left:string[], right:string[]) {
  function positions(lines:string[]) {
    const result = new Map<string,number>();
    lines.forEach((line,i) => result.set(line,result.has(line) ? -1 : i));
    return result;
  }
  const old = positions(left), next = positions(right);
  // Patience anchors keep repeated JSON punctuation from aligning unrelated packages.
  const pairs:{left:number;right:number;prev:number}[] = [], tails:number[] = [];
  for (const [key,i] of old) {
    const j = next.get(key);
    if (i < 0 || j === undefined || j < 0) continue;
    let lo = 0, hi = tails.length;
    while (lo < hi) {
      const mid = (lo+hi)>>>1;
      if (pairs[tails[mid]].right < j) lo = mid+1; else hi = mid;
    }
    pairs.push({left:i,right:j,prev:lo ? tails[lo-1] : -1});
    tails[lo] = pairs.length-1;
  }
  const chain = [];
  for (let k=tails.at(-1) ?? -1;k>=0;k=pairs[k].prev) chain.push(pairs[k]);
  return chain.reverse();
}

export function compareConflict(current:string, incoming:string, exact=false):ConflictComparison {
  const left = current.match(/[^\n]*\n|[^\n]+$/g) ?? [];
  const right = incoming.match(/[^\n]*\n|[^\n]+$/g) ?? [];
  // Compare indentation independently of content. Picks always use the original bytes.
  const a = exact ? left : left.map(line=>line.trim());
  const b = exact ? right : right.map(line=>line.trim());
  const ranges:Range[] = [];
  let i=0, j=0, whitespace=false;
  function same(count:number) {
    for (let n=0;n<count;n++) if (left[i+n]!==right[j+n]) whitespace=true;
    i+=count; j+=count;
  }
  function change(removed:number,added:number) {
    const last = ranges.at(-1);
    if (last && last.left+last.removed===i && last.right+last.added===j) {
      last.removed+=removed; last.added+=added;
    } else ranges.push({left:i,right:j,removed,added});
    i+=removed; j+=added;
  }
  function gap(endA:number,endB:number) {
    while (i<endA && j<endB && a[i]===b[j]) same(1);
    let suffix=0;
    while (endA-suffix>i && endB-suffix>j && a[endA-suffix-1]===b[endB-suffix-1]) suffix++;
    const n=endA-suffix-i, m=endB-suffix-j;
    if (n || m) {
      // Bound Myers on unrelated/generated files. Ordinary insertions and
      // deletions need no search and the surrounding anchors remain exact.
      const runs=n && m && Math.abs(n-m)<=256
        ? diffArrays(a.slice(i,i+n),b.slice(j,j+m),{maxEditLength:256}) : undefined;
      if (!runs) change(n,m);
      else for (const run of runs) {
        if (run.added) change(0,run.count);
        else if (run.removed) change(run.count,0);
        else same(run.count);
      }
    }
    same(suffix);
  }
  for (const anchor of anchors(a,b)) {gap(anchor.left,anchor.right);same(1);}
  gap(a.length,b.length);
  return {left,right,ranges,whitespace};
}


interface Span {
  row:number; length:number; left:string[]; right:string[];
  a:number; b:number; removed:number; added:number; changed:boolean;
  leftLine:number; rightLine:number; block:number; change:number;
}

// Index aligned spans once. Scrolling looks up only the rows in the viewport.
export function fileComparison(text:string, blocks:ConflictBlock[]) {
  const spans:Span[]=[];
  const changes:{row:number; removed:number; added:number; current:string; incoming:string; line:number; owner:number; formatting:boolean; optional:boolean}[]=[];
  let length=0, offset=0, sourceLine=1;
  function append(span:Omit<Span,'row'|'length'|'change'>, formatting=false, optional=false) {
    const size=Math.max(span.removed,span.added);
    if (!size) return;
    const change=span.changed ? changes.length : -1;
    if (span.changed) changes.push({
      row:length, removed:span.removed, added:span.added, owner:span.block,
      current:span.left.slice(span.a,span.a+span.removed).join(''),
      incoming:span.right.slice(span.b,span.b+span.added).join(''),
      line:span.removed ? span.leftLine+span.a : span.rightLine+span.b, formatting, optional,
    });
    spans.push({...span,change,row:length,length:size});
    length+=size;
  }
  function common(end:number) {
    const lines=text.slice(offset,end).match(/[^\n]*\n|[^\n]+$/g) ?? [];
    append({left:lines,right:lines,a:0,b:0,removed:lines.length,added:lines.length,changed:false,leftLine:sourceLine,rightLine:sourceLine,block:-1});
  }
  blocks.forEach((block,index)=>{
    common(block.start);
    const diff=compareConflict(block.current,block.incoming);
    const fields={left:diff.left,right:diff.right,leftLine:block.line+1,rightLine:block.incomingLine,block:index};
    let a=0,b=0;
    // Keep formatting decisions stable when their visibility is toggled.
    function matched(count:number) {
      while(count>0) {
        const formatting=diff.left[a]!==diff.right[b];
        let n=1;
        while(n<count && (diff.left[a+n]!==diff.right[b+n])===formatting) n++;
        append({...fields,a,b,removed:n,added:n,changed:formatting},formatting,formatting && diff.ranges.length>0);
        a+=n;b+=n;count-=n;
      }
    }
    for(const range of diff.ranges) {
      matched(range.left-a);
      append({...fields,a,b,removed:range.removed,added:range.added,changed:true});
      a+=range.removed;b+=range.added;
    }
    matched(diff.left.length-a);
    offset=block.end;
    sourceLine=block.incomingLine+diff.right.length+1;
  });
  common(text.length);
  return {spans,changes,length};
}

export function fileResult(file:ReturnType<typeof fileComparison>, choices:(string|null)[]):string {
  return file.spans.map(span=>{
    if(span.change<0) return span.left.slice(span.a,span.a+span.removed).join('');
    const change=file.changes[span.change];
    if(choices[span.change]!=null) return choices[span.change];
    if(change.optional) return change.current;
    const eol=(change.current || change.incoming).includes('\r\n')?'\r\n':'\n';
    return '<<<<<<< Current'+eol+change.current+'======='+eol+change.incoming+'>>>>>>> Incoming'+eol;
  }).join('');
}

export function atRow(entries:readonly {row:number}[], row:number) {
  let lo=0,hi=entries.length;
  while(lo<hi) {
    const mid=(lo+hi)>>>1;
    if(entries[mid].row<=row) lo=mid+1; else hi=mid;
  }
  return Math.max(0,lo-1);
}

export function visibleSource(file:ReturnType<typeof fileComparison>,start:number,count:number,incoming:boolean,showFormatting=false):SourceLine[] {
  const rows:SourceLine[]=[];
  for(let i=atRow(file.spans,start);i<file.spans.length && rows.length<count;i++) {
    const span=file.spans[i], lines=incoming?span.right:span.left;
    const base=incoming?span.b:span.a, size=incoming?span.added:span.removed;
    for(let n=Math.max(0,start-span.row);n<span.length && rows.length<count;n++) {
      const gap=n>=size;
      rows.push({gap,text:gap?'':lines[base+n],number:(incoming?span.rightLine:span.leftLine)+base+n,changed:span.changed && (showFormatting || !file.changes[span.change].optional),formatting:span.changed && file.changes[span.change].formatting,change:span.change,block:span.block});
    }
  }
  return rows;
}
