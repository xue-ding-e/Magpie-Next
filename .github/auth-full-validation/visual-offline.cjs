// Validation-only harness: synthetic fixture pages, never a live Magpie server.
const fs = require('node:fs/promises');
const path = require('node:path');
const pw = require('playwright');
let serial = 0, violations = 0;
process.once('exit', () => { if (violations) process.exitCode = 1; });
const violation = () => { violations++; process.exitCode = 1; throw new Error('Unmocked browser network blocked'); };
for (const engine of ['chromium', 'webkit']) {
  const launch = pw[engine].launch.bind(pw[engine]);
  pw[engine].launch = async options => {
    if (engine !== process.env.BROWSER) throw new Error('Unexpected browser engine');
    const browser = await launch(options);
    const newContext = browser.newContext.bind(browser);
    browser.newContext = async options => {
      const ctx = await newContext({ ...options, serviceWorkers: 'block' });
      // Any request without a page fixture is refused, including sockets.
      await ctx.route('**/*', async route => { await route.abort(); violation(); });
      await ctx.routeWebSocket('**/*', socket => { socket.close(); violation(); });
      const newPage = ctx.newPage.bind(ctx);
      ctx.newPage = async () => {
        const page = await newPage();
        const route = page.route.bind(page);
        page.route = (pattern, handler, options) => route(pattern, async (requestRoute, request) => {
          const url = new URL(requestRoute.request().url());
          if (url.origin !== 'http://magpie.test' || url.username || url.password) {
            await requestRoute.abort(); violation();
          }
          // Fixtures must fulfill or abort, never fetch/continue to a server.
          const safe = new Proxy(requestRoute, { get(target, key) {
            if (key === 'continue' || key === 'fetch' || key === 'fallback') return violation;
            const value = Reflect.get(target, key);
            return typeof value === 'function' ? value.bind(target) : value;
          }});
          return handler(safe, request);
        }, options);
        return page;
      };
      return ctx;
    };
    browser.newPage = async options => (await browser.newContext(options)).newPage();
    const close = browser.close.bind(browser);
    browser.close = async (...args) => {
      // Keep the last synthetic screen before each test's cleanup, including
      // failures; filenames contain no URL, user, account, token or test input.
      const dir = process.env.AUTH_VALIDATION_ARTIFACTS;
      if (dir) {
        await fs.mkdir(dir, { recursive: true });
        for (const ctx of browser.contexts()) for (const page of ctx.pages()) {
          if (!page.isClosed()) await page.screenshot({
            path: path.join(dir, `${engine}-${process.pid}-${++serial}.png`), timeout: 3000,
          }).catch(() => {});
        }
      }
      return close(...args);
    };
    return browser;
  };
}
