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
    const link = document.createElement('a');
    link.href = '/docs?q=preview#start';
    link.target = '_blank';
    button.appendChild(link);
    const click = new MouseEvent('click', { bubbles: true, cancelable: true });
    link.dispatchEvent(click);
    expect(click.defaultPrevented).toBe(true);
    expect(post).toHaveBeenCalledWith({ source: 'lola-preview', type: 'new-tab', documentId: doc, url: link.href }, '*');
    history.replaceState(null, '', '/#/settings');
    expect(post).toHaveBeenCalledWith(expect.objectContaining({ type: 'location', url: location.href }), '*');
    const lookup = vi.spyOn(document, 'querySelector');
    send({ type: 'comment-anchors', documentId: doc, anchors: [
      { number: 1, path: '/#/settings', selector: '#menu' },
      { number: 2, path: '/#/home', selector: '#home-menu' },
    ] });
    expect(lookup).toHaveBeenCalledWith('#menu');
    expect(lookup).not.toHaveBeenCalledWith('#home-menu');
    const deselects = () => post.mock.calls.filter(([m]) => m.type === 'annotation-deselect').length;
    const beforeClick = activate.mock.calls.length;
    press();
    expect(deselects()).toBe(1);
    expect(activate.mock.calls.length).toBe(beforeClick + 2);
    send({ type: 'comment-anchors', documentId: doc, anchors: [] });
    press();
    expect(deselects()).toBe(1);
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

it('uses the enabled annotation shortcuts on the page and leaves typing, composition, copy and handled shortcuts alone', () => {
  const frame = document.createElement('iframe');
  document.body.appendChild(frame);
  const child = frame.contentWindow!;
  const listen = vi.spyOn(child, 'addEventListener');
  const post = vi.spyOn(child.parent, 'postMessage').mockImplementation(() => {});
  try {
    new Function('window', 'document', script)(child, child.document);
    const documentId = post.mock.calls[0][0].documentId;
    child.dispatchEvent(new MessageEvent('message', { source: child.parent, data: {
      target: 'lola-preview', type: 'comment-shortcut', documentId, enabled: true, keys: ['c', 'p', 't', 'a', 'r'],
    }}));
    // jsdom cannot dispatch trusted keyboard events. Exercise the actual listener.
    const keydown = listen.mock.calls.find(([event]) => event === 'keydown')![1] as EventListener;
    const press = (target: HTMLElement, extra = {}) => {
      const preventDefault = vi.fn();
      keydown({ key: 'c', isTrusted: true, composedPath: () => [target], preventDefault, ...extra } as unknown as Event);
      return preventDefault;
    };
    for (const key of ['c','p','t','a','r']) {
      expect(press(document.body, { key })).toHaveBeenCalledOnce();
      expect(post).toHaveBeenCalledWith({ source:'lola-preview', type:'comment-shortcut', documentId, key }, '*');
      expect(press(document.createElement('textarea'), { key })).not.toHaveBeenCalled();
    }
    expect(press(document.body, { key:'x' })).not.toHaveBeenCalled();
    for (const tag of ['input', 'textarea', 'select']) expect(press(document.createElement(tag))).not.toHaveBeenCalled();
    const editor = document.createElement('div');
    editor.contentEditable = 'true'; editor.setAttribute('contenteditable', 'true');
    expect(press(editor)).not.toHaveBeenCalled();
    for (const extra of [{ metaKey: true }, { ctrlKey: true }, { isComposing: true }, { defaultPrevented: true }, { repeat: true }]) {
      expect(press(document.body, extra)).not.toHaveBeenCalled();
    }
  } finally { frame.remove(); vi.restoreAllMocks(); }
});

it('highlights a clicked element and reports only enclosed visible targets for a drag', () => {
  const frame = document.createElement('iframe');
  document.body.appendChild(frame);
  const child = frame.contentWindow!, doc = child.document;
  const post = vi.spyOn(child.parent, 'postMessage').mockImplementation(() => {});
  try {
    doc.body.innerHTML = '<button id="one">First</button><button id="two">Second</button><button id="partial">Partial</button><button id="covered">Hidden</button>';
    const nodes = Array.from(doc.querySelectorAll('button'));
    nodes.forEach((el, i) => {
      vi.spyOn(el, 'getBoundingClientRect').mockReturnValue(new DOMRect(10 + i * 30, 10, 25, 20));
    });
    Object.defineProperty(doc, 'elementFromPoint', {value: (x: number) =>
      nodes.find(el => el.id !== 'covered' && x >= el.getBoundingClientRect().left && x <= el.getBoundingClientRect().right) ?? doc.body});
    new Function('window', 'document', script)(child, doc);
    const documentId = post.mock.calls[0][0].documentId;
    const send = (m: object) => child.dispatchEvent(new MessageEvent('message', {
      source: child.parent, data: {target: 'lola-preview', documentId, ...m},
    }));
    send({type:'pick', on:true});
    send({type:'hover-at', x:12, y:12});
    const highlight = doc.querySelector<HTMLElement>('[data-lola-preview]')!;
    expect(highlight.style.left).toBe('10px');
    expect(highlight.style.width).toBe('25px');
    send({type:'pick-at', x:12, y:12});
    expect(post.mock.calls.map(([m]) => m).findLast(m => m.type === 'pick')).toMatchObject({
      selector:'button#one', html: nodes[0].outerHTML,
    });
    expect(highlight.style.display).toBe('block');
    const pick = (w: number) => {
      send({type:'pick', on:true});
      send({type:'pick-at', x:0, y:0, region:true, rect:{x:0,y:0,w,h:40}});
      return post.mock.calls.map(([m]) => m).findLast(m => m.type === 'pick');
    };
    expect(pick(80)).toMatchObject({selector:'button#one', targets:['button#one', 'button#two'], html:''});
    expect(pick(180).targets).toEqual(['button#one', 'button#two', 'button#partial']);
  } finally { frame.remove(); vi.restoreAllMocks(); }
});

it('positions labels against the site without highlighting or collecting element contents', () => {
  const frame = document.createElement('iframe');
  document.body.appendChild(frame);
  const child = frame.contentWindow!, doc = child.document;
  const pane = doc.createElement('section');
  pane.id = 'ledger';
  doc.body.appendChild(pane);
  vi.spyOn(pane, 'getBoundingClientRect').mockReturnValue(new DOMRect(15, 30, 180, 100));
  pane.scrollTop = 40;
  const hit = vi.fn((): Element => pane);
  Object.defineProperty(doc, 'elementFromPoint', {value:hit});
  Object.defineProperty(child, 'scrollX', {value:32, configurable:true});
  Object.defineProperty(child, 'scrollY', {value:120, configurable:true});
  const post = vi.spyOn(child.parent, 'postMessage').mockImplementation(() => {});
  try {
    new Function('window', 'document', script)(child, doc);
    const documentId = post.mock.calls[0][0].documentId;
    const send = (m: object) => child.dispatchEvent(new MessageEvent('message', {
      source:child.parent, data:{target:'lola-preview', documentId, ...m},
    }));
    send({type:'pick', on:true, tool:'text'});
    const placement = {type:'pick-at', label:true, x:25, y:50, rect:{x:25,y:50,w:80,h:30}};
    send(placement);
    const result = () => post.mock.calls.map(([m]) => m).findLast(m => m.type === 'pick');
    expect(result()).toMatchObject({selector:'section#ledger', targets:[], html:'', text:'', rect:{x:15,y:-10}});
    expect(doc.querySelector<HTMLElement>('[data-lola-preview]')?.style.display).toBe('none');
    pane.scrollTop += 20;
    pane.dispatchEvent(new Event('scroll'));
    expect(post.mock.calls.map(([m]) => m).findLast(m => m.type === 'selection-bounds').rect).toEqual({x:25,y:30,w:80,h:30});
    hit.mockReturnValue(doc.body);
    send({...placement, editId:7});
    expect(result()).toMatchObject({selector:':root', editId:7, targets:[], rect:{x:-32,y:-120}});
  } finally { frame.remove(); vi.restoreAllMocks(); }
});
