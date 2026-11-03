"""Bounded per-file accounting; two owned process groups and unchanged tests."""
import hashlib
import importlib.util
import json
import os
import re
import shutil
import signal
import subprocess
import tempfile
import time
from pathlib import Path

CONTROL = Path(__file__).resolve().parent
ROOT = CONTROL.parents[1]
SOURCE = ROOT / 'source'


def module(name):
    spec = importlib.util.spec_from_file_location(name, CONTROL / (name + '.py'))
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


def counts(tap, code):
    values = {k: [int(x) for x in re.findall(r'^# ' + k + r' (\d+)$', tap, re.M)]
              for k in ('tests', 'pass', 'fail', 'cancelled', 'skipped', 'todo')}
    complete = all(len(v) == 1 for v in values.values())
    values = {k: v[0] for k, v in values.items()} if complete else None
    consistent = bool(values and values['tests'] > 0 and
                      sum(values[k] for k in ('pass', 'fail', 'cancelled', 'skipped', 'todo')) == values['tests'])
    passed = bool(consistent and code == 0 and values['pass'] == values['tests'] and
                  all(values[k] == 0 for k in ('fail', 'cancelled', 'skipped', 'todo')))
    return {'counts': values, 'terminal_summary': complete, 'counts_consistent': consistent, 'passed': passed}


def kill_group(pid):
    # Only groups created below with start_new_session=True are ever registered.
    try:
        os.killpg(pid, signal.SIGKILL)
    except ProcessLookupError:
        pass


def observed_rss(pids):
    total = 0
    for path in Path('/proc').iterdir():
        if not path.name.isdigit():
            continue
        try:
            stat = (path / 'stat').read_text()
            fields = stat[stat.rfind(')') + 2:].split()
            if int(fields[2]) in pids:
                total += int(fields[21]) * os.sysconf('SC_PAGE_SIZE')
        except (OSError, ValueError, IndexError):
            continue
    return total


def validate_temp(tmp):
    assert all(not (p / '.git').exists() for p in (tmp, *tmp.parents))
    assert len(str(tmp / 'playwright-webkit-xxxxxxxx/inspector-pipe').encode()) < 108


def command_for(name):
    return ['node', '--test', '--test-reporter=tap', '--test-concurrency=1', name]


def environment_for(tmp, out):
    home = tmp / 'home'
    home.mkdir(mode=0o700)
    env = dict(os.environ, HOME=str(home), USERPROFILE=str(home),
               XDG_CONFIG_HOME=str(home / '.config'), XDG_CACHE_HOME=str(home / '.cache'),
               XDG_DATA_HOME=str(home / '.local/share'), XDG_STATE_HOME=str(home / '.local/state'),
               APPDATA=str(home / 'AppData/Roaming'), LOCALAPPDATA=str(home / 'AppData/Local'),
               TMPDIR=str(tmp), TMP=str(tmp), TEMP=str(tmp), NODE_PATH=str(CONTROL / 'node_modules'),
               NODE_OPTIONS='--require=' + str(CONTROL / 'offline.cjs'), ARTIFACT_DIR=str(out / 'screens'))
    for key in ('BROWSER', 'PLAYWRIGHT_CHANNEL', 'AUTH_FULL_REPORTS', 'AUTH_DIAG_TRACE',
                'AUTH_DIAG_CLICK', 'AUTH_DIAG_GEOMETRY', 'AUTH_WIDTH_PROBE'):
        env.pop(key, None)
    return env


def execute(manifest, records, report, source, node, budget):
    """Separate scheduler enables real harmless child-process control tests."""
    queue = list(manifest['selected'])
    active = {}
    stop = False
    reason = None
    peak = 0
    start = time.monotonic()
    file_index = {f['path']: f for f in manifest['files']}
    old_handlers = {}
    external = json.loads((CONTROL / 'external-current-witnesses.json').read_text())

    def terminate(signum, frame):
        nonlocal stop, reason
        stop, reason = True, 'cancelled'
        for pid in list(active):
            kill_group(pid)

    def save():
        elapsed = time.monotonic() - start
        payload = {
            'source': manifest['product'], 'batch': manifest['batch_id'],
            'playwright': '1.62.1', 'node': node,
            'elapsed_seconds': elapsed,
            'cumulative_before_batch': budget,
            'cumulative_test_seconds': budget['spent_test_seconds'] + elapsed,
            'external_current_witnesses': external,
            'job_elapsed_seconds': None, 'job_elapsed_status': 'pending official completed-job receipt',
            'files': records, 'peak_observed_rss': peak, 'stop_reason': reason,
            'selected_batch_passed': all(records[n]['state'] == 'passed' for n in manifest['selected']),
            'executed_in_this_batch_count': sum(v.get('executed_in_batch', False) for v in records.values()),
            'current_source_executed_file_count': sum(v['state'] != 'not_run' for v in records.values()),
            'carried_current_source_file_count': sum(v.get('carried_current_source_evidence', False) for v in records.values()),
            'current_source_terminal_pass_count': sum(v['state'] == 'passed' for v in records.values()),
            'current_source_not_run_count': sum(v['state'] == 'not_run' for v in records.values()),
            'complete_suite_passed': False, 'full_suite_gate': 'RED',
            'note': '32 new original files selected. Prior same-source31 passed/1 failed retained with direct prior ledger receipts, not executed again. Old94e24 are history only; whole827 and visual10 remain mandatory; aggregate RED.',
        }
        temp = report / 'ledger.tmp'
        temp.write_text(json.dumps(payload, indent=2) + '\n')
        temp.replace(report / 'ledger.json')

    def finish(pid, interrupted=False):
        item = active[pid]
        proc = item['proc']
        kill_group(pid)
        proc.wait(timeout=5)
        item['stream'].close()
        duration = time.monotonic() - item['started']
        result = counts((item['out'] / 'result.tap').read_text(errors='replace'), proc.returncode)
        state = ('timeout' if item['timed_out'] else 'interrupted' if interrupted else
                 'passed' if result['passed'] else 'failed' if result['terminal_summary'] else 'incomplete')
        result.update(state=state, exit=proc.returncode, process_reaped=True,
                      elapsed_seconds=duration, source=manifest['product'],
                      file_sha256=file_index[item['name']]['sha256'])
        records[item['name']].update(result)
        (item['out'] / 'summary.json').write_text(json.dumps(result, indent=2) + '\n')
        item['temporary'].cleanup()
        del active[pid]
        save()

    for sig in (signal.SIGTERM, signal.SIGINT):
        old_handlers[sig] = signal.signal(sig, terminate)
    save()
    try:
        while queue or active:
            elapsed = time.monotonic() - start
            rss = observed_rss(set(active))
            peak = max(peak, rss)
            size = sum(p.stat().st_size for p in report.rglob('*') if p.is_file())
            resource_limit = (rss > manifest['max_observed_process_rss_bytes'] or
                              size > manifest['max_report_bytes'] or
                              shutil.disk_usage(report).free < manifest['minimum_free_bytes'])
            if stop or elapsed >= manifest['batch_seconds'] or resource_limit:
                reason = reason or ('resource-limit' if resource_limit else 'batch-deadline')
                stop = True
                for pid in list(active):
                    kill_group(pid)
            while queue and len(active) < manifest['concurrency'] and not stop:
                name = queue.pop(0)
                assert hashlib.sha256((source / name).read_bytes()).hexdigest() == file_index[name]['sha256']
                key = hashlib.sha256(name.encode()).hexdigest()[:16]
                out = report / key
                out.mkdir()
                temporary = tempfile.TemporaryDirectory(prefix='ma.', dir=os.environ['RUNNER_TEMP'])
                stream = None
                try:
                    tmp = Path(temporary.name)
                    validate_temp(tmp)
                    env = environment_for(tmp, out)
                    stream = (out / 'result.tap').open('w')
                    command = command_for(name)
                    proc = subprocess.Popen(command, cwd=source, env=env, stdout=stream,
                                            stderr=subprocess.STDOUT, start_new_session=True)
                    # Register ownership before any subsequent operation that may fail.
                    active[proc.pid] = {'proc': proc, 'name': name, 'out': out, 'stream': stream,
                                        'temporary': temporary, 'started': time.monotonic(), 'timed_out': False}
                except Exception:
                    if stream is not None:
                        stream.close()
                    temporary.cleanup()
                    raise
                records[name].update(state='running', report=key, command=command, executed_in_batch=True)
                save()
            for pid, item in list(active.items()):
                proc = item['proc']
                if proc.poll() is None and time.monotonic() - item['started'] >= manifest['per_file_seconds']:
                    item['timed_out'] = True
                    kill_group(pid)
                if proc.poll() is not None:
                    finish(pid, interrupted=stop)
            if stop and not active:
                break
            if active:
                time.sleep(0.2)
    except Exception:
        reason = reason or 'controller-error'
        raise
    finally:
        cleanup_errors = []
        for pid in list(active):
            try:
                finish(pid, interrupted=True)
            except Exception as error:
                # One cleanup/report failure must not leave another owned group live.
                item = active.get(pid)
                if item is not None:
                    kill_group(pid)
                    try:
                        item['proc'].wait(timeout=5)
                    except Exception:
                        pass
                    item['stream'].close()
                    records[item['name']].update(state='incomplete', cleanup_error=True,
                                                 process_reaped=item['proc'].poll() is not None)
                    try:
                        item['temporary'].cleanup()
                    except Exception:
                        pass
                    del active[pid]
                cleanup_errors.append(type(error).__name__)
        if cleanup_errors:
            reason = 'cleanup-error'
        try:
            save()
        finally:
            for sig, old in old_handlers.items():
                signal.signal(sig, old)
        if cleanup_errors:
            raise RuntimeError('Owned cleanup was attempted for all children: ' + ', '.join(cleanup_errors))
    return not stop and all(records[name]['state'] == 'passed' for name in manifest['selected'])


def main():
    # Repeat exact source/runtime/cost admission immediately before execution.
    # Missing or mismatched execution/timing receipts fail closed even outside
    # the workflow gate. Separate sentinel semantic review is not product credit.
    gate = module('gate')
    manifest, budget = gate.validate()
    node = subprocess.check_output(['node', '--version'], text=True).strip()
    assert node == 'v22.23.4'
    assert json.loads((CONTROL / 'node_modules/playwright/package.json').read_text())['version'] == '1.62.1'
    records = module('migration').seed_records(manifest, node)
    report = ROOT / 'reports'
    report.mkdir(exist_ok=False)
    for name in ('prior-ledger.json', 'prior-outcome.json', 'prior-job.json',
                 'source-migration.json', 'cumulative-receipt.json', 'sentinel-outcome.json',
                 'sentinel-jobs.json', 'control-hashes.json', 'source.json',
                 'historical-evidence-index.json', 'open-obligations.json',
                 'external-current-witnesses.json', 'inventory.json'):
        shutil.copyfile(CONTROL / name, report / name)
    shutil.copyfile(CONTROL / 'node_modules/playwright-core/browsers.json', report / 'browser-revisions.json')
    batch_passed = execute(manifest, records, report, SOURCE, node, budget)
    subprocess.run(['git', 'diff', '--exit-code'], cwd=SOURCE, check=True)
    assert not subprocess.check_output(['git', 'status', '--porcelain'], cwd=SOURCE, text=True).strip()
    assert batch_passed, 'Batch has failed/incomplete files. Independent files continued; full-suite gate remains RED.'
    print('Selected batch completed; full-suite gate remains RED, not release acceptance.')


if __name__ == '__main__':
    main()
