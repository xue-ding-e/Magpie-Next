"""Serial owned native/compile/resource phases inside a read-only job cgroup.

OOM group 0 is NOT a whole-tree kill guarantee. If this supervisor dies, the
missing completion evidence fails the always-run verifier; runner container
removal is the separately checked platform fallback, never a cleanup claim.
"""
import importlib.util
import json
import os
from pathlib import Path
import signal
import sys

CONTROL = Path(__file__).resolve().parent
ROOT = CONTROL.parents[1]
STAGES = ('100k', '16mib', 'held', 'released')


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


owned = module('container_resource_owned', CONTROL / 'owned_process.py')
guard = module('container_resource_guard', CONTROL / 'probe.py')


def reports():
    return Path(os.environ['RUNNER_TEMP']).resolve(strict=True) / 'dictionary-container-resource-reports'


def write(name, value):
    with (reports() / name).open('x', encoding='utf-8') as stream:
        json.dump(value, stream, indent=2)
        stream.write('\n')


def read_json(path):
    with Path(path).open('r', encoding='utf-8') as stream:
        data = stream.read((256 << 10) + 1)
    guard.require(len(data) <= 256 << 10, 'oversized evidence file')
    return json.loads(data)


def baseline():
    value = read_json(Path(os.environ['RUNNER_TEMP']) / guard.OUTPUT)
    guard.require(value.get('ok') is True and value.get('hard_limit_bytes') == guard.LIMIT
        and value.get('swap_limit_bytes') == 0 and value.get('cgroup_readonly') is True,
        'missing first-step hard-cap proof')
    guard.require(value['memory_events_before'] == value['memory_events_after'], 'initial events changed')
    guard.require(all(x == 0 for x in value['memory_events_before'].values()), 'initial nonzero memory event')
    return value['memory_events_before']


def observe(label):
    snapshot = guard.collect()
    # Persist observed facts even if the immediately following check fails.
    write(label + '-memory.json', {'memory_current_bytes': snapshot['memory_current'],
        'job_cumulative_memory_peak_bytes': snapshot['memory_peak'],
        'memory_events': snapshot['events'], 'oom_group': snapshot['controls']['memory.oom.group'],
        'peak_is_not_per_stage': True, 'memory_current_delta_may_be_zero': True})
    guard.verify(snapshot)
    guard.require(snapshot['events'] == baseline(), 'memory event changed; no next phase or automatic retry')
    return snapshot


def successful(result):
    return result == {'returncode': 0, 'timed_out': False, 'orphaned': False, 'clean': True}


def native_evidence():
    expected = {'ok': True, 'tests_run': 5, 'failures': 0,
        'errors': 0, 'skips': 0, 'owned_children_empty': True}
    guard.require(read_json(reports() / 'native-cases.json') == expected, 'five native cases not all proven')
    guard.require(read_json(reports() / 'native-complete.json') ==
        {'ok': True, 'native': expected, 'owned_children_empty': True}, 'native completion missing')
    guard.require(successful(read_json(reports() / 'native-owned.json')), 'native supervisor not clean')


def run_phase(name, command, environment, timeout):
    observe(name + '-before')
    result = owned.run_owned(command, cwd=ROOT, environment=environment,
        log_path=reports() / (name + '.log'), timeout=timeout)
    write(name + '-owned.json', result)
    observe(name + '-after')
    guard.require(successful(result), name + ' failed/timed out/left owned descendants; no next phase')
    guard.require(owned.linux_children() == [], name + ' left owned children')
    return result


def native():
    reports().mkdir(exist_ok=False)
    baseline()
    environment = dict(os.environ, DICTIONARY_REPORTS=str(reports()))
    run_phase('native', [sys.executable, '-I', '-B', str(CONTROL / 'native_lifecycle.py')], environment, 60)
    native_result = read_json(reports() / 'native-cases.json')
    guard.require(native_result == {'ok': True, 'tests_run': 5, 'failures': 0,
        'errors': 0, 'skips': 0, 'owned_children_empty': True}, 'five native cases not all proven')
    write('native-complete.json', {'ok': True, 'native': native_result, 'owned_children_empty': True})
    # setup-go has a separate one-minute timeout; do not let it use the reserve.
    guard.require(owned.remaining_timeout(60) >= 60, 'insufficient budget before bounded Go setup')


def install_fixtures():
    for source, destination in (
        ('resource_test.go', 'internal/localdictionary/dictionary_validation_resource_test.go'),
        ('localdictionary_offline_test.go', 'internal/localdictionary/dictionary_validation_offline_test.go')):
        with (ROOT / destination).open('xb') as stream:
            stream.write((CONTROL / 'fixtures' / source).read_bytes())


def require_resource_pass(path):
    with path.open('r', encoding='utf-8') as stream:
        log = stream.read((16 << 20) + 1)
    guard.require(len(log) <= 16 << 20, 'resource output exceeded bound')
    lines = log.splitlines()
    guard.require(lines.count('=== RUN   TestDictionaryValidationResource') == 1
        and sum(line.startswith('=== RUN ') for line in lines) == 1,
        'resource test did not run exactly once')
    guard.require(sum(line.startswith('--- PASS: TestDictionaryValidationResource (') for line in lines) == 1
        and lines.count('PASS') == 1 and not any(line.startswith(('--- FAIL:', '--- SKIP:', 'FAIL')) for line in lines),
        'resource test pass/skip/failure evidence invalid')


def workload():
    native_evidence()  # Missing, skipped or dirty native runs never enable Go.
    environment = dict(os.environ)
    run_phase('go-env', ['go', 'env', '-json', 'GOPATH', 'GOMODCACHE', 'GOCACHE'], environment, 10)
    caches = read_json(reports() / 'go-env.log')
    guard.require(set(caches) == {'GOPATH', 'GOMODCACHE', 'GOCACHE'} and
        all(isinstance(p, str) and Path(p).is_absolute() for p in caches.values()), 'invalid pinned Go cache paths')
    home_module = module('container_resource_home', ROOT / 'build/ci_home.py')
    home_record = None
    completed = []
    all_passed = False
    try:
        owned.remaining_timeout(120)
        with owned.defer_cancellation():
            home_record = home_module.create_home(os.environ['RUNNER_TEMP'])
        home = Path(home_record['home'])
        environment.update(caches)
        environment.update({'HOME': str(home), 'USERPROFILE': str(home),
            'XDG_CONFIG_HOME': str(home / '.config'), 'XDG_CACHE_HOME': str(home / '.cache'),
            'XDG_DATA_HOME': str(home / '.local/share'), 'XDG_STATE_HOME': str(home / '.local/state'),
            'APPDATA': str(home / 'AppData/Roaming'), 'LOCALAPPDATA': str(home / 'AppData/Local'),
            'GOMEMLIMIT': '1536MiB', 'DICTIONARY_RESOURCE_MODE': 'readonly-job-container'})
        install_fixtures()
        binary = Path(os.environ['RUNNER_TEMP']).resolve(strict=True) / 'dictionary-container-resource.test'
        guard.require(not binary.exists(), 'resource binary already exists')
        run_phase('compile', ['go', 'test', '-mod=readonly', '-tags', 'nogui', '-p=1', '-c', '-o', str(binary),
            './internal/localdictionary'], environment, 120)
        for stage in STAGES:
            # Same complete synthetic corpus at every target. A shorter budget
            # can fail a stage; it never silently shrinks the 100k/16MiB target.
            stage_env = dict(environment, DICTIONARY_RESOURCE_STAGE=stage)
            run_phase(stage, [str(binary), '-test.v', '-test.count=1', '-test.timeout=110s',
                '-test.run=^TestDictionaryValidationResource$'], stage_env, 120)
            require_resource_pass(reports() / (stage + '.log'))
            completed.append(stage)
        all_passed = True
    finally:
        with owned.finishing():
            if home_record is not None:
                # Never clean another home or before the owned tree was reaped.
                guard.require(owned.linux_children() == [], 'children remain; do not race home cleanup')
                home_module.remove_home(os.environ['RUNNER_TEMP'], home_record)
                write('home-cleanup.json', {'ok': True, 'identity_checked': True, 'owned_children_empty': True})
    if all_passed:
        observe('workload-final')
        write('workload-complete.json', {'ok': True, 'stages': completed,
            'owned_children_empty': True, 'home_cleanup': True, 'GOMEMLIMIT_is_only_soft': True})


def finalize():
    # No child launch, retry or kill here. An OOM-killed supervisor may leave
    # children adopted by container PID 1: only runner postStop can remove that
    # container. Missing phase markers must fail, never merely warn/skip.
    def alarm(_signum, _frame):
        raise RuntimeError('FINAL EVIDENCE FAILED: verification exceeded 10 seconds')
    signal.signal(signal.SIGALRM, alarm)
    signal.alarm(10)
    try:
        observe('acceptance-final')
        native_evidence()
        expected = {'ok': True, 'stages': list(STAGES), 'owned_children_empty': True,
            'home_cleanup': True, 'GOMEMLIMIT_is_only_soft': True}
        guard.require(read_json(reports() / 'workload-complete.json') == expected, 'workload/final cleanup marker missing')
        for name in ('native', 'go-env', 'compile', *STAGES):
            guard.require(successful(read_json(reports() / (name + '-owned.json'))), 'missing clean phase: ' + name)
        for stage in STAGES:
            require_resource_pass(reports() / (stage + '.log'))
        write('acceptance.json', {'in_container_checks_passed': True,
            'overall_acceptance': 'PENDING independent complete runner image and postStop deletion log verification',
            'oom_group_zero_is_not_group_one': True, 'no_automatic_retry_or_downgrade': True})
        print('PASS in-container phases; final acceptance still requires complete runner container-removal evidence')
    finally:
        signal.alarm(0)


if __name__ == '__main__':
    if sys.argv[1:] == ['finalize']:
        finalize()
    else:
        with owned.cancellation_signals():
            if sys.argv[1:] == ['native']:
                native()
            elif sys.argv[1:] == ['workload']:
                workload()
            else:
                raise SystemExit('expected native, workload or finalize')
