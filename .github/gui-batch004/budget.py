"""Bound exact prior GUI costs plus separately classified no-op scheduling costs."""
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
        # execution occurred; retain official bytes without inventing duration.
        if job['conclusion'] == 'skipped':
            continue
        started = dt.datetime.fromisoformat(job['started_at'].replace('Z', '+00:00'))
        ended = dt.datetime.fromisoformat(job['completed_at'].replace('Z', '+00:00'))
        total += seconds((ended - started).total_seconds())
    return total


def admitted_budget(manifest):
    receipt = load('cumulative-receipt.json')
    assert receipt['source'] == manifest['product'] == 'ff997c492a908293dc0de152ee20f83152b88aa9'
    assert digest('prior-ledger.json') == receipt['prior_ledger_sha256'] == '186c852350cf25b6eefd15f840b0e29ae70fefb7fa6a9b9c85bfc4a814580661'
    assert digest('prior-outcome.json') == receipt['prior_outcome_sha256'] == '4d36fed543eacba7f23451016ad9c6926d1ecf51ed3eaa3b1402e5dadbb66c33'
    assert digest('prior-job.json') == receipt['prior_jobs_sha256']
    prior, outcome, jobs = load('prior-ledger.json'), load('prior-outcome.json'), load('prior-job.json')
    assert receipt['prior_run'] == outcome['run'] == 41598441535
    assert receipt['prior_head'] == outcome['head'] == 'f2ccdbd931b038baa87a66b4fcfe8745cd13a915'
    assert prior['source'] == outcome['source'] == receipt['source']
    assert prior['cumulative_before_batch']['spent_test_seconds'] == receipt['before_prior_batch_test_seconds'] == 1045.758023034
    assert prior['cumulative_before_batch']['spent_job_seconds'] == receipt['before_prior_batch_job_seconds'] == 1512
    actual_test = seconds(prior['elapsed_seconds'])
    actual_job = job_seconds(jobs, 41598441535, receipt['prior_head'])
    assert actual_test == receipt['prior_batch_controller_seconds'] == outcome['test_seconds'] == 740.1287714770001
    assert actual_job == receipt['prior_batch_job_seconds'] == outcome['official_job_seconds'] == 797
    test_after = receipt['before_prior_batch_test_seconds'] + actual_test
    job_after = receipt['before_prior_batch_job_seconds'] + actual_job
    assert test_after == receipt['after_prior_batch_test_seconds'] == outcome['cumulative_test_seconds'] == prior['cumulative_test_seconds'] == 1785.886794511
    assert job_after == receipt['after_prior_batch_job_seconds'] == outcome['cumulative_job_seconds'] == 2309
    sentinel = receipt['sentinel']
    assert digest('sentinel-outcome.json') == sentinel['outcome_sha256'] == '1fb45e7e16c2cf9bc2f8bc43b934812b7685d9c712b1432cee5da1375a0b20d5'
    assert digest('sentinel-jobs.json') == sentinel['jobs_sha256'] == 'c8f23e247a6b8aad783d543a730cebc33704ede2c567bdd1fa8d1a15f5675fb3'
    sjobs, soutcome = load('sentinel-jobs.json'), load('sentinel-outcome.json')
    assert sentinel['run'] == soutcome['run'] == 41600206909
    assert sentinel['head'] == soutcome['head'] == 'bdb3b893b0b8a3db298343e206bedafcf8473cda'
    assert soutcome['source'] == receipt['source']
    assert {j['name']: j['conclusion'] for j in sjobs['jobs']} == soutcome['results']
    assert soutcome['results']['optional_gui'] == 'skipped'
    assert soutcome['results']['failed_package_sentinel'] == 'failure'
    assert soutcome['purpose'] == 'No-op scheduling evidence; no publisher or build execution'
    sentinel_test = seconds(sentinel['test_seconds'])
    sentinel_job = job_seconds(sjobs, 41600206909, sentinel['head'])
    assert sentinel_test == soutcome['test_seconds'] == 0
    assert sentinel_job == sentinel['job_seconds'] == soutcome['official_job_seconds'] == 35
    test_before_quota, job_before_quota = test_after + sentinel_test, job_after + sentinel_job
    assert test_before_quota == receipt['before_quota_diagnostic_test_seconds'] == 1785.886794511
    assert job_before_quota == receipt['before_quota_diagnostic_job_seconds'] == 2344
    quota = receipt['quota_diagnostic']
    assert digest('quota-diagnostic-outcome.json') == quota['outcome_sha256'] == '5294f898ac38ac219bc910aaa8c4d088cb3ec5823856d4c9eb824d9b6ae87f73'
    assert digest('quota-diagnostic-summary.json') == quota['summary_sha256'] == '4b034a33648ef5e29f64dbdb9ce720c8fe3272d1dfb150195a854248a5dfa4e9'
    assert digest('quota-diagnostic-jobs.json') == quota['jobs_sha256'] == 'dc7070130f0ab79affae248e717288a597b28e4e2f4f528cad7fb5ff5c62ec40'
    qoutcome, qsummary, qjobs = load('quota-diagnostic-outcome.json'), load('quota-diagnostic-summary.json'), load('quota-diagnostic-jobs.json')
    assert quota['run'] == qoutcome['run'] == 41603192722
    assert quota['head'] == qoutcome['head'] == '7e31c76e2091c001db9556a858d46d844be5e818'
    assert quota['source'] == qoutcome['source'] == qsummary['source'] == receipt['source']
    assert qoutcome['summary'] == qsummary
    assert quota['acceptance'] is qsummary['acceptance'] is False
    assert qsummary['instrumented_diagnostic'] is True and qsummary['assertion_evidence'] is False
    assert qsummary['state'] == 'controller-error' and qsummary['error'].startswith('JSONDecodeError(')
    assert qsummary['source_restored'] is True and qsummary['process_reaped'] is True
    assert qsummary['original_sha256'] == qsummary['restored_sha256'] == '3e7a452fa6a0400cdfd2d6b9f3910e80028f1203f403fb7db28af497aa529ce6'
    assert qsummary['prior_test_seconds'] == test_before_quota and qsummary['prior_job_seconds'] == job_before_quota
    assert qsummary['counts'] == {'tests': 2, 'pass': 0, 'fail': 2, 'cancelled': 0, 'skipped': 0, 'todo': 0}
    quota_test = seconds(qsummary['elapsed_seconds'])
    quota_job = job_seconds(qjobs, 41603192722, quota['head'])
    assert quota_test == quota['test_seconds'] == 16.387983831
    assert quota_job == quota['job_seconds'] == qoutcome['official_job_seconds'] == 88
    budget = manifest['flow_budget']
    test_total, job_total = test_before_quota + quota_test, job_before_quota + quota_job
    assert test_total == budget['spent_test_seconds'] == receipt['spent_test_seconds'] == qoutcome['cumulative_test_seconds'] == 1802.274778342
    assert job_total == budget['spent_job_seconds'] == receipt['spent_job_seconds'] == qoutcome['cumulative_job_seconds'] == 2432
    assert budget['maximum_test_seconds'] == receipt['maximum_test_seconds'] == 28800
    assert budget['maximum_job_seconds'] == receipt['maximum_job_seconds'] == 43200
    assert test_total + manifest['batch_seconds'] <= 28800, '8h test budget exhausted'
    assert job_total + manifest['job_ceiling_seconds'] <= 43200, '12h job budget exhausted'
    return {'spent_test_seconds': test_total, 'spent_job_seconds': job_total,
            'remaining_test_seconds_before_batch': 28800 - test_total,
            'remaining_job_seconds_before_batch': 43200 - job_total,
            'prior_gui_run': 41598441535, 'prior_batch_controller_seconds': actual_test,
            'no_op_scheduling_run': 41600206909, 'no_op_scheduling_test_seconds': 0,
            'no_op_scheduling_job_seconds': 35, 'quota_diagnostic_run': 41603192722,
            'quota_diagnostic_test_seconds': quota_test, 'quota_diagnostic_job_seconds': quota_job,
            'quota_diagnostic_acceptance': False, 'batch_reservation_test_seconds': 1800,
            'batch_reservation_job_seconds': 3300}
