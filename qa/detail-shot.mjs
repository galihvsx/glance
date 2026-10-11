// detail-shot.mjs — screenshot the issue peek panel directly via ?peek=<uuid>.
// Usage: node detail-shot.mjs <baseUrl> <sessionCookie> <issueUuid> <outPath>
import { chromium } from 'playwright-core';

const [base, cookie, uuid, out] = process.argv.slice(2);
if (!base || !cookie || !uuid || !out) {
  console.error('usage: node detail-shot.mjs <baseUrl> <sessionCookie> <issueUuid> <outPath>');
  process.exit(1);
}

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
await page.goto(`${base}/w/acme/p/EXAMP/board?peek=${uuid}`, { waitUntil: 'networkidle', timeout: 30000 }).catch(e => console.error('goto failed', e.message));
await page.waitForTimeout(1500);
await page.screenshot({ path: out });
console.log('shot:', out);
// scroll the panel's scroll container to the bottom for the activity section
await page.evaluate(() => {
  const panel = document.querySelector('[role="dialog"]');
  const scroller = panel?.querySelector('.overflow-y-auto');
  if (scroller) scroller.scrollTop = scroller.scrollHeight;
});
await page.waitForTimeout(600);
await page.screenshot({ path: out.replace(/\.png$/, '-bottom.png') });
console.log('shot:', out.replace(/\.png$/, '-bottom.png'));
await browser.close();
