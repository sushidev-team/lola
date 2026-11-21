const fs = require('node:fs');
const { createRequire } = require('node:module');
const path = require('node:path');
const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const local = createRequire(path.join(process.cwd(), 'package.json'));
let playwright;
try { playwright = local('playwright'); }
catch { try { playwright = local('playwright-core'); }
catch { throw new Error('Browser capture requires Playwright in this project and an installed Chromium browser.'); } }
(async () => {
  const browser = await playwright.chromium.launch({ headless: true, channel: input.channel || undefined });
  try {
    const context = await browser.newContext({ viewport: { width: input.width, height: input.height }, reducedMotion: 'reduce', serviceWorkers: 'block' });
    const page = await context.newPage();
    page.setDefaultTimeout(15000);
    // addInitScript goes through the browser protocol, so an app's CSP cannot block it
    // the way it would a <script> element.
    if (input.prepareJS) await page.addInitScript({ content: input.prepareJS });
    await page.goto(input.url, { waitUntil: 'networkidle', timeout: 30000 });
    await page.evaluate(() => document.fonts.ready);
    const url = page.url();
    if (!/^https?:\/\//.test(url)) throw new Error('Capture redirected outside HTTP(S).');
    let prepared = { found: [], waited: false };
    if (input.prepareJS && (input.waitFor || input.scrollY || (input.marks && input.marks.length))) {
      prepared = await page.evaluate(spec => window.__lolaPrepare(spec, 12000), { waitFor: input.waitFor, scrollY: input.scrollY, marks: input.marks });
    }
    const target = input.selector ? page.locator(input.selector) : page;
    await target.screenshot({ path: input.destination, type: 'png', animations: 'disabled', timeout: 15000 });
    process.stdout.write(JSON.stringify({ url, found: prepared.found, waited: prepared.waited }));
  } finally { await browser.close(); }
})().catch(error => { process.stderr.write(String(error)); process.exitCode = 1; });
