// Run with Node's test runner and Playwright on the module path.
// A successful subagent override keeps the agent's original request in
// Route.Model. Its main label must follow the selected model, including a
// later fallback, while the story still says what the agent asked for.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve(__dirname, "../assets");
const asked = "gpt-6-sol", selected = "codex/gpt-6.1-sol";
const now = new Date().toISOString();
const seat = { id: "codex@fixture", provider: "codex", name: "ChatGPT", kind: "account", who: "fixture@example.test", model: "gpt-6.1-sol" };
const backup = { ...seat, id: "backup@fixture", provider: "backup", name: "Backup", model: "fallback-model" };
const prompt = { window: 272000, tokens: 9000, counted: true, turns: 1, parts: [{ kind: "chat", tokens: 9000, items: [{ name: "turn", tokens: 9000 }] }] };
const attempt = (w, more = {}) => ({ id: w.id, model: w.model, start: now, done: true, status: 200, ms: 100, ...more });
const route = (id, more = {}) => ({
  id, seq: id, time: now, agent: "codex", kind: "collab_spawn", model: asked, provider: "codex", prompt,
  subagent: { asked, to: selected }, order: [seat], tries: [attempt(seat)], done: true, status: 200, ms: 100, ...more,
});
const routes = [
  route(107),
  route(106, { done: false, status: 0, tries: [] }),
  // The configured override was rejected: its target must not become the label.
  route(105, { subagent: { asked, to: selected, kept: "no ChatGPT account on in magpie serves it" },
    order: [{ ...seat, model: asked }], tries: [attempt({ ...seat, model: asked })] }),
  // A later fallback is the actual selection, not the configured override.
  route(104, { order: [seat, backup], tries: [attempt(seat, { status: 503, fail: "other" }), attempt(backup)] }),
  // Ordinary groups and ordinary requests retain their existing asked labels.
  route(103, { kind: "", subagent: undefined, model: "group/best", order: [seat, backup], tries: [attempt(backup)] }),
  route(102, { kind: "", subagent: undefined }),
  // Even inconsistent historical override metadata cannot overrule an actual try.
  route(101, { order: [{ ...seat, model: asked }], tries: [attempt({ ...seat, model: asked }, { status: 403, fail: "other" })], status: 403 }),
];
const labels = [seat.model, selected, asked, backup.model, "group/best", asked, asked];

function serve(lang, feed) {
  return async (r) => {
    const url = new URL(r.request().url());
    const json = (data) => r.fulfill({ json: data });
    if (url.pathname === "/boot.js") return r.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:${JSON.stringify(lang)},theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return r.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json({ agents: [{ id: "codex", name: "Codex", path: "/fixture/config.toml", fields: [] }], profiles: [], settings: { lang, theme: "light" } });
    if (url.pathname === "/api/gateway/trace") {
      if (url.searchParams.has("wait")) return new Promise((resolve) => {
        feed.next = async (updates) => {
          await json({ mine: true, now, seq: Math.max(...updates.map((r) => r.seq)), totals: { requests: 7, rerouted: 1, errors: 1 }, routes: updates });
          resolve();
        };
        feed.ready();
      });
      return json({ mine: true, now, seq: 107, totals: { requests: 7, rerouted: 1, errors: 1 }, routes });
    }
    if (url.pathname === "/api/gateway/history") return json({ cut: false, days: [], routes: [] });
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true } });
    if (url.pathname === "/api/groups") return json({ groups: [], models: [], pools: [] });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    await r.fulfill({ body: await fs.readFile(file), contentType: { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)] });
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of (process.env.LANGS ? process.env.LANGS.split(",") : ["en", "zh", "zh-TW", "ja", "de"])) {
    test(`${engine} ${lang}: an overridden subagent's labels follow its actual model and retain its original request`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      t.after(() => browser.close());
      const errors = [];
      const feed = {};
      const waiting = new Promise((resolve) => { feed.ready = resolve; });
      const open = async (url, width) => {
        const page = await browser.newPage({ viewport: { width, height: 900 }, reducedMotion: "reduce" });
        page.setDefaultTimeout(5000);
        page.setDefaultNavigationTimeout(60000);
        page.on("pageerror", (e) => errors.push(e.message));
        await page.route("**/*", serve(lang, feed));
        await page.goto(url);
        return page;
      };
      const page = await open("http://magpie.test/?view=routing", 1100);
      await page.locator(".rt-req").nth(routes.length - 1).waitFor();
      assert.deepEqual(await page.locator(".rt-req .asked .m").allTextContents(), labels,
        "a successful override shows the actual try (or selected target before a try), while kept and ordinary requests keep their asked model");

      const original = await page.evaluate((model) => t("{agent} asked for {model}", { agent: "Codex", model }), asked);
      for (const i of [0, 1, 3]) {
        const row = page.locator(".rt-req").nth(i);
        assert.match(await row.getAttribute("title"), new RegExp(labels[i].replaceAll(".", "\\.")), "the title names the chosen model");
        assert.ok((await row.getAttribute("title")).includes(original), "the title explicitly retains the original asked model");
      }
      for (const i of [0, 1, 2, 3, 4, 5, 6]) {
        await page.locator(".rt-req").nth(i).dispatchEvent("click");
        await page.waitForFunction((expected) => document.querySelector(".rt-ctx .ctx-model")?.textContent === expected, labels[i]);
        assert.equal(await page.locator(".rt-ctx .ctx-model").textContent(), labels[i], "the context heading agrees with its request");
        const story = await page.locator(".rt-steps").textContent();
        assert.ok(story.includes(i === 4 ? "group/best" : asked), "the story retains the original request for audit");
        if (i === 2) {
          const why = await page.locator(".rt-req").nth(i).locator(".kind").getAttribute("title");
          assert.ok(why.includes(asked) && why.includes(selected), "a rejected override still explains why the original model stayed");
        }
      }

      // The reusable context card's display option does not rewrite its route,
      // and callers without that option retain the default label.
      const card = await page.evaluate(({ asked, prompt }) => {
        const r = { id: 1, done: true, model: asked, prompt };
        const changed = window.ctxCard(r, { model: "display-only" });
        const plain = window.ctxCard(r);
        return [changed.querySelector(".ctx-model").textContent, plain.querySelector(".ctx-model").textContent, r.model];
      }, { asked, prompt });
      assert.deepEqual(card, ["display-only", asked, asked]);

      // The first actual try can arrive while every prompt measurement
      // stays unchanged. Both labels must update on that trace update.
      await page.locator(".rt-req").nth(1).dispatchEvent("click");
      await page.waitForFunction((model) => document.querySelector(".rt-ctx .ctx-model")?.textContent === model, selected);
      await waiting;
      await feed.next([{ ...routes[1], seq: 108, order: [backup], tries: [attempt(backup, { done: false, status: 0 })] }]);
      await page.waitForFunction((model) => document.querySelector(".rt-ctx .ctx-model")?.textContent === model, backup.model);
      assert.equal(await page.locator(".rt-req").nth(1).locator(".asked .m").textContent(), backup.model);
      await page.close();

      // The panel is another view of the same requests; it must agree at
      // its narrower width rather than reintroducing the original label.
      const panel = await open("http://magpie.test/?mode=panel", 440);
      await panel.locator('[data-ptab="routing"]').click();
      await panel.locator(".pr-req").nth(5).waitFor();
      assert.deepEqual(await panel.locator(".pr-req .pr-a .m").allTextContents(), labels.slice(0, 6));
      assert.ok((await panel.locator(".pr-req").first().getAttribute("title")).includes(original));
      assert.ok(await panel.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "the panel fits its window");
      assert.deepEqual(errors, []);
    });
  }
}
