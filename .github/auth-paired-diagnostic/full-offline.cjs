// Validation-only fixture guard. Unlike the focused guard, allow mocked
// origins and fallback to earlier mocked handlers used by existing tests.
// The final context handler always refuses actual browser HTTP traffic.
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const pw = require('playwright');
const { safeURL } = require('./safe-url.cjs');
let violations = 0;
const refuse = () => { violations++; process.exitCode = 1; throw new Error('Unmocked browser network refused'); };
process.once('exit', () => { if (violations) process.exitCode = 1; });
const report = process.env.AUTH_FULL_REPORTS;
const entry = process.argv[1] && path.resolve(process.argv[1]);
const testsRoot = path.resolve('internal/gui/tests') + path.sep;
if (report && entry && entry.startsWith(testsRoot) && entry.endsWith('.test.cjs')) {
  fs.mkdirSync(path.join(report, 'loaded'), { recursive: true });
  fs.writeFileSync(path.join(report, 'loaded', crypto.createHash('sha256').update(entry).digest('hex') + '.json'), JSON.stringify({ file: path.relative(process.cwd(), entry) }));
}
// Playwright unroute(pattern, handler) removes by function identity. Keep
// exactly one wrapper per original handler for each page/context owner.
function protectRoutes(owner) {
  const wrappers = new WeakMap();
  const route = owner.route.bind(owner);
  const unroute = owner.unroute.bind(owner);
  owner.route = (pattern, handler, options) => {
    let wrapped = wrappers.get(handler);
    if (!wrapped) {
      wrapped = async (r, request) => handler(new Proxy(r, { get(target, key) {
        if (key === 'continue' || key === 'fetch') return refuse;
        const value = Reflect.get(target, key);
        return typeof value === 'function' ? value.bind(target) : value;
      }}), request);
      wrappers.set(handler, wrapped);
    }
    return route(pattern, wrapped, options);
  };
  owner.unroute = (pattern, handler) => unroute(pattern, handler ? (wrappers.get(handler) || handler) : handler);
}
// Browser-created memory URLs from the sole synthetic fixture origin are
// not network fetches. Match the entire canonical shape, never other origins,
// null origins, suffixes, encoded forms, query strings or fragments.
function localFixtureBlob(value) {
  return /^blob:http:\/\/magpie\.test\/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(?![\s\S])/.test(String(value));
}
function protectGeometry(page) {
  if (!page.screenshot || !process.env.AUTH_DIAG_GEOMETRY) return;
  const screenshot = page.screenshot.bind(page);
  page.screenshot = async options => {
    // Original test calls screenshot only after its own animation wait. Read
    // geometry here without changing classes, styles, scroll or assertions.
    const measurement = await page.evaluate(() => {
      const body = document.querySelector('.editor.disconnect-ask > .ebody');
      if (!body) return null;
      const css = getComputedStyle(body), rect = body.getBoundingClientRect();
      const diff = body.querySelector('.ag-diff-wrap'), d = diff?.getBoundingClientRect();
      return { clientWidth: body.clientWidth, scrollWidth: body.scrollWidth, offsetWidth: body.offsetWidth, clientLeft: body.clientLeft,
        rect: {left:rect.left,right:rect.right,width:rect.width}, diff:d ? {left:d.left,right:d.right,width:d.width}:null,
        css:{width:css.width,boxSizing:css.boxSizing,paddingLeft:css.paddingLeft,paddingRight:css.paddingRight,borderLeftWidth:css.borderLeftWidth,borderRightWidth:css.borderRightWidth,overflowX:css.overflowX,overflowY:css.overflowY,gridTemplateColumns:css.gridTemplateColumns,scrollbarGutter:css.scrollbarGutter},
        devicePixelRatio, viewport:{width:innerWidth,height:innerHeight}, zoom:getComputedStyle(document.documentElement).getPropertyValue('--zoom') };
    });
    if (measurement) fs.appendFileSync(process.env.AUTH_DIAG_GEOMETRY, JSON.stringify(measurement)+'\n');
    return screenshot(options);
  };
}
const contexts = new WeakSet();
async function protect(ctx) {
  if (contexts.has(ctx)) return ctx;
  contexts.add(ctx);
  // A private predicate keeps unroute("**/*") from removing the final
  // network refusal. unrouteAll removes user handlers and reinstalls it.
  const denyPattern = url => {
    const local = localFixtureBlob(url);
    if (local && process.env.AUTH_DIAG_LOCAL_BLOBS) fs.appendFileSync(process.env.AUTH_DIAG_LOCAL_BLOBS, JSON.stringify({classification:'synthetic-local-blob',allowed:true})+'\n');
    return !local;
  };
  const deny = async route => {
    if (process.env.AUTH_DIAG_DENIED) {
      const request = route.request();
      fs.appendFileSync(process.env.AUTH_DIAG_DENIED, JSON.stringify({ event: 'denied-http', method: request.method(), ...safeURL(request.url()) }) + '\n');
    }
    await route.abort(); refuse();
  };
  const rawRoute = ctx.route.bind(ctx);
  await rawRoute(denyPattern, deny);
  await ctx.routeWebSocket('**/*', socket => { socket.close(); refuse(); });
  if (ctx.unrouteAll) {
    const unrouteAll = ctx.unrouteAll.bind(ctx);
    ctx.unrouteAll = async options => { await unrouteAll(options); await rawRoute(denyPattern, deny); };
  }
  protectRoutes(ctx);
  for (const page of ctx.pages()) { protectRoutes(page); protectGeometry(page); }
  const newPage = ctx.newPage.bind(ctx);
  ctx.newPage = async () => {
    const page = await newPage();
    protectRoutes(page);
    protectGeometry(page);
    return page;
  };
  return ctx;
}
for (const engine of ['chromium', 'webkit']) {
  const launch = pw[engine].launch.bind(pw[engine]);
  pw[engine].launch = async options => {
    const browser = await launch(options);
    const newContext = browser.newContext.bind(browser);
    browser.newContext = async options => protect(await newContext({ ...options, serviceWorkers: 'block' }));
    browser.newPage = async options => (await browser.newContext(options)).newPage();
    return browser;
  };
  const persistent = pw[engine].launchPersistentContext.bind(pw[engine]);
  pw[engine].launchPersistentContext = async (directory, options) => {
    const root = fs.realpathSync(process.env.TMPDIR) + path.sep;
    if (!fs.realpathSync(directory).startsWith(root)) throw new Error('Persistent test profile outside isolated temporary directory');
    return protect(await persistent(directory, { ...options, serviceWorkers: 'block' }));
  };
}
