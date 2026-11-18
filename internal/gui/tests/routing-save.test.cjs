// Run with Node's test runner and Playwright on the module path; see README.md.
// A provider's Routing (Smart, In order, In turn…) and Stays (Auto,
// Session…) are picked in its editor and made with its Save, as the rest of
// the editor is; Cancel drops them. A click used to post provider/route and
// redraw the editor at once, so looking through what each option says
// lagged, and saved a pick only looked at (01huadalang on Discord, on a
// WorkBuddy AI subscription with several accounts). Stays, which a
// provider's Routing page row had alone, is beside Routing in the editor
// too. Accounts and keys alike; nothing is sent, redrawn or scrolled before
// the Save. In English and Chinese, Chromium and WebKit.
//
// The routings besides Smart are the built-in plugin rotation's
// (plugins/rotation): switched off, the editor offers Smart alone, at a
// phone's width too.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { reach } = require("./reader.cjs");

const assets = path.resolve(__dirname, "../assets");
const base = { icon: "generic", catalog: "", models: [{ id: "glm-5", name: "GLM-5", on: true }], agents: [], fallback: [], headers: {}, routing: "", affinity: "", ready: true };
const sub = {
  ...base, id: "workbuddy-ai", name: "WorkBuddy AI", chat: "", responses: "", anthropic: "", keyList: [],
  account: {
    agent: "workbuddy-ai", agentName: "WorkBuddy AI", user: "one@example.test", plan: "Pro",
    logins: ["one", "two", "three"].map((n, i) => ({ user: `${n}@example.test`, plan: "Pro", active: i === 0, on: true })),
  },
};
const relay = {
  ...base, id: "relay", name: "Relay", chat: "https://relay.example.invalid/v1", responses: "", anthropic: "", key: { set: true, masked: "sk-…1234" },
  keyList: [1, 2].map((i) => ({ id: `key-${i}`, name: `Key ${i}`, masked: `sk-…000${i}`, active: i === 1, on: true })),
};

function serve(lang, posts, off = []) {
  const state = { agents: [], profiles: [], settings: { lang, theme: "light" } };
  const providers = { providers: [sub, relay], presets: [], excluded: [], gateway: { running: true, window: true } };
  return async (route) => {
    const url = new URL(route.request().url());
    const json = (data) => route.fulfill({ json: data });
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:${JSON.stringify(lang)},theme:"light",web:true,featuresOff:${JSON.stringify(off)}};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json(state);
    if (url.pathname === "/api/providers") return json(providers);
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (route.request().method() === "POST" && url.pathname.startsWith("/api/provider")) {
      posts.push({ path: url.pathname, body: route.request().postDataJSON() });
      return json(providers);
    }
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    await route.fulfill({ body: await fs.readFile(file), contentType: { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)] });
  };
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    test(`${engine} ${lang}: reset priority defaults to weekly, stages, validates, saves, reloads and clears`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || "chromium" }));
      t.after(() => browser.close());
      for (const width of [980, 420]) for (const source of [sub, relay]) {
        const p = structuredClone(source), posts = [], errors = [];
        const page = await browser.newPage({ viewport: { width, height: 1000 }, reducedMotion: "reduce" });
        page.setDefaultTimeout(8000);
        page.setDefaultNavigationTimeout(60000);
        page.on("pageerror", (e) => errors.push(e.message));
        const data = () => ({ providers: [p], presets: [], excluded: [], gateway: { running: true, window: true } });
        const fallback = serve(lang, posts);
        await page.route("**/*", async (route) => {
          const url = new URL(route.request().url());
          if (url.pathname === "/api/providers") return route.fulfill({ json: data() });
          if (url.pathname === "/api/provider/save") {
            const body = route.request().postDataJSON();
            posts.push({ path: url.pathname, body });
            if (body.resetPriority?.hours) p.resetPriority = body.resetPriority;
            else if (body.resetPriority) delete p.resetPriority;
            return route.fulfill({ json: data() });
          }
          return fallback(route);
        });
        await page.goto("http://magpie.test/?view=providers");
        const word = (key) => page.evaluate((k) => t(k), key);
        const click = async (control) => { await reach(page, control); await control.click(); };
        const open = async () => {
          await click(page.locator(".row.provider", { hasText: p.name }).first());
          await page.locator(".editor .accts").first().waitFor();
          const advanced = page.locator(".editor details.advanced");
          if (!await advanced.evaluate((e) => e.open)) await click(advanced.locator(":scope > summary"));
          await page.locator(".reset-priority input[type=checkbox]").waitFor({ state: "attached" });
        };
        const box = page.locator(".reset-priority"), options = box.locator(".reset-priority-options");
        const choose = async (value, index = 0) => {
          const control = options.locator(".segs").nth(index).locator(`.opt[data-seg="${value}"]`);
          await reach(page, control);
          const y = await page.locator("#view-providers").evaluate((e) => e.scrollTop);
          await control.click();
          assert.equal(await page.locator("#view-providers").evaluate((e) => e.scrollTop), y, "a choice never scrolls the page");
        };
        const save = async () => {
          await click(page.getByRole("button", { name: await word("Save"), exact: true }));
          await page.waitForFunction(() => !document.querySelector(".editor"));
        };
        await open();
        assert.equal(await box.locator("input[type=checkbox]").isChecked(), false, "priority starts off");
        await click(box.locator("label").first());
        assert.equal(await options.locator('.segs .opt[data-seg="week"]').getAttribute("aria-pressed"), "true", "weekly is the default");
        assert.equal(await options.locator('.segs').nth(1).locator('.opt[data-seg=""]').getAttribute("aria-pressed"), "true", "current routing is the default");
        const keys = await page.evaluate(() => {
          const keys = ["Before quota resets", "Prefer accounts near reset", "Quota cycle", "Weekly quota", "Monthly quota", "Daily quota", "Short quota window", "Any quota window", "Hours before reset", "Preferred accounts", "Current routing", "Soonest reset first", "Reset priority hours: enter a number above 0 up to 8760"];
          return Object.entries(I18N).flatMap(([locale, entries]) => keys.filter((k) => !entries[k]).map((k) => locale + ": " + k));
        });
        assert.deepEqual(keys, [], "every label is translated in all four dictionaries");
        const hours = options.locator("input");
        for (const cycle of ["month", "day", "short", "any", "week"]) await choose(cycle);
        await reach(page, hours);
        await hours.fill("0");
        await click(page.getByRole("button", { name: await word("Save"), exact: true }));
        assert.equal(posts.length, 0, "invalid hours never save");
        assert.equal(await page.locator(".editor").count(), 1);
        await reach(page, hours);
        await hours.fill("6.5");
        await choose("month");
        await choose("soonest", 1);
        if (process.env.ARTIFACT_DIR && engine === "chromium" && lang === "zh" && width === 980 && source.id === sub.id) {
          await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
          await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, "reset-priority-zh.png") });
        }
        assert.equal(posts.length, 0, "choices stay staged until Save");
        assert.equal(await page.locator(".editor select").count(), 0, "no native select");
        assert.ok(await page.evaluate(() => document.scrollingElement.scrollWidth <= innerWidth + 1), "fits the window");
        await save();
        assert.deepEqual(posts[0].body.resetPriority, { hours: 6.5, window: "month", order: "soonest" });
        await open();
        assert.equal(await options.locator('.segs .opt[data-seg="month"]').getAttribute("aria-pressed"), "true");
        assert.equal(await hours.inputValue(), "6.5");
        assert.equal(await options.locator('.segs').nth(1).locator('.opt[data-seg="soonest"]').getAttribute("aria-pressed"), "true");
        await choose("day");
        await click(page.getByRole("button", { name: await word("Cancel"), exact: true }));
        await page.getByRole("alertdialog").locator("button").last().click();
        await open();
        assert.equal(await options.locator('.segs .opt[data-seg="month"]').getAttribute("aria-pressed"), "true", "Cancel drops the staged cycle");
        await click(box.locator("label").first());
        await save();
        assert.equal(posts.at(-1).body.resetPriority.hours, 0, "switching off explicitly clears the policy");
        await open();
        assert.equal(await box.locator("input[type=checkbox]").isChecked(), false, "off survives reopening");
        assert.deepEqual(errors, [], `${width}px ${source.name}: no page errors`);
        await page.close();
      }
    });
  }
}

const ROUTINGS = [
  ["", "Smart", "智能"], ["order", "In order", "按顺序"], ["drain", "Until used up", "用完再换"], ["rotate", "In turn", "轮流"],
  ["usage", "Least used first", "用量少的优先"], ["pace", "Use before expiry", "到期前用完"],
];
const STAYS = [["", "Auto", "自动"], ["session", "Session", "整个会话"], ["turn", "Within a turn", "一轮之内"], ["off", "Off", "关闭"]];

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const lang of ["en", "zh"]) {
    const zh = lang === "zh";
    const L = { routing: zh ? "路由" : "Routing", stays: zh ? "会话保持" : "Stays", unsaved: zh ? "未保存" : "unsaved", save: zh ? "保存" : "Save", cancel: zh ? "取消" : "Cancel" };
    for (const p of [sub, relay]) {
      test(`${engine} ${lang}: ${p.name}'s Routing and Stays are made with its Save, not at each click`, async (t) => {
        const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || "chromium" }));
        t.after(() => browser.close());
        const page = await (await browser.newContext({ viewport: { width: 900, height: 720 }, reducedMotion: "reduce" })).newPage();
        page.setDefaultTimeout(5000);
        page.setDefaultNavigationTimeout(60000);
        const errors = [], posts = [];
        page.on("pageerror", (e) => errors.push(e.message));
        await page.route("**/*", serve(lang, posts));
        await page.goto("http://magpie.test/?view=providers");
        const open = async () => {
          await page.locator(".row.provider", { hasText: p.name }).first().click();
          await page.locator(".editor .accts").first().waitFor();
          // Routing and Stays are under Advanced, open already when one is set
          const adv = page.locator(".editor details.advanced");
          if (!(await adv.evaluate((d) => d.open))) await adv.locator(":scope > summary").click();
        };
        // the field under a label: the label's next sibling
        const fieldOf = (label) => page.locator(".editor label", { hasText: new RegExp(`^${label}$`) }).locator("xpath=following-sibling::div[1]");
        const opt = (label, name) => fieldOf(label).locator(".segs .opt", { hasText: new RegExp(`^${name}$`) });
        const hint = (label) => fieldOf(label).locator(":scope > .hint").first();
        const unsaved = (label) => fieldOf(label).getByText(L.unsaved, { exact: true });
        const said = (s) => page.evaluate(([k, zh]) => zh ? I18N.zh[k] : k, [s, zh]);
        await open();
        const missing = await page.evaluate(() => [
          "Stays", "Auto", "Session", "Within a turn", "Off", "unsaved", "Made when the provider is saved; Cancel drops it",
          "A conversation stays with the account or key that answered it while what the vendor cached of it is worth keeping — within a turn always, across turns while it's fresh.",
          "A conversation stays with the account or key that answered it for the whole session, while it can answer.",
          "A conversation stays put within a turn, while the agent sends tool results back; when you speak again, routing decides afresh.",
          "Every request is routed afresh, whoever answered its conversation before.",
        ].filter((k) => !I18N.zh[k]));
        assert.deepEqual(missing, [], "every string has its Chinese");
        const editor = await page.locator(".editor").elementHandle();
        const accts = await page.locator(".editor .accts").first().elementHandle();
        const y = await page.evaluate(() => document.scrollingElement.scrollTop);
        assert.equal(await unsaved(L.routing).isVisible(), false);
        // each option shows what it does: nothing sent, redrawn or scrolled
        for (const [id, en, cn] of [...ROUTINGS.slice(1), ROUTINGS[0]]) {
          await opt(L.routing, zh ? cn : en).click();
          assert.match(await opt(L.routing, zh ? cn : en).getAttribute("class"), /\bon\b/);
          const desc = await page.evaluate((id) => ROUTINGS.find((r) => r[0] === id)[2], id);
          assert.equal(await hint(L.routing).textContent(), await said(desc), `${en} says what it does`);
          assert.equal(await unsaved(L.routing).isVisible(), id !== "", `${en}: unsaved said`);
        }
        for (const [id, en, cn] of STAYS.slice(1)) {
          await opt(L.stays, zh ? cn : en).click();
          const desc = await page.evaluate((id) => STAYS.find((r) => r[0] === id)[2], id);
          assert.equal(await hint(L.stays).textContent(), await said(desc), `${en} says what it does`);
          assert.equal(await unsaved(L.stays).isVisible(), true);
        }
        await page.waitForTimeout(200);
        assert.deepEqual(posts, [], "nothing is sent before the Save");
        assert(await editor.evaluate((e) => e.isConnected) && await accts.evaluate((e) => e.isConnected), "the editor is not redrawn");
        assert.equal(await page.evaluate(() => document.scrollingElement.scrollTop), y, "a click never scrolls the page");
        // Cancel drops them; opened again, the editor is as saved
        await page.getByRole("button", { name: L.cancel, exact: true }).click();
        await page.getByRole("alertdialog").locator("button").last().click();
        await open();
        assert.match(await opt(L.routing, zh ? "智能" : "Smart").getAttribute("class"), /\bon\b/);
        assert.match(await opt(L.stays, zh ? "自动" : "Auto").getAttribute("class"), /\bon\b/);
        assert.equal(await unsaved(L.routing).isVisible(), false);
        assert.equal(await unsaved(L.stays).isVisible(), false);
        assert.deepEqual(posts, []);
        // picked again and saved: the Save carries them
        await opt(L.routing, zh ? "轮流" : "In turn").click();
        await opt(L.stays, zh ? "整个会话" : "Session").click();
        await page.getByRole("button", { name: L.save, exact: true }).click();
        await page.waitForFunction(() => !document.querySelector(".editor"));
        assert.deepEqual(posts.map((x) => x.path), ["/api/provider/save"]);
        assert.equal(posts[0].body.routing, "rotate");
        assert.equal(posts[0].body.affinity, "session");
        assert.deepEqual(errors, []);
      });
    }
    test(`${engine} ${lang}: with the routing strategies off, the editor offers Smart alone`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || "chromium" }));
      t.after(() => browser.close());
      for (const width of [900, 420]) {
        for (const p of [sub, relay]) {
          const where = `${width}px ${p.name}`;
          const page = await (await browser.newContext({ viewport: { width, height: 720 }, reducedMotion: "reduce" })).newPage();
          page.setDefaultTimeout(5000);
          page.setDefaultNavigationTimeout(60000);
          const errors = [];
          page.on("pageerror", (e) => errors.push(e.message));
          await page.route("**/*", serve(lang, [], ["rotation"]));
          await page.goto("http://magpie.test/?view=providers");
          await page.locator(".row.provider", { hasText: p.name }).first().click();
          await page.locator(".editor .accts").first().waitFor();
          const field = page.locator(".editor label", { hasText: new RegExp(`^${L.routing}$`) }).locator("xpath=following-sibling::div[1]");
          const shown = await field.locator(".segs").first().evaluate((box) => [...box.querySelectorAll(".opt")].filter((b) => getComputedStyle(b).display !== "none").map((b) => b.textContent));
          assert.deepEqual(shown, [zh ? "智能" : "Smart"], `${where}: Smart alone`);
          assert.match(await field.locator(".segs .opt").first().getAttribute("class"), /\bon\b/, `${where}: Smart picked`);
          assert.ok(await page.evaluate(() => document.scrollingElement.scrollWidth <= innerWidth + 1), `${where}: fits`);
          // on again, every routing is back
          await page.evaluate(() => applyFeatures([{ id: "rotation", on: true }]));
          const back = await field.locator(".segs").first().evaluate((box) => [...box.querySelectorAll(".opt")].filter((b) => getComputedStyle(b).display !== "none").length);
          // By weight is offered for accounts as for keys (rubbzha on X)
          assert.equal(back, 9, `${where}: every routing back`);
          assert.deepEqual(errors, [], where);
          await page.context().close();
        }
      }
    });
  }
}
