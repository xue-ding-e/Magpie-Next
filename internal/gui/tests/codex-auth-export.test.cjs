// Browser integration: invented saved credentials and a fully intercepted API.
// Run in Chromium and WebKit; never start magpie or read account files.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { test } = require('node:test');
const { chromium, webkit } = require('playwright');
const assets = path.resolve(__dirname, '../assets');
const user = 'synthetic@example.invalid';
const secret = 'synthetic-access-token';
const raw = JSON.stringify({ auth_mode: 'chatgpt', tokens: { access_token: secret, refresh_token: 'synthetic-refresh', id_token: 'synthetic-id', account_id: 'synthetic-account' } }, null, 2);
const masked = JSON.stringify({ auth_mode: '[hidden]', tokens: { access_token: '[hidden]', refresh_token: '[hidden]', id_token: '[hidden]', account_id: '[hidden]' } }, null, 2);

for (const engine of (process.env.BROWSER ? [process.env.BROWSER] : ['chromium', 'webkit'])) {
  for (const lang of ['en', 'zh', 'zh-TW', 'ja', 'de']) {
    test(`${engine} ${lang}: saved auth is masked, revealed explicitly and cleared on dismissal`, async t => {
      const browser = await (engine === 'webkit' ? webkit.launch() : chromium.launch({ channel: 'chromium' }));
      t.after(() => browser.close());
      const page = await (await browser.newContext({ viewport: { width: 1000, height: 800 }, reducedMotion: 'reduce' })).newPage();
      page.setDefaultTimeout(5000);
      const posts = [], errors = [];
      let hold = false, release, announce;
      t.after(() => release?.());
      page.on('pageerror', e => errors.push(e.message));
      await page.route('**/*', async route => {
        const request = route.request(), url = new URL(request.url());
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
          if (body.action === 'preview') return json({ content: masked, revision: 'synthetic-saved-id' });
          assert.equal(body.revision, 'synthetic-saved-id');
          if (hold) await new Promise(resolve => { release = resolve; announce(); });
          return json({ content: raw, revision: 'synthetic-saved-id' }).catch(() => {}); // a closed dialog aborts this request
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
      const open = async () => {
        await row.hover();
        if (await row.locator('.acc-more').count()) {
          if (await row.locator('.acc-more').getAttribute('aria-expanded') !== 'true') await row.locator('.acc-more').click();
        }
        await row.getByRole('button', { name: 'auth.json', exact: true }).click();
        await page.waitForFunction(() => document.querySelector('.codex-auth-preview')?.textContent.includes('[hidden]'));
      };
      const label = key => page.evaluate(key => t(key), key);
      const dialog = page.locator('.codex-auth-export');
      await open();
      assert.equal(posts.length, 1);
      assert.equal(posts[0].action, 'preview');
      assert.equal((await page.locator('body').textContent()).includes(secret), false);
      await dialog.getByRole('button', { name: await label('Reveal credentials'), exact: true }).click();
      await page.waitForFunction(secret => document.querySelector('.codex-auth-preview')?.textContent.includes(secret), secret);
      assert.equal(await dialog.locator('pre').textContent(), raw);
      await dialog.getByRole('button', { name: await label('Hide credentials'), exact: true }).click();
      await page.waitForFunction(() => document.querySelector('.codex-auth-preview')?.textContent.includes('[hidden]'));
      assert.equal((await page.locator('body').textContent()).includes(secret), false);
      hold = true;
      const held = new Promise(resolve => { announce = resolve; });
      await dialog.getByRole('button', { name: await label('Reveal credentials'), exact: true }).click();
      await held;
      await page.keyboard.press('Escape');
      await dialog.waitFor({ state: 'detached' });
      await row.waitFor(); // cancellation returns to the provider editor
      release?.();
      assert.equal((await page.locator('body').textContent()).includes(secret), false);
      assert.deepEqual(errors, []);
    });
  }
}
