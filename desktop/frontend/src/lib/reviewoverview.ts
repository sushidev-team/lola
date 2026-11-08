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
/** Prefer the author's short label. For legacy prose, retain a short source
 * clause; identifiers and code paths belong in the expansion, not the label. */
export function compactReviewLabel(body: string): string {
  const clean = body.replace(/^\s*(?:[-+*]|\d+[.)])\s+/, '');
  const explicit = clean.match(/^\*\*(.+?)\*\*/)?.[1];
  let label = explicit || plainReviewText(clean.replace(/`[^`]*`/g, '')).replace(/\b(?:Likelihood|Probability|Impact|Severity)\s*:\s*(?:low|medium|high|unknown|critical)[.;|]?/gi, '').trim();
  if (!explicit) {
    const outcome = label.match(/\bso (?:that )?(.+?)(?:[.!?]|$)/i)?.[1];
    label = outcome || label.replace(/\([^)]*\)/g, '').split(/\s[—–]\s|:\s|,\s|\.\s|\s(?:before|after|while|including|because|and)\s/i)[0];
    label = label.replace(/`[^`]*`/g, '').replace(/\b\w*\d{4}[_\w.-]*\b/g, '').replace(/(?:\/?[\w{}.-]+){2,}\/[\w/{}.-]+/g, '')
      .replace(/\b(?:the|a|an|their|now|whole|must)\b/gi, '').replace(/\s+/g, ' ').trim();
  }
  const words = label.replace(/[.;:—–]+$/, '').trim().split(/\s+/).filter(Boolean);
  let short = words.slice(0,explicit ? 8 : 6).join(' ');
  while (short.length > (explicit ? 80 : 54) && short.includes(' ')) short = short.slice(0,short.lastIndexOf(' '));
  if (short.length > (explicit ? 80 : 54)) short = 'Implementation update';
  short = short.replace(/\s+(?:at|on|to|with|from|for|in|uses)$/i, '');
  return (short || 'Review item').replace(/^./, c => c.toUpperCase());
}
/** Explicit author fields support visual comparisons without inferring behavior. */
function reviewField(body: string, field: string): string {
  return plainReviewText(body).match(new RegExp(`\\b${field}:\\s*(.*?)(?=\\s+(?:Before|After|New|Includes|Details|Likelihood|Probability|Impact|Severity|Outcome|Basis|Assess):|$)`, 'i'))?.[1]?.trim() || '';
}
export type ReviewChange = OverviewItem & { description: string; parts: string[] };
function featureParts(body: string): string[] {
  const parts: string[] = [];
  let includes = false;
  for (const line of body.split('\n')) {
    if (/^\s+(?:\*\*)?Includes:(?:\*\*)?\s*$/i.test(line)) { includes = true; continue; }
    if (includes && /^\s+[\w ]+:/.test(line)) includes = false;
    const part = includes && line.match(/^\s+[-*+]\s+(.+)/);
    if (part) parts.push(shortLine(part[1],150));
  }
  return parts;
}
export function reviewChange(item: OverviewItem): ReviewChange {
  return {...item,title:compactReviewLabel(item.body),description:shortLine(reviewField(item.body,'New') || reviewField(item.body,'After'),160),parts:featureParts(item.body)};
}
/** Consequences need enough room to remain meaningful; never truncate them to
 * a topic noun or infer a failure from an unrelated category. */
export function riskHeadline(body: string): string {
  const explicit = body.replace(/^\s*(?:[-+*]|\d+[.)])\s+/, '').match(/^\*\*(.+?)\*\*/)?.[1];
  const outcome = reviewField(body,'Outcome');
  // Short legacy topic labels can defer to an explicit outcome. Preserve newly
  // authored consequence headings instead of clipping their longer rationale.
  const heading = explicit && (explicit.split(/\s+/).length > 3 || !outcome) ? explicit : outcome;
  if (heading) return shortLine(heading,110).replace(/[.!]$/, '').replace(/^./, c => c.toUpperCase());
  const text = plainReviewText(body).replace(/\b(?:Likelihood|Probability|Impact|Severity)\s*:\s*(?:low|medium|high|unknown)[.;|]?/gi,'');
  const description = text.replace(/^[^:]{1,50}:\s*/, '');
  return shortLine(description,110).replace(/[.!]$/, '');
}
export type RiskLevel = 'low' | 'medium' | 'high' | 'unknown';
export type ReviewRisk = OverviewItem & { likelihood: RiskLevel; impact: RiskLevel; priority: number; description: string; assess: string };
export const riskWeight: Record<RiskLevel,number> = {unknown:0,low:1,medium:2,high:3};
export function reviewRisk(item: OverviewItem): ReviewRisk {
  const text = plainReviewText(item.body);
  const likelihood = (text.match(/\b(?:likelihood|probability)\s*:\s*(low|medium|high|unknown)\b/i)?.[1]?.toLowerCase() || 'unknown') as RiskLevel;
  const impact = (text.match(/\b(?:impact|severity)\s*:\s*(low|medium|high|unknown)\b/i)?.[1]?.toLowerCase() || 'unknown') as RiskLevel;
  // Unknowns need review before low-rated risks; they are never presented as low.
  const priority = likelihood === 'unknown' || impact === 'unknown' ? 5 : riskWeight[likelihood] * riskWeight[impact];
  return {...item,title:riskHeadline(item.body),likelihood,impact,priority,description:shortLine(reviewField(item.body,'Outcome'),220),assess:shortLine(reviewField(item.body,'Assess'),220)};
}
export function reviewOverview(pkg: Pick<ReviewPackage,'title'|'body'>) {
  const changes: ReviewChange[] = [], verification: OverviewItem[] = [], risks: ReviewRisk[] = [], details: OverviewItem[] = [];
  let riskReportProvided = false;
  let summary = '';
  for (const section of reviewSections(pkg.body)) {
    if (/^(summary|overview|tl;?dr)$/i.test(section.title)) {
      if (!summary) summary = shortLine(section.body,220);
      if (plainReviewText(section.body) !== summary) details.push({title:section.title,body:section.body});
    } else if (/chang|feature|implement|what.*done/i.test(section.title)) {
      for (const item of reviewItems(section.body)) {
        if (/^[\w.-]+ (?:is|was|remains) unchanged\b/i.test(plainReviewText(item.body))) details.push({title:'Unchanged scope',body:item.body});
        else changes.push(reviewChange(item));
      }
    }
    else if (/risk|unresolved|remaining|limitation|caveat/i.test(section.title)) {
      riskReportProvided = true;
      risks.push(...reviewItems(section.body).filter(item => !/^(?:none|no (?:known |remaining |unresolved )?risks(?: reported)?)[.!]?$/i.test(plainReviewText(item.body))).map(reviewRisk));
    }
    else if (/verif|test|validation/i.test(section.title)) verification.push(...reviewItems(section.body));
    else details.push({title:section.title,body:section.body});
  }
  const title = pkg.title.replace(/^[a-z]+(?:\([^)]*\))?!?:\s*/i, '').replace(/^./, c => c.toUpperCase());
  risks.sort((a,b) => b.priority - a.priority || riskWeight[b.impact] - riskWeight[a.impact]);
  return {title, summary:summary || shortLine(changes[0]?.body || title,160), changes, verification, risks, details, riskReportProvided};
}
export type ConfidenceSignal = { label: string; value: string; confirmed: boolean; failed?: boolean };
/** Equal-weight coverage of five observable review signals, NOT a probability of
 * correctness. Facts about another revision cannot score the selected package. */
export function reviewConfidence(pkg: Pick<ReviewPackage,'head'|'base'|'repo'|'pr'>, facts?: PRReadinessData) {
  const current = !!pkg.head && !!facts?.head && !!facts.base && pkg.base === facts.base && pkg.head === facts.head && pkg.repo === facts.repo && (!pkg.pr || pkg.pr === facts.number);
  const signals: ConfidenceSignal[] = [
    {label:'Revision',value:current ? 'Matches current PR' : facts ? 'Different revision' : 'Not confirmed',confirmed:current},
    {label:'CI checks',value:'Not confirmed',confirmed:false},
    {label:'PR review',value:'Not confirmed',confirmed:false},
    {label:'Merge conflicts',value:'Not confirmed',confirmed:false},
    {label:'Review threads',value:'Not confirmed',confirmed:false},
  ];
  if (current && facts) {
    const pinnedChecks = facts.checksHead === pkg.head;
    signals[1] = {label:'CI checks',value:pinnedChecks ? ({pass:'Passed',fail:'Failed',pending:'Running',none:'No checks'} as Record<string,string>)[facts.checks] || 'Unknown' : 'Revision unconfirmed',confirmed:pinnedChecks && facts.checks === 'pass',failed:pinnedChecks && facts.checks === 'fail'};
    signals[2] = {label:'PR review',value:({APPROVED:'Approved',CHANGES_REQUESTED:'Changes requested',REVIEW_REQUIRED:'Review required'} as Record<string,string>)[facts.review] || 'No approval',confirmed:facts.review === 'APPROVED',failed:facts.review === 'CHANGES_REQUESTED'};
    signals[3] = {label:'Merge conflicts',value:facts.mergeable === 'MERGEABLE' ? 'None detected' : facts.mergeable === 'CONFLICTING' ? 'Conflicts found' : 'Unknown',confirmed:facts.mergeable === 'MERGEABLE',failed:facts.mergeable === 'CONFLICTING'};
    signals[4] = {label:'Review threads',value:facts.threads ? facts.threads.length ? `${facts.threads.length} unresolved` : 'All resolved' : 'Unknown',confirmed:Array.isArray(facts.threads) && facts.threads.length === 0,failed:!!facts.threads?.length};
  }
  const confirmed = signals.filter(s => s.confirmed).length;
  return {signals,confirmed,score:current ? confirmed * 20 : null};
}
