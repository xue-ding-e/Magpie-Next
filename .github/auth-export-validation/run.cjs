const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const dir = path.resolve('.validation-runtime/reports');
fs.mkdirSync(dir, { recursive: true });
const tests = ['codex-auth-export', 'modal-morph', 'account-return', 'codex-signout']
  .map(name => `internal/gui/tests/${name}.test.cjs`);
let failed = false;
for (const engine of ['chromium', 'webkit']) {
  const result = spawnSync(process.execPath, ['--require', path.resolve(__dirname, 'offline.cjs'),
    '--test', '--test-concurrency=1', '--test-reporter=tap', ...tests], {
    env: { ...process.env, BROWSER: engine, AUTH_VALIDATION_ARTIFACTS: path.join(dir, 'screenshots') },
    encoding: 'utf8', timeout: 200000, maxBuffer: 16 * 1024 * 1024,
  });
  const log = (result.stdout || '') + (result.stderr || '');
  fs.writeFileSync(path.join(dir, `${engine}.tap`), log);
  process.stdout.write(log);
  const count = name => { const all = [...log.matchAll(new RegExp(`^# ${name} (\\d+)\\s*$`, 'gm'))]; return all.length === 1 ? Number(all[0][1]) : -1; };
  const counts = Object.fromEntries(['tests', 'pass', 'fail', 'cancelled', 'skipped', 'todo'].map(k => [k, count(k)]));
  const ok = result.status === 0 && !result.error && counts.tests === 18 && counts.pass === 18 &&
    ['fail', 'cancelled', 'skipped', 'todo'].every(k => counts[k] === 0);
  fs.writeFileSync(path.join(dir, `${engine}.json`), JSON.stringify({ engine, exit: result.status, signal: result.signal, counts, ok }, null, 2));
  if (!ok) failed = true;
}
if (failed) process.exitCode = 1;
