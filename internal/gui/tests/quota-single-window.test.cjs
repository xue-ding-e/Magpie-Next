// A lone quota spans both card columns; multiple windows keep their two-column grid.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const wb = [
  { provider: "workbuddy", name: "WorkBuddy", icon: "workbuddy-color", plan: "Free", user: "a", windows: [{ name: "Credits", used: 29.6, display: "710.99 / 2400", amount: 710.99, limit: 2400, unit: "credits" }] },
  { provider: "workbuddy-ai", name: "WorkBuddy AI", icon: "workbuddy-color", plan: "Free", user: "b", windows: [{ name: "Credits", used: 0, display: "0 / 100", amount: 0, limit: 100, unit: "credits" }] },
];
const trae = { after: { provider: "trae-cn-plugin", name: "Trae CN", icon: "trae", plan: "Credits", user: "c", windows: [{ name: "Credits", used: 39.28, amount: 1492.58, limit: 3800, unit: "credits" }] } };
function serve(lang, quotas) {
  const settings = { lang, theme: "light", quotaLeft: true, currency: "usd" };
  const state = { agents: [], profiles: [], settings };
  return async (r) => {
    const url = new URL(r.request().url());
    const json = (data) => r.fulfill({ json: data });
    if (url.pathname === "/boot.js") return r.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"${lang}",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return r.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json(state);
    if (url.pathname === "/api/settings") return json(settings);
    if (url.pathname === "/api/gateway/trace") return url.searchParams.get("wait") ? new Promise(() => {}) : json({ mine: true, now: new Date().toISOString(), seq: 0, totals: {}, routes: [] });
    if (url.pathname === "/api/gateway/history") return json({ cut: false, days: [], routes: [] });
    if (url.pathname === "/api/groups") return json({ models: [], groups: [], pools: [] });
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true, url: "http://127.0.0.1:3999" } });
    if (url.pathname === "/api/usage/quotas") return json(quotas);
    if (url.pathname === "/api/usage") return json({ calls: 0, input: 0, output: 0, cost: 0, agents: [], models: [], days: [] });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    try { await r.fulfill({ body: await fs.readFile(file), contentType }); } catch { await r.fulfill({ status: 404, body: "" }); }
  };
}


for (const [engineName, engine] of [["webkit", webkit], ["chromium", chromium]]) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    test(`${engineName}/${lang}: one quota fills its row without changing multiple windows`, async () => {
      const browser = await engine.launch();
      try {
        for (const width of [560, 1100]) for (const multiple of [false, true]) {
          const context = await browser.newContext({ viewport: { width, height: 820 }, reducedMotion: "reduce" });
          const page = await context.newPage();
          const errors = [], writes = [];
          page.on("pageerror", e => errors.push(e.message));
          page.on("console", m => { if (m.type() === "error") errors.push(m.text()); });
          page.on("request", q => { if (q.method() !== "GET") writes.push(q.method()); });
          page.setDefaultTimeout(5000);
          const quotas = [...wb, trae.after].map(q => ({ ...q, windows: multiple
            ? [q.windows[0], { ...q.windows[0], name: "5 hours" }] : q.windows }));
          try {
            await page.route("**/*", serve(lang, quotas));
            await page.goto("http://magpie.test/?view=usage");
            await page.locator(".subscription-card .quota-n").nth(multiple ? 5 : 2).waitFor();
            await page.waitForTimeout(300);
            const rows = await page.evaluate(() => [...document.querySelectorAll(".quota-windows")].map(g => {
              const r = g.getBoundingClientRect();
              return { width: r.width, left: r.left, right: r.right,
                items: [...g.children].map(q => { const b = q.getBoundingClientRect(); return {
                  width: b.width, left: b.left, right: b.right, top: b.top,
                  cut: [...q.querySelectorAll(".quota-n, .quota-n > span")].some(n => n.scrollWidth > n.clientWidth),
                }; }),
              };
            }));
            assert.equal(rows.length, 3);
            for (const row of rows) {
              assert.equal(row.items.length, multiple ? 2 : 1);
              if (multiple) {
                assert.ok(row.items[0].right < row.items[1].left, "multiple windows remain in separate columns");
                assert.equal(row.items[0].top, row.items[1].top);
                assert.ok(Math.abs(row.items[0].width - row.items[1].width) <= 1, "equal two-column tracks");
              } else {
                assert.ok(Math.abs(row.items[0].width - row.width) <= 1, "single window spans the entire row");
                assert.equal(row.items[0].cut, false, "single-window count is not clipped");
              }
            }
            assert.deepEqual(writes, []);
            assert.deepEqual(errors, []);
            assert.deepEqual(await page.locator("#status.err").allTextContents(), [], "no application-handled error toast");
          } finally { await context.close(); }
        }
      } finally { await browser.close(); }
    });
  }
}
