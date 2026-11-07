// Only local, headless Windows WebKit pages created by this fixture process.
// The pinned SDK uses the same command during page initialization. A page
// whose native active override is false stops delivering animation frames,
// so an ordinary Playwright click cannot finish its stability check.
const assert = require("node:assert/strict");
const playwright = require("playwright");
const owned = new WeakSet();
let installed = false;

// A busy full shard can keep a visible, focused WebKit page from delivering
// its first animation frame for longer than the ordinary local case. This
// remains inside the click deadline; any time spent here is subtracted before
// Playwright's original actionability check runs.
const setupMax = 3000;
const expired = () => new playwright.errors.TimeoutError("Owned WebKit foreground setup exceeded its bounded click budget");

async function failureState(page) {
  let timer;
  try {
    return await Promise.race([
      page.evaluate(() => new Promise((resolve) => {
        let painted = false;
        requestAnimationFrame(() => { painted = true; });
        setTimeout(() => resolve({ visibility: document.visibilityState, focus: document.hasFocus(), painted }), 100);
      })),
      new Promise((resolve) => { timer = setTimeout(() => resolve({ captureTimedOut: true }), 500); }),
    ]);
  } catch { return { captureUnavailable: true }; }
  finally { clearTimeout(timer); }
}

async function foreground(page, budget = setupMax) {
  assert(owned.has(page), "activity setup is restricted to this fixture's local headless page");
  const deadline = performance.now() + Math.min(setupMax, budget);
  let cancelled = false, timer;
  const check = () => { if (cancelled || performance.now() >= deadline) throw expired(); };
  const work = async () => {
    check(); await page.bringToFront();
    check();
    const native = playwright._connection.toImpl(page).delegate;
    await native._pageProxySession.send("Emulation.setActiveAndFocused", { active: true });
    check();
    for (;;) {
      check();
      const state = await page.evaluate(() => ({ visibility: document.visibilityState, focus: document.hasFocus() }));
      check();
      assert.equal(state.visibility, "visible", "fixture page must be visible before its real click");
      assert.equal(state.focus, true, "fixture page must be focused before its real click");
      let frame;
      try {
        frame = await Promise.race([
          page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => resolve(true)))),
          new Promise((resolve) => setTimeout(() => resolve(false), 250)),
        ]);
      } catch { frame = false; }
      if (frame) break;
      check();
      await native._pageProxySession.send("Emulation.setActiveAndFocused", { active: true });
    }
  };
  try {
    await Promise.race([work(), new Promise((_, reject) => { timer = setTimeout(() => { cancelled = true; reject(expired()); }, Math.max(1, deadline - performance.now())); })]);
  } finally { cancelled = true; clearTimeout(timer); }
}

function ownPage(page) {
  if (owned.has(page)) return;
  owned.add(page);
  page.on("close", () => owned.delete(page));
  const frames = new WeakSet();
  const ownFrame = (frame) => {
    if (frames.has(frame)) return;
    frames.add(frame);
    const click = frame.click;
    frame.click = async function (...args) {
      if (page.isClosed()) return click.apply(this, args);
      const options = args[1] || {}, timeout = this._timeout(options), started = performance.now();
      if (!Number.isFinite(timeout) || timeout < 0) return click.apply(this, args);
      try {
        await foreground(page, timeout || setupMax);
        if (timeout) {
          const remaining = timeout - (performance.now() - started);
          if (remaining <= 0) throw expired();
          args[1] = { ...options, timeout: remaining };
        }
        // Keep real actionability and all other options. Setup consumes the
        // existing click deadline, and a rejected setup never issues a click.
        return await click.apply(this, args);
      } catch (error) {
        error.fixtureActivity = await failureState(page);
        console.error("OWNED_WEBKIT_ACTIVITY " + JSON.stringify(error.fixtureActivity));
        throw error;
      }
    };
  };
  for (const frame of page.frames()) ownFrame(frame);
  page.on("frameattached", ownFrame);
}

function install() {
  if (installed || process.platform !== "win32") return;
  assert.equal(require("playwright/package.json").version, "1.61.1", "recheck WebKit activity adapter when upgrading Playwright");
  installed = true;
  const launch = playwright.webkit.launch;
  playwright.webkit.launch = async function (options = {}) {
    const browser = await launch.call(this, options);
    const headful = playwright._connection.toImpl(browser).options.headful;
    if (typeof headful !== "boolean") {
      await browser.close();
      assert.fail("recheck resolved WebKit launch mode");
    }
    if (headful) return browser;
    // Do not patch browser prototypes, connect, CDP or persistent profiles.
    // Browser.newPage also calls this instance's newContext in this SDK.
    const newContext = browser.newContext;
    browser.newContext = async function (...args) {
      const context = await newContext.apply(this, args);
      context.on("page", ownPage);
      for (const page of context.pages()) ownPage(page);
      return context;
    };
    return browser;
  };
}

install();
module.exports = { foreground };
