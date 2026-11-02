// Additional validation-only visual coverage. Product source and the existing
// 36-case focused regression remain unchanged. Only synthetic masked views are
// captured; this test never asks the API for raw credential JSON.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { test } = require('node:test');
const { chromium, webkit } = require('playwright');
const assets = path.resolve(process.env.MAGPIE_AUTH_ASSETS || 'internal/gui/assets');
const user = 'synthetic@example.invalid';
const masked = JSON.stringify({ auth_mode: '[hidden]', tokens: { access_token: '[hidden]', refresh_token: '[hidden]', id_token: '[hidden]', account_id: '[hidden]' } }, null, 2);
const destination = path.resolve(process.env.AUTH_VISUAL_ARTIFACTS || '.validation-runtime/reports/auth-visual');
for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ['chromium', 'webkit'])) {
 for (const lang of ['en', 'zh', 'zh-TW', 'ja', 'de']) {
  test(`${engine} ${lang}: masked preview screenshots and close/Escape cleanup`, async t => {
   const browser = await (engine === 'webkit' ? webkit.launch() : chromium.launch({ channel: 'chromium' }));
   t.after(() => browser.close());
   const page = await (await browser.newContext({ viewport: { width: 1000, height: 800 }, reducedMotion: 'reduce', serviceWorkers: 'block' })).newPage();
   page.setDefaultTimeout(5000);
   const posts = [], errors = [];
   page.on('pageerror', e => errors.push(e.message));
      await page.route('**/*', async route => {
        const request = route.request(), url = new URL(request.url());
        if (url.origin !== 'http://magpie.test') { await route.abort(); throw new Error('Unmocked visual-test origin refused'); }
        const json = data => route.fulfill({ json: data });
        if (url.pathname === '/boot.js') return route.fulfill({ contentType: 'text/javascript', body: `window.bootPrefs={lang:${JSON.stringify(lang)},theme:"light",web:true};` });
        if (url.pathname === '/wails/runtime.js') return route.fulfill({ contentType: 'text/javascript', body: 'export const Window = {};' });
        if (url.pathname === '/api/state') return json({ agents: [], profiles: [], settings: { lang, theme: 'light' } });
        if (url.pathname === '/api/providers') return json({ providers: [{
          id: 'codex', name: 'Codex', icon: 'codex-color', chat: '', responses: '', anthropic: '', catalog: '', routing: 'smart',
          models: [{ id: 'gpt-6', name: 'GPT-6', on: true }], agents: [], fallback: [], headers: {}, keyList: [],
          account: { agent: 'codex', agentName: 'Codex', agentIcon: 'codex-color', user, plan: 'PLUS', logins: [{ user, plan: 'PLUS', active: true, on: true }] },
        }], presets: [], excluded: [], gateway: { running: true, window: true } });
        if (url.pathname === '/api/groups') return json({ groups: [] });
        if (url.pathname === '/api/plugins') return json({ plugins: [] });
        if (url.pathname === '/api/login/auth-json') {
          const body = request.postDataJSON(); posts.push(body);
          assert.equal(body.action, 'preview', 'visual capture must never request raw credentials');
          return json({ content: masked, revision: 'synthetic-saved-id' });
        }
        if (url.pathname.startsWith('/api/')) return json({});
        const file = path.join(assets, url.pathname === '/' ? 'index.html' : url.pathname);
        const contentType = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.png': 'image/png' }[path.extname(file)];
        await route.fulfill({ body: await fs.readFile(file), contentType });
      });

   await page.goto('http://magpie.test/?view=providers');
   await page.locator('.row.provider', { hasText: 'Codex' }).first().click();
   const row = page.locator('.editor .acc', { hasText: user });
   await row.waitFor();
   const dialog = page.locator('.codex-auth-export');
   const open = async () => {
    await row.hover();
    if (await row.locator('.acc-more').count() && await row.locator('.acc-more').getAttribute('aria-expanded') !== 'true') await row.locator('.acc-more').click();
    await row.getByRole('button', { name: 'auth.json', exact: true }).click();
    await page.waitForFunction(() => document.querySelector('.codex-auth-preview')?.textContent.includes('[hidden]'));
    assert.equal(await dialog.locator('pre').textContent(), masked);
   };
   await fs.mkdir(path.join(destination, 'masked'), { recursive: true });
   await fs.mkdir(path.join(destination, 'closed'), { recursive: true });
   await open();
   await dialog.screenshot({ path: path.join(destination, 'masked', `${engine}-${lang}.png`), animations: 'disabled' });
   const close = await page.evaluate(() => t('Close'));
   await dialog.getByRole('button', { name: close, exact: true }).click();
   await dialog.waitFor({ state: 'detached' });
   await row.waitFor();
   assert.equal(await page.locator('.codex-auth-preview').count(), 0);
   await page.screenshot({ path: path.join(destination, 'closed', `${engine}-${lang}-close.png`), animations: 'disabled' });
   await open();
   await page.keyboard.press('Escape');
   await dialog.waitFor({ state: 'detached' });
   await row.waitFor();
   assert.equal(await page.locator('.codex-auth-preview').count(), 0);
   await page.screenshot({ path: path.join(destination, 'closed', `${engine}-${lang}-escape.png`), animations: 'disabled' });
   assert.equal(posts.length, 2);
   assert.ok(posts.every(p => p.action === 'preview'));
   assert.deepEqual(errors, []);
  });
 }
}
