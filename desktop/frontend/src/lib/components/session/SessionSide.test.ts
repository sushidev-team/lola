import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { freshSession, ssh, terms, store } from './talk.testkit';
import { SSH } from '$lib/terms.svelte';
import { feed } from '$lib/feed.svelte';
import { changes } from '$lib/changes.svelte';
import { preview } from '$lib/preview.svelte';
import { DaemonService, PreviewService } from '@bindings/desktop';
import SessionPreview from './SessionPreview.svelte';
import SessionSide from './SessionSide.svelte';

afterEach(cleanup);

describe('conversation overview', () => {
  it('shows progress reported by the agent', async () => {
    const id = freshSession({ worktree: '/w', board: {
      updatedAt: new Date().toISOString(), hasProgress: true, percent: 40,
      progressDerived: true, done: 2, total: 5,
      todos: [{ text: 'Implement', state: 'done' }, { text: 'Test', state: 'active' }],
    } });
    terms.select(id, SSH);
    render(SessionSide, { sessionId: id });
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '40');
    expect(screen.getByText('Test')).toBeInTheDocument();

  });

  it('uses posted plans when there is no report board, without inventing progress', () => {
    const id = freshSession({ worktree: '/w' });
    ssh.view(id).items = [{ id: 'plan', kind: 'element', element: {
      id: 'plan', kind: 'plan', title: 'Improve history', steps: [
        { text: 'Inspect', state: 'done' }, { text: 'Implement', state: 'active' },
      ],
    } }] as never;
    render(SessionSide, { sessionId: id });
    expect(screen.getByRole('progressbar', { name: 'Reported plan' })).toHaveAttribute('aria-valuenow', '1');
    expect(screen.getByText('Improve history')).toBeInTheDocument();
    expect(screen.queryByText('No plan reported yet.')).toBeNull();
  });
});

it('shows measured changes and test command results and navigates recent activity', async () => {
  const id = freshSession({ worktree: '/w' });
  changes.note(id, { files: [{ additions: 12, deletions: 3 }] } as never);
  feed.view(id).items = [{ id: 'test-run', kind: 'tool', order: 1, seq: 1, tool: { name: 'Bash', category: 'run', summary: 'npm test', status: 'error' } }];
  const { container } = render(SessionSide, { sessionId: id });
  expect(screen.getByText('1 file')).toBeInTheDocument();
  expect(screen.getByText('+12')).toBeInTheDocument();
  expect(screen.getByRole('region', { name: 'Local checks' })).toHaveTextContent('Failed');
  expect(screen.queryByText('Recent test commands')).toBeNull();
  expect(screen.queryByRole('button', { name: /older command/ })).toBeNull();
  await fireEvent.click(container.querySelector('[data-side-activity] button')!);
  expect(ssh.view(id).jump?.block).toBe('item:test-run');
});


it('unifies reported checks and command evidence without an empty duplicate', async () => {
  const id = freshSession({ worktree: '/w', prNumber: 12, prUrl: 'https://github.com/acme/app/pull/12', checks: 'fail', prStale: true, board: {
    updatedAt: new Date().toISOString(), phase: 'done',
    checks: [{ name: 'tests', state: 'pass', summary: '340 passed, 7 skipped', evidence: 'ran' }, { name: 'typecheck', state: 'pass' }],
  } });
  const { container } = render(SessionSide, { sessionId: id });
  expect(screen.getAllByRole('heading', { name: 'Local checks' })).toHaveLength(1);
  expect(screen.getByText('340 passed, 7 skipped')).toBeInTheDocument();
  expect(screen.queryByText(/^[· ]*ran$/)).toBeNull();
  expect(container.querySelector('[data-side-tests]')).toBeNull();
  expect(screen.queryByText(/No test commands/)).toBeNull();
  expect(screen.queryByText('Local checks', { selector: 'span' })).toBeNull();
  expect(screen.getByRole('region', { name: 'PR status' })).toBeInTheDocument();

});

it('keeps local report results separate from actual command outcomes', () => {
  const id = freshSession({ worktree: '/w', board: { updatedAt: new Date().toISOString(), checks: [{ name: 'tests', state: 'pass', evidence: 'contradicted', evidenceNote: 'Latest run failed' }] } });
  feed.view(id).items = [{ id: 'failed-test', kind: 'tool', order: 1, seq: 1, tool: { name: 'Bash', category: 'run', summary: 'npm test', status: 'error' } }];
  render(SessionSide, { sessionId: id });
  const local = screen.getByRole('region', { name: 'Local checks' });
  expect(within(local).getByText('1/1 passed')).toBeInTheDocument();
  expect(within(local).getByText(/last run failed/)).toBeInTheDocument();
  expect(within(local).getByText('Failed')).toBeInTheDocument();
});

it('moves Dev controls into the overview and preserves actions and URLs', async () => {
  const id = freshSession({ devCommands: ['npm run dev'], devActive: true, devUrls: ['http://localhost:3000', 'http://127.0.0.1:5173'] });
  const dev = vi.spyOn(store, 'dev').mockResolvedValue(undefined);
  const open = vi.spyOn(preview, 'open').mockImplementation(() => {});
  const { container } = render(SessionSide, { sessionId: id });
  const panel = container.querySelector('[data-side-dev]')! as HTMLElement;
  await fireEvent.click(within(panel).getByRole('button', { name: 'Stop dev' }));
  expect(dev).toHaveBeenCalledWith(id, false);
  await fireEvent.click(within(panel).getByRole('button', { name: /3000/ }));
  expect(open).toHaveBeenCalledWith(id, 'http://localhost:3000');
  await fireEvent.click(within(panel).getByRole('button', { name: /5173/ }));
  expect(open).toHaveBeenCalledWith(id, 'http://127.0.0.1:5173');
  dev.mockRestore(); open.mockRestore();
});




it('groups stack links by display name and keeps legacy links accessible', async () => {
  const id = freshSession({
    stack: true, devActive: true, devCommands: ['web: vite', 'api: serve'],
    apps: [{ name: 'web', label: 'Storefront', ports: [20006, 20007] }, { name: 'api', label: 'Catalog API', devUrls: ['http://localhost:8000'] }],
    devUrls: ['http://127.0.0.1:20006', 'http://localhost:20007', 'http://localhost:9191'],
  });
  const open = vi.mocked(PreviewService.Open).mockResolvedValue('http://preview.localhost:5555/');
  const external = vi.spyOn(store, 'openURL').mockResolvedValue(undefined);
  render(SessionSide, { sessionId: id });
  render(SessionPreview);
  try {
    await fireEvent.click(within(screen.getByRole('list', { name: 'Catalog API' })).getByRole('button', { name: /8000/ }));
    const servers = within(await screen.findByRole('navigation', { name: 'Dev servers' }));
    const api = within(servers.getByRole('group', { name: 'Catalog API' }));
    expect(api.getByRole('button', { name: 'localhost:8000' })).toHaveAttribute('aria-pressed', 'true');
    await waitFor(() => expect(open).toHaveBeenCalledWith(id, 'http://localhost:8000'));
    const site = within(servers.getByRole('group', { name: 'Storefront' }));
    expect(servers.getAllByRole('heading', { name: 'Storefront' })).toHaveLength(1);
    expect(site.getAllByRole('button')).toHaveLength(2);
    await fireEvent.click(site.getByRole('button', { name: 'localhost:20007' }));
    expect(open).toHaveBeenLastCalledWith(id, 'http://localhost:20007');
    expect(within(servers.getByRole('group', { name: 'Other addresses' })).getByRole('button', { name: 'localhost:9191' })).toBeInTheDocument();
    expect(external).not.toHaveBeenCalled();
    await fireEvent.click(screen.getByRole('button', { name: 'Open in external browser' }));
    expect(external).toHaveBeenCalledWith('http://localhost:20007');
  } finally {
    preview.close();
    external.mockRestore();
  }
});

it('collapses completed plans and keeps the detail available', async () => {
  const id = freshSession({ board: { phase: 'done', hasProgress: true, percent: 100, todos: [{ text: 'Implement it', state: 'done' }] } });
  render(SessionSide, { sessionId: id });
  expect(screen.queryByText('Implement it')).toBeNull();
  await fireEvent.click(screen.getByRole('button', { name: /Plan complete/ }));
  expect(screen.getByText('Implement it')).toBeInTheDocument();
  expect(screen.queryByRole('heading', { name: 'Session' })).toBeNull();
});

it('opens Changes from the whole card', async () => {
  const id = freshSession({ worktree: '/w' });
  const open = vi.spyOn(ssh, 'openChanges').mockImplementation(() => {});
  render(SessionSide, { sessionId: id });
  await fireEvent.click(screen.getByRole('button', { name: 'View changes' }));
  expect(open).toHaveBeenCalledWith(store.sessionById(id));
  open.mockRestore();
});


it('places the editable task outline in Overview', () => {
  const id = freshSession({ tasks: [{ id: 'work', kind: 'task', text: 'Implement invitations', state: 'doing', author: 'user', updatedAt: new Date().toISOString() }] });
  render(SessionSide, { sessionId: id });
  const overview = screen.getByRole('complementary', { name: 'Session overview' });
  expect(within(screen.getByRole('region', { name: 'Session tasks' })).getByText('Implement invitations')).toBeInTheDocument();
  expect(within(overview).getByRole('button', { name: 'Add task' })).toBeInTheDocument();
  expect(screen.queryByText('No plan reported yet.')).toBeNull();
});


it('reveals older test commands only when more than three are available', async () => {
  const id = freshSession({ worktree: '/w' });
  feed.view(id).items = Array.from({ length: 5 }, (_, i) => ({ id: `test-${i}`, kind: 'tool', order: i + 1, seq: i + 1, tool: { name: 'Bash', category: 'run', summary: `npm test -- test${i}`, status: 'ok' } }));
  render(SessionSide, { sessionId: id });
  const local = within(screen.getByRole('region', { name: 'Local checks' }));
  expect(local.queryByRole('button', { name: /test0/ })).toBeNull();
  await fireEvent.click(local.getByRole('button', { name: 'Show 2 older commands' }));
  expect(local.getByRole('button', { name: /test0/ })).toBeInTheDocument();
  await fireEvent.click(local.getByRole('button', { name: /test0/ }));
  expect(ssh.view(id).jump?.block).toBe('item:test-0');
  await fireEvent.click(local.getByRole('button', { name: 'Show fewer' }));
  expect(local.queryByRole('button', { name: /test0/ })).toBeNull();
});

it('shows the agent\'s port-setup request beside the dev toggle and opens the review, never applying anything', async () => {
  const { nav } = await import('$lib/nav.svelte');
  const id = freshSession({ worktree: '/w', project: 'fe', devCommands: ['npm run dev'], portSetupAsk: 'Port 3000 is fixed in vite.config.ts' });
  render(SessionSide, { sessionId: id });
  expect(screen.getByText('Port 3000 is fixed in vite.config.ts')).toBeInTheDocument();
  await fireEvent.click(screen.getByRole('button', { name: 'Review port setup' }));
  expect(nav.overlay).toBe('project');
  expect(nav.overlayTab).toBe('worktree');
  await fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
  expect(screen.queryByText('Port 3000 is fixed in vite.config.ts')).toBeNull();
});

it('shows a sandboxed agent\'s dev-start request while dev is off and starts nothing until the click', async () => {
  const id = freshSession({ worktree: '/w', project: 'fe', devCommands: ['npm run dev'], devStartAsk: 'check the cart page' });
  const dev = vi.spyOn(store, 'dev').mockResolvedValue(undefined as never);
  render(SessionSide, { sessionId: id });
  expect(screen.getByText('check the cart page')).toBeInTheDocument();
  expect(dev).not.toHaveBeenCalled();
  const buttons = screen.getAllByRole('button', { name: 'Start dev' });
  await fireEvent.click(buttons[buttons.length - 1]);
  expect(dev).toHaveBeenCalledWith(id, true);
});

it('prepares each tab the way the agent said: viewport, caption, then asks the page to scroll and outline, once per document', async () => {
  const id = freshSession({ devActive: true, devUrls: ['http://localhost:3000'] });
  vi.mocked(PreviewService.Open).mockResolvedValue('http://preview.localhost:5555/');
  render(SessionPreview);
  preview.openTabs(id, [
    { url: 'http://localhost:3000/a', title: 'Home', caption: 'Check the header', prep: { device: 'mobile', marks: [{ selector: '#x', label: 'Header' }] } },
    { url: 'http://localhost:3000/b', title: 'Settings' },
  ]);
  try {
    expect(await screen.findByRole('tab', { name: 'Home' })).toBeInTheDocument();
    expect(screen.getByText(/Check the header/)).toBeInTheDocument();
    const frame = await waitFor(() => { const f = document.querySelector('iframe'); expect(f).not.toBeNull(); return f as HTMLIFrameElement; });
    expect(frame.getAttribute('style')).toContain('390');
    const post = vi.spyOn(frame.contentWindow!, 'postMessage');
    const say = (data: object) => window.dispatchEvent(new MessageEvent('message', { data: { source: 'lola-preview', ...data }, source: frame.contentWindow, origin: 'http://preview.localhost:5555' }));
    say({ type: 'ready', url: 'http://127.0.0.1:5555/somewhere-else' });
    expect(post).not.toHaveBeenCalled();
    say({ type: 'ready', url: 'http://127.0.0.1:5555/a' });
    expect(post).toHaveBeenCalledWith({ target: 'lola-preview', type: 'prepare', spec: { waitFor: undefined, scrollY: undefined, marks: [{ selector: '#x', label: 'Header' }] } }, 'http://preview.localhost:5555');
    say({ type: 'prepared', found: [false], waited: true });
    expect(await screen.findByText('Not found on the page: Header')).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('tab', { name: 'Settings' }));
    expect(screen.queryByText(/Check the header/)).toBeNull();
    say({ type: 'ready', url: 'http://127.0.0.1:5555/b' });
    expect(post).toHaveBeenCalledTimes(1);
  } finally {
    preview.close();
  }
});

it('runs a prepared flow across navigations: first document at any path, resume after the last step seen, then stop', async () => {
  const id = freshSession({ devActive: true, devUrls: ['http://shop.test'] });
  vi.mocked(PreviewService.Open).mockResolvedValue('http://preview.localhost:5555/');
  render(SessionPreview);
  const steps = [
    { do: 'fill', selector: '#e', value: 'dev@shop.test' },
    { do: 'click', text: 'Sign in' },
    { do: 'goto', path: '/cart' },
    { do: 'click', text: 'Checkout' },
  ];
  preview.open(id, 'http://shop.test/cart', { prep: { steps, marks: [{ selector: '.total' }] }, caption: 'Cart total' });
  try {
    const frame = await waitFor(() => { const f = document.querySelector('iframe'); expect(f).not.toBeNull(); return f as HTMLIFrameElement; });
    expect(screen.getByText(/it performs 4 steps here/)).toBeInTheDocument();
    const post = vi.spyOn(frame.contentWindow!, 'postMessage');
    const say = (data: object) => window.dispatchEvent(new MessageEvent('message', { data: { source: 'lola-preview', ...data }, source: frame.contentWindow, origin: 'http://preview.localhost:5555' }));
    const specs = () => post.mock.calls.map((c) => (c[0] as { spec: { from?: number; steps?: unknown[] } }).spec);
    // The app redirected to its login page: a flow starts there all the same.
    say({ type: 'ready', url: 'http://127.0.0.1:5555/login' });
    expect(specs()).toHaveLength(1);
    expect(specs()[0].from).toBeUndefined();
    // jsdom's postMessage spy accepts reactive proxies that WKWebView rejects.
    expect(structuredClone(post.mock.calls[0][0]).spec).toMatchObject({ steps, marks: [{ selector: '.total' }] });
    // Step 1 (Sign in) navigated: the next document resumes after it.
    say({ type: 'step', i: 0 });
    say({ type: 'step', i: 1 });
    say({ type: 'ready', url: 'http://127.0.0.1:5555/dashboard' });
    expect(specs()[1].from).toBe(2);
    // The goto navigated too.
    say({ type: 'step', i: 2 });
    say({ type: 'ready', url: 'http://127.0.0.1:5555/cart' });
    expect(specs()[2].from).toBe(3);
    // A step that did not work is shown as text, and nothing resumes afterwards.
    say({ type: 'prepared', found: [false], waited: false, failed: { index: 3, step: 'click Checkout', why: '<b>nothing</b> to click was found' } });
    expect(await screen.findByText(/Step 4 \(click Checkout\) didn't work: <b>nothing<\/b> to click was found/)).toBeInTheDocument();
    say({ type: 'ready', url: 'http://127.0.0.1:5555/elsewhere' });
    expect(post).toHaveBeenCalledTimes(3);
  } finally {
    preview.close();
  }
});

it('stops resuming a flow that keeps navigating', async () => {
  const id = freshSession({ devActive: true, devUrls: ['http://shop.test'] });
  vi.mocked(PreviewService.Open).mockResolvedValue('http://preview.localhost:5555/');
  render(SessionPreview);
  preview.open(id, 'http://shop.test/', { prep: { steps: [{ do: 'click', text: 'Next' }, { do: 'click', text: 'Next' }] } });
  try {
    const frame = await waitFor(() => { const f = document.querySelector('iframe'); expect(f).not.toBeNull(); return f as HTMLIFrameElement; });
    const post = vi.spyOn(frame.contentWindow!, 'postMessage');
    const say = (data: object) => window.dispatchEvent(new MessageEvent('message', { data: { source: 'lola-preview', ...data }, source: frame.contentWindow, origin: 'http://preview.localhost:5555' }));
    say({ type: 'ready', url: 'http://127.0.0.1:5/' });
    for (let n = 0; n < 6; n++) { say({ type: 'step', i: 0 }); say({ type: 'ready', url: 'http://127.0.0.1:5/loop' }); }
    expect(post).toHaveBeenCalledTimes(3); // the first load plus two resumes, one per step
  } finally {
    preview.close();
  }
});

it('reopens a verified protected route directly and replays sign-in when the route redirects to login', async () => {
  const origin = 'http://preview.localhost:5555';
  const open = vi.mocked(PreviewService.Open).mockReset().mockResolvedValueOnce(origin + '/login').mockResolvedValueOnce(origin + '/settings').mockResolvedValueOnce(origin + '/login');
  const inline = { sessionId: 'stack', url: 'http://shop.test/login', prep: {
    steps: [{ do: 'fill', selector: '#email', value: 'review@example.test' }, { do: 'goto', path: '/settings' }],
    marks: [{selector: '#legal'}],
  }};
  const props = {inline, resumeKey:'stack-settings-reopen'};
  const frame = () => screen.getByTitle('Preview of shop.test/login') as HTMLIFrameElement;
  const say = (data: object) => window.dispatchEvent(new MessageEvent('message', { data: {source:'lola-preview', ...data}, origin, source:frame().contentWindow }));
  const first = render(SessionPreview, props);
  await waitFor(() => expect(frame()).toHaveAttribute('src', origin + '/login'));
  say({type:'prepared', found:[true], url:origin + '/settings'});
  first.unmount();
  render(SessionPreview, props);
  await waitFor(() => expect(frame()).toHaveAttribute('src', origin + '/settings'));
  const post = vi.spyOn(frame().contentWindow!, 'postMessage');
  say({type:'ready', documentId:'restored', url:origin + '/settings'});
  say({type:'ready', documentId:'restored', url:origin + '/settings'});
  expect(post.mock.calls.filter(([m]) => m.type === 'prepare')).toHaveLength(1);
  expect(post).toHaveBeenCalledWith(expect.objectContaining({spec:expect.objectContaining({steps:undefined, marks:inline.prep.marks})}), origin);
  say({type:'ready', documentId:'expired', url:origin + '/login'});
  await waitFor(() => expect(frame()).toHaveAttribute('src', origin + '/login'));
  expect(open).toHaveBeenCalledTimes(3);
});

it('waits for the picker and preserves edits made during a feedback send', async () => {
  const origin = 'http://preview.localhost:5555', url = 'http://localhost:3000';
  const id = freshSession({ devUrls: [url] });
  vi.mocked(PreviewService.Open).mockResolvedValue(origin + '/');
  let finish!: () => void;
  vi.mocked(DaemonService.SendFeedback).mockImplementationOnce(() => new Promise(resolve => {
    finish = () => resolve({ delivered: true } as never);
  }) as ReturnType<typeof DaemonService.SendFeedback>);
  const refresh = vi.spyOn(store, 'refresh').mockResolvedValue();
  render(SessionPreview);
  preview.open(id, url);
  try {
    const frame = await screen.findByTitle('Preview of localhost:3000') as HTMLIFrameElement;
    const picker = screen.getByRole('button', { name: 'Pick element' });
    const say = (data: object) => fireEvent(window, new MessageEvent('message', {
      source: frame.contentWindow, origin, data: { source: 'lola-preview', ...data },
    }));
    expect(picker).toBeDisabled();
    await say({ type: 'ready', documentId: 'one' });
    expect(picker).toBeEnabled();
    await fireEvent.click(picker);
    expect(picker).not.toHaveAttribute('aria-pressed', 'true');
    await say({ type: 'mode', picking: true });
    expect(picker).toHaveAttribute('aria-pressed', 'true');
    await say({ type: 'pick', selector: '#title', url: origin + '/#/details' });
    const note = screen.getByRole('textbox', { name: 'Element comment' });
    await fireEvent.input(note, { target: { value: 'Change title' } });
    await fireEvent.click(screen.getByRole('button', { name: 'Send to agent' }));
    expect(DaemonService.SendFeedback).toHaveBeenCalledWith({
      session: id, elements: [expect.objectContaining({ url: url + '/#/details', note: 'Change title' })],
    });
    await fireEvent.input(note, { target: { value: 'Change title and spacing' } });
    finish();
    await waitFor(() => expect(refresh).toHaveBeenCalled());
    expect(screen.getByRole('textbox', { name: 'Element comment' })).toHaveValue('Change title and spacing');
  } finally { preview.close(); refresh.mockRestore(); }
});

it('removes a parked preview when its stack stops advertising the origin', async () => {
  const url = 'http://shop.test', id = freshSession({ apps: [{ name: 'shop', devUrls: [url] }] });
  vi.mocked(PreviewService.Open).mockResolvedValue('http://preview.localhost:5555/settings');
  render(SessionPreview, { inline: { sessionId: id, url: url + '/settings' }, active: false });
  await screen.findByTitle('Preview of shop.test/settings');
  store.sessions = [];
  expect(await screen.findByRole('alert')).toHaveTextContent('no longer reports this dev server');
  expect(document.querySelector('iframe')).toBeNull();
});
