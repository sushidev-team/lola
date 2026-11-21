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
    // The script reports each step once it has had its effect, so a step that navigates
    // (a login) is resumed after it, and one still looking for its target is run again.
    let done = -1;
    await page.exposeFunction("__lolaStepDone", i => { done = i; });
    await page.goto(input.url, { waitUntil: 'networkidle', timeout: 30000 });
    await page.evaluate(() => document.fonts.ready);
    const url = page.url();
    if (!/^https?:\/\//.test(url)) throw new Error('Capture redirected outside HTTP(S).');
    let prepared = { found: [], waited: false };
    const steps = input.steps || [];
    if (input.prepareJS && (input.waitFor || input.scrollY || (input.marks && input.marks.length) || steps.length)) {
      const spec = { redirected: new URL(url).pathname !== new URL(input.url).pathname, waitFor: input.waitFor, scrollY: input.scrollY, marks: input.marks, steps };
      let from = 0;
      for (let attempt = 0; ; attempt++) {
        try {
          prepared = await page.evaluate(s => window.__lolaPrepare(s, 12000), { ...spec, from });
          break;
        } catch (error) {
          // A click or goto that navigated destroyed the script; carry on from the next step.
          if (!/context was destroyed|navigation/i.test(String(error)) || attempt > steps.length) throw error;
          await page.waitForLoadState("networkidle", { timeout: 30000 });
          from = done + 1;
        }
      }
      if (prepared.failed) throw new Error("step " + (prepared.failed.index + 1) + " (" + prepared.failed.step + ") did not work: " + prepared.failed.why + "; nothing was saved");
      if (steps.length) await page.waitForLoadState("networkidle", { timeout: 10000 }).catch(() => {});
      if (!/^https?:\/\//.test(page.url())) throw new Error("Capture redirected outside HTTP(S).");
      // Ask again right before the shot: the outline must be on the page that is saved.
      // waitFor is judged on the final document too: a step may have replaced the page.
      if (input.waitFor && !(await page.evaluate(w => window.__lolaReady({ waitFor: w }), input.waitFor))) prepared.waited = true;
      if (input.marks && input.marks.length) prepared.found = await page.evaluate(m => window.__lolaCheck({ marks: m }), input.marks);
    }
    const target = input.selector ? page.locator(input.selector) : page;
    await target.screenshot({ path: input.destination, type: 'png', animations: 'disabled', timeout: 15000 });
    process.stdout.write(JSON.stringify({ url: page.url(), found: prepared.found, waited: prepared.waited }));
  } finally { await browser.close(); }
})().catch(error => { process.stderr.write(String(error)); process.exitCode = 1; });
