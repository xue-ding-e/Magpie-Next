"""Lightweight controller checks with synthetic Python children; no GUI/Go work."""
import copy
import hashlib
import importlib.util
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

CONTROL = Path(__file__).resolve().parent


def module(name):
    spec = importlib.util.spec_from_file_location(name, CONTROL / (name + '.py'))
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


def reject(fn):
    try:
        fn()
    except (AssertionError, ValueError, KeyError):
        return
    raise AssertionError('Expected fail-closed rejection')


def tap(passed=3, failed=0, skipped=0, cancelled=0, todo=0):
    return '\n'.join(f'# {k} {v}' for k, v in dict(tests=3, **{'pass': passed, 'fail': failed},
                                                   skipped=skipped, cancelled=cancelled, todo=todo).items())


runner = module('run')
migration = module('migration')
budget = module('budget')
manifest = json.loads((CONTROL / 'inventory.json').read_text())
assert runner.counts(tap(), 0)['passed']
assert runner.counts(tap(2, 1), 1)['terminal_summary'] and not runner.counts(tap(2, 1), 1)['passed']
for text, code in [('', 0), (tap(), 1), (tap(2, 1), 0), (tap(2, 0, 1), 0),
                   (tap(2, 0, 0, 1), 1), (tap(2, 0, 0, 0, 1), 0),
                   (tap() + '\n# tests 3', 0), (tap(), -9), (tap(4), 0), (tap(0), 0)]:
    assert not runner.counts(text, code)['passed']
print('PASS: 12 terminal classifier checks')

receipt = migration.verify_trees()
assert receipt == json.loads((CONTROL / 'source-migration.json').read_text())
old = migration.read('old-source-tree.json')
mutated = copy.deepcopy(old['leaves'])
mutated.pop(next(iter(mutated)))
assert migration.tree_sha(mutated) != old['official_tree']
records = migration.seed_records(manifest, 'v22.23.4')
assert len(records) == 827 and all(v['state'] == 'not_run' and not v['executed_in_batch'] for v in records.values())
assert sum(v['old_source_provenance']['state'] != 'not_run' for v in records.values()) == 24
assert sum(v['old_source_provenance']['state'] == 'failed' for v in records.values()) == 4
assert all(v['old_source_provenance']['accepted_on_current_source'] is False for v in records.values())
for field, value in [('product', migration.OLD_SOURCE), ('selected', manifest['selected'][:-1]),
                     ('selected', [manifest['selected'][0]] * 32)]:
    invalid = copy.deepcopy(manifest)
    invalid[field] = value
    reject(lambda: migration.seed_records(invalid, 'v22.23.4'))
reject(lambda: migration.seed_records(manifest, 'v22.0.0'))
print('PASS: reconstructed full trees, omitted-leaf rejection, 827 reset/current + 24 archived provenance, bad source/selection/runtime rejected')

assert budget.known_base() == (1013.758023034, 1300)
actual_budget = budget.admitted_budget(manifest)
assert actual_budget['spent_test_seconds'] == 1045.758023034
assert actual_budget['spent_job_seconds'] == 1512
assert actual_budget['required_gui_conservative_test_charge'] == 32
original_load = budget.load
mutations = [
    ('required-gui-receipt.json', 'status', 'pending'),
    ('required-gui-receipt.json', 'charged_test_seconds', 30.638664386000016),
    ('required-gui-receipt.json', 'run_id', 41584387550),
    ('required-gui-receipt.json', 'source', migration.OLD_SOURCE),
    ('required-gui-receipt.json', 'head_sha', '0' * 40),
]
for value in [None, -1, float('nan'), True, 28800]:
    mutations.append(('required-gui-receipt.json', 'charged_test_seconds', value))
try:
    for filename, field, value in mutations:
        changed = copy.deepcopy(original_load(filename))
        changed[field] = value
        budget.load = lambda name: changed if name == filename else original_load(name)
        reject(lambda: budget.admitted_budget(manifest))
    budget.load = original_load
    for field, value in [('maximum_test_seconds', 28801), ('known_spent_job_seconds', 0),
                          ('spent_test_seconds', 1013.758023034), ('spent_job_seconds', 1300)]:
        invalid = copy.deepcopy(manifest)
        invalid['flow_budget'][field] = value
        reject(lambda: budget.admitted_budget(invalid))
finally:
    budget.load = original_load
print('PASS: actual hash-bound proof accounting32/212, raw30.638664386 separate; pending/invalid/NaN/reset/budget-extension rejected')

original_command, original_popen = runner.command_for, runner.subprocess.Popen
original_validate_temp = runner.validate_temp
with tempfile.TemporaryDirectory(prefix='m3.', dir='/tmp') as directory:
    base = Path(directory)
    old_temp = os.environ.get('RUNNER_TEMP')
    os.environ['RUNNER_TEMP'] = str(base)
    # The hosted local harness places a read-only .git marker in /tmp. These
    # synthetic Python children never load product/Node code or user config;
    # validate ownership here instead. Production validate_temp is unchanged.
    def synthetic_temp(tmp):
        assert tmp.parent == base and tmp.name.startswith('ma.')
        assert len(str(tmp / 'playwright-webkit-xxxxxxxx/inspector-pipe').encode()) < 108
    runner.validate_temp = synthetic_temp
    spawned, max_live = [], [0]

    def tracked_popen(*args, **kwargs):
        assert kwargs['start_new_session'] is True
        process = original_popen(*args, **kwargs)
        spawned.append(process)
        max_live[0] = max(max_live[0], sum(p.poll() is None for p in spawned))
        assert max_live[0] <= 2
        return process

    runner.subprocess.Popen = tracked_popen

    def execute_case(label, commands, per_file=2, batch=8, expect_error=False):
        folder = base / label
        source, report = folder / 'source', folder / 'reports'
        source.mkdir(parents=True)
        report.mkdir()
        selected = list(commands)
        files = []
        states = {}
        for name in selected:
            (source / name).write_text('synthetic fixture\n')
            digest = hashlib.sha256((source / name).read_bytes()).hexdigest()
            files.append({'path': name, 'sha256': digest})
            states[name] = {'state': 'not_run', 'sha256': digest, 'executed_in_batch': False}
        run_manifest = dict(manifest, selected=selected, files=files, per_file_seconds=per_file,
                            batch_seconds=batch, minimum_free_bytes=0)
        runner.command_for = lambda name: commands[name]
        try:
            passed = runner.execute(run_manifest, states, report, source, 'synthetic-only',
                                    {'spent_test_seconds': 0, 'spent_job_seconds': 0})
            assert not expect_error
        except (FileNotFoundError, RuntimeError):
            assert expect_error
            passed = False
        ledger = json.loads((report / 'ledger.json').read_text())
        assert ledger['complete_suite_passed'] is False and ledger['full_suite_gate'] == 'RED'
        assert all(p.poll() is not None for p in spawned)
        assert not list(base.glob('ma.*'))
        assert not any(v['state'] == 'running' for v in states.values())
        return passed, states, ledger

    def py(code):
        return [sys.executable, '-c', code]

    passing = py('import time; time.sleep(.1); print(' + repr(tap()) + ')')
    failing = py('print(' + repr(tap(2, 1)) + '); raise SystemExit(1)')
    try:
        result, states, _ = execute_case('ordinary', {'a': failing, 'b': passing, 'c': py("print('partial')"), 'd': passing})
        assert not result and [v['state'] for v in states.values()] == ['failed', 'passed', 'incomplete', 'passed']
        result, states, _ = execute_case('allpass', {'a': passing, 'b': passing, 'c': passing})
        assert result and all(v['state'] == 'passed' for v in states.values())
        descendant_file = base / 'descendant.pid'
        sleeper = py('import subprocess,sys,time,pathlib; p=subprocess.Popen([sys.executable,"-c","import time;time.sleep(60)"]); pathlib.Path(' + repr(str(descendant_file)) + ').write_text(str(p.pid)); time.sleep(60)')
        result, states, _ = execute_case('timeout', {'a': sleeper, 'b': passing, 'c': passing}, per_file=.35)
        assert not result and [v['state'] for v in states.values()] == ['timeout', 'passed', 'passed']
        descendant = Path('/proc') / descendant_file.read_text()
        if descendant.exists():
            assert (descendant / 'stat').read_text().split(') ', 1)[1].startswith('Z '), 'Owned descendant still live'
        result, states, ledger = execute_case('deadline', {'a': py('import time;time.sleep(60)'), 'b': py('import time;time.sleep(60)'), 'c': passing}, batch=.3)
        assert not result and ledger['stop_reason'] == 'batch-deadline'
        assert [v['state'] for v in states.values()] == ['interrupted', 'interrupted', 'not_run']
        result, states, ledger = execute_case('spawnerror', {'a': py('import time;time.sleep(60)'), 'b': ['/no/such/synthetic-command'], 'c': passing}, expect_error=True)
        assert not result and ledger['stop_reason'] == 'controller-error'
        assert [v['state'] for v in states.values()] == ['interrupted', 'not_run', 'not_run']
        original_write = Path.write_text
        def no_summaries(path, *args, **kwargs):
            if path.name == 'summary.json':
                raise OSError(28, 'synthetic ENOSPC')
            return original_write(path, *args, **kwargs)
        Path.write_text = no_summaries
        try:
            result, states, ledger = execute_case('reporterror', {'a': passing, 'b': py('import time;time.sleep(60)')}, expect_error=True)
            assert not result and ledger['stop_reason'] == 'cleanup-error'
            assert all(v['state'] == 'incomplete' and v['cleanup_error'] and v['process_reaped'] for v in states.values())
        finally:
            Path.write_text = original_write
        assert max_live[0] == 2
    finally:
        runner.command_for, runner.subprocess.Popen = original_command, original_popen
        runner.validate_temp = original_validate_temp
        for p in spawned:
            if p.poll() is None:
                runner.kill_group(p.pid)
                p.wait()
        if old_temp is None:
            os.environ.pop('RUNNER_TEMP', None)
        else:
            os.environ['RUNNER_TEMP'] = old_temp
print('PASS: six harmless process scenarios; concurrency2, failure continuation, timeout/group cleanup, batch deadline/not_run, startup/report-write error cleanup')
print('LOCAL CONTROLS ONLY. No browser, product suite, compilation, CI activation or release acceptance.')
