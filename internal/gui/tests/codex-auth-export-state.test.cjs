// Synthetic controller tests; no account files, browser, clipboard or network.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, '../assets/app.js'), 'utf8');
const editorSource = fs.readFileSync(path.join(__dirname, '../assets/codex-auth-editor.js'), 'utf8');
const start = editorSource.indexOf('function showCodexAuth(user) {');
assert.notEqual(start, -1, 'Codex saved-auth entry must exist');
const code = editorSource.slice(start);
const flush = async () => { for (let n = 0; n < 8; n++) await Promise.resolve(); };
function harness(web = false) {
  class Element {
    constructor(tag, cls, text = '') { this.tag = tag; this.cls = cls; this.textContent = text; this.children = []; this.style = {}; this.isConnected = true; this.disabled = false; this.classList = { contains: c => this.cls.split(' ').includes(c), add: c => { this.cls += ' ' + c; } }; }
    append(...children) { this.children.push(...children); }
    setAttribute() {}
    click() { if (this.tag === 'a') downloads.push(this.download); else if (!this.disabled) return this.onclick?.(); }
  }
  const requests = [], downloads = [], clipboard = [], listeners = new Map();
  let editor;
  const modal = new Element('div', ''); modal.hidden = false;
  const context = {
    console: { log: () => assert.fail('must not log credentials'), error: () => assert.fail('must not log credentials') },
    el: (...a) => new Element(...a), t: (s, v) => v ? s.replace('{path}', v.path) : s,
    $: () => modal, web, AbortController, Blob, setTimeout: callback => callback(),
    URL: { createObjectURL: () => 'blob:synthetic', revokeObjectURL: () => {} },
    navigator: { clipboard: { writeText: async text => { clipboard.push(text); } } },
    document: { hidden: false, addEventListener: (n, f) => listeners.set(n, f), removeEventListener: n => listeners.delete(n) },
    fetch: (url, options) => new Promise((resolve, reject) => requests.push({ url, ...options, data: JSON.parse(options.body), resolve, reject })),
    closeCodexAuth: null, confirmAsk: null, restored: 0,
    renderProviders: () => { context.restored++; },
    openModal: ed => { context.closeCodexAuth?.(); if (editor) editor.isConnected = false; editor = ed; modal.hidden = false; },
    closeConfirmAsk: () => { const back = context.confirmAsk?.back; context.confirmAsk = null; context.closeCodexAuth?.(); back?.(); modal.hidden = true; },
  };
  vm.createContext(context); vm.runInContext(code, context);
  const button = label => editor.children.at(-1).children.find(e => e.textContent === label);
  const preview = () => editor.children.find(e => e.tag === 'pre').textContent;
  const note = () => editor.children.find(e => e.tag === 'p' && e !== editor.children[1]).textContent;
  const answer = (i, content = '{"tokens":"[hidden]"}', id = 'saved-1') => requests[i].resolve({ ok: true, json: async () => ({ content, revision: id }) });
  return { context, requests, downloads, clipboard, button, preview, note, answer, show: user => context.showCodexAuth(user), hideWindow: () => { context.document.hidden = true; listeners.get('visibilitychange')?.(); } };
}

test('opens masked only; raw JSON is displayed as text on explicit reveal', async () => {
  const h = harness(); h.show('synthetic@example.invalid');
  assert.deepEqual(h.requests[0].data, { user: 'synthetic@example.invalid', revision: '', action: 'preview' });
  h.answer(0); await flush();
  assert.match(h.preview(), /hidden/); assert.equal(h.clipboard.length, 0); assert.equal(h.downloads.length, 0);
  h.button('Reveal credentials').click();
  assert.equal(h.requests[1].data.revision, 'saved-1');
  const raw = '{"tokens":{"access_token":"<script>synthetic</script>"}}';
  h.answer(1, raw); await flush(); assert.equal(h.preview(), raw);
  h.button('Hide credentials').click(); assert.equal(h.preview(), '');
  h.answer(2); await flush(); assert.match(h.preview(), /hidden/);
});

test('close and account replacement discard late raw replies before copy', async () => {
  for (const replacement of [false, true]) {
    const h = harness(); h.show('one'); h.answer(0); await flush();
    h.button('Copy auth.json').click();
    if (replacement) h.show('two'); else h.button('Close').click();
    assert.equal(h.requests[1].signal.aborted, true);
    if (!replacement) assert.equal(h.context.restored, 1, "Close returns to the provider editor");
    h.answer(1, 'SYNTHETIC-SECRET'); await flush();
    assert.equal(h.requests.filter(r => r.url === '/api/copy').length, 0);
    assert.equal(h.preview(), '');
  }
});

test('hide and window hide invalidate a pending reveal', async () => {
  for (const windowHidden of [false, true]) {
    const h = harness(); h.show('one'); h.answer(0); await flush();
    h.button('Reveal credentials').click();
    if (windowHidden) h.hideWindow(); else h.button('Hide credentials').click();
    h.answer(1, 'SYNTHETIC-SECRET'); await flush(); assert.equal(h.preview(), '');
  }
});

test('repeated actions are serialized and failures never echo sensitive errors', async () => {
  const h = harness(); h.show('one'); h.answer(0); await flush();
  h.button('Reveal credentials').click(); h.button('Reveal credentials').click();
  assert.equal(h.requests.length, 2);
  h.requests[1].reject(new Error('SYNTHETIC-SECRET')); await flush();
  assert.equal(h.preview(), ''); assert.equal(h.note(), 'Could not prepare this saved Codex sign-in');
  h.button('Copy auth.json').click(); h.answer(2, 'SYNTHETIC-SECRET'); await flush();
  assert.equal(h.requests[3].url, '/api/copy');
  h.requests[3].resolve({ ok: false }); await flush();
  assert.equal(h.preview(), ''); assert.equal(h.note(), 'Could not prepare this saved Codex sign-in');
});

test('changed account identity refuses reveal and clipboard access', async () => {
  const h = harness(); h.show('one'); h.answer(0); await flush();
  h.button('Copy auth.json').click(); h.answer(1, 'SYNTHETIC-SECRET', 'replaced-account'); await flush();
  assert.equal(h.requests.length, 2); assert.equal(h.preview(), ''); assert.match(h.note(), /Could not/);
});

test('browser export is explicit and late export does not download', async () => {
  for (const cancel of [false, true]) {
    const h = harness(true); h.show('one'); h.answer(0); await flush();
    assert.equal(h.downloads.length, 0);
    h.button('Export auth.json').click(); assert.equal(h.requests[1].data.action, 'export');
    if (cancel) h.button('Close').click();
    h.answer(1, '{"tokens":{"access_token":"synthetic"}}'); await flush();
    assert.equal(h.downloads.length, cancel ? 0 : 1);
    assert.equal(h.preview(), '');
  }
});

test('browser clipboard rejection is generic, not a raw-text fallback', async () => {
  const h = harness(true); h.context.navigator.clipboard.writeText = async () => { throw new Error('SYNTHETIC-SECRET'); };
  h.show('one'); h.answer(0); await flush(); h.button('Copy auth.json').click();
  h.answer(1, 'SYNTHETIC-SECRET'); await flush();
  assert.equal(h.note(), 'Could not prepare this saved Codex sign-in'); assert.equal(h.preview(), '');
});

test('all modal close/replace paths invalidate the controller', () => {
  assert.match(source, /function openModal\(content(?:, quiet)?\) \{\n  closeCodexAuth\?\.\(\);/);
  assert.match(source, /function closeModal\(\) \{\n  closeCodexAuth\?\.\(\);/);
});


test('direct modal replacement clears the old confirmation owner', async () => {
  const h = harness(); h.show('one'); h.answer(0); await flush();
  h.context.openModal(h.context.el('div', 'unrelated-editor'));
  assert.equal(h.context.confirmAsk, null);
  h.context.closeConfirmAsk();
  assert.equal(h.context.restored, 0, 'unrelated modal must not return to old account');
});
