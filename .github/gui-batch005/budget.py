"""Keep the exact cumulative budget using one preceding ledger and official job."""
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


def job_seconds(payload, run, head):
    assert payload['total_count'] == len(payload['jobs']) > 0
    seen, total = set(), 0
    for job in payload['jobs']:
        assert job['run_id'] == run and job['head_sha'] == head and job['id'] not in seen
        seen.add(job['id'])
        assert job['status'] == 'completed' and job['conclusion'] is not None
        # Skipped records sometimes have negative GitHub timestamp pairs. No
        # execution occurred; preserve official bytes without invented duration.
        if job['conclusion'] == 'skipped':
            continue
        started = dt.datetime.fromisoformat(job['started_at'].replace('Z', '+00:00'))
        ended = dt.datetime.fromisoformat(job['completed_at'].replace('Z', '+00:00'))
        total += seconds((ended - started).total_seconds())
    return total


def targeted_debt(receipt, test_before, job_before, product):
    """Charge both actual targeted runs without promoting their files or source."""
    expected = [
        ('quota-redgreen', 41621675336, 'f028f88cf058085f21d62e6d923516f0c248d922',
         '22c4264e4d6baf2472a7113404d54710d0ea36824c6f728f3f9c7f5d48bd3ffe',
         '40abad3a24a329178a4e68ac461de4fe860614879d69cafd481fe5aaeaf4b50e',
         '129c20cd630df9e08adaabe87d336d2039174f81a3fdd8cba3277dabbfa48930', 126.893009289, 203),
        ('quota-fold-pair', 41623227697, '449311d7036c4849760c6f6fdec233e2385482d9',
         '527c828eba48794073346dbdf1fdd5cad5792900e7743c66837d02e79858d488',
         'afd4038fde428feb05bd21108b00368f91cdae83e9e4d443f0a4e4be71477f7a',
         'bf8f452f09467e91f12e3558a873965121284782710dab0ee235a573a2c11ad0', 50.309264089999985, 133),
    ]
    entries = receipt['additional_targeted_runs']
    assert len(entries) == 2
    candidate = 'd076db4c3cfaf85fc1def65efbe40936d2c7bf02'
    totals = []
    for entry, (label, run, head, ohash, shash, jhash, duration, job_duration) in zip(entries, expected):
        assert entry['label'] == label and entry['run'] == run and entry['head'] == head
        assert entry['base'] == product and entry['candidate'] == candidate
        assert entry['credit_to_827'] is False
        assert digest(label + '-outcome.json') == entry['outcome_sha256'] == ohash
        assert digest(label + '-summary.json') == entry['summary_sha256'] == shash
        assert digest(label + '-jobs.json') == entry['jobs_sha256'] == jhash
        outcome, summary, jobs = load(label + '-outcome.json'), load(label + '-summary.json'), load(label + '-jobs.json')
        assert outcome['run'] == run and outcome['head'] == head
        assert outcome['base'] == product and outcome['candidate'] == candidate
        assert outcome['summary_sha256'] == shash
        assert outcome['all_reaped'] is True and outcome['source_before_equals_after'] is True
        assert outcome['full_suite_passed'] is False and summary['full_suite_passed'] is False
        assert summary['targeted_only'] is True and summary['source_before'] == summary['source_after']
        for key, source in [('base', product), ('candidate', candidate)]:
            assert summary['source_before'][key]['head'] == source
            assert summary['source_before'][key]['clean'] is True
        assert summary['prior_test_seconds'] == test_before and summary['prior_job_seconds'] == job_before
        rows = summary['files']
        assert all(r['process_reaped'] is True and r['terminal_complete'] is True and
                   r['timeout'] is False and r['interrupted'] is False for r in rows)
        for row in rows:
            counts = row['counts']
            assert all(counts[k] == 0 for k in ('cancelled', 'skipped', 'todo'))
            assert counts['tests'] == counts['pass'] + counts['fail'] > 0
        if label == 'quota-redgreen':
            assert summary['state'] == outcome['state'] == 'failed' and len(rows) == 6
            assert rows[0]['source'] == product and rows[0]['expected_red'] is True
            assert rows[0]['counts']['tests'] == rows[0]['counts']['fail'] == 2 and rows[0]['counts']['pass'] == 0
            assert all(row['source'] == candidate for row in rows[1:])
            assert {k: sum(row['counts'][k] for row in rows[1:]) for k in ('tests', 'pass', 'fail')} == outcome['candidate_counts'] == {'tests': 26, 'pass': 24, 'fail': 2}
            assert outcome['baseline'] == {'tests': 2, 'pass': 0, 'fail': 2, 'expected_red': True}
        else:
            assert summary['state'] == outcome['state'] == 'pair-complete-red' and len(rows) == 2
            assert [row['source'] for row in rows] == [product, candidate]
            assert all(row['file'] == 'internal/gui/tests/usage-fold.test.cjs' for row in rows)
            assert rows[0]['counts'] == rows[1]['counts'] == outcome['base_counts'] == outcome['candidate_counts'] == {'tests': 6, 'pass': 4, 'fail': 2, 'cancelled': 0, 'skipped': 0, 'todo': 0}
            assert summary['current_pair_passed'] is False
        actual_process = sum(seconds(row['elapsed_seconds']) for row in rows)
        actual_controller = seconds(summary['controller_elapsed_seconds'])
        actual_job = job_seconds(jobs, run, head)
        assert actual_process == outcome['process_seconds'] and actual_process <= actual_controller
        assert actual_controller == entry['controller_seconds'] == outcome['charged_test_seconds'] == duration
        assert actual_job == entry['job_seconds'] == outcome['official_job_seconds'] == job_duration
        test_before += actual_controller
        job_before += actual_job
        assert test_before == outcome['cumulative_test_seconds'] and job_before == outcome['cumulative_job_seconds']
        totals.append({'run': run, 'controller_seconds': actual_controller, 'job_seconds': actual_job, 'credit_to_827': False})
    return test_before, job_before, totals


def admitted_budget(manifest):
    receipt = load('cumulative-receipt.json')
    assert receipt['source'] == manifest['product'] == 'ff997c492a908293dc0de152ee20f83152b88aa9'
    assert digest('prior-ledger.json') == receipt['prior_ledger_sha256'] == '09f5b0c0b824b8fac2b8fcaa21bc022172955d456bf622bc1bbe3c1aee3a666c'
    assert digest('prior-outcome.json') == receipt['prior_outcome_sha256'] == 'c8002cc33d205b2f9707e050fab16fd7da15c587da1ab3b773aa175a7ba3c6d8'
    assert digest('prior-job.json') == receipt['prior_jobs_sha256'] == '6983c51c304a2e50c0762ac418e882c162845f145dfee65e71624652def48168'
    prior, outcome, jobs = load('prior-ledger.json'), load('prior-outcome.json'), load('prior-job.json')
    assert receipt['prior_run'] == outcome['run'] == 41604270125
    assert receipt['prior_head'] == outcome['head'] == 'ffaefbfe46e748ebf0ac3c85220e7fc7b49d9049'
    assert prior['source'] == outcome['source'] == receipt['source']
    before = prior['cumulative_before_batch']
    assert before['spent_test_seconds'] == receipt['before_prior_batch_test_seconds'] == 1802.274778342
    assert before['spent_job_seconds'] == receipt['before_prior_batch_job_seconds'] == 2432
    assert before['no_op_scheduling_run'] == 41600206909 and before['no_op_scheduling_job_seconds'] == 35
    assert before['quota_diagnostic_run'] == 41603192722 and before['quota_diagnostic_acceptance'] is False
    assert before['quota_diagnostic_test_seconds'] == 16.387983831 and before['quota_diagnostic_job_seconds'] == 88
    actual_test = seconds(prior['elapsed_seconds'])
    actual_job = job_seconds(jobs, 41604270125, receipt['prior_head'])
    assert actual_test == receipt['prior_batch_controller_seconds'] == outcome['test_seconds'] == 622.1676534379999
    assert actual_job == receipt['prior_batch_job_seconds'] == outcome['official_job_seconds'] == 676
    test_total = receipt['before_prior_batch_test_seconds'] + actual_test
    job_total = receipt['before_prior_batch_job_seconds'] + actual_job
    assert test_total == receipt['before_additional_targeted_test_seconds'] == outcome['cumulative_test_seconds'] == prior['cumulative_test_seconds'] == 2424.44243178
    assert job_total == receipt['before_additional_targeted_job_seconds'] == outcome['cumulative_job_seconds'] == 3108
    test_total, job_total, targeted = targeted_debt(receipt, test_total, job_total, manifest['product'])
    budget = manifest['flow_budget']
    assert test_total == budget['spent_test_seconds'] == receipt['spent_test_seconds'] == 2601.644705159
    assert job_total == budget['spent_job_seconds'] == receipt['spent_job_seconds'] == 3444
    assert budget['maximum_test_seconds'] == receipt['maximum_test_seconds'] == 28800
    assert budget['maximum_job_seconds'] == receipt['maximum_job_seconds'] == 43200
    assert test_total + manifest['batch_seconds'] <= 28800, '8h test budget exhausted'
    assert job_total + manifest['job_ceiling_seconds'] <= 43200, '12h job budget exhausted'
    return {'spent_test_seconds': test_total, 'spent_job_seconds': job_total,
            'remaining_test_seconds_before_batch': 28800 - test_total,
            'remaining_job_seconds_before_batch': 43200 - job_total,
            'prior_gui_run': 41604270125, 'prior_batch_controller_seconds': actual_test,
            'prior_batch_job_seconds': actual_job,
            'historical_sentinel_and_quota_diagnostic_debt_included': True,
            'additional_targeted_runs': targeted,
            'batch_reservation_test_seconds': 1800, 'batch_reservation_job_seconds': 3300}
