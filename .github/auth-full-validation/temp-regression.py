# CI-only regression for the original unchanged workdir fixture.
import json
import os
from pathlib import Path
import subprocess
import tempfile

ROOT = Path.cwd().resolve()
NAME = 'TestPluginToldWhereTheAgentWorks'
REPORT = ROOT / '.validation-runtime/reports/temp-regression'
REPORT.mkdir(parents=True, exist_ok=True)
assert (ROOT / '.git').exists()
new_tmp = Path(os.environ['TMPDIR']).resolve(strict=True)
assert not any((p / '.git').exists() for p in (new_tmp, *new_tmp.parents))
command = ['go', 'test', '-mod=readonly', '-p', '2', '-tags', 'nogui', '-count=1', '-timeout', '2m', '-json', './internal/gateway', '-run', '^' + NAME + '$']

def run(label, directory):
    result = subprocess.run(command, env=dict(os.environ, TMPDIR=str(directory)), stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=420)
    (REPORT / (label + '.jsonl')).write_text(result.stdout)
    events = [json.loads(line) for line in result.stdout.splitlines() if line.startswith('{')]
    assert not any(e.get('Action') == 'skip' for e in events), 'Skipped regression is not evidence'
    return result.returncode, events

with tempfile.TemporaryDirectory(prefix='old-worktree-tmp.', dir=ROOT / '.validation-runtime') as old_tmp:
    code, events = run('old-red', old_tmp)
    failures = [e.get('Test') for e in events if e.get('Action') == 'fail' and e.get('Test')]
    output = ''.join(e.get('Output', '') for e in events if e.get('Test') == NAME)
    assert code == 1 and failures == [NAME], 'Old environment must fail only the exact original test'
    assertions = [line for line in output.splitlines() if 'plugin_workdir_test.go:113:' in line]
    assert len(assertions) == 1 and 'not in one:' in assertions[0] and 'git "yes"; want ' in assertions[0] and assertions[0].endswith(', "no"'), 'Old failure must be the original Git ancestor assertion, not compilation or timeout'
code, events = run('new-green', new_tmp)
passed = [e.get('Test') for e in events if e.get('Action') == 'pass' and e.get('Test')]
assert code == 0 and passed == [NAME] and not any(e.get('Action') == 'fail' for e in events), 'External temporary directory must pass the exact original test'
(REPORT / 'summary.json').write_text(json.dumps({'old_expected_failure': NAME, 'new_pass': NAME, 'product_source': '7feed2b7e7a94ad0263ad3f44a1b0ab3b2fc065c'}, indent=2))
print('Original unchanged gateway fixture: exact Git-ancestor assertion RED; external temporary directory GREEN')
