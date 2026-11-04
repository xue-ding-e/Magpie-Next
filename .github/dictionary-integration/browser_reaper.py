"""Browser-only child subreaper: reap exited adopted helpers, never kill a process."""
import importlib.util
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


def reap_adopted(node_pid, *, final=False):
    """Only wait for stable direct children, never the Popen-managed node PID."""
    reaped = 0
    children = owned.linux_children()
    if len(children) > 256:
        raise RuntimeError('too many adopted children to verify')
    for pid in children:
        if pid == node_pid:
            if final:
                raise RuntimeError('node PID reappeared after Popen reaped it')
            continue
        before = Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()
        if len(before) < 20 or int(before[1]) != os.getpid():
            raise RuntimeError('adopted child ownership not verified')
        fd = os.pidfd_open(pid)
        try:
            after = Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()
            if len(after) < 20 or int(after[1]) != os.getpid() or before[19] != after[19]:
                raise RuntimeError('adopted child identity changed')
            if after[0] != 'Z':
                if final:
                    raise RuntimeError('live adopted child after node exit')
                continue
            # pidfd remains open while waitpid consumes this exact zombie.
            # No -1 wait: subprocess alone owns the node exit status.
            got, status = os.waitpid(pid, os.WNOHANG)
            if got != pid:
                raise RuntimeError('adopted zombie was not reaped')
            reaped += 1
            if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
                raise RuntimeError('adopted helper did not exit normally')
        finally:
            os.close(fd)
    if final and owned.linux_children():
        raise RuntimeError('adopted children remain after final sweep')
    return reaped


def run(command, *, cwd):
    # The API accepts an explicit command only for isolated synthetic proofs.
    # The executable entry point below exposes no command override.
    owned.remaining_timeout(300)
    owned.linux_subreaper()
    deadline = time.monotonic() + owned.remaining_timeout(300)
    reaped = 0
    with owned.cancellation_signals():
        process = subprocess.Popen(command, cwd=cwd)
        # Deliberately no terminate/kill/finally wait here. On uncertainty or
        # cancellation, exit and let the unchanged outer owner reclaim the tree.
        while True:
            code = process.poll()
            if code is not None:
                reaped += reap_adopted(process.pid, final=True)
                print('# browser reaper verified reaped=' + str(reaped), flush=True)
                return code
            reaped += reap_adopted(process.pid)
            if time.monotonic() >= deadline:
                raise RuntimeError('browser reaper shared budget exhausted')
            time.sleep(.02)


if __name__ == '__main__':
    if sys.argv[1:] or sys.platform != 'linux':
        raise SystemExit('ENVIRONMENT NOT RUN: fixed Linux browser reaper only')
    try:
        result = run(COMMAND, cwd=ROOT)
    except (Exception, owned.Cancelled) as error:
        # Fixed errors only. Never print argv, process command lines or env.
        print('BROWSER REAPER FAILED: ' + type(error).__name__, file=sys.stderr)
        raise SystemExit(1)
    raise SystemExit(result if result >= 0 else 1)
