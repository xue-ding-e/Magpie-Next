"""Browser-only child subreaper: reap exited adopted helpers, never kill a process."""
import importlib.util
import json
import re
import os
from pathlib import Path
import subprocess
import sys
import time

CONTROL = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('browser_reaper_owned', CONTROL / 'owned_process.py')
owned = importlib.util.module_from_spec(spec)
spec.loader.exec_module(owned)
ROOT = CONTROL.parents[1]
COMMAND = ['node', '--test', '--test-reporter=tap', '--test-concurrency=1',
           'internal/gui/tests/local-dictionary.test.cjs']


ERROR_CODES = frozenset(('CHILD_COUNT_LIMIT', 'NODE_PID_REAPPEARED', 'OWNERSHIP_UNVERIFIED', 'IDENTITY_CHANGED', 'LIVE_CHILD_AT_EXIT', 'ZOMBIE_NOT_REAPED', 'ABNORMAL_HELPER_EXIT', 'RESIDUAL_CHILDREN', 'BUDGET_EXHAUSTED'))

STAGES = frozenset(('CHILDREN_READ', 'FINAL_CHILDREN_READ', 'STAT_BEFORE',
                    'STAT_AFTER', 'PIDFD_OPEN', 'WAITPID', 'PIDFD_CLOSE',
                    'SUBREAPER', 'POPEN', 'POLL', 'BUDGET', 'MONOTONIC', 'SLEEP'))
EXCEPTION_CLASSES = frozenset(('OSError', 'FileNotFoundError', 'ProcessLookupError',
    'PermissionError', 'ChildProcessError', 'ValueError', 'TypeError',
    'RuntimeError', 'TimeoutExpired', 'Cancelled', 'InterruptedError'))
_failure_error = None
_failure_stage = None


def observed(stage, operation, *args, **kwargs):
    """Remember only the failing operation, so successful finally-close cannot mask it."""
    global _failure_error, _failure_stage
    try:
        return operation(*args, **kwargs)
    except (Exception, owned.Cancelled) as error:
        _failure_error, _failure_stage = error, stage
        raise


class ReaperError(RuntimeError):
    """Only fixed public failure codes; never carry process/user content."""
    def __init__(self, code):
        self.code = code if code in ERROR_CODES else 'UNEXPECTED_ERROR'
        super().__init__(self.code)


def report_failure(error):
    try:
        if isinstance(error, ReaperError):
            code = error.code if error.code in ERROR_CODES else 'UNEXPECTED_ERROR'
        elif isinstance(error, owned.Cancelled):
            code = 'CANCELLED'
        else:
            code = 'UNEXPECTED_ERROR'
        print('BROWSER REAPER FAILED: ' + code, file=sys.stderr, flush=True)
        if code == 'UNEXPECTED_ERROR':
            stage = _failure_stage if error is _failure_error and _failure_stage in STAGES else 'UNKNOWN'
            kind = type(error).__name__
            kind = kind if kind in EXCEPTION_CLASSES else 'OtherError'
            number = getattr(error, 'errno', None) if isinstance(error, OSError) else None
            number = number if type(number) is int and 0 <= number <= 4095 else None
            print('BROWSER REAPER OPERATION ' + json.dumps({
                'stage': stage, 'exception': kind, 'errno': number,
            }, sort_keys=True), file=sys.stderr, flush=True)
    except Exception:
        # Broken logging must never expose an original exception traceback.
        pass


def report_abnormal_helper(stat_line, status):
    """One bounded record immediately before fail-closed, after verified waitpid."""
    try:
        comm = stat_line.split('(', 1)[1].rsplit(')', 1)[0]
        comm = re.sub(r'[^A-Za-z0-9_.-]', '_', comm)[:32]
        record = {'owned': True, 'state': 'Z', 'comm': comm,
                  'exitcode': os.WEXITSTATUS(status) if os.WIFEXITED(status) else None,
                  'signal': os.WTERMSIG(status) if os.WIFSIGNALED(status) else None}
        print('BROWSER REAPER ABNORMAL_HELPER ' + json.dumps(record, sort_keys=True), file=sys.stderr, flush=True)
    except Exception:
        # Diagnostic failure cannot replace the strict helper failure below.
        pass


def reap_adopted(node_pid, *, final=False):
    """Only wait for stable direct children, never the Popen-managed node PID."""
    reaped = 0
    children = observed('CHILDREN_READ', owned.linux_children)
    if len(children) > 256:
        raise ReaperError('CHILD_COUNT_LIMIT')
    for pid in children:
        if pid == node_pid:
            if final:
                raise ReaperError('NODE_PID_REAPPEARED')
            continue
        before = observed('STAT_BEFORE', lambda: Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split())
        if len(before) < 20 or observed('STAT_BEFORE', int, before[1]) != os.getpid():
            raise ReaperError('OWNERSHIP_UNVERIFIED')
        fd = observed('PIDFD_OPEN', os.pidfd_open, pid)
        try:
            after_line = observed('STAT_AFTER', lambda: Path(f'/proc/{pid}/stat').read_text())
            after = observed('STAT_AFTER', lambda: after_line.rsplit(')', 1)[1].split())
            if len(after) < 20 or observed('STAT_AFTER', int, after[1]) != os.getpid() or before[19] != after[19]:
                raise ReaperError('IDENTITY_CHANGED')
            if after[0] != 'Z':
                if final:
                    raise ReaperError('LIVE_CHILD_AT_EXIT')
                continue
            # pidfd remains open while waitpid consumes this exact zombie.
            # No -1 wait: subprocess alone owns the node exit status.
            got, status = observed('WAITPID', os.waitpid, pid, os.WNOHANG)
            # A zombie group leader is not waitable until its subthreads exit
            # (Linux wait_consider_task/delay_group_leader). During node's
            # lifetime, WNOHANG=0 means pending, never a successful reap.
            # At final sweep it still fails immediately: no shutdown grace.
            if got == 0 and not final:
                continue
            if got != pid:
                raise ReaperError('ZOMBIE_NOT_REAPED')
            reaped += 1
            if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
                report_abnormal_helper(after_line, status)
                raise ReaperError('ABNORMAL_HELPER_EXIT')
        finally:
            observed('PIDFD_CLOSE', os.close, fd)
    if final and observed('FINAL_CHILDREN_READ', owned.linux_children):
        raise ReaperError('RESIDUAL_CHILDREN')
    return reaped


def run(command, *, cwd):
    # The API accepts an explicit command only for isolated synthetic proofs.
    # The executable entry point below exposes no command override.
    observed('BUDGET', owned.remaining_timeout, 300)
    observed('SUBREAPER', owned.linux_subreaper)
    deadline = observed('MONOTONIC', time.monotonic) + observed('BUDGET', owned.remaining_timeout, 300)
    reaped = 0
    with owned.cancellation_signals():
        process = observed('POPEN', subprocess.Popen, command, cwd=cwd)
        # Deliberately no terminate/kill/finally wait here. On uncertainty or
        # cancellation, exit and let the unchanged outer owner reclaim the tree.
        while True:
            code = observed('POLL', process.poll)
            if code is not None:
                reaped += reap_adopted(process.pid, final=True)
                print('# browser reaper verified reaped=' + str(reaped), flush=True)
                return code
            reaped += reap_adopted(process.pid)
            if observed('MONOTONIC', time.monotonic) >= deadline:
                raise ReaperError('BUDGET_EXHAUSTED')
            observed('SLEEP', time.sleep, .02)


if __name__ == '__main__':
    if sys.argv[1:] or sys.platform != 'linux':
        raise SystemExit('ENVIRONMENT NOT RUN: fixed Linux browser reaper only')
    try:
        result = run(COMMAND, cwd=ROOT)
    except (Exception, owned.Cancelled) as error:
        # Fixed errors only. Never print argv, process command lines or env.
        report_failure(error)
        raise SystemExit(1)
    raise SystemExit(result if result >= 0 else 1)
