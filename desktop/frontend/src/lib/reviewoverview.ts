import type { PRReadinessData, ReviewPackage } from '@bindings/internal/protocol';
import { reviewSections } from './reviewmaterial';

export type OverviewItem = { title: string; body: string };
export function plainReviewText(text: string): string {
  return text.replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1').replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/`([^`]+)`/g, '$1').replace(/(\*\*|__|~~)(.*?)\1/g, '$2').replace(/\*([^*]+)\*/g, '$1').replace(/^\s*(?:[-+*]|\d+[.)]|#{1,6})\s+/gm, '').replace(/\s+/g, ' ').trim();
}
function shortLine(text: string, max = 140): string {
  const plain = plainReviewText(text);
  const sentence = plain.match(/^.*?[.!?](?=\s|$)/)?.[0] || plain;
  return sentence.length <= max ? sentence : sentence.slice(0,max-1).replace(/\s+\S*$/, '') + '…';
}
/** Retain source Markdown; only the collapsed labels are shortened. Nested lists
 * and fenced examples stay with their parent item instead of becoming features. */
export function reviewItems(body: string): OverviewItem[] {
  const chunks: string[] = [];
  let lines: string[] = [], fence = '';
  const flush = () => { if (lines.join('\n').trim()) chunks.push(lines.join('\n').trim()); lines = []; };
  for (const line of body.split('\n')) {
    const marker = line.match(/^\s*(`{3,}|~{3,})/);
    if (marker) {
      if (!fence) fence = marker[1];
      else if (marker[1][0] === fence[0] && marker[1].length >= fence.length) fence = '';
      lines.push(line); continue;
    }
    if (!fence && /^(?:[-+*]|\d+[.)])\s+/.test(line)) flush();
    else if (!fence && !line.trim() && lines.length && !/^(?:[-+*]|\d+[.)])\s+/.test(lines[0])) flush();
    lines.push(line);
  }
  flush();
  return chunks.map(body => ({title:shortLine(body),body}));
}
export function reviewOverview(pkg: Pick<ReviewPackage,'title'|'body'>) {
  const changes: OverviewItem[] = [], verification: OverviewItem[] = [], risks: OverviewItem[] = [], details: OverviewItem[] = [];
  let summary = '';
  for (const section of reviewSections(pkg.body)) {
    if (/^(summary|overview|tl;?dr)$/i.test(section.title)) {
      if (!summary) summary = shortLine(section.body,220);
      if (plainReviewText(section.body) !== summary) details.push({title:section.title,body:section.body});
    } else if (/chang|feature|implement|what.*done/i.test(section.title)) changes.push(...reviewItems(section.body));
    else if (/risk|unresolved|remaining|limitation|caveat/i.test(section.title)) risks.push(...reviewItems(section.body));
    else if (/verif|test|validation/i.test(section.title)) verification.push(...reviewItems(section.body));
    else details.push({title:section.title,body:section.body});
  }
  const title = pkg.title.replace(/^[a-z]+(?:\([^)]*\))?!?:\s*/i, '').replace(/^./, c => c.toUpperCase());
  return {title, summary:summary || changes[0]?.title || title, changes, verification, risks, details};
}
export type ConfidenceSignal = { label: string; value: string; confirmed: boolean; failed?: boolean };
/** Equal-weight coverage of five observable review signals, NOT a probability of
 * correctness. Facts about another revision cannot score the selected package. */
export function reviewConfidence(pkg: Pick<ReviewPackage,'head'|'repo'|'pr'>, facts?: PRReadinessData) {
  const current = !!pkg.head && !!facts?.head && pkg.head === facts.head && pkg.repo === facts.repo && (!pkg.pr || pkg.pr === facts.number);
  const signals: ConfidenceSignal[] = [
    {label:'Revision',value:current ? 'Matches current PR' : facts ? 'Different revision' : 'Not confirmed',confirmed:current},
    {label:'CI checks',value:'Not confirmed',confirmed:false},
    {label:'PR review',value:'Not confirmed',confirmed:false},
    {label:'Merge conflicts',value:'Not confirmed',confirmed:false},
    {label:'Review threads',value:'Not confirmed',confirmed:false},
  ];
  if (current && facts) {
    signals[1] = {label:'CI checks',value:({pass:'Passed',fail:'Failed',pending:'Running',none:'No checks'} as Record<string,string>)[facts.checks] || 'Unknown',confirmed:facts.checks === 'pass',failed:facts.checks === 'fail'};
    signals[2] = {label:'PR review',value:({APPROVED:'Approved',CHANGES_REQUESTED:'Changes requested',REVIEW_REQUIRED:'Review required'} as Record<string,string>)[facts.review] || 'No approval',confirmed:facts.review === 'APPROVED',failed:facts.review === 'CHANGES_REQUESTED'};
    signals[3] = {label:'Merge conflicts',value:facts.mergeable === 'MERGEABLE' ? 'None detected' : facts.mergeable === 'CONFLICTING' ? 'Conflicts found' : 'Unknown',confirmed:facts.mergeable === 'MERGEABLE',failed:facts.mergeable === 'CONFLICTING'};
    signals[4] = {label:'Review threads',value:facts.threads ? facts.threads.length ? `${facts.threads.length} unresolved` : 'All resolved' : 'Unknown',confirmed:Array.isArray(facts.threads) && facts.threads.length === 0,failed:!!facts.threads?.length};
  }
  const confirmed = signals.filter(s => s.confirmed).length;
  return {signals,confirmed,score:current ? confirmed * 20 : null};
}
