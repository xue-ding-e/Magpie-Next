// Real pointer and keyboard resizing of the shipped provider and dictionary
// dialogs. APIs are local fixtures; no account or masking policy is changed.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const { reach, watchSettingsReads, settingsDrawn } = require("./reader.cjs");

const assets = path.resolve(__dirname, "../assets");
// An external partial asset overlay makes each old-CSS/old-logic control run
// the same tests without temporarily reverting a shared working tree.
const overlay = process.env.MAGPIE_RESIZE_OVERLAY;
const frames = (page) => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));

async function open(t, engine, lang, { dictionary = false, holdUsage = false, holdResize = false, oneAccount = false } = {}) {
  const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
  let releaseUsage;
  const usageReady = new Promise(resolve => { releaseUsage = resolve; });
  let releaseResize, requestedResize;
  const resizeReady = new Promise(resolve => { releaseResize = resolve; });
  const resizeRequested = new Promise(resolve => { requestedResize = resolve; });
  t.after(async () => { releaseUsage(); releaseResize(); await browser.close(); });
  const page = await browser.newPage({ viewport: { width: 1280, height: 1200 }, reducedMotion: "reduce" });
  page.setDefaultTimeout(5000);
  page.setDefaultNavigationTimeout(60000);
  const errors = [], posts = [];
  let syncs = 0;
  page.on("pageerror", error => errors.push(error.message));
  const users = oneAccount ? ["first@example.invalid"] : ["first@example.invalid", "second@example.invalid"];
  const provider = {
    id: "codex", name: "Codex", icon: "codex-color", chat: "", responses: "", anthropic: "", catalog: "",
    models: [{ id: "gpt-6", on: true }, { id: "gpt-6-mini", on: true }],
    agents: [], fallback: [], headers: {}, keyList: [], proxy: "",
    account: { agent: "codex", agentName: "Codex", user: users[0], plan: "PLUS",
      logins: users.map((user, index) => ({ user, plan: "PLUS", on: true, active: index === 0 })) },
  };
  const state = { agents: [], profiles: [], settings: { lang, theme: "light" } };
  await page.route("**/*", async route => {
    const request = route.request(), url = new URL(request.url()), p = url.pathname;
    const json = value => route.fulfill({ json: value });
    if (url.host !== "magpie.test") return route.abort();
    if (p === "/surface-resize-editor.js" && holdResize) {
      requestedResize(); await resizeReady;
    }
    if (p === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs=${JSON.stringify({ lang, theme: "light", web: true })};` });
    if (p === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window={};" });
    if (p === "/api/state") return json(state);
    if (p === "/api/settings") return json({ ...state.settings, redactWords: [] });
    if (p === "/api/update/using") {
      // Upstream reports transient window presence for automatic restarts;
      // it does not save provider, account or dictionary settings.
      assert.equal(request.method(), "POST");
      const body = request.postDataJSON();
      assert.deepEqual(Object.keys(body), ["open"]);
      assert.equal(typeof body.open, "boolean");
      return json({});
    }
    if (p === "/api/providers") return json({ providers: [provider], presets: [], excluded: [], gateway: { running: true, window: true } });
    if (p === "/api/groups") return json({ models: [], groups: [], pools: [] });
    if (p === "/api/login/usage") {
      if (holdUsage) await usageReady;
      return json(Object.fromEntries(users.map(user => [user, { windows: [{ name: "5 hours", used: 20, resetsAt: new Date(Date.now() + 36e5).toISOString() }] }])));
    }
    if (p === "/api/sync") { syncs++; return json(state); }
    if (p === "/api/settings/local-dictionary") {
      if (request.method() === "POST") posts.push({ path: p, body: request.postDataJSON() });
      return json({ revision: "new", count: 0, bytes: 0 });
    }
    if (p.startsWith("/api/")) {
      if (request.method() === "POST") posts.push({ path: p, body: request.postDataJSON() });
      return json({});
    }
    if (!/^\/[\w./-]*$/.test(p)) return route.abort();
    const relative = p === "/" ? "index.html" : p.slice(1);
    let body;
    if (overlay) {
      try { body = await fs.readFile(path.join(overlay, relative)); }
      catch (error) { if (error.code !== "ENOENT") throw error; }
    }
    body ??= await fs.readFile(path.join(assets, relative));
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(relative)];
    return route.fulfill({ body, contentType });
  });
  let dialog;
  if (dictionary) {
    await watchSettingsReads(page);
    await page.goto("http://magpie.test/?view=settings&tab=privacy");
    await settingsDrawn(page);
    const button = page.getByRole("button", { name: await page.evaluate(() => t("Manage local dictionary")), exact: true });
    await reach(page, button); await button.click();
    dialog = page.locator("dialog.local-dictionary");
    await page.waitForFunction(() => document.querySelector("dialog.local-dictionary textarea")?.disabled === false);
  } else {
    await page.goto("http://magpie.test/?view=providers");
    await page.locator('.row.provider[data-id="codex"]').click();
    dialog = page.locator("#modal .dialog");
    if (!holdResize) await dialog.locator(":scope > .surface-resize").waitFor();
    if (!holdUsage) await page.waitForFunction(() => !loginUsage.codex.loading);
    await page.waitForFunction(() => !document.querySelector("#modal .dialog").getAnimations().length);
  }
  await frames(page);
  return { page, dialog, errors, posts, releaseUsage, releaseResize, resizeRequested, syncs: () => syncs };
}

const measure = dialog => dialog.evaluate(element => {
  const rect = element.getBoundingClientRect(), parent = element.closest(".modal");
  const room = parent?.getBoundingClientRect(), style = parent && getComputedStyle(parent);
  return { left: rect.left, top: rect.top, right: rect.right, bottom: rect.bottom, width: rect.width, height: rect.height,
    inlineWidth: element.style.width, inlineHeight: element.style.height,
    room: parent ? { top: room.top + parseFloat(style.paddingTop), bottom: room.bottom - parseFloat(style.paddingBottom),
      left: room.left + parseFloat(style.paddingLeft), right: room.right - parseFloat(style.paddingRight) }
      : { top: 24, bottom: innerHeight - 24, left: 16, right: innerWidth - 16 } };
});
const fits = at => {
  assert(at.left >= at.room.left - 1 && at.right <= at.room.right + 1 && at.top >= at.room.top - 1 && at.bottom <= at.room.bottom + 1,
    "the resized dialog stays inside the available viewport: " + JSON.stringify(at));
};
const centered = at => {
  assert(Math.abs(at.left - (at.room.left + at.room.right - at.width) / 2) < 1.5
    && Math.abs(at.top - (at.room.top + at.room.bottom - at.height) / 2) < 1.5,
  "Home centers the default size in the current viewport: " + JSON.stringify(at));
};
async function handleHits(dialog) {
  const hits = await dialog.locator(":scope > .surface-resize").evaluate(handle => {
    const r = handle.getBoundingClientRect();
    return [[2, 2], [r.width - 2, 2], [2, r.height - 2], [r.width - 2, r.height - 2], [r.width / 2, r.height / 2], [r.width - 4, r.height - 4]].map(([x, y]) => {
      const hit = document.elementFromPoint(r.left + x, r.top + y);
      return { x, y, hit: handle.contains(hit), target: hit?.className,
        ...(handle.contains(hit) ? {} : { viewport: [innerWidth, innerHeight],
          handle: r.toJSON(), surface: handle.parentElement.getBoundingClientRect().toJSON(),
          inset: getComputedStyle(handle).right, radius: getComputedStyle(handle).borderRadius,
          parentRadius: getComputedStyle(handle.parentElement).borderRadius }) };
    });
  });
  assert(hits.every(hit => hit.hit), "the whole resize handle receives pointer input: " + JSON.stringify(hits));
}
async function footerClear(dialog, selector) {
  const overlaps = await dialog.evaluate((element, selector) => {
    const grip = element.querySelector(":scope > .surface-resize").getBoundingClientRect();
    return [...element.querySelectorAll(selector)].filter(button => {
      const r = button.getBoundingClientRect();
      return r.width && r.height && r.left < grip.right && r.right > grip.left && r.top < grip.bottom && r.bottom > grip.top;
    }).map(button => button.textContent);
  }, selector);
  assert.deepEqual(overlaps, [], "the footer reserves room for the complete resize handle");
}
async function screenshot(page, engine, lang, name) {
  if (!process.env.MAGPIE_UI_ARTIFACTS) return;
  await fs.mkdir(process.env.MAGPIE_UI_ARTIFACTS, { recursive: true });
  await page.screenshot({ path: path.join(process.env.MAGPIE_UI_ARTIFACTS, `${engine}-${lang}-${name}.png`) });
}
const sameGeometry = (before, after, why) => {
  for (const key of ["top", "left", "width", "height"]) assert(Math.abs(before[key] - after[key]) < 1.5,
    why + ": " + key + " " + JSON.stringify({ before, after }));
};
async function openTools(row) {
  // The upstream account row keeps unset policies behind its original ⋯.
  if (await row.locator(".acc-tools").isVisible()) return;
  await row.hover();
  await row.locator(".acc-more").click();
  await row.locator(".acc-tools").waitFor();
}

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"])) {
  for (const replacement of [false, true]) {
    test(`${engine} en: a late resize module cannot attach to a ${replacement ? "replacement question" : "closed editor"}`, async t => {
      const { page, dialog, errors, posts, releaseResize, resizeRequested } = await open(t, engine, "en", { holdResize: true, oneAccount: replacement });
      await resizeRequested;
      assert.equal(await dialog.locator(":scope > .surface-resize").count(), 0);
      if (replacement) {
        const row = dialog.locator(".acc").first();
        await openTools(row);
        await row.getByRole("button", { name: "Sign out", exact: true }).click();
        await dialog.locator(".forget-ask").waitFor();
      } else {
        await dialog.locator(":scope > .editor > .bar").getByRole("button", { name: "Cancel", exact: true }).click();
        await page.locator("#modal").waitFor({ state: "hidden" });
      }
      releaseResize();
      // Wait for the actual module evaluation, including the loader's
      // already registered completion, before checking its visible effect.
      await page.evaluate(async () => { await import("./surface-resize-editor.js"); });
      await frames(page);
      assert.equal(await page.locator("#modal .surface-resize").count(), 0,
        "the obsolete import must not put a resize control on another surface");
      if (replacement) {
        await dialog.locator(".forget-ask").getByRole("button", { name: "Cancel", exact: true }).click();
      } else {
        await page.locator('.row.provider[data-id="codex"]').click();
      }
      await dialog.locator(":scope > .surface-resize").waitFor();
      assert.equal(await dialog.locator(":scope > .surface-resize").count(), 1,
        "a fresh editor still gets exactly one working resize control");
      const before = await measure(dialog);
      await dialog.locator(":scope > .surface-resize").press("ArrowDown"); await frames(page);
      assert((await measure(dialog)).height > before.height, "the reopened control changes the dialog's size");
      assert.deepEqual(posts, [], "closing and resizing must not post a saved change: " + JSON.stringify(posts));
      assert.deepEqual(errors, []);
    });
  }
  for (const lang of ["en", "zh", "zh-TW", "ja", "de"]) {
    test(`${engine} ${lang}: provider resize hit area, vertical pointer growth and refresh survive`, async t => {
      const { page, dialog, errors, posts, releaseUsage, syncs } = await open(t, engine, lang, { holdUsage: true });
      await handleHits(dialog);
      const handle = dialog.locator(":scope > .surface-resize"), originalHandle = await handle.elementHandle();
      const originalAccounts = await dialog.locator(".editor .accts").elementHandle();
      await handle.evaluate(element => element.addEventListener("pointerdown", event => { window.__resizePointer = event.pointerId; window.__resizeTrusted = event.isTrusted; }));
      const before = await measure(dialog), grip = await handle.boundingBox();
      await page.mouse.move(grip.x + grip.width - 4, grip.y + grip.height - 4);
      await page.mouse.down();
      await page.mouse.move(grip.x + grip.width + 36, grip.y + grip.height + 36, { steps: 4 });
      await frames(page);
      const during = await measure(dialog);
      releaseUsage();
      await page.waitForFunction(() => !loginUsage.codex.loading && !document.querySelector("#modal .aq .skeleton"));
      await frames(page);
      assert.equal(await originalAccounts.evaluate(element => element.isConnected), false, "the allowance reply redraws the account list");
      assert(await originalHandle.evaluate(element => element.isConnected && element.hasPointerCapture(window.__resizePointer)),
        "an allowance refresh keeps the real drag's handle and pointer capture");
      sameGeometry(during, await measure(dialog), "an allowance redraw during a drag preserves the requested geometry");
      assert.equal(await page.evaluate(() => window.__resizeTrusted), true, "the drag starts with real pointer input");
      await page.mouse.move(grip.x + grip.width + 96, grip.y + grip.height + 96, { steps: 6 });
      await page.mouse.up(); await frames(page);
      const after = await measure(dialog);
      t.diagnostic(JSON.stringify({ before, after }));
      assert(after.width > before.width + 60, "a trusted drag widens the editor");
      assert(after.height > before.height + 80, "a trusted drag grows the editor downward");
      assert(Math.abs(after.top - before.top) < 1.5, "pointer resizing keeps the top edge fixed instead of holding the moving grip");
      fits(after);
      assert.equal(await dialog.evaluate(element => element.classList.contains("surface-sizing")), false);
      await footerClear(dialog, ":scope > .editor > .bar button");
      await screenshot(page, engine, lang, "provider-drag-redraw");

      // A second real drag can consume all vertical room, including the
      // space above the initially centred editor, before it is clamped.
      const corner = await handle.boundingBox();
      await page.mouse.move(corner.x + corner.width - 4, corner.y + corner.height - 4);
      await page.mouse.down();
      await page.mouse.move(corner.x + corner.width - 4, 1700, { steps: 10 });
      await page.mouse.up(); await frames(page);
      const tallest = await measure(dialog); fits(tallest);
      assert(Math.abs(tallest.height - (tallest.room.bottom - tallest.room.top)) < 1.5,
        "a pointer drag can use the full viewport height: " + JSON.stringify(tallest));
      await handle.press("Home"); await frames(page);

      // Existing account menus still open and close after the resize/redraw.
      const row = dialog.locator('.acc[data-account-id="first@example.invalid"]');
      await openTools(row);
      for (const selector of [".acap", ".alane"]) {
        await row.locator(selector).click();
        await page.locator(".proto-menu").waitFor();
        await row.locator(selector).click();
        await page.locator(".proto-menu").waitFor({ state: "detached" });
      }
      await row.locator(".amodels").click();
      await row.locator(".acct-models").waitFor();
      await row.locator(".acct-models").getByRole("button", { name: await page.evaluate(() => t("Cancel")), exact: true }).click();
      assert.equal(posts.length, 0, "resizing and canceling menus do not save account policies");
      await dialog.locator(".editor > .bar").getByRole("button", { name: await page.evaluate(() => t("Cancel")), exact: true }).click();
      await page.waitForFunction(() => document.querySelector("#modal").hidden);
      const response = page.waitForResponse(r => new URL(r.url()).pathname === "/api/sync" && r.request().method() === "POST");
      await page.locator("#sync").click(); await response;
      assert.equal(syncs(), 1, "the existing page refresh still reaches its endpoint");
      assert.deepEqual(errors, []);
    });

    test(`${engine} ${lang}: keyboard growth uses vertical room, viewport bounds and Home restores geometry`, async t => {
      const { page, dialog, errors, posts } = await open(t, engine, lang);
      const handle = dialog.locator(":scope > .surface-resize"), before = await measure(dialog);
      await handle.focus(); await handle.press("ArrowDown"); await frames(page);
      const down = await measure(dialog);
      assert(Math.abs(down.height - before.height - 10) < 1.5, "ArrowDown grows by ten pixels");
      assert(Math.abs(down.top - before.top) < 1.5, "keyboard growth must not move the dialog upward to hold its grip");
      await handle.press("Shift+ArrowDown"); await frames(page);
      assert(Math.abs((await measure(dialog)).height - down.height - 40) < 1.5, "Shift grows by forty pixels");
      for (let i = 0; i < 25; i++) { await handle.press("Shift+ArrowDown"); await frames(page); }
      const tall = await measure(dialog);
      t.diagnostic(JSON.stringify({ before, tall }));
      fits(tall);
      assert(Math.abs(tall.bottom - tall.room.bottom) < 1.5, "keyboard growth can reach the available lower edge");
      assert(Math.abs(tall.height - (tall.room.bottom - tall.room.top)) < 1.5,
        "keyboard growth can use the full viewport height: " + JSON.stringify(tall));
      await screenshot(page, engine, lang, "provider-keyboard-max");
      await handle.press("Home"); await frames(page);
      const reset = await measure(dialog);
      // The first allowance reply can shorten the initially held dialog.
      // Home restores the default dimensions and centers their current size.
      for (const key of ["width", "height"]) assert(Math.abs(reset[key] - before[key]) < 1.5, "Home restores " + key + ": " + JSON.stringify({ before, reset }));
      centered(reset);
      assert.equal(reset.inlineWidth + reset.inlineHeight, "", "Home drops explicit dimensions");
      await screenshot(page, engine, lang, "provider-home");

      await handle.press("Shift+ArrowRight"); await handle.press("Shift+ArrowDown");
      for (const viewport of [{ width: 500, height: 420 }, { width: 420, height: 280 }]) {
        await page.setViewportSize(viewport); await frames(page);
        fits(await measure(dialog));
        await handleHits(dialog);
      }
      // The native Mac title bar removes additional vertical room. Exercise
      // its CSS geometry here without claiming this is a native macOS run.
      await page.evaluate(() => document.body.classList.add("mac")); await frames(page);
      fits(await measure(dialog));
      await handleHits(dialog);
      await handle.press("Home"); await frames(page); fits(await measure(dialog));
      await page.evaluate(() => document.body.classList.remove("mac"));
      await page.setViewportSize({ width: 1280, height: 1200 }); await frames(page);
      await handle.press("Home"); await frames(page);
      const restored = await measure(dialog);
      for (const key of ["width", "height"]) assert(Math.abs(restored[key] - before[key]) < 1.5, "Home restores the default size after a viewport change: " + key);
      centered(restored);
      assert.equal(posts.length, 0);
      assert.deepEqual(errors, []);
    });

    test(`${engine} ${lang}: held resize keys survive redraw and release restores ordinary anchoring`, async t => {
      const { page, dialog, errors, posts, releaseUsage } = await open(t, engine, lang, { holdUsage: true });
      // This case asks for 60px while the top stays still. Leave that room
      // below the expanded upstream account tools; the viewport-bound cases
      // above separately require the dialog to move up when it runs out.
      await page.setViewportSize({ width: 1280, height: 1600 }); await frames(page);
      const row = dialog.locator('.acc[data-account-id="first@example.invalid"]');
      await openTools(row); await frames(page);
      const handle = dialog.locator(":scope > .surface-resize"), before = await measure(dialog);
      assert(before.room.bottom - before.bottom >= 68, "the fixture leaves room for all six 10px steps");
      const originalAccounts = await dialog.locator(".editor .accts").elementHandle();
      await handle.focus();
      await page.keyboard.down("ArrowDown"); await frames(page);
      releaseUsage();
      await page.waitForFunction(() => !loginUsage.codex.loading);
      await frames(page);
      assert.equal(await originalAccounts.evaluate(element => element.isConnected), false, "the allowance reply redraws the account list");
      for (let i = 0; i < 5; i++) { await page.keyboard.down("ArrowDown"); await frames(page); }
      await page.keyboard.up("ArrowDown"); await frames(page);
      const grown = await measure(dialog);
      if (Math.abs(grown.top - before.top) >= 1.5 || Math.abs(grown.height - before.height - 60) >= 1.5)
        t.diagnostic(JSON.stringify({ before, grown }));
      assert(Math.abs(grown.height - before.height - 60) < 1.5, "every held arrow step grows by ten pixels across a redraw");
      assert(Math.abs(grown.top - before.top) < 1.5, "a held key keeps the top edge while room remains below");
      fits(grown);
      assert.equal(await dialog.evaluate(element => element.classList.contains("surface-sizing")), false, "key release ends explicit sizing");
      const cap = row.locator(".acap");
      const at = await cap.boundingBox();
      await cap.click(); await page.locator(".proto-menu").waitFor(); await frames(page);
      const now = await cap.boundingBox();
      assert(Math.abs(now.y - at.y) < 1.5, "an ordinary menu retains its pressed button after key release");
      sameGeometry(grown, await measure(dialog), "ordinary menus keep the new geometry");
      await cap.click(); await page.locator(".proto-menu").waitFor({ state: "detached" });
      await handle.press("Home"); await frames(page); centered(await measure(dialog));
      assert.equal(posts.length, 0);
      assert.deepEqual(errors, []);
    });

    test(`${engine} ${lang}: dictionary resize handle is whole, footer stays clear and Home fits`, async t => {
      const { page, dialog, errors, posts } = await open(t, engine, lang, { dictionary: true });
      await handleHits(dialog);
      const before = await measure(dialog), handle = dialog.locator(":scope > .surface-resize"), grip = await handle.boundingBox();
      await page.mouse.move(grip.x + grip.width - 3, grip.y + grip.height - 3); await page.mouse.down();
      await page.mouse.move(grip.x + grip.width + 57, grip.y + grip.height + 57, { steps: 6 }); await page.mouse.up(); await frames(page);
      const grown = await measure(dialog);
      assert(grown.width > before.width + 40 && grown.height > before.height + 40, "a real drag resizes the whole dictionary");
      for (const viewport of [{ width: 1280, height: 1200 }, { width: 420, height: 760 }]) {
        await page.setViewportSize(viewport); await frames(page); fits(await measure(dialog));
        await handleHits(dialog);
        await footerClear(dialog, ".dictionary-buttons button");
      }
      await handle.focus(); await handle.press("Home"); await frames(page);
      const reset = await measure(dialog); fits(reset);
      assert.equal(reset.inlineWidth + reset.inlineHeight, "");
      await screenshot(page, engine, lang, "dictionary-home-420");
      assert.equal(posts.length, 0, "dictionary resizing never saves entries");
      assert.deepEqual(errors, []);
    });

    test(`${engine} ${lang}: resize targets and footers fit seven window widths`, async t => {
      const { page, dialog, errors, posts } = await open(t, engine, lang);
      const handle = dialog.locator(":scope > .surface-resize");
      for (const width of [360, 420, 640, 900, 1280, 1440, 1920]) {
        await page.setViewportSize({ width, height: 900 }); await frames(page);
        await handle.press("Home"); await frames(page);
        fits(await measure(dialog));
        await handleHits(dialog);
        await footerClear(dialog, ":scope > .editor > .bar button");
        const layout = await dialog.evaluate(element => ({ width: element.clientWidth, scroll: element.scrollWidth, doc: document.documentElement.scrollWidth }));
        assert(layout.scroll <= layout.width + 1 && layout.doc <= width, "resizing does not introduce horizontal overflow: " + JSON.stringify({ width, layout }));
      }
      assert.equal(posts.length, 0);
      assert.deepEqual(errors, []);
    });
  }
}
