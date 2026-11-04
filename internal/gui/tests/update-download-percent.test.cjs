// Run with Node's test runner and Playwright on the module path; see README.md.
// The header's Update pill while an update downloads again (a click after a
// failed one): the backend's answer leaves done out while it is 0, and
// total while the size isn't known, so the first reads have no percent. The
// pill says 0% then, "Downloading…" with no size, never "NaN%" (inaction
// on Discord), and the percent once there is one. The click doesn't move the page, nothing
// has a left border. English and Chinese; no backend, the API is faked.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");

const words = {
  en: { update: "Update", downloading: "Downloading…", zero: "Downloading… 0%", half: "Downloading… 50%" },
  zh: { update: "更新", downloading: "正在下载…", zero: "正在下载… 0%", half: "正在下载… 50%" },
};

function settingsPayload(lang) {
  return {
    theme: "light", lang, tray: "panel", quotaLeft: false, currency: "usd",
    dock: false, dockWindow: false, proxy: "", redact: false, redactPersonal: false, redactWords: [],
    codexWarmup: "", claudeWarmup: "", codexWarmAt: "", claudeWarmAt: "", workbuddyCheckin: false, noStats: false,
    trayUsage: "", trayUsageEvery: 3, vision: "", imageGen: "",
    version: "0.1.400", dir: "~/.config/magpie", gateway: "http://127.0.0.1:3425",
    proxyNow: "none", proxySource: "none", login: false,
    visionModels: [], imageGenModels: [], workbuddyCheckins: [], lanURLs: [],
    fx: { rate: 7.2, at: new Date().toISOString(), stale: false },
  };
}

// ctl.update is what /api/update answers; ctl.installs counts the clicks' asks
function server(lang, ctl) {
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:false};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ agents: [], profiles: [], settings: settingsPayload(lang), fx: settingsPayload(lang).fx });
    if (url.pathname === "/api/settings") return json(settingsPayload(lang));
    if (url.pathname === "/api/update/install") {
      ctl.installs++;
      return json({ ...ctl.update });
    }
    if (url.pathname === "/api/update") {
      ctl.updateArrived?.();
      if (ctl.updateGate) await ctl.updateGate;
      return json({ ...ctl.update });
    }
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/usage/quotas") return json([]);
    if (url.pathname === "/api/groups") return json({ groups: [], models: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = url.pathname === "/app.js" && ctl.appJs ? ctl.appJs : path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

const scrolls = (page) => page.evaluate(() => [window.scrollY, document.scrollingElement.scrollTop, ...[...document.querySelectorAll(".view")].map((v) => v.scrollTop)].join(","));

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  if (engine === "webkit") test("webkit: a late initial update response settles the header without native ResizeObserver errors", async (t) => {
    const browser = await webkit.launch();
    let context, page, release, arrive, closing = false;
    const gate = new Promise((resolve) => { release = resolve; });
    const arrived = new Promise((resolve) => { arrive = resolve; });
    const bounded = (promise, label) => {
      let timer;
      return Promise.race([promise, new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error(label + " timed out")), 5000);
      })]).finally(() => clearTimeout(timer));
    };
    t.after(async () => {
      closing = true; release();
      try { if (page) await bounded(page.unrouteAll({ behavior: "wait" }), "route cleanup"); }
      finally {
        try { if (context) await bounded(context.close(), "context close"); }
        finally { await bounded(browser.close(), "browser close"); }
      }
    });
    context = await browser.newContext({ viewport: { width: 683, height: 560 }, reducedMotion: "reduce" });
    page = await context.newPage();
    page.setDefaultTimeout(5000);
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.addInitScript(() => {
      // Observe the real callbacks without changing delivery or layout.
      const Native = window.ResizeObserver;
      window.__topFitObservers = [];
      window.ResizeObserver = class extends Native {
        constructor(callback) {
          const record = { targets: [], deliveries: 0 };
          super((entries, watcher) => { record.deliveries++; return callback.call(watcher, entries, watcher); });
          this.record = record;
          window.__topFitObservers.push(record);
        }
        observe(target, options) { this.record.targets.push(target); return super.observe(target, options); }
      };
    });
    const ctl = { installs: 0, update: { state: "error", retry: true, current: "0.1.400", latest: "0.1.401", error: "timed out" },
      updateGate: gate, updateArrived: arrive, appJs: process.env.TOPFIT_APP_JS };
    if (ctl.appJs) assert(path.isAbsolute(ctl.appJs), "TOPFIT_APP_JS must name an absolute controlled app asset");
    const serve = server("en", ctl);
    await page.route("**/*", async (route) => { try { await serve(route); } catch (error) { if (!closing) throw error; } });
    let sample = 0;
    const stable = async () => {
      const state = await page.waitForFunction((sample) => {
        const targets = [".top", ".brand", ".actions"].map((s) => document.querySelector(s));
        const observer = window.__topFitObservers.find((r) => targets.every((e) => e && r.targets.includes(e)));
        if (!observer || !observer.deliveries) return false;
        const classes = targets[0].className, deliveries = observer.deliveries;
        const boxes = targets.map((e) => { const r = e.getBoundingClientRect(); return [r.x, r.y, r.width, r.height]; });
        const key = JSON.stringify([classes, deliveries, boxes]), previous = window.__topFitStable;
        const frames = previous?.sample === sample && previous.key === key ? previous.frames + 1 : 1;
        window.__topFitStable = { sample, key, frames };
        return frames >= 8 ? { classes, deliveries, boxes } : false;
      }, ++sample, { timeout: 5000, polling: "raf" });
      try { return await state.jsonValue(); } finally { await state.dispose(); }
    };
    await page.goto("http://magpie.test/");
    await bounded(arrived, "initial update GET");
    await bounded(page.evaluate(() => document.fonts.ready.then(() => true)), "native font readiness");
    const before = await stable();
    assert.equal(await page.locator("#update").isHidden(), true, "the initial update response remains held");
    assert.equal(before.classes.split(" ").includes("tight"), false, "the hidden-pill header fits at this boundary");
    assert.deepEqual(errors, [], "observer initialization has no native errors");
    ctl.updateGate = null;
    release();
    await page.locator("#update").waitFor({ state: "visible" });
    assert.equal((await page.locator("#update span").innerText()).trim(), words.en.update);
    const after = await stable();
    assert.equal(after.classes.split(" ").includes("tight"), true, "the late update pill crosses the compact boundary");
    assert(after.deliveries > before.deliveries, "the native header observer received the update's size change");
    assert.deepEqual(errors, [], "the late response must not cause native ResizeObserver errors");
  });

  test(engine + ": a downloading update never says NaN", async (t) => {
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    t.after(() => browser.close());
    for (const lang of ["en", "zh"]) {
      const w = words[lang];
      await t.test(lang + ": no percent until there is one", async () => {
        const ctl = { installs: 0, update: { state: "error", retry: true, current: "0.1.400", latest: "0.1.401", error: "timed out" } };
        const errors = [];
        const page = await (await browser.newContext({ viewport: { width: 900, height: 560 }, reducedMotion: "reduce" })).newPage();
        page.setDefaultTimeout(5000);
        page.on("pageerror", (e) => errors.push(e.message));
        await page.route("**/*", server(lang, ctl));
        await page.goto("http://magpie.test/");
        const pill = page.locator("#update"), label = pill.locator("span").first();
        await pill.waitFor({ state: "visible" });
        assert.equal((await label.innerText()).trim(), w.update);
        const seen = [];
        await page.exposeFunction("__seen", (s) => seen.push(s));
        await label.evaluate((l) => new MutationObserver(() => window.__seen(l.textContent)).observe(l, { childList: true, characterData: true, subtree: true }));

        // the download starts: the size known, nothing in yet (done left out)
        ctl.update = { state: "downloading", current: "0.1.400", latest: "0.1.401", total: 1000 };
        const before = await scrolls(page);
        await pill.click();
        assert.equal(await scrolls(page), before, "the click must not scroll the page");
        assert.equal(ctl.installs, 1);
        await page.waitForTimeout(300);
        assert.equal((await label.innerText()).trim(), w.zero);

        // nor the size yet
        ctl.update = { state: "downloading", current: "0.1.400", latest: "0.1.401" };
        await page.waitForTimeout(900);
        assert.equal((await label.innerText()).trim(), w.downloading);

        // halfway
        ctl.update = { state: "downloading", current: "0.1.400", latest: "0.1.401", done: 500, total: 1000 };
        await page.waitForFunction((s) => document.querySelector("#update span").textContent.trim() === s, w.half);

        assert.deepEqual(seen.filter((s) => /NaN|undefined|Infinity/.test(s)), [], "the pill said " + seen.join(" | "));
        assert.equal(await pill.evaluate((e) => getComputedStyle(e).borderLeftWidth === "0px" || getComputedStyle(e).borderLeftColor === getComputedStyle(e).borderTopColor), true);
        assert.deepEqual(errors, []);
        await page.context().close();
      });
    }
  });
}
