import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { freshSession, ssh, terms, store } from './talk.testkit';
import { SSH } from '$lib/terms.svelte';
import { feed } from '$lib/feed.svelte';
import { changes } from '$lib/changes.svelte';
import { preview } from '$lib/preview.svelte';
import { DaemonService, PreviewService } from '@bindings/desktop';
import SessionPreview from './SessionPreview.svelte';
import PreviewWorkspace from './PreviewWorkspace.svelte';
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

it('shows measured changes and test command results and links test results to the conversation', async () => {
  const id = freshSession({ worktree: '/w' });
  changes.note(id, { files: [{ additions: 12, deletions: 3 }] } as never);
  feed.view(id).items = [{ id: 'test-run', kind: 'tool', order: 1, seq: 1, tool: { name: 'Bash', category: 'run', summary: 'npm test', status: 'error' } }];
  const { container } = render(SessionSide, { sessionId: id });
  expect(screen.getByText('1 file')).toBeInTheDocument();
  expect(screen.getByText('+12')).toBeInTheDocument();
  expect(screen.getByRole('region', { name: 'Local checks' })).toHaveTextContent('Failed');
  expect(screen.queryByText('Recent test commands')).toBeNull();
  expect(screen.queryByRole('button', { name: /older command/ })).toBeNull();
  expect(container.querySelector('[data-side-activity]')).toBeNull();
  await fireEvent.click(within(screen.getByRole('region', { name: 'Local checks' })).getByRole('button', {name: /npm test/}));
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
  const brief = screen.getByRole('region', { name: 'Session task summary' });
  expect(container.querySelector('[data-session-side]')?.firstElementChild).toBe(brief);
  const panel = within(brief).getByRole('region', { name: 'Dev' });
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
  render(PreviewWorkspace);
  try {
    await fireEvent.click(within(screen.getByRole('list', { name: 'Catalog API' })).getByRole('button', { name: /8000/ }));
    await fireEvent.click(await screen.findByRole('button', { name: 'Edit preview address' }));
    const servers = within(await screen.findByRole('navigation', { name: 'Dev servers' }));
    const api = within(servers.getByRole('group', { name: 'Catalog API' }));
    expect(api.getByRole('button', { name: 'Open localhost:8000 in a new tab' })).not.toHaveAttribute('aria-pressed', 'true');
    await waitFor(() => expect(open).toHaveBeenCalledWith(id, 'http://localhost:8000'));
    const site = within(servers.getByRole('group', { name: 'Storefront' }));
    expect(servers.getAllByRole('heading', { name: 'Storefront' })).toHaveLength(1);
    expect(site.getAllByRole('button')).toHaveLength(2);
    expect(within(servers.getByRole('group', { name: 'Other addresses' })).getByRole('button', { name: 'Open localhost:9191 in a new tab' })).toBeInTheDocument();
    await fireEvent.click(site.getByRole('button', { name: 'Open localhost:20007 in a new tab' }));
    expect(open).toHaveBeenLastCalledWith(id, 'http://localhost:20007');
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

it('keeps the brief first and moves actionable evidence ahead of routine sections', async () => {
  const id = freshSession({ worktree: '/w', agentState: 'working', board: { phase: 'investigating' } });
  const { container } = render(SessionSide, { sessionId: id });
  const overview = container.querySelector('[data-session-side]')!;
  const firstAfterBrief = () => overview.children[1];
  const review = screen.getByRole('region', { name: 'Session review' });
  expect(firstAfterBrief()).toBe(review);
  expect(screen.queryByText('investigating')).toBeNull();
  expect(screen.queryByRole('heading', { name: 'Local checks' })).toBeNull();
  expect(screen.queryByRole('region', { name: 'PR status' })).toBeNull();

  store.sessions = [{ ...store.sessions[0], board: { blocked: 'Choose a migration window' } }] as never;
  await waitFor(() => expect(firstAfterBrief()).toHaveTextContent('Choose a migration window'));
  store.sessions = [{ ...store.sessions[0], board: { checks: [{ name: 'Build', state: 'fail' }] } }] as never;
  await waitFor(() => expect(firstAfterBrief()).toHaveTextContent('Local checks'));
  store.sessions = [{ ...store.sessions[0], board: {}, prNumber: 42 }] as never;
  await waitFor(() => expect(firstAfterBrief()).toBe(review));
  expect(within(review).getByRole('region', { name: 'PR status' })).toBeInTheDocument();
});

it('opens code changes from the Review section', async () => {
  const id = freshSession({ worktree: '/w' });
  const open = vi.spyOn(ssh, 'openChanges').mockImplementation(() => {});
  render(SessionSide, { sessionId: id });
  await fireEvent.click(screen.getByRole('button', { name: 'View changes in Review' }));
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
  render(PreviewWorkspace);
  preview.openTabs(id, [
    { url: 'http://localhost:3000/a', title: 'Home', caption: 'Check the header', prep: { device: 'mobile', marks: [{ selector: '#x', label: 'Header' }] } },
    { url: 'http://localhost:3000/b', title: 'Settings' },
  ]);
  try {
    const toggle = await screen.findByRole('button', { name: 'Page sidebar' });
    if (toggle.getAttribute('aria-expanded') === 'false') await fireEvent.click(toggle);
    expect(screen.getByRole('tab', { name: 'Home' })).toBeInTheDocument();
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
    expect(screen.getByText(/Check the header/)).not.toBeVisible();
    say({ type: 'ready', url: 'http://127.0.0.1:5555/b' });
    expect(post).toHaveBeenCalledTimes(1);
  } finally {
    preview.close();
  }
});

it('runs a prepared flow across navigations: first document at any path, resume after the last step seen, then stop', async () => {
  const id = freshSession({ devActive: true, devUrls: ['http://shop.test'] });
  vi.mocked(PreviewService.Open).mockResolvedValue('http://preview.localhost:5555/');
  render(PreviewWorkspace);
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
  render(PreviewWorkspace);
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
  render(PreviewWorkspace);
  preview.open(id, url);
  try {
    const frame = await screen.findByTitle('Preview of localhost:3000') as HTMLIFrameElement;
    const picker = screen.getByRole('button', { name: 'Inspect element' });
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

it('shows dev page loading through proxy startup, document navigation and a failed reload', async () => {
  const url = 'http://shop.test', origin = 'http://preview.localhost:5555';
  const sessionId = freshSession({ devUrls: [url] });
  let resolveOpen!: (value: string) => void;
  const open = vi.mocked(PreviewService.Open).mockImplementationOnce(() => new Promise<string>(resolve => {
    resolveOpen = resolve;
  }) as ReturnType<typeof PreviewService.Open>);
  const view = render(SessionPreview, { inline: { sessionId, url } });
  const bar = () => screen.queryByRole('progressbar', { name: 'Loading page' });
  expect(bar()).toBeVisible();
  expect(bar()).not.toHaveAttribute('aria-valuenow');
  resolveOpen(origin + '/');
  const frame = await screen.findByTitle('Preview of shop.test') as HTMLIFrameElement;
  await fireEvent.load(frame);
  expect(bar()).toBeNull();
  const say = (data: object) => fireEvent(window, new MessageEvent('message', {
    origin, source: frame.contentWindow, data: { source: 'lola-preview', documentId: 'page', ...data },
  }));
  await say({ type: 'ready', url: origin + '/' });
  await say({ type: 'page-loading', loading: true, documentId: 'stale' });
  expect(bar()).toBeNull();
  await say({ type: 'page-loading', loading: true });
  expect(bar()).toBeVisible();
  await fireEvent.load(frame);
  expect(bar()).toBeNull();
  open.mockRejectedValueOnce(new Error('server offline'));
  view.component.reloadPage();
  await screen.findByRole('alert');
  expect(bar()).toBeNull();
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

it('keeps duplicate pages live and targets toolbar actions at the selected route', async () => {
  vi.stubGlobal('innerWidth', 1440);
  const url = 'http://localhost:20000', origin = 'http://preview.localhost:5555';
  const id = freshSession({ devActive: true, devCommands: ['vite'], devUrls: [url] });
  const open = vi.mocked(PreviewService.Open).mockClear().mockResolvedValue(origin + '/');
  const external = vi.spyOn(store, 'openURL').mockResolvedValue();
  const sidebar = within(render(SessionSide, { sessionId: id }).container);
  render(PreviewWorkspace);
  expect(sidebar.queryByRole('button', { name: 'Open preview' })).toBeNull();
  const server = sidebar.getByRole('button', { name: 'localhost:20000' });
  server.focus();
  await fireEvent.click(server);
  try {
    const dialog = within(screen.getByRole('dialog', { name: 'Preview' }));
    const activeFrame = () => within(dialog.getByRole('tabpanel')).getByTitle('Preview of localhost:20000');
    const first = await screen.findByTitle('Preview of localhost:20000') as HTMLIFrameElement;
    await fireEvent(window, new MessageEvent('message', { source: first.contentWindow, origin,
      data: { source: 'lola-preview', type: 'location', title: 'Billing', url: origin + '/billing#details', favicon: origin + '/brand.svg' },
    }));
    const billingTab = dialog.getByRole('tab', { name: 'Billing', hidden: true });
    const icon = billingTab.querySelector('img')!;
    expect(icon).toHaveAttribute('src', origin + '/brand.svg');
    await fireEvent.error(icon);
    expect(billingTab.querySelector('img')).toBeNull();
    await fireEvent.click(screen.getByRole('button', { name: 'Edit preview address' }));
    const add = screen.getByRole('button', { name: 'Open localhost:20000 in a new tab' });
    expect(add).toHaveTextContent(/^localhost:20000$/);
    await fireEvent.click(add);
    const second = activeFrame();
    expect(first).not.toBe(second);
    await fireEvent.click(screen.getByRole('button', { name: 'Mobile' }));
    expect(second.getAttribute('style')).toContain('390px');
    await fireEvent.click(dialog.getByRole('button', { name: 'close' }));
    expect(screen.queryByRole('dialog', { name: 'Preview' })).toBeNull();
    expect(server).toHaveFocus();
    expect(first).toBeInTheDocument();
    expect(second).not.toBeVisible();
    const reopen = sidebar.getByRole('button', { name: 'Open preview' });
    reopen.focus();
    await fireEvent.click(reopen);
    expect(screen.getByRole('button', { name: 'Page sidebar' })).toHaveFocus();
    expect(activeFrame()).toBe(second);
    expect(second.getAttribute('style')).toContain('390px');
    await fireEvent.click(screen.getByRole('tab', { name: 'Billing' }));
    expect(activeFrame()).toBe(first);
    expect(first.getAttribute('style')).toContain('100%');
    expect(open).toHaveBeenCalledTimes(2);
    await fireEvent.click(screen.getByRole('button', { name: 'Open in external browser' }));
    expect(external).toHaveBeenCalledWith(url + '/billing#details');
    await fireEvent.click(screen.getByRole('button', { name: 'Reload' }));
    expect(open).toHaveBeenLastCalledWith(id, url + '/billing#details');
    await fireEvent.keyDown(screen.getByRole('tab', { name: 'Billing' }), { key: 'ArrowDown' });
    const selected = within(screen.getByRole('tablist', { name: 'Open pages' })).getByRole('tab', { selected: true });
    expect(selected).toHaveFocus();
    await fireEvent.keyDown(selected, { key: 'Delete' });
    expect(second).not.toBeInTheDocument();
    expect(preview.tabs).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'Page sidebar' })).toHaveFocus();
    await fireEvent.click(screen.getByRole('button', { name: 'Page sidebar' }));
    await fireEvent.keyDown(dialog.getByRole('tab', { selected: true }), { key: 'Delete' });
    expect(first).not.toBeInTheDocument();
    expect(sidebar.queryByRole('button', { name: 'Open preview' })).toBeNull();
  } finally {
    vi.unstubAllGlobals();
    external.mockRestore();
    preview.close();
  }
});

it('navigates to /cp in place, follows the sign-in redirect, and switches stack origins', async () => {
  const site = 'https://shop.test', api = 'http://localhost:8000';
  const id = freshSession({ apps: [{ name: 'site', devUrls: [site] }, { name: 'api', devUrls: [api] }] });
  const proxy = 'http://preview.localhost:5555';
  const open = vi.mocked(PreviewService.Open).mockClear().mockImplementation((_, url) => {
    const { pathname, search, hash } = new URL(url);
    return Promise.resolve(proxy + pathname + search + hash) as ReturnType<typeof PreviewService.Open>;
  });
  render(PreviewWorkspace);
  preview.open(id, site, { prep: { steps: [{ do: 'click', text: 'Prepare' }] } });
  const edit = () => fireEvent.click(screen.getByRole('button', { name: 'Edit preview address' }));
  const submit = async (value: string) => {
    const input = screen.getByRole('textbox', { name: 'Preview address' });
    await fireEvent.input(input, { target: { value } });
    await fireEvent.click(screen.getByRole('button', { name: 'Go' }));
  };
  try {
    const original = await screen.findByTitle('Preview of shop.test');
    await edit();
    await submit('file:///tmp/example');
    expect(await screen.findByRole('alert')).toHaveTextContent('website address');
    expect(open).toHaveBeenCalledTimes(1);
    expect(original).toBeInTheDocument();

    await submit('/cp');
    await waitFor(() => expect(document.querySelector('iframe')).toHaveAttribute('src', proxy + '/cp'));
    const frame = document.querySelector('iframe')!;
    const post = vi.spyOn(frame.contentWindow!, 'postMessage');
    await fireEvent(window, new MessageEvent('message', { source: frame.contentWindow, origin: proxy,
      data: { source: 'lola-preview', type: 'ready', documentId: 'login', url: proxy + '/cp/auth/login' },
    }));
    expect(post.mock.calls.some(([data]) => data.type === 'prepare')).toBe(false);
    expect(screen.getByRole('button', { name: 'Edit preview address' })).toHaveTextContent(site + '/cp/auth/login');
    await fireEvent.click(screen.getByRole('button', { name: 'Mobile' }));

    await edit();
    await submit('localhost:8000/status?debug=1#health');
    expect(preview.tabs).toHaveLength(1);
    expect(open).toHaveBeenLastCalledWith(id, api + '/status?debug=1#health');
    expect(preview.request).toMatchObject({ url: api + '/status?debug=1#health', prep: { device: 'mobile' } });
    expect(preview.request?.prep?.steps).toBeUndefined();
    expect(await screen.findByTitle('Preview of localhost:8000/status?debug=1#health')).toHaveStyle({ width: '390px' });
    await fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    expect(open).toHaveBeenLastCalledWith(id, site + '/cp/auth/login');
    await fireEvent.click(screen.getByRole('button', { name: 'Back' }));
    expect(open).toHaveBeenLastCalledWith(id, site);
    expect(screen.getByRole('button', { name: 'Back' })).toBeDisabled();

    const postMessage = vi.fn();
    vi.stubGlobal('webkit', { messageHandlers: { lolaExternalPreview: { postMessage } } });
    const tab = preview.tabs[preview.active];
    await edit();
    await submit('sushi.dev');
    expect(preview.tabs).toHaveLength(1);
    expect(postMessage).toHaveBeenCalledWith({ action: 'load', id: tab.id, url: 'https://sushi.dev/' });
    await edit();
    await submit('example.org/docs');
    expect(postMessage).toHaveBeenCalledWith({ action: 'load', id: tab.id, url: 'https://example.org/docs' });
    await edit();
    await submit('localhost:8000/login');
    expect(open).toHaveBeenLastCalledWith(id, api + '/login');
    expect(preview.request?.external).toBeFalsy();
  } finally {
    preview.close();
    vi.unstubAllGlobals();
  }
});

it('opens dev and external new-tab links in Preview, keeping the source page live', async () => {
  const site = 'https://shop.test', proxy = 'http://preview.localhost:5555';
  const id = freshSession({ devUrls: [site] });
  const open = vi.mocked(PreviewService.Open).mockClear().mockResolvedValue(proxy + '/');
  const postMessage = vi.fn();
  vi.stubGlobal('webkit', { messageHandlers: { lolaExternalPreview: { postMessage } } });
  render(PreviewWorkspace);
  preview.open(id, site);
  try {
    const frame = await screen.findByTitle('Preview of shop.test') as HTMLIFrameElement;
    const message = (data: object) => fireEvent(window, new MessageEvent('message', {
      source: frame.contentWindow, origin: proxy, data: { source: 'lola-preview', documentId: 'shop', ...data },
    }));
    await message({ type: 'ready', url: proxy + '/' });
    await message({ type: 'new-tab', url: proxy + '/cart?items=2#summary' });
    expect(preview.request?.url).toBe(site + '/cart?items=2#summary');
    expect(open).toHaveBeenLastCalledWith(id, preview.request?.url);
    preview.select(0);
    await waitFor(() => expect(frame).toBeVisible());
    const docs = 'https://docs.example.org/guide';
    await message({ type: 'new-tab', documentId: 'stale', url: docs });
    expect(preview.tabs).toHaveLength(2);
    await message({ type: 'new-tab', url: docs });
    const tab = preview.tabs[preview.active];
    expect(tab.request.external).toBe(true);
    expect(postMessage).toHaveBeenCalledWith({ action: 'load', id: tab.id, url: docs });
    expect(open).toHaveBeenCalledTimes(2);
    const progress = (loading: boolean, fraction: number) => fireEvent(window, new CustomEvent('lola-external-preview', {
      detail: { id: tab.id, type: 'progress', loading, fraction },
    }));
    const bar = () => screen.queryByRole('progressbar', { name: 'Loading page' });
    await progress(true, 0.42);
    expect(bar()).toHaveAttribute('aria-valuenow', '42');
    preview.select(0);
    await waitFor(() => expect(screen.queryByRole('progressbar', { name: 'Loading page', value: { now: 42 } })).toBeNull());
    preview.select(preview.tabs.indexOf(tab));
    await waitFor(() => expect(bar()).toHaveAttribute('aria-valuenow', '42'));
    await progress(false, 1);
    expect(bar()).toBeNull();
    window.dispatchEvent(new CustomEvent('lola-external-preview', { detail: {
      id: tab.id, type: 'location', url: docs + '/second', title: 'Second page', back: true,
    } }));
    const back = screen.getByRole('button', { name: 'Back' });
    await waitFor(() => expect(back).toBeEnabled());
    await fireEvent.click(back);
    expect(postMessage).toHaveBeenCalledWith({ action: 'back', id: tab.id });
    expect(frame).toBeInTheDocument();
  } finally {
    preview.close();
    cleanup();
    vi.unstubAllGlobals();
  }
});
