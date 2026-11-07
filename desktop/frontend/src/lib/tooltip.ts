import { syntaxTokens } from './syntax';
let sequence = 0;
let dismissActive: (() => void) | undefined;
type TooltipContent = string | { text: string; language?: string; heading?: string; meta?: string; tone?: string; instant?: boolean };
const colors = { plain: '', kw: 'text-magenta', str: 'text-good', com: 'text-faint', num: 'text-orange', flag: 'text-info', var: 'text-orange', cmd: 'text-accent-ink', path: 'text-ink-2' };
/** Text-only rendering, with enough hover tolerance to enter and scroll the preview. */
export function tooltip(node: HTMLElement, content: TooltipContent) {
  let tip: HTMLDivElement | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let leaveTimer: ReturnType<typeof setTimeout> | undefined;
  const previous = node.getAttribute('aria-describedby');
  function hide() {
    if (dismissActive === hide) dismissActive = undefined;
    clearTimeout(timer);
    clearTimeout(leaveTimer);
    tip?.remove(); tip = undefined;
    if (previous) node.setAttribute('aria-describedby', previous);
    else node.removeAttribute('aria-describedby');
  }
  function stay() { clearTimeout(leaveTimer); }
  function leave() { clearTimeout(timer); leaveTimer = setTimeout(hide, 160); }
  function render() {
    if (tip) { stay(); return; }
    hide();
    const { text, language = 'text', heading, meta, tone } = typeof content === 'string' ? { text: content } : content;
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
    if (meta) {
      const detail = document.createElement('div');
      detail.className = 'cv-tooltip-meta';
      detail.textContent = meta;
      tip.append(detail);
    }
    tip.addEventListener('mouseenter', stay);
    tip.addEventListener('mouseleave', leave);
    tip.addEventListener('focusin', stay);
    tip.addEventListener('focusout', leave);
    document.body.append(tip);
    node.setAttribute('aria-describedby', [previous, tip.id].filter(Boolean).join(' '));
    const anchor = node.getBoundingClientRect();
    const rect = tip.getBoundingClientRect();
    tip.style.left = `${Math.max(8, Math.min(anchor.left + 12, window.innerWidth - rect.width - 8))}px`;
    tip.style.top = `${Math.max(8, anchor.top >= rect.height + 8 ? anchor.top - rect.height - 6 : Math.min(anchor.bottom + 6, window.innerHeight - rect.height - 8))}px`;
  }
  function show() {
    if (tip) { stay(); return; }
    clearTimeout(timer);
    if (typeof content !== 'string' && content.instant) render();
    else timer = setTimeout(render, 300);
  }
  function key(event: KeyboardEvent) { if (event.key === 'Escape') hide(); }
  function scroll(event: Event) { if (!(event.target instanceof Node) || !tip?.contains(event.target)) hide(); }
  node.addEventListener('mouseenter', show);
  node.addEventListener('mouseleave', leave);
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
      node.removeEventListener('mouseenter', show); node.removeEventListener('mouseleave', leave);
      node.removeEventListener('focusin', show); node.removeEventListener('focusout', leave);
      node.removeEventListener('click', hide); window.removeEventListener('keydown', key);
      window.removeEventListener('scroll', scroll, true); window.removeEventListener('resize', hide);
    },
  };
}
