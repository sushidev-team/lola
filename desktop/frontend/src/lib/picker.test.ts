import { expect, it, vi } from 'vitest';
// Load the same injected source the Go proxy serves, outside the Vite root.
const nodeFs = 'node:' + 'fs';
const { readFileSync } = await import(/* @vite-ignore */ nodeFs);
const root = (globalThis as unknown as { process: { cwd(): string } }).process.cwd();
const script: string = readFileSync(root + '/../../internal/preview/picker.js', 'utf8');

it('blocks pointer controls only while inspecting and anchors comments to the hash route', () => {
  const post = vi.spyOn(window, 'postMessage').mockImplementation(() => {});
  const button = document.createElement('button');
  button.id = 'menu';
  document.body.appendChild(button);
  const activate = vi.fn();
  button.addEventListener('pointerdown', activate);
  button.addEventListener('pointerup', activate);
  const send = (data: object) => window.dispatchEvent(new MessageEvent('message', {
    source: window, data: { target: 'lola-preview', ...data },
  }));
  const press = () => {
    for (const type of ['pointerdown', 'pointerup']) button.dispatchEvent(new Event(type, { bubbles: true, cancelable: true }));
  };
  try {
    new Function(script)();
    const doc = post.mock.calls[0][0].documentId;
    send({ type: 'pick', on: true });
    press();
    expect(activate).not.toHaveBeenCalled();
    send({ type: 'pick', on: false });
    press();
    expect(activate).toHaveBeenCalledTimes(2);

    send({ type: 'hello', watchLocation: true });
    history.replaceState(null, '', '/#/settings');
    expect(post).toHaveBeenCalledWith(expect.objectContaining({ type: 'location', url: location.href }), '*');
    const lookup = vi.spyOn(document, 'querySelector');
    send({ type: 'comment-anchors', documentId: doc, anchors: [
      { number: 1, path: '/#/settings', selector: '#menu' },
      { number: 2, path: '/#/home', selector: '#home-menu' },
    ] });
    expect(lookup).toHaveBeenCalledWith('#menu');
    expect(lookup).not.toHaveBeenCalledWith('#home-menu');
    send({ type: 'comment-anchors', documentId: doc, anchors: [] });
  } finally {
    vi.restoreAllMocks();
    button.remove();
    history.replaceState(null, '', '/');
  }
});
