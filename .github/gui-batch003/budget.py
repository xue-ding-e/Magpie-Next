"""Fail-closed cumulative recovery accounting; no reset across source changes."""
import datetime as dt
import hashlib
import json
import math
from pathlib import Path

CONTROL = Path(__file__).resolve().parent


def load(name):
    return json.loads((CONTROL / name).read_text())


def digest(name):
    return hashlib.sha256((CONTROL / name).read_bytes()).hexdigest()


def seconds(value):
    assert isinstance(value, (int, float)) and not isinstance(value, bool)
    assert math.isfinite(value) and value >= 0
    return value


def job_seconds(payload, run):
    assert payload['total_count'] == len(payload['jobs']) > 0
    seen, total = set(), 0
    for job in payload['jobs']:
        assert job['run_id'] == run and job['id'] not in seen
        seen.add(job['id'])
        assert job['status'] == 'completed' and job['conclusion'] is not None
        # GitHub can attach negative timestamps to skipped jobs. Preserve them,
        # but a skipped job is no execution and never invent a duration for it.
        if job['conclusion'] == 'skipped':
            continue
        started = dt.datetime.fromisoformat(job['started_at'].replace('Z', '+00:00'))
        ended = dt.datetime.fromisoformat(job['completed_at'].replace('Z', '+00:00'))
        elapsed = seconds((ended - started).total_seconds())
        total += elapsed
    return total


def known_base():
    receipt, outcome = load('cumulative-receipt.json'), load('prior-outcome.json')
    assert digest('prior-outcome.json') == receipt['prior_outcome_sha256']
    assert digest('prior-ledger.json') == receipt['prior_ledger_sha256']
    assert outcome['cumulative_test_seconds'] == receipt['prior_cumulative_test_seconds'] == 1013.758023034
    assert outcome['cumulative_job_seconds'] == receipt['prior_cumulative_job_seconds'] == 1274
    first = receipt['required_gui_attempt1']
    assert digest('required-gui-attempt1-jobs.json') == first['jobs_sha256']
    jobs = load('required-gui-attempt1-jobs.json')
    assert job_seconds(jobs, 41584387550) == first['job_elapsed_seconds'] == 26
    assert set(first['executed_job_ids']) == {j['id'] for j in jobs['jobs'] if j['conclusion'] != 'skipped'}
    assert all(j['conclusion'] == 'skipped' for j in jobs['jobs'] if j['name'] in ('positive_gui', 'negative_gui'))
    assert first['test_elapsed_seconds'] == 0
    assert receipt['known_spent_test_seconds'] == 1013.758023034
    assert receipt['known_spent_job_seconds'] == 1300
    return receipt['known_spent_test_seconds'], receipt['known_spent_job_seconds']


def proof_timing(manifest, receipt, jobs):
    expected_head = 'fb99fa33a5f56174bc279098deec43d9ea84ed1c'
    assert receipt['head_sha'] == expected_head
    assert all(j['head_sha'] == expected_head for j in jobs['jobs'])
    assert digest('required-gui-evidence/outcome.json') == receipt['outcome_sha256'] == 'd695bcbe7efeb6c7cacc5ae6e044f5abe291f35873a4b6590ea30e2c105a1aaa'
    outcome = load('required-gui-evidence/outcome.json')
    archives = load('required-gui-evidence/archive-digests.json')
    assert set(archives) == {'positive/artifact.zip', 'negative/artifact.zip'}
    for name, checksum in outcome['files'].items():
        if name in archives:
            # Complete ZIPs were independently hashed locally and remain in the
            # original artifact archive. Do not replicate binary evidence in Git.
            assert archives[name]['sha256'] == checksum and archives[name]['bytes'] > 0
        else:
            assert digest('required-gui-evidence/' + name) == checksum
    positive = load('required-gui-evidence/positive/extracted/summary.json')
    negative = load('required-gui-evidence/negative/extracted/summary.json')
    assert positive['source']['head'] == negative['source']['before'] == manifest['product']
    assert positive['full_gui_suite_passed'] is negative['full_gui_suite_passed'] is False
    expected_files = ['internal/gui/tests/codex-auth-export-state.test.cjs', 'internal/gui/tests/codex-auth-export.test.cjs']
    assert [r['file'] for r in positive['results']] == expected_files
    assert [r['counts']['tests'] for r in positive['results']] == [9, 10]
    for row in positive['results']:
        assert row['process_reaped'] and row['passed'] and row['exit'] == 0
        assert row['counts']['pass'] == row['counts']['tests']
        assert all(row['counts'][k] == 0 for k in ('fail', 'cancelled', 'skipped', 'todo'))
    assert len(negative['results']) == 1
    row = negative['results'][0]
    assert row['file'] == 'internal/gui/tests/required-gui-negative.test.cjs'
    assert row['process_reaped'] and not row['passed'] and row['exit'] == 1
    assert row['counts'] == {'tests': 1, 'pass': 0, 'fail': 1, 'cancelled': 0, 'skipped': 0, 'todo': 0}
    expected = []
    for kind, summary in [('positive', positive), ('negative', negative)]:
        for index, row in enumerate(summary['results']):
            expected.append({'file': row['file'], 'elapsed_seconds': seconds(row['elapsed_seconds']),
                             'purpose': 'original_product' if kind == 'positive' else 'intentional_negative_control',
                             'execution_source': summary['source']['head'],
                             'evidence': f'required-gui-evidence/{kind}/extracted/summary.json#results[{index}]'})
    actual = sum(e['elapsed_seconds'] for e in expected)
    tests = load('required-gui-final-tests.json')
    assert tests['run_id'] == 41584865002 and tests['source'] == manifest['product']
    assert tests['actual_elapsed_verified'] is True and tests['complete_run_accounting'] is True
    assert tests['timing_scope'] == 'sum_of_actual_gui_process_wall_seconds'
    assert tests['processes'] == expected
    assert tests['actual_gui_process_elapsed_seconds'] == receipt['test_elapsed_seconds'] == outcome['test_seconds'] == actual
    positive_jobs = [j for j in jobs['jobs'] if j['name'] == 'positive_gui']
    assert len(positive_jobs) == 1 and positive_jobs[0]['conclusion'] == 'success'
    execution = [e for e in positive_jobs[0]['steps'] if e['name'] == 'Execute exact required-GUI runner']
    assert len(execution) == 1 and execution[0]['conclusion'] == 'success'
    step = execution[0]
    positive_wall = seconds((dt.datetime.fromisoformat(step['completed_at'].replace('Z', '+00:00')) -
                             dt.datetime.fromisoformat(step['started_at'].replace('Z', '+00:00'))).total_seconds())
    negative_wall = sum(seconds(e['elapsed_seconds']) for e in negative['results'])
    charge = math.ceil(positive_wall) + math.ceil(negative_wall)
    assert tests['charge_policy'] == 'ceil_positive_official_execute_step_seconds_plus_ceil_negative_process_seconds'
    assert tests['positive_official_execute_step_seconds'] == positive_wall == 31
    assert tests['negative_actual_process_seconds'] == negative_wall
    assert seconds(receipt['charged_test_seconds']) == tests['charged_test_seconds'] == outcome['charged_test_seconds'] == charge == 32
    assert charge >= actual
    return actual, charge


def admitted_budget(manifest):
    test_base, job_base = known_base()
    receipt = load('required-gui-receipt.json')
    assert receipt['status'] == 'verified_terminal', 'Pending actual requiredGUI receipt; batch003 is not admitted'
    assert receipt['source'] == manifest['product']
    run = receipt['run_id']
    assert run == 41584865002
    assert digest('required-gui-final-jobs.json') == receipt['official_jobs_sha256']
    assert digest('required-gui-final-tests.json') == receipt['test_evidence_sha256']
    jobs = load('required-gui-final-jobs.json')
    actual_job = job_seconds(jobs, run)
    assert seconds(receipt['job_elapsed_seconds']) == actual_job == 212
    actual_test, test_charge = proof_timing(manifest, receipt, jobs)
    # Batches retain controller wall time. Proof uses conservative official
    # positive step + rounded actual negative process. Keep raw actual separate.
    budget = manifest['flow_budget']
    assert budget['known_spent_test_seconds'] == test_base and budget['known_spent_job_seconds'] == job_base
    assert budget['maximum_test_seconds'] == 28800 and budget['maximum_job_seconds'] == 43200
    total_test, total_job = test_base + test_charge, job_base + actual_job
    assert budget['spent_test_seconds'] == total_test == 1045.758023034
    assert budget['spent_job_seconds'] == total_job == 1512
    assert total_test + manifest['batch_seconds'] <= budget['maximum_test_seconds'], '8h test budget exhausted'
    assert total_job + manifest['job_ceiling_seconds'] <= budget['maximum_job_seconds'], '12h job budget exhausted'
    return {'spent_test_seconds': total_test, 'spent_job_seconds': total_job,
            'remaining_test_seconds_before_batch': 28800 - total_test,
            'remaining_job_seconds_before_batch': 43200 - total_job,
            'required_gui_run': run, 'required_gui_actual_process_seconds': actual_test,
            'required_gui_conservative_test_charge': test_charge,
            'batch_reservation_test_seconds': 1800, 'batch_reservation_job_seconds': 3300}
