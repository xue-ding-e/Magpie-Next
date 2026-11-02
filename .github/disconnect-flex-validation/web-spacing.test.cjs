// Run with Node's test runner and Playwright on the module path; see README.md.
// 断开确认框的文件预览应占满正文，不能落入编辑器的标签列；Codex 的
// 保留说明另占一行。使用真实页面和隔离的 API 数据，不读取或改写用户配置。
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");

const assets = path.resolve("internal/gui/assets");
const changes = (id) => [
  {
    path: id === "omp" ? "~/.omp/agent/config.yml" : "~/.codex/config.toml",
    lines: [
      { op: "-", text: "default: magpie/group/auto-model" },
      { op: "-", text: "defaultThinkingLevel: xhigh" },
      ...Array.from({ length: 12 }, (_, i) => ({ op: "-", text: `  model-${i}: magpie/group/auto-model-${i}` })),
    ],
  },
  {
    path: id === "omp" ? "~/.omp/agent/models.yml" : "~/.codex/models.json",
    lines: [{ op: "~", text: "providers: {}", was: "providers: {magpie: {baseUrl: 'http://127.0.0.1:3425/v1', models: [{id: group/auto-model, name: Auto model}]}}" }],
  },
];

function serve(lang, textSize, posts) {
  const state = {
    agents: ["omp", "codex"].map((id) => ({
      id, name: id === "omp" ? "omp" : "Codex", icon: id === "omp" ? "omp" : "generic", wired: true,
      path: id === "omp" ? "~/.omp/agent/config.yml" : "~/.codex/config.toml",
      fields: [{ key: "model", label: "model", value: "magpie/relay/m1", options: [{ value: "magpie/relay/m1", label: "Model one", ref: "relay/m1" }] }],
    })),
    profiles: [], settings: { lang, theme: "light", textSize },
  };
  return async (route) => {
    const req = route.request(), url = new URL(req.url());
    const json = (data) => route.fulfill({ json: data });
    if (req.method() === "POST") posts.push(url.pathname);
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = ${JSON.stringify({ lang, theme: "light", textSize, web: false })};` });
    if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
    if (url.pathname === "/api/state") return json(state);
    if (url.pathname === "/api/settings") return json(state.settings);
    if (url.pathname.startsWith("/api/agents/preview/")) return json({ changes: changes(url.pathname.split("/").pop()) });
    if (url.pathname === "/api/usage/quotas") return json([]);
    if (url.pathname === "/api/groups") return json({ groups: [] });
    if (url.pathname === "/api/plugins") return json({ plugins: [] });
    if (url.pathname === "/api/agents/cli") return json({ agents: {}, pending: false });
    if (url.pathname === "/api/providers") return json({ providers: [], presets: [], excluded: [], gateway: { running: true, window: true } });
    if (url.pathname.startsWith("/api/")) return json({});
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    await route.fulfill({ body: await fs.readFile(file), contentType });
  };
}

// CI-only compact-web coverage using the same synthetic preview replies.
for (const engine of ["chromium", "webkit"]) {
  for (const lang of ["en", "zh"]) {
    test(`${engine} ${lang}: compact web disconnect preserves spacing and width`, async (t) => {
      const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
      t.after(() => browser.close());
      for (const [width, height, touch] of [[560, 420, false], [900, 700, true]]) {
        const context = await browser.newContext({ viewport: { width, height }, hasTouch: touch, reducedMotion: "reduce" });
        const page = await context.newPage(), posts = [], errors = [];
        page.setDefaultTimeout(5000);
        page.on("pageerror", (e) => errors.push(e.message));
        page.on("console", (m) => { if (m.type() === "error") errors.push(m.text()); });
        const fixture = serve(lang, 100, posts);
        await page.route("**/*", route => new URL(route.request().url()).pathname === "/boot.js"
          ? route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = ${JSON.stringify({ lang, theme: "light", textSize: 100, web: true })};` }) : fixture(route));
        await page.goto("http://magpie.test/?view=agents");
        assert.ok(await page.evaluate(() => document.body.classList.contains("web")), "real web mode is active");
        if (touch) assert.ok(await page.evaluate(() => matchMedia("(pointer: coarse)").matches), "the wider viewport really uses the coarse-pointer branch");
        await page.locator('.row.agent[data-id="codex"] .ag-conn').click();
        const ask = page.locator(".editor.disconnect-ask");
        await ask.locator(".ag-diff-file").last().waitFor();
        await page.locator("#modal").evaluate((m) => Promise.all(m.getAnimations({ subtree: true }).map((a) => a.finished)));
        const layout = await ask.evaluate(ed => {
          const b = ed.querySelector(".ebody"), p = b.querySelector(".lib-confirm"), d = b.querySelector(".ag-diff-wrap"), n = b.querySelector(".ag-note");
          const rect = e => { const r = e.getBoundingClientRect(); return { left: r.left, right: r.right, top: r.top, bottom: r.bottom }; };
          const r = b.getBoundingClientRect(), css = getComputedStyle(b);
          return { body: { left: r.left + b.clientLeft + parseFloat(css.paddingLeft), right: r.left + b.clientLeft + b.clientWidth - parseFloat(css.paddingRight) }, paragraph: rect(p), diff: rect(d), note: rect(n), padding: [parseFloat(css.paddingLeft), parseFloat(css.paddingRight)], gap: getComputedStyle(b).getPropertyValue("--disconnect-gap").trim(), overflow: b.scrollWidth - b.clientWidth };
        });
        assert.equal(layout.gap, "6px", "the original compact-web gap is retained");
        assert.deepEqual(layout.padding, [12, 12], "compact-web body padding is unchanged");
        assert.ok(Math.abs(layout.diff.top - layout.paragraph.bottom - 8) <= 1, `paragraph to preview remains 6+2px: ${JSON.stringify(layout)}`);
        assert.ok(Math.abs(layout.note.top - layout.diff.bottom - 16) <= 1, `preview to note remains 6+10px: ${JSON.stringify(layout)}`);
        for (const item of [layout.paragraph, layout.diff, layout.note]) {
          assert.ok(Math.abs(item.left - layout.body.left) <= 1 && Math.abs(item.right - layout.body.right) <= 1, `compact-web blocks span body: ${JSON.stringify(layout)}`);
        }
        assert.ok(layout.overflow <= 1, "compact-web body has no horizontal overflow");
        const cancel = ask.locator(".bar").getByRole("button", { name: lang === "zh" ? "取消" : "Cancel", exact: true });
        await cancel.focus();
        await page.keyboard.press("Tab");
        assert.ok(await ask.locator(".bar .primary").evaluate(b => b === document.activeElement));
        await page.keyboard.press("Shift+Tab");
        assert.ok(await cancel.evaluate(b => b === document.activeElement));
        await page.keyboard.press("Escape");
        await page.locator("#modal").waitFor({ state: "hidden" });
        assert.deepEqual(posts, [], "web preview and keyboard dismissal do not write configuration");
        assert.deepEqual(errors, [], "web preview has no runtime or console errors");
        await context.close();
      }
    });
  }
}
