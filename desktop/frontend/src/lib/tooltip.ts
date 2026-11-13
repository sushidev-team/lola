import { syntaxTokens } from './syntax';
import { mount, unmount } from 'svelte';
import Icon from './components/Icon.svelte';
import type { IconName } from './icons';

export type CommitTooltipDetails = { author: string; date: string; exactDate: string; sha: string; branches: { name: string; current: boolean; remote: boolean }[]; moreBranches: string[]; checkoutLabel?: string; checkoutDetail?: string };
let sequence = 0;
let dismissActive: (() => void) | undefined;
type TooltipContent = string | { text: string; commit?: CommitTooltipDetails; language?: string; heading?: string; code?: string; meta?: string; tone?: string; instant?: boolean; delay?: number; anchor?: 'element' | 'pointer'; hoverTarget?: string };
const colors = { plain: '', kw: 'text-magenta', str: 'text-good', com: 'text-faint', num: 'text-orange', flag: 'text-info', var: 'text-orange', cmd: 'text-accent-ink', path: 'text-ink-2' };
/** Text-only rendering, with enough hover tolerance to enter and scroll the preview. */
export function tooltip(node: HTMLElement, content: TooltipContent) {
  // Pointer previews can belong to one cell while keyboard focus stays on its row.
  const hoverNode = typeof content !== 'string' && content.hoverTarget ? node.querySelector<HTMLElement>(content.hoverTarget) ?? node : node;
  let tip: HTMLDivElement | undefined;
  let iconComponents: ReturnType<typeof mount>[] = [];
  let timer: ReturnType<typeof setTimeout> | undefined;
  let leaveTimer: ReturnType<typeof setTimeout> | undefined;
  const previous = node.getAttribute('aria-describedby');
  let pointer: { x: number; y: number } | undefined;
  function track(event: MouseEvent) { if (!tip) pointer = { x: event.clientX, y: event.clientY }; }
  function hide() {
    if (dismissActive === hide) dismissActive = undefined;
    clearTimeout(timer);
    clearTimeout(leaveTimer);
    for (const component of iconComponents) void unmount(component);
    iconComponents = [];
    tip?.remove(); tip = undefined;
    if (previous) node.setAttribute('aria-describedby', previous);
    else node.removeAttribute('aria-describedby');
  }
  function stay() { clearTimeout(leaveTimer); }
  function leave() { clearTimeout(timer); leaveTimer = setTimeout(hide, 160); }
  function render() {
    if (tip) { stay(); return; }
    hide();
    const { text, language = 'text', heading, code, meta, tone, commit } = typeof content === 'string' ? { text: content } : content;
    if (!text) return;
    dismissActive?.();
    dismissActive = hide;
    tip = document.createElement('div');
    tip.className = 'cv-tooltip';
    tip.id = `cv-tooltip-${++sequence}`;
    tip.setAttribute('role', 'tooltip');
    tip.tabIndex = 0;
    if (heading) {
      tip.classList.add('cv-tooltip-card');
      if (tone) tip.dataset.activityKind = tone;
      const title = document.createElement('div');
      title.className = 'cv-tooltip-heading';
      title.textContent = heading;
      tip.append(title);
    }
    if (commit) {
      tip.classList.add('cv-tooltip-commit');
      const element = (tag: string, className: string, text = '') => { const el = document.createElement(tag); el.className = className; el.textContent = text; return el; };
      const icon = (target: HTMLElement, name: IconName) => { const host = element('span', 'git-tip-icon'); target.append(host); iconComponents.push(mount(Icon, { target: host, props: { name, size: 13 } })); };
      const author = element('div', 'git-tip-author'); icon(author, 'user'); author.append(element('span', '', commit.author)); tip.append(author);
      const facts = element('div', 'git-tip-facts');
      const date = element('time', 'git-tip-date', commit.date); date.setAttribute('aria-label', commit.exactDate);
      const sha = element('code', 'git-tip-sha', commit.sha.slice(0, 8)); sha.setAttribute('aria-label', `Commit ${commit.sha}`);
      facts.append(date, sha); tip.append(facts);
      if (commit.branches.length) {
        const branches = element('div', 'git-tip-branches'); branches.setAttribute('aria-label', 'In branch history'); branches.append(element('span', 'git-tip-branch-label', 'Branch history'));
        for (const branch of commit.branches) {
          const chip = element('span', `git-tip-branch${branch.current ? ' current' : ''}`); chip.setAttribute('aria-label', `${branch.current ? 'Current branch' : branch.remote ? 'Remote branch' : 'Local branch'}: ${branch.name}`);
          icon(chip, branch.remote ? 'cloud' : 'git-branch'); chip.append(element('span', '', branch.name)); branches.append(chip);
        }
        if (commit.moreBranches.length) { const more = element('span', 'git-tip-more', `+${commit.moreBranches.length}`); more.setAttribute('aria-label', `More branches: ${commit.moreBranches.join(', ')}`); branches.append(more); }
        tip.append(branches);
      }
      if (commit.checkoutLabel) { const checkout = element('div', 'git-tip-checkout'); icon(checkout, 'monitor'); checkout.append(element('span', '', commit.checkoutLabel)); if (commit.checkoutDetail) checkout.setAttribute('aria-label', commit.checkoutDetail); tip.append(checkout); }
    } else {
      const body = document.createElement('div');
      body.className = 'cv-tooltip-body';
      tip.append(body);
      for (const token of syntaxTokens(text, language)) {
        if (token.kind === 'plain') body.append(document.createTextNode(token.text));
        else {
          const span = document.createElement('span');
          span.className = colors[token.kind];
          span.textContent = token.text;
          body.append(span);
        }
      }
      if (code) {
        const value = document.createElement('code');
        value.textContent = code;
        value.style.cssText = 'display:block;font-family:var(--font-mono);font-size:12px;overflow-wrap:anywhere;margin-top:4px';
        tip.append(value);
      }
      if (meta) {
        const detail = document.createElement('div');
        detail.className = 'cv-tooltip-meta';
        detail.textContent = meta;
        tip.append(detail);
      }
    }
    tip.addEventListener('mouseenter', stay);
    tip.addEventListener('mouseleave', leave);
    tip.addEventListener('focusin', stay);
    tip.addEventListener('focusout', leave);
    document.body.append(tip);
    node.setAttribute('aria-describedby', [previous, tip.id].filter(Boolean).join(' '));
    const anchor = node.getBoundingClientRect();
    const rect = tip.getBoundingClientRect();
    const cursor = typeof content !== 'string' && content.anchor === 'pointer' ? pointer : undefined;
    const left = cursor ? cursor.x + 12 : anchor.left + 12;
    const top = cursor ? (cursor.y + 18 + rect.height <= window.innerHeight - 8 ? cursor.y + 18 : cursor.y - rect.height - 12) : anchor.top >= rect.height + 8 ? anchor.top - rect.height - 6 : anchor.bottom + 6;
    tip.style.left = `${Math.max(8, Math.min(left, window.innerWidth - rect.width - 8))}px`;
    tip.style.top = `${Math.max(8, Math.min(top, window.innerHeight - rect.height - 8))}px`;
  }
  function show(event: Event) {
    stay(); // Re-entering cancels the previous leave timer before it can cancel this reveal.
    if (event instanceof MouseEvent) track(event);
    else pointer = undefined;
    if (tip) { stay(); return; }
    clearTimeout(timer);
    if (typeof content !== 'string' && content.instant) render();
    else timer = setTimeout(render, typeof content === 'string' ? 300 : content.delay ?? 300);
  }
  function key(event: KeyboardEvent) { if (event.key === 'Escape') hide(); }
  function scroll(event: Event) { if (!(event.target instanceof Node) || !tip?.contains(event.target)) hide(); }
  hoverNode.addEventListener('mouseenter', show);
  hoverNode.addEventListener('mousemove', track);
  node.addEventListener('contextmenu', hide);
  node.addEventListener('dragstart', hide);
  hoverNode.addEventListener('mouseleave', leave);
  node.addEventListener('focusin', show);
  node.addEventListener('focusout', leave);
  node.addEventListener('click', hide);
  window.addEventListener('keydown', key);
  window.addEventListener('scroll', scroll, true);
  window.addEventListener('resize', hide);
  return {
    update(value: TooltipContent) {
      if (JSON.stringify(value) === JSON.stringify(content)) return;
      const visible = !!tip;
      content = value; hide();
      if (visible) render();
    },
    destroy() {
      hide();
      hoverNode.removeEventListener('mousemove', track); node.removeEventListener('contextmenu', hide); node.removeEventListener('dragstart', hide);
      hoverNode.removeEventListener('mouseenter', show); hoverNode.removeEventListener('mouseleave', leave);
      node.removeEventListener('focusin', show); node.removeEventListener('focusout', leave);
      node.removeEventListener('click', hide); window.removeEventListener('keydown', key);
      window.removeEventListener('scroll', scroll, true); window.removeEventListener('resize', hide);
    },
  };
}
