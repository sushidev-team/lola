const { chromium } = require('/opt/nori/node_modules/playwright');
const { spawn } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const out = path.resolve(process.env.PILOT_EVIDENCE_DIR || 'pilot-evidence');
fs.mkdirSync(out, { recursive: true });
const log = fs.openSync(path.join(out, 'storybook.log'), 'w');
const server = spawn('./node_modules/.bin/storybook', ['dev', '--ci', '--no-open', '--host', '127.0.0.1', '--port', '6006'], {
  stdio: ['ignore', log, log], env: { ...process.env, STORYBOOK_DISABLE_TELEMETRY: '1', CI: 'true', LARAVEL_BYPASS_ENV_CHECK: '1' },
});
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const report = { fixture: 'existing UI/Dialog stories; synthetic long-content probe explicitly identified below', started_at: new Date().toISOString(), cases: [], errors: [], font_requests: [] };
let browser;
(async () => {
  let ready = false;
  for (let i = 0; i < 120; i++) {
    if (server.exitCode !== null) throw new Error(`Storybook exited ${server.exitCode}; see storybook.log`);
    try { const r = await fetch('http://127.0.0.1:6006/index.json'); if (r.ok) { ready = true; break; } } catch {}
    await sleep(500);
  }
  assert.ok(ready, 'Storybook must start within 60 seconds');
  browser = await chromium.launch({ headless: true });
  for (const theme of ['light', 'dark']) {
    for (const size of [{ name: 'desktop', width: 1280, height: 900 }, { name: 'narrow', width: 390, height: 844 }]) {
      const page = await browser.newPage({ viewport: { width: size.width, height: size.height }, colorScheme: theme, reducedMotion: 'reduce' });
      const errors = [];
      page.on('request', request => { if (request.url().includes('.woff')) report.font_requests.push({ url: request.url() }); });
      page.on('requestfailed', request => { if (request.url().includes('.woff')) report.font_requests.push({ failed: request.url(), reason: request.failure()?.errorText }); });
      page.on('console', message => { if (/font|OTS/i.test(message.text())) report.font_requests.push({ console: message.text() }); });
      // Storybook inherits Laravel's disabled publicDir; serve only committed fonts.
      await page.route('**/fonts/*.woff2', async route => {
        const pathname = new URL(route.request().url()).pathname;
        assert.match(pathname, /^\/fonts\/[a-z0-9-]+\.woff2$/);
        report.font_requests.push({ fulfilled: pathname });
        await route.fulfill({ path: path.resolve('public', '.' + pathname), contentType: 'font/woff2' });
      });
      // Laravel/Storybook emits this invalid development prefix for BlockNote's
      // duplicate Inter faces; fulfill only those exact installed font assets.
      await page.route('**/undefined/node_modules/@blocknote/core/src/fonts/inter-v12-latin/*', async route => {
        const name = path.posix.basename(new URL(route.request().url()).pathname);
        assert.match(name, /^inter-v12-latin-(?:regular|[0-9]{3})\.woff2?$/);
        report.font_requests.push({ fulfilled_dependency_font: name });
        await route.fulfill({ path: path.resolve('node_modules/@blocknote/core/src/fonts/inter-v12-latin', name), contentType: name.endsWith('woff2') ? 'font/woff2' : 'font/woff' });
      });
      page.on('pageerror', e => errors.push(e.message));
      await page.goto('http://127.0.0.1:6006/iframe.html?id=ui-dialog--with-form&viewMode=story', { waitUntil: 'domcontentloaded' });
      const trigger = page.getByRole('button', { name: 'Edit Profile', exact: true });
      await trigger.waitFor({ timeout: 60000 });
      await page.evaluate(theme => document.documentElement.classList.toggle('dark', theme === 'dark'), theme);
      await trigger.click();
      const dialog = page.getByRole('dialog');
      await dialog.waitFor();
      await page.waitForTimeout(350);
      const fonts = await page.evaluate(async () => {
        await document.fonts.load('400 16px Inter');
        await document.fonts.load('500 16px Inter');
        await document.fonts.ready;
        return { inter400: document.fonts.check('400 16px Inter'), inter500: document.fonts.check('500 16px Inter') };
      });
      assert.ok(fonts.inter400 && fonts.inter500, 'Committed Inter fonts must load before capture');
      const box = await dialog.boundingBox();
      assert.ok(box && box.x >= 15 && box.y >= 15 && box.x + box.width <= size.width - 15 && box.y + box.height <= size.height - 15, 'Dialog must retain viewport gutters');
      assert.ok(await dialog.evaluate(el => el.contains(document.activeElement)), 'Initial focus must be in dialog');
      for (let i = 0; i < 8; i++) {
        await page.keyboard.press('Tab');
        assert.ok(await dialog.evaluate(el => el.contains(document.activeElement)), 'Tab focus must stay in dialog');
      }
      const styles = await dialog.evaluate(el => {
        const overlay = [...document.querySelectorAll('div[data-state="open"]')].find(n => n !== el && getComputedStyle(n).position === 'fixed' && getComputedStyle(n).inset === '0px');
        const probe = document.createElement('div');
        probe.style.backgroundColor = 'var(--overlay-scrim)';
        probe.style.boxShadow = 'var(--shadow-dialog)';
        document.body.appendChild(probe);
        const result = { actual_scrim: overlay ? getComputedStyle(overlay).backgroundColor : null, token_scrim: getComputedStyle(probe).backgroundColor, actual_shadow: getComputedStyle(el).boxShadow, token_shadow: getComputedStyle(probe).boxShadow };
        probe.remove(); return result;
      });
      if (process.env.PILOT_ASSERT_STYLE === '1') {
        assert.equal(styles.actual_scrim, styles.token_scrim, 'Scrim must match existing theme token');
        const visibleShadows = value => value.split(/,(?![^(]*\))/).map(v => v.trim()).filter(v => !v.startsWith('rgba(0, 0, 0, 0)')).join(', ');
        assert.equal(visibleShadows(styles.actual_shadow), visibleShadows(styles.token_shadow), 'Visible dialog elevation must match existing token');
      }
      await page.screenshot({ path: path.join(out, `${theme}-${size.name}.png`), animations: 'disabled' });
      await dialog.evaluate(el => {
        const long = document.createElement('div');
        long.dataset.pilotSyntheticOverflow = 'true';
        for (let i = 0; i < 70; i++) { const p = document.createElement('p'); p.textContent = `Long dialog content row ${i + 1}`; long.appendChild(p); }
        el.insertBefore(long, el.lastElementChild);
        el.scrollTop = el.scrollHeight;
      });
      await page.waitForTimeout(150);
      const overflow = await dialog.evaluate(el => ({ scrollTop: el.scrollTop, scrollHeight: el.scrollHeight, clientHeight: el.clientHeight, scrollWidth: el.scrollWidth, clientWidth: el.clientWidth }));
      assert.ok(overflow.scrollTop > 0 && overflow.scrollWidth <= overflow.clientWidth + 1, 'Long content must scroll vertically without horizontal overflow');
      const close = dialog.getByRole('button', { name: 'Close', exact: true });
      const closeBox = await close.boundingBox();
      const tallBox = await dialog.boundingBox();
      assert.ok(closeBox && tallBox && closeBox.y >= tallBox.y && closeBox.y + closeBox.height <= tallBox.y + tallBox.height, 'Close button must remain in visible scrollport');
      await page.screenshot({ path: path.join(out, `${theme}-${size.name}-overflow.png`), animations: 'disabled' });
      await page.keyboard.press('Escape');
      await dialog.waitFor({ state: 'hidden' });
      assert.ok(await trigger.evaluate(el => el === document.activeElement), 'Escape must restore trigger focus');
      assert.deepEqual(errors, [], 'No page runtime errors');
      report.cases.push({ theme, viewport: size, fonts, styles, gutter_bounds: box, keyboard: { initial_focus: true, tab_containment: true, escape: true, focus_return: true }, synthetic_overflow: overflow, close_visible_after_scroll: true, errors });
      await page.close();
    }
  }
  report.result = 'pass';
})().catch(e => { report.result = 'fail'; report.errors.push(e.stack); process.exitCode = 1; }).finally(async () => {
  if (browser) await browser.close();
  server.kill('SIGTERM');
  fs.closeSync(log);
  report.finished_at = new Date().toISOString();
  fs.writeFileSync(path.join(out, 'interaction.json'), JSON.stringify(report, null, 2) + '\n');
  console.log(JSON.stringify({ result: report.result, cases: report.cases.length, evidence: out, errors: report.errors }));
});
