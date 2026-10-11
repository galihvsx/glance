// detail-interact.mjs — interaction checks on the peek panel:
// 1. click upvote -> count becomes 1, button pressed
// 2. open the state dropdown in the property strip
// 3. open the start-date popover
// Usage: node detail-interact.mjs <baseUrl> <sessionCookie> <issueUuid> <outDir>
import { chromium } from 'playwright-core';

const [base, cookie, uuid, outDir] = process.argv.slice(2);
// Chromium path for headless screenshots: override with PLAYWRIGHT_CHROMIUM_PATH.
const chromiumPath = process.env.PLAYWRIGHT_CHROMIUM_PATH ||
  '/home/hatch/.cache/ms-playwright/chromium-1243/chrome-linux64/chrome';

const browser = await chromium.launch({
  headless: true,
  executablePath: chromiumPath,
});
const ctx = await browser.newContext({ viewport: { width: 1920, height: 1080 } });
await ctx.addCookies([{ name: 'glance_session', value: cookie, domain: '127.0.0.1', path: '/' }]);
const page = await ctx.newPage();
const errors = [];
page.on('pageerror', (e) => errors.push('pageerror: ' + e.message));
await page.goto(`${base}/w/acme/p/EXAMP/board?peek=${uuid}`, { waitUntil: 'networkidle', timeout: 30000 });
await page.waitForTimeout(1200);

// 1. vote toggle
await page.screenshot({ path: `${outDir}/interact-open.png` });
const panelCount = await page.locator('[role="dialog"]').count();
console.log('dialog panels:', panelCount);
const voteGroup = page.locator('[aria-label="Issue votes"]');
console.log('vote groups:', await voteGroup.count());
await voteGroup.locator('button').first().click();
await page.waitForTimeout(800);
const html = await voteGroup.innerHTML();
console.log('vote group html:', html.slice(0, 600));
const pressed = await voteGroup.locator('button').first().getAttribute('aria-pressed');
const count = await voteGroup.locator('span').first().textContent();
console.log('vote: aria-pressed =', pressed, '| count =', count);

// 2. state dropdown in the property strip
const strip = page.locator('text=Add assignees').locator('..');
await page.locator('[role="dialog"]').getByText('In Progress', { exact: true }).click();
await page.waitForTimeout(600);
const stateOpts = await page.locator('[role="dialog"] [class*="w-52"] button').count().catch(() => -1);
await page.screenshot({ path: `${outDir}/interact-state.png` });
console.log('state dropdown options visible (screenshot taken)');
await page.keyboard.press('Escape');
await page.waitForTimeout(400);

// 3. start date popover
await page.locator('[role="dialog"]').getByText('Start date', { exact: true }).click();
await page.waitForTimeout(600);
const dateInput = await page.locator('input[type="date"]').count();
await page.screenshot({ path: `${outDir}/interact-date.png` });
console.log('date inputs visible:', dateInput);

console.log('page errors:', errors.length ? errors : 'none');
await browser.close();
if (pressed !== 'true' || (count || '').trim() !== '1') { console.log('VOTE CHECK FAILED'); process.exit(1); }
console.log('VOTE CHECK PASSED');
