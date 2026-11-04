// Run with Node's test runner and Playwright on the module path; see README.md.
// An agent's model list that is still loading when the Agents page is drawn
// again (the state re-read after a changed list closes, or any refresh): the
// click isn't dropped. The list comes up under the redrawn button, which is
// marked open and gets the focus back on Escape, and a click on the redrawn
// button while it loads takes it back, as one on the first would. For each
// button that opens the list: a connected row's Pick, Claude Desktop's model
// menu button, and the line under a name that counts the models.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { reach } = require("./reader.cjs");

const assets = path.resolve(__dirname, "../assets");

const WAIT_MS = 5000, CLEANUP_MS = 2000;

// This is a failure deadline, not a delay or an automatic gate release.
function bounded(promise, label, timeout = WAIT_MS) {
  let timer;
  const deadline = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error(label + " timed out after " + timeout + "ms")), timeout);
  });
  return Promise.race([promise, deadline]).finally(() => clearTimeout(timer));
}

// A request cannot answer until the test releases it, however long input takes.
function requestGate(label) {
  let arrived, release, finished;
  const seen = new Promise((r) => { arrived = r; });
  const held = new Promise((r) => { release = r; });
  const done = new Promise((r) => { finished = r; });
  const gate = {
    arrived: () => bounded(seen, label + " reached the fixture"),
    finished: (timeout = WAIT_MS) => bounded(done, label + " completed its response", timeout),
    started: false, released: false,
    release() { gate.released = true; release(); },
    async reply(send) {
      gate.started = true;
      arrived();
      await held;
      try { return await send(); } finally { finished(); }
    },
  };
  return gate;
}

function fixture() {
  const list = [
    { id: "openai/gpt-5.5", name: "GPT-5.5", group: "OpenAI", icon: "openai", context: 272e3 },
    { id: "openai/gpt-5.4", name: "GPT-5.4", group: "OpenAI", icon: "openai", context: 272e3 },
    { id: "zai/glm-5.2", name: "GLM-5.2", group: "Z.ai", icon: "zai", context: 1e6 },
  ];
  const fx = { posts: 0 };
  const pending = { models: [], state: [] }, gates = [];
  fx.hold = (kind) => {
    const gate = requestGate(kind + " GET #" + (gates.length + 1));
    pending[kind].push(gate);
    gates.push(gate);
    return gate;
  };
  const reply = (kind, send) => pending[kind].shift()?.reply(send) ?? send();
  fx.drain = async () => {
    for (const gate of gates) gate.release();
    await Promise.all(gates.filter((g) => g.started).map((g) => g.finished(CLEANUP_MS)));
  };
  const count = () => {
    const on = list.filter((m) => !m.hidden);
    return { shown: on.length, listed: list.length, names: on.map((m) => m.name) };
  };
  const state = () => ({
    agents: [
      {
        id: "codex", name: "Codex", path: "/test/config.toml", icon: "codex-color", wired: true,
        fields: [{ key: "model", label: "model", value: "magpie/openai/gpt-5.5", options: [{ value: "magpie/openai/gpt-5.5", label: "GPT-5.5", ref: "openai/gpt-5.5" }] }],
        models: count(),
      },
      {
        id: "claude-desktop", name: "Claude Desktop", path: "/test/claude_desktop_config.json", icon: "claude-color", wired: true,
        fields: [{ key: "provider", label: "provider", value: "magpie", options: [{ value: "magpie", label: "magpie", icon: "magpie" }] }],
        models: count(),
      },
      // nothing to connect: its models counted on the line under its name
      { id: "plain", name: "Plain", path: "/test/plain.json", icon: "generic", fields: [], models: { shown: 0, listed: 0 } },
    ],
    profiles: [], settings: { lang: "en", theme: "light" },
  });
  fx.serve = async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:"en",theme:"light",web:true};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return reply("state", () => json(state()));
    if (url.pathname.startsWith("/api/agent-models/")) {
      if (req.method() === "POST") {
        fx.posts++;
        const { hidden } = req.postDataJSON();
        for (const m of list) m.hidden = hidden.includes(m.id);
        return json({ models: list, count: count() });
      }
      return reply("models", () => json({ models: list }));
    }
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname === "/api/agents/cli") return json({ agents: {}, pending: false });
    if (url.pathname === "/api/providers") return json({ providers: [], gateway: { running: true, window: true } });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
  return fx;
}

// the buttons that open an agent's model list, found again after each redraw
const BUTTONS = {
  "a connected row's Pick": '.row.agent[data-id="codex"] .ag-exp .ag-chips .ag-quiet',
  "Claude Desktop's model menu": '.row.agent[data-id="claude-desktop"] > .ag-menu',
  "the line counting the models": '.row.agent[data-id="plain"] .ag-models',
};

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const width of [1100, 560]) {
    test(`${engine} ${width}px: an agent's model list loading while the agents are drawn again`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      const page = await browser.newPage({ viewport: { width, height: 700 } });
      page.setDefaultTimeout(WAIT_MS);
      const errors = [];
      page.on("pageerror", (e) => errors.push(e.message));
      const fx = fixture();
      await page.route("**/*", fx.serve);
      t.after(async () => {
        try { await fx.drain(); } finally { await bounded(browser.close(), "browser close", CLEANUP_MS); }
      });
      await page.goto("http://magpie.test/");
      await page.locator('.row.agent[data-id="codex"] .ag-link').click();
      // Plain, with nothing set, is folded under the two connected ones; Codex's
      // opened row holds its own settings, so the fold is wheeled into sight
      // once that row has slid open
      const more = page.locator("#agents .agent-more");
      // Only finite animations owned by this row/fold can block setup.
      // Animations elsewhere and infinite spinner cycles do not delay it.
      const settled = (selector) => page.waitForFunction((selector) => {
        const root = document.querySelector(selector);
        return !!root && !root.classList.contains("moving") && !root.getAnimations({ subtree: true }).some((a) =>
          (a.pending || a.playState === "running") && Number.isFinite(a.effect?.getComputedTiming().endTime));
      }, selector, { polling: 25, timeout: WAIT_MS });
      await settled('#agents .row.agent[data-id="codex"]');
      await reach(page, more);
      await more.click();
      await page.locator('#agents .agent-more[aria-expanded="true"]').waitFor();
      await settled("#agents > .agent-fold");
      // Observe completion, not just delivery of HTTP bytes. In particular, a
      // cancelled request's late continuation must finish before checking it.
      await page.evaluate(() => {
        const open = openAgentModels;
        window.__modelActions = [];
        openAgentModels = function (...args) {
          const call = { done: false };
          window.__modelActions.push(call);
          return open.apply(this, args).finally(() => { call.done = true; });
        };
      });
      const completed = (i) => page.waitForFunction((i) => window.__modelActions[i]?.done, i, { polling: 25, timeout: WAIT_MS });
      const pop = page.locator(".am-pop:not(.leaving)");
      const redraw = () => page.evaluate(() => renderAgents());

      // every case is checked, so a run says which of them fail
      const failed = [];
      const check = (ok, said) => { if (!ok) failed.push(said); };
      const opened = () => pop.waitFor({ timeout: 2000 }).then(() => true, () => false);
      const shut = async () => {
        if (!await pop.count()) return;
        await page.keyboard.press("Escape");
        await pop.waitFor({ state: "detached" });
      };

      // in view and clear of the header and footer, reached with the wheel: the
      // page puts back a scroll that isn't the reader's (scrollIntoView,
      // Playwright's own before a click)
      const pressOn = async (btn) => {
        const id = await btn.evaluate((e) => e.closest(".row.agent").dataset.id);
        await settled("#agents .row.agent[data-id=" + JSON.stringify(id) + "]");
        await reach(page, btn);
        const clear = await btn.evaluateAll((es) => {
          const e = es.length === 1 ? es[0] : null;
          const v = e?.closest(".view");
          if (!v) return false;
          const r = e.getBoundingClientRect(), b = v.getBoundingClientRect();
          const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
          return r.width > 0 && r.height > 0 && r.left >= b.left && r.right <= b.right
            && r.top >= b.top && r.bottom <= b.bottom && !!hit && e.contains(hit);
        });
        assert.ok(clear, "the model-list button is wholly in view and receives the pointer");
        const call = await page.evaluate(() => window.__modelActions.length);
        await btn.click();
        assert.equal(await page.evaluate(() => window.__modelActions.length), call + 1, "one native click starts one model-list action");
        return call;
      };

      // Native request waits retain the page timeout if a click sends no GET.
      const requested = async (pathname, action) => {
        const [, call] = await Promise.all([
          page.waitForRequest((r) => r.method() === "GET" && new URL(r.url()).pathname === pathname, { timeout: WAIT_MS }),
          action(),
        ]);
        return call;
      };

      for (const [name, sel] of Object.entries(BUTTONS)) {
        const btn = page.locator(sel);
        await btn.waitFor();
        const press = () => pressOn(btn);
        const pathname = "/api/agent-models/" + await btn.evaluate((e) => encodeURIComponent(e.closest(".row.agent").dataset.id));

        // drawn again while it loads: the list still comes, at the new button
        const loading = fx.hold("models");
        const opening = await requested(pathname, press);
        await loading.arrived();
        await redraw();
        assert.equal(loading.released, false, "the redraw happened before the model response");
        assert.equal(await pop.count(), 0, "the model response is still held");
        await settled("#agents"); // the response must use the redrawn layout at rest
        loading.release();
        await loading.finished();
        await completed(opening);
        if (await opened()) {
          assert.equal(await btn.count(), 1);
          check(/\bopen\b/.test(await btn.getAttribute("class")), `${name}: the redrawn button isn't marked open`);
          // A short viewport can put the list above its button. Compare the
          // actual adjacent edges once its entrance animation has finished.
          await settled(".am-pop:not(.leaving)");
          const layout = await pop.evaluate((e, sel) => {
            const r = document.querySelector(sel).getBoundingClientRect(), p = e.getBoundingClientRect();
            const up = e.classList.contains("up");
            return { gap: up ? r.top - p.bottom : p.top - r.bottom,
              onSide: up ? p.bottom <= r.top : p.top >= r.bottom,
              inside: p.left >= 8 && p.right <= innerWidth - 8 && p.top >= popTop(8) && p.bottom <= innerHeight - 8,
              height: p.height, up };
          }, sel);
          check(Math.abs(layout.gap - 8) <= 2, `${name}: the list is ${layout.gap}px from its button`);
          check(layout.onSide && layout.inside && layout.height > 0, `${name}: the list must fit the viewport on its chosen side`);
          await page.keyboard.press("Escape");
          await pop.waitFor({ state: "detached" });
          check(await btn.evaluate((e) => e === document.activeElement), `${name}: Escape didn't give the redrawn button the focus`);
        } else {
          check(false, `${name}: the redraw dropped the click`);
        }

        // drawn again while it loads, then the new button clicked: taken back
        const cancelled = fx.hold("models");
        const pending = await requested(pathname, press);
        await cancelled.arrived();
        await redraw();
        const cancellation = await press();
        await completed(cancellation);
        assert.equal(cancelled.released, false, "the second click completed while the first GET was held");
        assert.equal(await pop.count(), 0, "cancellation did not first open the list");
        cancelled.release();
        await cancelled.finished();
        await completed(pending);
        check(await pop.count() === 0, `${name}: a second click on the redrawn button didn't take it back`);
        await shut();
        check(!/\bopen\b/.test(await btn.getAttribute("class")), `${name}: the button stays marked open`);
      }

      // the state re-read a changed list's close queues lands while the
      // next list loads (agent-models.test.cjs: Escape, then Pick again)
      const pick = page.locator(BUTTONS["a connected row's Pick"]);
      await pressOn(pick);
      await pop.waitFor();
      await Promise.all([
        page.waitForResponse((r) => r.request().method() === "POST" && new URL(r.url()).pathname === "/api/agent-models/codex", { timeout: WAIT_MS }),
        pop.locator(".am-mr", { hasText: "GLM-5.2" }).click(),
      ]);
      const stateRead = fx.hold("state"), nextModels = fx.hold("models");
      await requested("/api/state", () => page.keyboard.press("Escape"));
      await stateRead.arrived();
      await pop.waitFor({ state: "detached" });
      const nextOpening = await requested("/api/agent-models/codex", () => pressOn(pick));
      await nextModels.arrived();
      const oldPick = await pick.elementHandle();
      assert.ok(oldPick, "the next request started from the existing button");
      try {
        stateRead.release();
        await stateRead.finished();
        await page.waitForFunction((e) => !e.isConnected, oldPick, { polling: 25, timeout: WAIT_MS });
      } finally { await bounded(oldPick.dispose(), "old button handle disposal", CLEANUP_MS); }
      assert.equal(nextModels.released, false, "the state redraw completed while the next model GET was held");
      nextModels.release();
      await nextModels.finished();
      await completed(nextOpening);
      check(await opened(), "the state re-read after a changed list dropped the click on Pick");
      assert.equal(fx.posts, 1);
      await shut();
      assert.deepEqual(failed, []);
      assert.deepEqual(errors, []);
    });
  }
}
