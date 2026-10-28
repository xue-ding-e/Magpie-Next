"""Isolated two-budget experiment; never changes product refs or publishes artifacts."""
import hashlib
import json
import os
from pathlib import Path
import runpy
import signal
import subprocess
import sys
import time

SOURCE = 'c953886a7aed6a47499760f456df52f379846cf5'
FILE = 'internal/gateway/plugin_stream_regression_test.go'
BUDGET = '''\t// Race instrumentation adds work to every streamed chunk. Keep the client
\t// budget bounded without changing the separate stalled-client deadline.
\tif raceOn {
\t\tclient.Timeout = 3 * time.Minute
\t}
'''
NAMES = ['TestPluginStreamLargeReplies', 'TestPluginStreamLongActiveReader', 'TestPluginStreamStalledClientKeepsOtherRequestsMoving']
EXPECTED = set(NAMES + ['TestPluginStreamLargeReplies/' + s for s in ['JSON_fast', 'JSON_throttled', 'SSE_fast', 'SSE_throttled']])

def command(args, **kw):
    return subprocess.check_output(args, text=True, **kw).strip()

def sha(data):
    return hashlib.sha256(data).hexdigest()

def one(text, old, new):
    assert text.count(old) == 1, repr(old)
    return text.replace(old, new, 1)

def diagnostics(text):
    # Identical observational changes in both arms; payloads/assertions untouched.
    text = one(text, '\tp.read += int64(n)\n', '\tbefore := p.read\n\tp.read += int64(n)\n\tif p.read/(4<<20) != before/(4<<20) {\n\t\tfmt.Printf("CONTROL read_bytes=%d elapsed=%v rate=%d\\n", p.read, time.Since(p.started), p.rate)\n\t}\n')
    text = one(text, '\tproduced := make(chan time.Time, 1)\n', '\tproduced := make(chan time.Time, 1)\n\tvendorStart := time.Now()\n\tfmt.Printf("CONTROL case=%s size=%d stream=%v rate=%d\\n", t.Name(), size, stream, rate)\n')
    text = one(text, '\t\tcase produced <- time.Now():\n', '\t\tcase produced <- time.Now():\n\t\t\tfmt.Printf("CONTROL vendor_done size=%d elapsed=%v\\n", size, time.Since(vendorStart))\n')
    return text

def members(group):
    rows = command(['ps', '-axo', 'pid=,pgid=']).splitlines()
    return [int(row.split()[0]) for row in rows if int(row.split()[1]) == group]

def main():
    assert sys.platform == 'darwin', sys.platform
    assert command(['git', 'rev-parse', 'HEAD']) == SOURCE
    assert not command(['git', 'status', '--porcelain', '--untracked-files=no'])
    assert command(['go', 'env', 'GOVERSION']) == 'go1.26.10'
    assert command(['bun', '--version']) == '1.4.2'
    print(command(['go', 'version']), 'bun', command(['bun', '--version']), flush=True)
    fixture = Path(FILE)
    original = fixture.read_text()
    assert BUDGET not in original
    candidate = one(original, '\turl, model, client := pluginReplyGateway(t, up)\n\tstart := time.Now()', '\turl, model, client := pluginReplyGateway(t, up)\n' + BUDGET + '\tstart := time.Now()')
    assert sha(candidate.encode()) == '1678e9a2ae24918fbdd5683b0daed95b93f76af1a0012ff7adf0abb3984549ae'
    baseline_diag, candidate_diag = diagnostics(original), diagnostics(candidate)
    assert candidate_diag.replace(BUDGET, '', 1) == baseline_diag
    helper = runpy.run_path('build/ci_home.py')
    env_base = os.environ.copy()
    for key in ['GOPATH', 'GOMODCACHE', 'GOCACHE']:
        env_base[key] = command(['go', 'env', key])
    results = []
    try:
        for label, text in [('baseline75', baseline_diag), ('candidate180', candidate_diag)]:
            fixture.write_text(text)
            assert command(['git', 'diff', '--name-only']) == FILE
            record = helper['create_home'](os.environ['RUNNER_TEMP'])
            home = Path(record['home'])
            env = env_base.copy()
            for key, sub in {'HOME':'', 'USERPROFILE':'', 'XDG_CONFIG_HOME':'config', 'XDG_CACHE_HOME':'cache', 'XDG_DATA_HOME':'data', 'XDG_STATE_HOME':'state', 'APPDATA':'appdata', 'LOCALAPPDATA':'localappdata', 'TMPDIR':'tmp'}.items():
                target = home / sub
                target.mkdir(exist_ok=True)
                env[key] = str(target)
            output = Path(os.environ['RUNNER_TEMP']) / ('magpie-' + label + '.jsonl')
            print(json.dumps({'arm':label, 'source':SOURCE, 'original_sha256':sha(original.encode()), 'instrumented_sha256':sha(text.encode()), 'home':str(home)}), flush=True)
            args = ['go', 'test', '-race', '-json', '-count=1', '-timeout=20m', './internal/gateway', '-run', '^(' + '|'.join(NAMES) + ')$']
            proc = None
            cleanup_ok = True
            try:
                with output.open('w') as log:
                    proc = subprocess.Popen(args, env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
                    cursor = 0
                    deadline = time.monotonic() + 1500
                    try:
                        while proc.poll() is None:
                            if time.monotonic() >= deadline:
                                raise subprocess.TimeoutExpired(args, 1500)
                            time.sleep(5)
                            with output.open() as live:
                                live.seek(cursor)
                                chunk = live.read()
                                cursor = live.tell()
                            if chunk:
                                print(chunk, end='', flush=True)
                        rc = proc.returncode
                        with output.open() as live:
                            live.seek(cursor)
                            print(live.read(), end='', flush=True)
                    except subprocess.TimeoutExpired:
                        os.killpg(proc.pid, signal.SIGTERM)
                        try:
                            proc.wait(timeout=10)
                        except subprocess.TimeoutExpired:
                            os.killpg(proc.pid, signal.SIGKILL)
                            proc.wait(timeout=10)
                        rc = 124
                # No arm starts until its process group has entirely exited.
                remaining = members(proc.pid)
                if remaining:
                    cleanup_ok = False
                    print('CONTROL unexpected surviving process group', remaining, flush=True)
                    os.killpg(proc.pid, signal.SIGTERM)
                    time.sleep(2)
                    if members(proc.pid):
                        os.killpg(proc.pid, signal.SIGKILL)
                        time.sleep(1)
                    assert not members(proc.pid), 'process group did not exit'
                passed, skipped, failed, ran = set(), set(), set(), set()
                for line in output.read_text().splitlines():
                    try:
                        event = json.loads(line)
                    except json.JSONDecodeError:
                        continue
                    name, action = event.get('Test'), event.get('Action')
                    if name and action in ('pass','skip','fail','run'):
                        {'pass':passed,'skip':skipped,'fail':failed,'run':ran}[action].add(name)
                result = {'arm':label,'exit':rc,'passed':sorted(passed),'skipped':sorted(skipped),'failed':sorted(failed),'missing':sorted(EXPECTED-ran),'clean_process_exit':cleanup_ok}
                print('CONTROL RESULT ' + json.dumps(result), flush=True)
                results.append(result)
                assert cleanup_ok, 'unexpected survivor; comparison invalid, stop'
            finally:
                # Any exception after spawn must stop the owned process group
                # before deleting its isolated home. Unknown state retains it.
                try:
                    if proc is not None:
                        if proc.poll() is None or members(proc.pid):
                            try:
                                os.killpg(proc.pid, signal.SIGTERM)
                            except ProcessLookupError:
                                pass
                            try:
                                proc.wait(timeout=10)
                            except subprocess.TimeoutExpired:
                                os.killpg(proc.pid, signal.SIGKILL)
                                proc.wait(timeout=10)
                            if members(proc.pid):
                                os.killpg(proc.pid, signal.SIGKILL)
                                time.sleep(1)
                        assert proc.poll() is not None and not members(proc.pid), 'cannot confirm group exit'
                except BaseException:
                    print('CONTROL cleanup unverified: retaining isolated home ' + str(home), flush=True)
                    raise
                else:
                    helper['remove_home'](os.environ['RUNNER_TEMP'], record)
                    assert not home.exists()
    finally:
        fixture.write_text(original)
    assert not command(['git', 'status', '--porcelain', '--untracked-files=no'])
    print('CONTROL SUMMARY ' + json.dumps(results), flush=True)
    assert len(results) == 2
    for result in results:
        assert not result['missing'] and not result['skipped'], 'missing/skipped tests invalidate comparison'
    result = results[1]
    assert result['exit'] == 0 and not result['failed'] and set(result['passed']) == EXPECTED, 'candidate failed'
    # A green comparison does NOT waive a failed full-suite or establish root cause.

if __name__ == '__main__':
    main()
