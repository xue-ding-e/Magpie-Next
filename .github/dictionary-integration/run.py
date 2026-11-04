"""Bounded synthetic candidate checks, using the reviewed owned-child supervisor."""
import importlib.util
import json
import os
from pathlib import Path
import re
import sys

CONTROL = Path(__file__).resolve().parent
ROOT = CONTROL.parents[1]
spec = importlib.util.spec_from_file_location('dictionary_owned', CONTROL / 'owned_process.py')
owned = importlib.util.module_from_spec(spec)
spec.loader.exec_module(owned)
phase = sys.argv[1]
reports = ROOT / '.validation-runtime/reports'
reports.mkdir(parents=True, exist_ok=True)
if phase == 'browser':
    command = [sys.executable, '-I', '-B', str(CONTROL / 'browser_reaper.py')]
    limit = 300
elif phase == 'core':
    command = ['go', 'test', '-mod=readonly', '-tags=nogui', '-race', '-p=1', '-parallel=2', '-count=1', '-timeout=5m', '-json', '-run=Dictionary|Import|HardlinkUpdate|FailedFirstWrite|FailedReplace|CompactionReplayPreservesNestedNumbers', './internal/redact', './internal/localdictionary', './internal/backup', './internal/gateway', './internal/gui']
    limit = 600
else:
    raise SystemExit('unknown validation phase')
log = reports / (phase + '.log')
result = owned.run_owned(command, cwd=ROOT, environment=dict(os.environ), log_path=log, timeout=limit)
with log.open() as f:
    text = f.read((8 << 20) + 1)
if len(text) > 8 << 20:
    raise SystemExit('synthetic log exceeds cap')
print(text)
print(json.dumps({'phase': phase, 'owned': result}, sort_keys=True))
if result != {'returncode': 0, 'timed_out': False, 'orphaned': False, 'clean': True}:
    raise SystemExit('process did not complete cleanly')
if phase == 'browser':
    counts = {}
    for key in ('tests', 'pass', 'fail', 'cancelled', 'skipped', 'todo'):
        values = re.findall(r'^# ' + key + r' (\d+)\s*$', text, re.M)
        if len(values) != 1: raise SystemExit('ambiguous browser result')
        counts[key] = int(values[0])
    if counts['tests'] != 10 or counts['pass'] != 10 or any(counts[k] for k in ('fail', 'cancelled', 'skipped', 'todo')):
        raise SystemExit('all ten engine/language cases must pass without skips')
else:
    records = [json.loads(line) for line in text.splitlines() if line.startswith('{')]
    packages = {'github.com/yetone/magpie/internal/' + p for p in ('redact', 'localdictionary', 'backup', 'gateway', 'gui')}
    passed = {r['Package'] for r in records if r.get('Test') and r.get('Action') == 'pass'}
    if passed != packages or any(r.get('Action') in ('fail', 'skip') for r in records):
        raise SystemExit('every selected package needs real passing tests and no skips')
print('PASS scoped synthetic ' + phase + ' checks; not release or native GUI acceptance')
