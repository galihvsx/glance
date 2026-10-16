// shot.mjs — headless visual QA for glance cycle 19.
// Usage: node shot.mjs <baseUrl> <sessionCookie> <outDir> <screen...>
// Screens: shell | board | detail | list | mywork | inbox | cycles
// Opens each screen, waits for network idle, screenshots 1920x1080.
import { chromium } from 'playwright-core';

const [base, cookie, outDir, ...screens] = process.argv.slice(2);
if (!base || !cookie || !outDir || screens.length === 0) {
  console.error('usage: node shot.mjs <baseUrl> <sessionCookie> <outDir> <screen...>');
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

// Workspace/project seeded by visual.sh: slug "acme", identifier "EXAMP"
const WS = 'acme';
const PROJ = 'EXAMP';

const routes = {
  shell: `/w/${WS}`,
  board: `/w/${WS}/p/${PROJ}/board`,
  detail: `/w/${WS}/p/${PROJ}/board`, // then click first card to open peek panel
  list: `/w/${WS}/p/${PROJ}`,
  mywork: `/w/${WS}/my-work`,
  inbox: `/w/${WS}/p/${PROJ}/intake`,
  cycles: `/w/${WS}/p/${PROJ}/cycles`,
};

for (const s of screens) {
  const route = routes[s];
  if (!route) { console.error('unknown screen:', s); continue; }
  await page.goto(base + route, { waitUntil: 'networkidle', timeout: 30000 }).catch(e => console.error('goto failed', s, e.message));
  await page.waitForTimeout(1200);
  if (s === 'detail') {
    // open the peek panel by clicking the first board card
    const card = page.locator('[data-testid="board-card"], [data-board-card]').first();
    if (await card.count() > 0) {
      await card.click();
    } else {
      // fallback: click the first element that looks like a card title
      await page.locator('main [role="button"], main article').first().click().catch(() => {});
    }
    await page.waitForTimeout(1200);
  }
  await page.screenshot({ path: `${outDir}/${s}.png` });
  console.log('shot:', s);
}
await browser.close();
