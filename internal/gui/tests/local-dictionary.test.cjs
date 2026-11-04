// Real page + synthetic API. No credentials, live gateway or persistent browser profile.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { test } = require('node:test');
const { chromium, webkit } = require('playwright');

const assets = process.env.MAGPIE_DICTIONARY_ASSETS || path.resolve(__dirname, '../assets');
const endpoint = '/api/settings/local-dictionary';
const engines = process.env.BROWSER ? [process.env.BROWSER] : ['chromium', 'webkit'];
const languages = ['en', 'zh', 'zh-TW', 'ja', 'de'];
const captions = ['Manage local dictionary', 'Save', 'Cancel', 'Reveal entries', 'Hide entries',
  'Previous', 'Next', 'Reload', 'Discard', 'Could not read UTF-8 file; no changes saved'];

async function fixture(t, engine, lang) {
  const browser = await (engine === 'webkit' ? webkit.launch() : chromium.launch({ channel: 'chromium' }));
  t.after(() => browser.close());
  const theme = lang === 'de' ? 'dark' : 'light';
  const page = await browser.newPage({ viewport: { width: 640, height: 1000 }, reducedMotion: 'reduce' });
  page.setDefaultTimeout(5000);
  const state = { revision: 'new', words: [], posts: [], reads: [], externalWrites: 0 };
  const errors = [], externalRequests = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await page.route('**/*', async (route) => {
    const request = route.request(), url = new URL(request.url()), p = url.pathname;
    const json = (body) => route.fulfill({ json: body });
    if (url.origin !== 'http://magpie.test') {
      externalRequests.push(request.url());
      return route.abort();
    }
    if (p === '/boot.js') return route.fulfill({ contentType: 'text/javascript',
      body: `window.bootPrefs=${JSON.stringify({ lang, theme, web: true })};` });
    if (p === '/wails/runtime.js') return route.fulfill({ contentType: 'text/javascript', body: 'export const Window={};' });
    if (p === endpoint) {
      if (request.method() === 'POST') {
        const body = request.postDataJSON();
        state.posts.push(body);
        // Conflict comes from a real changed revision, never a forced-error flag.
        if (body.revision !== state.revision) return route.fulfill({ status: 409,
          json: { error: 'dictionary changed; reload before saving' } });
        let incoming;
        try {
          incoming = body.format === 'json' ? JSON.parse(body.content)
            : body.content.replace(/^\uFEFF/, '').replace(/\r\n/g, '\n').split('\n');
          if (!Array.isArray(incoming) || incoming.some((word) => typeof word !== 'string')) throw new Error('invalid fixture input');
        } catch {
          return route.fulfill({ status: 400, json: { error: 'import must be a JSON string array' } });
        }
        state.words = [...new Set((body.mode === 'append' ? state.words : []).concat(incoming).filter((word) => word !== ''))];
        state.revision = 'saved-' + state.posts.length;
      } else {
        state.reads.push({ reveal: url.searchParams.get('reveal') === '1', offset: Number(url.searchParams.get('offset') || 0) });
      }
      const out = { revision: state.revision, count: state.words.length,
        bytes: state.words.reduce((total, word) => total + Buffer.byteLength(word), 0) };
      if (url.searchParams.get('reveal') === '1') {
        const offset = Math.max(0, Number(url.searchParams.get('offset') || 0));
        out.words = state.words.slice(offset, offset + 100);
      }
      return json(out);
    }
    if (p === '/api/settings') return json({ theme, lang, redactWords: [] });
    if (p === '/api/state') return json({ agents: [], profiles: [], settings: { theme, lang } });
    if (p === '/api/providers') return json({ providers: [], presets: [], excluded: [],
      gateway: { running: true, window: true, url: 'http://127.0.0.1:3999' } });
    if (p === '/api/groups') return json({ models: [], groups: [], pools: [] });
    if (p.startsWith('/api/')) return json({});
    const file = path.join(assets, p === '/' ? 'index.html' : p);
    const contentType = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css',
      '.svg': 'image/svg+xml', '.png': 'image/png' }[path.extname(file)];
    try { return await route.fulfill({ body: await fs.readFile(file), contentType }); }
    catch (error) {
      if (error.code !== 'ENOENT') throw error;
      return route.fulfill({ status: 404, body: '' });
    }
  });
  await page.goto('http://magpie.test/?view=settings&tab=privacy');
  const translated = await page.evaluate((labels) => Object.fromEntries(labels.map((label) => [label, t(label)])), captions);
  const dialog = page.locator('dialog.local-dictionary');
  const button = (label) => dialog.getByRole('button', { name: translated[label], exact: true });
  const idle = () => page.waitForFunction(() => {
    const d = document.querySelector('dialog.local-dictionary');
    return d && !d.querySelector('.dictionary-buttons .primary').disabled;
  });
  const response = (method) => page.waitForResponse((res) => new URL(res.url()).pathname === endpoint && res.request().method() === method);
  const open = async () => {
    await Promise.all([response('GET'), page.getByRole('button', { name: translated['Manage local dictionary'], exact: true }).click()]);
    await idle();
  };
  const read = async (label) => {
    await Promise.all([response('GET'), button(label).click()]);
    await idle();
  };
  const save = async (status = 200) => {
    const [res] = await Promise.all([response('POST'), button('Save').click()]);
    assert.equal(res.status(), status);
    await idle();
  };
  const choose = (group, value) => dialog.locator('fieldset').nth(group).locator(`[role="radio"][data-value="${value}"]`).click();
  const externalUpdate = (words) => {
    state.words = [...words];
    state.revision = 'external-' + ++state.externalWrites;
    return state.revision;
  };
  return { page, dialog, button, idle, response, open, read, save, choose, externalUpdate,
    translated, state, errors, externalRequests };
}

for (const engine of engines) {
  assert(['chromium', 'webkit'].includes(engine), 'unsupported browser engine');
  for (const lang of languages) {
    test(`${engine} ${lang}: local dictionary CAS, explicit reload, imports and pagination`, async (t) => {
      const f = await fixture(t, engine, lang);
      const { page, dialog, button, idle, response, open, read, save, choose,
        externalUpdate, translated, state, errors, externalRequests } = f;
      const input = dialog.locator('textarea'), file = dialog.locator('input[type="file"]');
      const preview = dialog.locator('pre'), alert = dialog.getByRole('alert');

      await open();
      assert.equal(state.reads.at(-1).reveal, false, 'opening reads metadata only');
      await input.fill('unsaved synthetic');
      await button('Cancel').click();
      await dialog.waitFor({ state: 'detached' });
      assert.equal(state.posts.length, 0, 'Cancel never saves');
      await open();
      assert.equal(await input.inputValue(), '', 'closed drafts do not return');
      await input.fill('synthetic,password\n保密词');
      await save();
      assert.deepEqual(state.words, ['synthetic,password', '保密词']);
      assert.equal(await input.inputValue(), '');
      assert.equal(await preview.isVisible(), false);
      await read('Reveal entries');
      assert.match(await preview.textContent(), /synthetic,password/);
      const beforeHide = state.reads.length;
      await button('Hide entries').click();
      assert.equal(await preview.isVisible(), false);
      assert.equal(await preview.textContent(), '');
      assert.equal(state.reads.length, beforeHide, 'Hide clears locally without another secret read');

      // Reading the changed library must not bless an existing replace draft.
      const draftRevision = state.revision, draft = 'old synthetic replace draft';
      await choose(0, 'replace');
      await input.fill(draft);
      const externalWords = Array.from({ length: 201 }, (_, i) => `external-synthetic-${String(i).padStart(3, '0')}`);
      const changedRevision = externalUpdate(externalWords);
      await read('Reveal entries');
      assert.match(await preview.textContent(), /external-synthetic-000/);
      await read('Next');
      assert.equal(state.reads.at(-1).offset, 100);
      await read('Previous');
      assert.equal(state.reads.at(-1).offset, 0);
      await save(409);
      assert.equal(state.posts.at(-1).revision, draftRevision, 'Reveal and paging retain the original save revision');
      assert.equal(state.posts.at(-1).mode, 'replace');
      assert.equal(state.posts.at(-1).content, draft);
      assert.equal(await input.inputValue(), draft, '409 retains the replace draft');
      assert.deepEqual(state.words, externalWords, 'external generation was not overwritten');
      await alert.filter({ hasText: 'dictionary changed' }).waitFor();

      // Cancelling the real confirmation changes neither draft nor CAS base.
      const readsBeforeCancel = state.reads.length;
      await button('Reload').click();
      const ask = page.locator('dialog.action-confirm');
      await ask.waitFor();
      await ask.getByRole('button', { name: translated.Cancel, exact: true }).click();
      await ask.waitFor({ state: 'detached' });
      await idle();
      assert.equal(await input.inputValue(), draft);
      assert.equal(state.reads.length, readsBeforeCancel, 'cancelled Reload does not fetch or rebase');
      await save(409);
      assert.equal(state.posts.at(-1).revision, draftRevision, 'cancelled Reload preserves stale CAS protection');
      assert.deepEqual(state.words, externalWords);

      // Only accepting Reload discards the draft and establishes the new base.
      await button('Reload').click();
      await ask.waitFor();
      await Promise.all([response('GET'), ask.getByRole('button', { name: translated.Discard, exact: true }).click()]);
      await ask.waitFor({ state: 'detached' });
      await idle();
      assert.equal(await input.inputValue(), '');
      assert.equal(await file.inputValue(), '');
      const jsonWords = [' padded,synthetic ', '秘密🔑', 'line\nbreak'];
      await choose(1, 'json');
      await input.fill(JSON.stringify(jsonWords));
      await save();
      assert.equal(state.posts.at(-1).revision, changedRevision, 'accepted Reload adopts the external revision');
      assert.equal(state.posts.at(-1).format, 'json');
      assert.deepEqual(state.words, jsonWords, 'JSON replace preserves literal whitespace and escaped newline');
      await choose(1, 'text');
      await input.fill('');
      await save();
      assert.deepEqual(state.words, [], 'explicit empty replacement clears the library');

      // The browser decodes files strictly, and selecting any file never posts.
      await input.fill('keep this synthetic draft');
      const writesBeforeFile = state.posts.length;
      await file.setInputFiles({ name: 'invalid.txt', mimeType: 'text/plain', buffer: Buffer.from([0xc3, 0x28]) });
      await alert.filter({ hasText: translated['Could not read UTF-8 file; no changes saved'] }).waitFor();
      await idle();
      assert.equal(await input.inputValue(), 'keep this synthetic draft');
      assert.equal(state.posts.length, writesBeforeFile, 'invalid UTF-8 cannot auto-save');
      const fileWords = ['file,synthetic', '保密文件', ' padded file '], fileText = JSON.stringify(fileWords);
      await file.setInputFiles({ name: 'synthetic.json', mimeType: 'application/json', buffer: Buffer.from(fileText) });
      await page.waitForFunction((text) => document.querySelector('dialog.local-dictionary textarea')?.value === text, fileText);
      await idle();
      assert.equal(await dialog.locator('fieldset').nth(1).locator('[data-value="json"]').getAttribute('aria-checked'), 'true');
      assert.equal(state.posts.length, writesBeforeFile, 'valid file selection also waits for Save');
      await save();
      assert.deepEqual(state.words, fileWords);
      assert.equal(await file.inputValue(), '', 'successful save drops the file selection');

      // More than one page is inspected without changing the dictionary.
      externalUpdate(externalWords);
      await read('Reload'); // no draft/file: no discard confirmation is needed
      await read('Reveal entries');
      assert.equal((await preview.textContent()).split('\n').length, 100);
      assert.equal(await button('Previous').isDisabled(), true);
      assert.equal(await button('Next').isDisabled(), false);
      await read('Next');
      assert.equal(state.reads.at(-1).offset, 100);
      assert.equal((await preview.textContent()).split('\n').length, 100);
      assert.match(await preview.textContent(), /^101: "external-synthetic-100"/);
      await read('Next');
      assert.equal(state.reads.at(-1).offset, 200);
      assert.equal(await preview.textContent(), '201: "external-synthetic-200"');
      assert.equal(await button('Next').isDisabled(), true);
      await read('Previous');
      assert.equal(state.reads.at(-1).offset, 100);
      await button('Hide entries').click();
      assert.equal(await preview.textContent(), '');

      await page.setViewportSize({ width: 360, height: 760 });
      await page.waitForFunction(() => window.innerWidth === 360);
      await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      assert.equal(await dialog.evaluate((d) => d.scrollWidth <= d.clientWidth), true, 'narrow translated dialog must not overflow');
      await input.fill('drag synthetic draft');
      await input.scrollIntoViewIfNeeded();
      const box = await input.boundingBox();
      await page.mouse.move(box.x + 12, box.y + 12);
      await page.mouse.down();
      await page.mouse.move(2, 2);
      await page.mouse.up();
      assert.equal(await dialog.isVisible(), true, 'dragging from the editor into the backdrop must not close it');
      assert.equal(await input.inputValue(), 'drag synthetic draft');
      const writesBeforeDismiss = state.posts.length;
      await page.mouse.click(2, 2);
      await dialog.waitFor({ state: 'detached' });
      assert.equal(state.posts.length, writesBeforeDismiss, 'backdrop dismissal never saves');
      await open();
      await page.keyboard.press('Escape');
      await dialog.waitFor({ state: 'detached' });
      const storage = await page.evaluate(() => JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }));
      assert.equal(storage.includes('synthetic'), false, 'dictionary drafts/entries never enter browser persistence');
      assert.deepEqual(errors, []);
      assert.deepEqual(externalRequests, [], 'the synthetic page must never contact external services');
    });
  }
}
