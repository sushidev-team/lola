import { slide } from 'svelte/transition';
import { cubicOut } from 'svelte/easing';

/** Reversible height reveal for conditional disclosure content. */
export function disclosureSlide(node: HTMLElement) {
  const reduce = !node.animate || window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  return slide(node, { duration: reduce ? 0 : 200, easing: cubicOut });
}

/** Native keyboard semantics, with interruptible opening/closing and local accordion groups. */
const controls = new WeakMap<HTMLDetailsElement, (open: boolean) => void>();
export function disclosureMotion(node: HTMLDetailsElement, group = false) {
  let animation: Animation | undefined;
  let target = node.open;
  function setOpen(open: boolean) {
    const from = node.getBoundingClientRect().height;
    animation?.cancel();
    target = open;
    const summary = node.querySelector('summary')!;
    if (open && group) {
      node.parentElement?.querySelectorAll<HTMLDetailsElement>(':scope > details').forEach(peer => {
        if (peer !== node && peer.open) controls.get(peer)?.(false);
      });
    }
    if (!node.animate || window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) {
      node.open = open;
      return;
    }
    node.open = true;
    const to = open ? node.getBoundingClientRect().height : summary.getBoundingClientRect().height + 2;
    node.style.overflow = 'hidden';
    animation = node.animate([{ height: `${from}px` }, { height: `${to}px` }], {
      duration: open ? 200 : 150, easing: 'cubic-bezier(0.16, 1, 0.3, 1)',
    });
    animation.onfinish = () => { node.open = open; node.style.overflow = ''; animation = undefined; };
  }
  function click(event: MouseEvent) {
    if (!(event.target instanceof Element) || event.target.closest('summary')?.parentElement !== node || event.target.closest('a,button')) return;
    event.preventDefault();
    setOpen(animation ? !target : !node.open);
  }
  controls.set(node, setOpen);
  node.addEventListener('click', click);
  return { destroy() { animation?.cancel(); controls.delete(node); node.removeEventListener('click', click); } };
}
