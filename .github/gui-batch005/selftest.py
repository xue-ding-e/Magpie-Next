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

migration.verify_source_continuity()
records = migration.seed_records(manifest, 'v22.23.4')
prior = migration.read('prior-ledger.json')
assert len(records) == 827 and all(not v['executed_in_batch'] for v in records.values())
assert sum(v['state'] == 'passed' for v in records.values()) == 57
assert sum(v['state'] == 'failed' for v in records.values()) == 7
assert sum(v['state'] == 'not_run' for v in records.values()) == 763
assert sum(v['old_source_provenance']['state'] != 'not_run' for v in records.values()) == 24
assert all(v['old_source_provenance']['accepted_on_current_source'] is False for v in records.values())
assert sum(v.get('carried_current_source_evidence', False) for v in records.values()) == 64
assert sum(v.get('last_execution', {}).get('run') == 41598441535 for v in records.values()) == 32
assert sum(v.get('last_execution', {}).get('run') == 41604270125 for v in records.values()) == 32
for path, row in prior['files'].items():
    assert records[path]['state'] == row['state']
    if row.get('last_execution'):
        assert records[path]['last_execution'] == row['last_execution']
    if row['executed_in_batch']:
        assert records[path]['last_execution']['ledger_sha256'] == migration.PRIOR_SHA
for field, value in [('product', migration.OLD_SOURCE), ('selected', manifest['selected'][:-1]),
                     ('selected', [manifest['selected'][0]] * 32),
                     ('selected', ['internal/gui/tests/removed-hide.test.cjs'] + manifest['selected'][1:])]:
    invalid = copy.deepcopy(manifest)
    invalid[field] = value
    reject(lambda: migration.seed_records(invalid, 'v22.23.4'))
reject(lambda: migration.seed_records(manifest, 'v22.0.0'))
original_read = migration.read
try:
    for mutate in ('erase_failure', 'wrong_source', 'not_reaped', 'fake_old_pass', 'relabel_prior_run'):
        altered = copy.deepcopy(original_read('prior-ledger.json'))
        failed = altered['files']['internal/gui/tests/quota-count-wrap.test.cjs']
        if mutate == 'erase_failure': failed['state'] = 'passed'
        if mutate == 'wrong_source': altered['source'] = migration.OLD_SOURCE
        if mutate == 'not_reaped': failed['process_reaped'] = False
        if mutate == 'fake_old_pass': next(iter(altered['files'].values()))['old_source_provenance']['accepted_on_current_source'] = True
        if mutate == 'relabel_prior_run': failed['last_execution']['run'] = 41604270125
        migration.read = lambda name: altered if name == 'prior-ledger.json' else original_read(name)
        reject(lambda: migration.seed_records(manifest, 'v22.23.4'))
finally:
    migration.read = original_read
print('PASS: same-source57pass/7fail retained;763not_run; old24 history only; two32 execution origins preserved; source/runtime/failure-erasure/rerun/relabel rejection')

actual_budget = budget.admitted_budget(manifest)
assert actual_budget['spent_test_seconds'] == 2601.644705159
assert actual_budget['spent_job_seconds'] == 3444
assert [r['run'] for r in actual_budget['additional_targeted_runs']] == [41621675336, 41623227697]
assert all(r['credit_to_827'] is False for r in actual_budget['additional_targeted_runs'])
assert actual_budget['historical_sentinel_and_quota_diagnostic_debt_included'] is True
original_load = budget.load
mutations = [('cumulative-receipt.json', 'spent_test_seconds', 1802.274778342),
             ('cumulative-receipt.json', 'spent_job_seconds', 2432),
             ('cumulative-receipt.json', 'prior_run', 0),
             ('cumulative-receipt.json', 'source', migration.OLD_SOURCE),
             ('quota-redgreen-summary.json', 'full_suite_passed', True),
             ('quota-fold-pair-summary.json', 'current_pair_passed', True),
             ('quota-fold-pair-summary.json', 'controller_elapsed_seconds', 0)]
for value in [None, -1, float('nan'), True, 28800]:
    mutations.append(('cumulative-receipt.json', 'prior_batch_controller_seconds', value))
try:
    for filename, field, value in mutations:
        changed = copy.deepcopy(original_load(filename))
        changed[field] = value
        budget.load = lambda name: changed if name == filename else original_load(name)
        reject(lambda: budget.admitted_budget(manifest))
    budget.load = original_load
    for mutation in ('drop_run', 'credit_candidate', 'substitute_process_time', 'swap_source'):
        changed = copy.deepcopy(original_load('cumulative-receipt.json'))
        if mutation == 'drop_run': changed['additional_targeted_runs'].pop()
        if mutation == 'credit_candidate': changed['additional_targeted_runs'][0]['credit_to_827'] = True
        if mutation == 'substitute_process_time': changed['additional_targeted_runs'][0]['controller_seconds'] = 126.77042398200003
        if mutation == 'swap_source': changed['additional_targeted_runs'][1]['base'] = 'd076db4c3cfaf85fc1def65efbe40936d2c7bf02'
        budget.load = lambda name: changed if name == 'cumulative-receipt.json' else original_load(name)
        reject(lambda: budget.admitted_budget(manifest))
    budget.load = original_load
    for field, value in [('maximum_test_seconds', 28801), ('maximum_job_seconds', 43201),
                          ('spent_test_seconds', 1802.274778342), ('spent_job_seconds', 2432)]:
        invalid = copy.deepcopy(manifest)
        invalid['flow_budget'][field] = value
        reject(lambda: budget.admitted_budget(invalid))
finally:
    budget.load = original_load
print('PASS: cumulative2601.644705159/3444 includes two exact targeted controller/job charges; no827 credit; dropped-run/process-time/source-substitution/reset/NaN/extension rejected')

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

    def execute_case(label, commands, per_file=2, batch=8, expect_error=False, carried=None):
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
        if carried:
            states.update(copy.deepcopy(carried))
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
        history = {
            'oldpass': {'state': 'passed', 'executed_in_batch': False, 'carried_current_source_evidence': True, 'source': manifest['product']},
            'oldfail': {'state': 'failed', 'executed_in_batch': False, 'carried_current_source_evidence': True, 'source': manifest['product']},
        }
        result, states, ledger = execute_case('carryhistory', {'a': passing, 'b': passing}, carried=history)
        assert result and ledger['selected_batch_passed'] is True and ledger['full_suite_gate'] == 'RED'
        assert states['oldpass'] == history['oldpass'] and states['oldfail'] == history['oldfail']
        assert ledger['carried_current_source_file_count'] == 2 and ledger['executed_in_this_batch_count'] == 2
        assert ledger['current_source_executed_file_count'] == 4 and ledger['current_source_terminal_pass_count'] == 3
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
print('PASS: seven harmless process scenarios; concurrency2, carried failure preserved, failure continuation, timeout/group cleanup, batch deadline/not_run, startup/report-write error cleanup')
print('LOCAL CONTROLS ONLY. No browser, product suite, compilation, CI activation or release acceptance.')
