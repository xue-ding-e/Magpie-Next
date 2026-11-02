const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
const report = path.resolve('.validation-runtime/reports/gui-full');
fs.mkdirSync(report, { recursive: true });
const inventory = fs.readdirSync('internal/gui/tests').filter(f => f.endsWith('.test.cjs')).sort().map(f => 'internal/gui/tests/' + f);
if (inventory.length !== 827) throw new Error('Full candidate GUI inventory drift');
fs.writeFileSync(path.join(report, 'inventory.json'), JSON.stringify(inventory, null, 2));
const env = { ...process.env, AUTH_FULL_REPORTS: report, NODE_OPTIONS: `--require=${path.resolve(__dirname, 'full-offline.cjs')} --test-reporter=tap` };
delete env.BROWSER;
const start = Date.now();
const out = fs.createWriteStream(path.join(report, 'full.tap'));
const child = spawn('make', ['test-ui', 'UI_TEST_CONCURRENCY=1'], { env, stdio: ['ignore', 'pipe', 'pipe'] });
const summaries = {}, failedFiles = new Set();
let pending = '';
child.stdout.on('data', b => {
 out.write(b); pending += b.toString();
 const lines = pending.split('\n'); pending = lines.pop();
 for (const line of lines) {
  const m = line.match(/^# (tests|pass|fail|cancelled|skipped|todo) (\d+)\s*$/);
  if (m) { (summaries[m[1]] ||= []).push(Number(m[2])); process.stdout.write(line + '\n'); }
  if (/^not ok/.test(line)) process.stdout.write(line + '\n');
  const location = line.match(/location: .*?(internal\/gui\/tests\/[^:'"]+\.test\.cjs)/);
  if (location) failedFiles.add(location[1]);
 }
});
child.stderr.on('data', b => { out.write(b); process.stderr.write(b); });
child.on('error', e => { process.stderr.write(e.message + '\n'); process.exitCode = 1; });
child.on('close', (code, signal) => {
 out.end();
 const loaded = new Set(fs.existsSync(path.join(report, 'loaded')) ? fs.readdirSync(path.join(report, 'loaded')).map(f => JSON.parse(fs.readFileSync(path.join(report, 'loaded', f))).file) : []);
 const counts = Object.fromEntries(['tests','pass','fail','cancelled','skipped','todo'].map(k => [k, summaries[k]?.length === 1 ? summaries[k][0] : -1]));
 const ok = code === 0 && !signal && counts.tests > 0 && counts.pass === counts.tests && ['fail','cancelled','skipped','todo'].every(k => counts[k] === 0) && loaded.size === inventory.length && inventory.every(f => loaded.has(f));
 fs.writeFileSync(path.join(report, 'summary.json'), JSON.stringify({ code, signal, elapsed_seconds: (Date.now()-start)/1000, counts, expected_files:827, loaded_files:loaded.size, failed_files:[...failedFiles], ok }, null, 2));
 if (!ok) process.exitCode = 1;
});
