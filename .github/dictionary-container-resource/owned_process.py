"""Own a complete child tree, including browsers that start another session."""
from contextlib import contextmanager, ExitStack
import ctypes
import json
import math
import re
import os
from pathlib import Path
import signal
import subprocess
import sys
import time


CANCEL_SIGNALS = tuple(s for s in (signal.SIGINT, signal.SIGTERM, getattr(signal, 'SIGBREAK', None)) if s is not None)


def remaining_timeout(local_seconds, cleanup_seconds=30):
    """A shared monotonic cutoff includes checkout/setup; absent is not infinite."""
    keys=('DICTIONARY_JOB_START_MONOTONIC','DICTIONARY_JOB_DEADLINE_MONOTONIC','DICTIONARY_JOB_CAP_SECONDS')
    raw=[os.environ.get(key,'') for key in keys]
    if any(len(value)>32 or re.fullmatch(r'[0-9]+(?:\.[0-9]{1,6})?',value) is None for value in raw):
        raise RuntimeError('ENVIRONMENT NOT RUN: missing or malformed shared job deadline')
    start,deadline,cap=map(float,raw)
    now=time.monotonic()
    if not all(math.isfinite(value) for value in (start,deadline,cap,local_seconds,cleanup_seconds)) or cap not in (600,900) or start<0 or start>now+0.01 or abs((deadline-start)-(cap-180))>0.01 or local_seconds<=0 or not 20<=cleanup_seconds<=60:
        raise RuntimeError('ENVIRONMENT NOT RUN: invalid shared job budget')
    available=deadline-now-cleanup_seconds
    if available<1:
        raise RuntimeError('BUDGET NOT RUN: shared job deadline leaves no verified cleanup margin')
    return min(float(local_seconds),available)


class Cancelled(BaseException):
    def __init__(self, signum):
        self.signum = signum
        super().__init__('validation cancelled by signal ' + str(signum))


@contextmanager
def cancellation_signals():
    def cancel(signum, _frame):
        raise Cancelled(signum)
    previous = {s: signal.getsignal(s) for s in CANCEL_SIGNALS}
    for s in previous:
        signal.signal(s, cancel)
    try:
        yield
    finally:
        for s, handler in previous.items():
            signal.signal(s, handler)


@contextmanager
def defer_cancellation():
    # Finish recording/binding a just-created child before delivering cancel.
    pending = []
    previous = {s: signal.getsignal(s) for s in CANCEL_SIGNALS}
    def record(signum, _frame):
        pending.append(signum)
    for s in previous:
        signal.signal(s, record)
    try:
        yield
    finally:
        for s, handler in previous.items():
            signal.signal(s, handler)
    if pending:
        raise Cancelled(pending[0])


@contextmanager
def finishing():
    # A second cancellation must not interrupt cleanup of the first one.
    previous = {s: signal.getsignal(s) for s in CANCEL_SIGNALS}
    for s in previous:
        signal.signal(s, signal.SIG_IGN)
    try:
        yield
    finally:
        for s, handler in previous.items():
            signal.signal(s, handler)


def linux_subreaper():
    if sys.platform != 'linux' or not hasattr(os, 'pidfd_open') or not hasattr(signal, 'pidfd_send_signal'):
        raise RuntimeError('ENVIRONMENT NOT RUN: Linux subreaper and pidfd ownership controls required')
    # Never use PIDs read from a procfs mounted for another PID namespace.
    try:
        if int(os.readlink('/proc/self')) != os.getpid():
            raise RuntimeError('procfs PID namespace does not match this process')
        status = Path('/proc/self/status').read_text().splitlines()
        ns = next(line.split()[1:] for line in status if line.startswith('NSpid:'))
        if ns != [str(os.getpid())]:
            raise RuntimeError('procfs namespace identity is ambiguous')
        if linux_children():
            raise RuntimeError('supervisor already has unaccounted children')
        fd = os.pidfd_open(os.getpid())
        try:
            signal.pidfd_send_signal(fd, 0)
        finally:
            os.close(fd)
    except (OSError, ValueError, StopIteration, RuntimeError) as error:
        raise RuntimeError('ENVIRONMENT NOT RUN: owned-child identity/control preflight failed: ' + str(error)) from error
    libc = ctypes.CDLL(None, use_errno=True)
    # PR_SET_CHILD_SUBREAPER adopts orphaned grandchildren even if they setsid.
    if libc.prctl(36, 1, 0, 0, 0) != 0:
        raise OSError(ctypes.get_errno(), 'cannot establish owned child subreaper')
    actual = ctypes.c_int()
    if libc.prctl(37, ctypes.byref(actual), 0, 0, 0) != 0 or actual.value != 1:
        raise RuntimeError('ENVIRONMENT NOT RUN: subreaper setting was not retained')


def linux_children():
    return [int(p) for p in Path(f'/proc/self/task/{os.getpid()}/children').read_text().split()]


def linux_reap_owned():
    """Kill/reap only this supervisor's children, adopted or direct, by pidfd."""
    found = False
    deadline = time.monotonic() + 10
    while True:
        children = linux_children()
        if children:
            found = True
        for pid in children:
            try:
                before = Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()
                if int(before[1]) != os.getpid():
                    continue
                started = before[19]
                fd = os.pidfd_open(pid)
            except (ProcessLookupError, FileNotFoundError):
                continue
            try:
                # Open by stable identity, then recheck parenthood: the PID
                # may have been recycled between the children read and open.
                try:
                    fields = Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()
                except FileNotFoundError:
                    continue
                if int(fields[1]) != os.getpid() or fields[19] != started:
                    continue
                signal.pidfd_send_signal(fd, signal.SIGKILL)
            except ProcessLookupError:
                pass
            finally:
                os.close(fd)
        while True:
            try:
                pid, _ = os.waitpid(-1, os.WNOHANG)
            except ChildProcessError:
                break
            if not pid:
                break
        if not linux_children():
            return found
        if time.monotonic() >= deadline:
            raise RuntimeError('owned children did not exit; no next engine or phase permitted')
        time.sleep(.02)


class WindowsJob:
    """A new unnamed Job Object; never modify the runner's or system's job."""
    def __init__(self):
        from ctypes import wintypes as w
        self.kernel = ctypes.WinDLL('kernel32', use_last_error=True)
        self.kernel.CreateJobObjectW.argtypes = [ctypes.c_void_p, w.LPCWSTR]
        self.kernel.CreateJobObjectW.restype = w.HANDLE
        self.kernel.SetInformationJobObject.argtypes = [w.HANDLE, ctypes.c_int, ctypes.c_void_p, w.DWORD]
        self.kernel.SetInformationJobObject.restype = w.BOOL
        self.kernel.AssignProcessToJobObject.argtypes = [w.HANDLE, w.HANDLE]
        self.kernel.AssignProcessToJobObject.restype = w.BOOL
        self.kernel.TerminateJobObject.argtypes = [w.HANDLE, w.UINT]
        self.kernel.TerminateJobObject.restype = w.BOOL
        self.kernel.QueryInformationJobObject.argtypes = [w.HANDLE, ctypes.c_int, ctypes.c_void_p, w.DWORD, ctypes.c_void_p]
        self.kernel.QueryInformationJobObject.restype = w.BOOL
        self.kernel.CloseHandle.argtypes = [w.HANDLE]
        self.kernel.CloseHandle.restype = w.BOOL

        class Basic(ctypes.Structure):
            _fields_ = [('process_time', ctypes.c_int64), ('job_time', ctypes.c_int64), ('flags', w.DWORD),
                        ('minimum', ctypes.c_size_t), ('maximum', ctypes.c_size_t), ('active_limit', w.DWORD),
                        ('affinity', ctypes.c_size_t), ('priority', w.DWORD), ('scheduling', w.DWORD)]
        class Extended(ctypes.Structure):
            _fields_ = [('basic', Basic), ('io', ctypes.c_uint64 * 6), ('process_memory', ctypes.c_size_t),
                        ('job_memory', ctypes.c_size_t), ('peak_process', ctypes.c_size_t), ('peak_job', ctypes.c_size_t)]
        class Accounting(ctypes.Structure):
            _fields_ = [('user_time', ctypes.c_int64), ('kernel_time', ctypes.c_int64), ('period_user', ctypes.c_int64),
                        ('period_kernel', ctypes.c_int64), ('page_faults', w.DWORD), ('total', w.DWORD),
                        ('active', w.DWORD), ('terminated', w.DWORD)]
        self.Accounting = Accounting
        self.handle = self.kernel.CreateJobObjectW(None, None)
        if not self.handle:
            raise ctypes.WinError(ctypes.get_last_error())
        info = Extended()
        # JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE; no breakaway flag is enabled.
        # https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects
        info.basic.flags = 0x2000
        if not self.kernel.SetInformationJobObject(self.handle, 9, ctypes.byref(info), ctypes.sizeof(info)):
            error = ctypes.WinError(ctypes.get_last_error())
            self.close()
            raise error

    def assign(self, process):
        if not self.kernel.AssignProcessToJobObject(self.handle, int(process._handle)):
            raise ctypes.WinError(ctypes.get_last_error())

    def active(self):
        info = self.Accounting()
        if not self.kernel.QueryInformationJobObject(self.handle, 1, ctypes.byref(info), ctypes.sizeof(info), None):
            raise ctypes.WinError(ctypes.get_last_error())
        return info.active

    def reap(self):
        found = self.active() != 0
        if found and not self.kernel.TerminateJobObject(self.handle, 1):
            raise ctypes.WinError(ctypes.get_last_error())
        deadline = time.monotonic() + 10
        while self.active():
            if time.monotonic() >= deadline:
                raise RuntimeError('owned Windows Job Object did not empty')
            time.sleep(.02)
        return found

    def close(self):
        if self.handle:
            self.kernel.CloseHandle(self.handle)
            self.handle = None


def stop_direct(process):
    # Popen owns this process handle/child identity; do not enumerate by name.
    # This independent fallback runs even when tree inspection itself fails.
    if process is None:
        return
    try:
        if process.poll() is None:
            try:
                process.kill()
            except ProcessLookupError:
                pass
    finally:
        process.wait(timeout=10)


def run_owned(command, *, cwd, environment, log_path, timeout, stderr_path=None):
    """Any error/cancel enters cleanup; return only after owned children exit."""
    process = None
    job = None
    reaper_ready = False
    result = {'returncode': None, 'timed_out': False, 'orphaned': False, 'clean': False}
    with cancellation_signals():
        try:
            remaining_timeout(timeout)  # refuse before starting or binding any child
            if os.name == 'nt':
                job = WindowsJob()
            else:
                linux_subreaper()
                reaper_ready = True
            with ExitStack() as streams:
                log = streams.enter_context(Path(log_path).open('wb'))
                errors = streams.enter_context(Path(stderr_path).open('wb')) if stderr_path else subprocess.STDOUT
                with defer_cancellation():
                    effective_timeout = remaining_timeout(timeout)
                    if job:
                        # This child reads stdin but starts no process until
                        # bound to the job. No assign-after-start race.
                        process = subprocess.Popen([sys.executable, '-I', '-B', str(Path(__file__).resolve()), '--windows-child'],
                            cwd=cwd, env=environment, stdin=subprocess.PIPE, stdout=log, stderr=errors)
                        job.assign(process)
                        process.stdin.write(json.dumps(command).encode('utf-8') + b'\n')
                        process.stdin.close()
                    else:
                        process = subprocess.Popen(command, cwd=cwd, env=environment, stdout=log, stderr=errors, start_new_session=True)
                try:
                    result['returncode'] = process.wait(timeout=remaining_timeout(effective_timeout))
                except subprocess.TimeoutExpired:
                    result['timed_out'] = True
        finally:
            with finishing():
                try:
                    try:
                        if job:
                            result['orphaned'] = job.reap()
                        elif reaper_ready:
                            result['orphaned'] = linux_reap_owned()
                    finally:
                        # Even an unbound stdin-gated child is stopped if job
                        # assignment and subsequent job inspection both fail.
                        stop_direct(process)
                    result['clean'] = True
                finally:
                    if job:
                        job.close()
    return result


if __name__ == '__main__':
    if sys.argv[1:] == ['--check-budget']:
        remaining_timeout(1)
        print('Shared job deadline leaves cleanup margin')
        raise SystemExit(0)
    if sys.argv[1:] != ['--windows-child'] or os.name != 'nt':
        raise SystemExit('internal Windows job gate only')
    command = json.loads(sys.stdin.buffer.readline())
    if not isinstance(command, list) or not command or not all(isinstance(s, str) for s in command):
        raise SystemExit('invalid gated command')
    # CreateProcess descendants inherit the assigned job; no breakaway flags.
    raise SystemExit(subprocess.call(command))
