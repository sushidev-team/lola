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

it('fills an unambiguous local sign-in form for the selected document', () => {
  const form = document.createElement('form');
  form.method = 'post';
  form.innerHTML = '<input autocomplete="username"><input type="password"><button>Sign in</button>';
  document.body.appendChild(form);
  const [user, pass] = form.querySelectorAll('input');
  const submit = vi.spyOn(form, 'requestSubmit').mockImplementation(() => {});
  const post = vi.spyOn(window, 'postMessage').mockImplementation(() => {});
  vi.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue([new DOMRect(0, 0, 100, 20)] as unknown as DOMRectList);
  const send = (data: object) => window.dispatchEvent(new MessageEvent('message', {
    source: window, data: { target: 'lola-preview', ...data },
  }));
  try {
    new Function(script)();
    send({ type: 'hello' });
    const documentId = post.mock.calls.at(-1)![0].documentId;
    const account = { type: 'test-login', username: 'tester', password: 'fixture-password', documentId };
    send({ ...account, documentId: 'old-page' });
    expect(pass.value).toBe('');
    form.action = 'https://external.example/login';
    send(account);
    expect(pass.value).toBe('');
    expect(submit).not.toHaveBeenCalled();
    form.action = '/login';
    pass.autocomplete = 'new-password';
    send(account);
    expect(pass.value).toBe('');
    pass.autocomplete = 'current-password';
    send(account);
    expect(user.value).toBe(account.username);
    expect(pass.value).toBe(account.password);
    expect(submit).toHaveBeenCalledWith(form.querySelector('button'));
    expect(post.mock.calls.at(-1)![0]).toEqual({ source: 'lola-preview', type: 'login-result', documentId, result: 'submitted' });
    form.method = 'get';
    send(account);
    expect(submit).toHaveBeenCalledTimes(1);
  } finally { form.remove(); vi.restoreAllMocks(); }
});

it('waits for a local sign-in form and preserves input typed before auto-login arrives', async () => {
  const post = vi.spyOn(window, 'postMessage').mockImplementation(() => {});
  vi.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue([new DOMRect(0, 0, 100, 20)] as unknown as DOMRectList);
  const form = document.createElement('form');
  form.action = '/register';
  form.method = 'post';
  form.innerHTML = '<input autocomplete="username"><input type="password" autocomplete="current-password"><button>Continue</button>';
  const [user, pass] = form.querySelectorAll('input');
  const send = (data: object) => window.dispatchEvent(new MessageEvent('message', {
    source: window, data: { target: 'lola-preview', ...data },
  }));
  new Function(script)();
  send({ type: 'hello' });
  const documentId = post.mock.calls.at(-1)![0].documentId;
  const ready = () => post.mock.calls.filter(([m]) => m.type === 'login-form' && m.documentId === documentId);
  try {
    document.body.appendChild(form);
    send({ type: 'watch-login', documentId, on: true });
    expect(ready()).toHaveLength(0);
    form.remove();
    form.action = '/login';
    document.body.appendChild(form);
    await vi.waitFor(() => expect(ready()).toHaveLength(1));
    user.value = 'someone typing';
    send({ type: 'test-login', automatic: true, documentId, username: 'test', password: 'example' });
    expect(user.value).toBe('someone typing');
    expect(pass.value).toBe('');
    expect(post.mock.calls.at(-1)![0].result).toBe('unavailable');
  } finally {
    send({ type: 'watch-login', documentId, on: false });
    form.remove();
    vi.restoreAllMocks();
  }
});
