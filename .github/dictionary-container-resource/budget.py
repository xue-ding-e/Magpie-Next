"""Bind the work deadline to this job's official start, including container init.

Public read-only API metadata, no token, redirect, retry or unbounded fallback.
The outer job timeout alone covers pull/initialization before this code starts.
"""
from datetime import datetime, timezone
from email.utils import parsedate_to_datetime
import json
import math
import os
from pathlib import Path
import re
import signal
import time
import urllib.request

REPO = 'xue-ding-e/Magpie-Next'
BRANCH = 'validation/local-dictionary-container-resource-diagnostics-20261102'
WORKFLOW = '.github/workflows/local-dictionary-container-resource.yml'
JOB = 'container-resources'
CAP = 600
RESERVE = 180
MAX_BODY = 256 << 10


def require(ok, reason):
    if not ok:
        raise RuntimeError('BUDGET NOT RUN: ' + reason)


def context(env):
    require(env.get('GITHUB_REPOSITORY') == REPO, 'wrong repository')
    require(env.get('GITHUB_REF') == 'refs/heads/' + BRANCH, 'wrong canonical ref')
    require(env.get('GITHUB_EVENT_NAME') in ('push', 'workflow_dispatch'), 'wrong event')
    require(env.get('GITHUB_JOB') == JOB, 'wrong job')
    require(re.fullmatch(r'[1-9][0-9]{0,15}', env.get('GITHUB_RUN_ID', '')) is not None, 'invalid run ID')
    require(env.get('GITHUB_RUN_ATTEMPT') == '1', 'reruns are not authorized')
    require(re.fullmatch(r'[0-9a-f]{40}', env.get('GITHUB_SHA', '')) is not None, 'invalid head SHA')
    return int(env['GITHUB_RUN_ID']), env['GITHUB_SHA']


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *_args, **_kwargs):
        raise RuntimeError('BUDGET NOT RUN: API redirect refused')


def unique_pairs(pairs):
    out = {}
    for key, value in pairs:
        require(key not in out, 'duplicate API JSON key')
        out[key] = value
    return out


def fetch(path):
    url = 'https://api.github.com/repos/' + REPO + path
    # Ignore proxy environment and never send a credential to this public API.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    request = urllib.request.Request(url, headers={'Accept': 'application/vnd.github+json',
        'Accept-Encoding': 'identity', 'X-GitHub-Api-Version': '2022-11-28',
        'User-Agent': 'Magpie-Next-container-budget'})
    before = time.monotonic()
    with opener.open(request, timeout=4) as response:
        require(response.status == 200 and response.geturl() == url, 'unexpected API response target/status')
        require(not response.headers.get('Content-Encoding'), 'encoded API response refused')
        require(not response.headers.get('Link'), 'paginated job evidence refused')
        body = response.read(MAX_BODY + 1)
        require(len(body) <= MAX_BODY, 'API response too large')
        server = parsedate_to_datetime(response.headers['Date'])
        require(server.utcoffset() is not None and server.utcoffset().total_seconds() == 0, 'API Date is not UTC')
    # Decode before recording the anchor: parsing and any scheduling pause are
    # part of the same bounded observation, not invisible time after the fetch.
    data = json.loads(body, object_pairs_hook=unique_pairs)
    observed_mono = time.monotonic()
    elapsed = observed_mono - before
    require(0 <= elapsed <= 5, 'API observation took too long')
    # Date is rounded to a second; include that second and all request latency.
    upper = server.timestamp() + 1 + elapsed
    require(abs(time.time() - server.timestamp()) <= 6, 'runner/API clocks disagree')
    return data, upper, observed_mono


def advance_upper(upper_utc, observed_mono, now_mono, local_utc):
    require(all(math.isfinite(value) for value in (upper_utc, observed_mono, now_mono, local_utc)),
        'nonfinite API observation anchor')
    require(now_mono >= observed_mono, 'monotonic observation moved backwards')
    # Pair UTC with its monotonic observation. In particular, a lagging local
    # wall clock must not hide a pause between API parsing and deadline mapping.
    return max(local_utc, upper_utc + (now_mono - observed_mono)) + 2


def derive(run, jobs, env, utc_upper, now_mono):
    run_id, head = context(env)
    require(run.get('id') == run_id and run.get('run_attempt') == 1, 'run/attempt mismatch')
    require(run.get('head_sha') == head and run.get('head_branch') == BRANCH, 'run head/ref mismatch')
    require(run.get('repository', {}).get('full_name') == REPO, 'run repository mismatch')
    require(run.get('path') == WORKFLOW and run.get('event') == env['GITHUB_EVENT_NAME'], 'run workflow/event mismatch')
    require(run.get('status') == 'in_progress', 'run is not current in-progress attempt')
    require(jobs.get('total_count') == 1 and len(jobs.get('jobs', [])) == 1, 'job is not unique')
    job = jobs['jobs'][0]
    require(job.get('name') == JOB and job.get('run_id') == run_id and job.get('head_sha') == head, 'job identity mismatch')
    require(job.get('status') == 'in_progress' and job.get('conclusion') is None, 'job already terminal')
    require(type(job.get('id')) is int and job['id'] > 0, 'missing job ID')
    stamp = job.get('started_at', '')
    require(isinstance(stamp, str) and re.fullmatch(r'\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ', stamp) is not None, 'invalid job start')
    start_utc = datetime.strptime(stamp, '%Y-%m-%dT%H:%M:%SZ').replace(tzinfo=timezone.utc).timestamp()
    elapsed = utc_upper - start_utc
    require(all(math.isfinite(x) for x in (utc_upper, now_mono, elapsed)), 'nonfinite clock')
    require(0 <= elapsed < CAP - RESERVE - 180, 'init consumed the available work budget or start is future')
    start = now_mono - elapsed
    require(start >= 0, 'invalid monotonic start mapping')
    return {'start_monotonic': start, 'deadline_monotonic': start + CAP - RESERVE,
        'cap_seconds': CAP, 'cleanup_reserve_seconds': RESERVE, 'job_id': job['id'],
        'job_started_at': stamp, 'elapsed_before_work_seconds_upper': elapsed,
        'run_id': run_id, 'attempt': 1, 'head_sha': head, 'includes_container_initialization': True,
        'method': 'public exact run-attempt/job metadata; conservative API Date/parse latency plus paired monotonic observation'}


def main():
    def timeout(_signum, _frame):
        raise RuntimeError('BUDGET NOT RUN: metadata observation exceeded 15 seconds')
    signal.signal(signal.SIGALRM, timeout)
    signal.alarm(15)
    try:
        run_id, _head = context(os.environ)
        run, _, _ = fetch(f'/actions/runs/{run_id}/attempts/1')
        jobs, upper, observed_mono = fetch(f'/actions/runs/{run_id}/attempts/1/jobs?per_page=100')
        # At least 2 additional seconds for server timestamp rounding/skew.
        now_mono = time.monotonic()
        utc_upper = advance_upper(upper, observed_mono, now_mono, time.time())
        result = derive(run, jobs, os.environ, utc_upper, now_mono)
        with (Path(os.environ['RUNNER_TEMP']) / 'dictionary-container-budget.json').open('x') as stream:
            json.dump(result, stream, indent=2)
        with open(os.environ['GITHUB_ENV'], 'a', encoding='utf-8') as stream:
            print(f"DICTIONARY_JOB_START_MONOTONIC={result['start_monotonic']:.6f}", file=stream)
            print(f"DICTIONARY_JOB_DEADLINE_MONOTONIC={result['deadline_monotonic']:.6f}", file=stream)
            print(f'DICTIONARY_JOB_CAP_SECONDS={CAP}', file=stream)
        print(json.dumps(result, sort_keys=True))
    finally:
        signal.alarm(0)


if __name__ == '__main__':
    main()
