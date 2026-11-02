"""Unprivileged delegated-cgroup guard; no sudo, policy change or OOM probe."""
from contextlib import contextmanager
import importlib.util
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time
import uuid

LIMIT = 2 * 1024 ** 3
spec=importlib.util.spec_from_file_location('dictionary_owned_process',Path(__file__).with_name('owned_process.py'))
owned=importlib.util.module_from_spec(spec)
spec.loader.exec_module(owned)

def fail(reason):
    raise RuntimeError('ENVIRONMENT NOT RUN: ' + reason + '; no unbounded fallback. Proposed alternative for separate review: 1000 entries / 256 KiB synthetic smoke only.')

def current_group():
    lines = Path('/proc/self/cgroup').read_text().splitlines()
    item = [line[3:] for line in lines if line.startswith('0::')]
    if len(item) != 1 or '..' in Path(item[0]).parts:
        fail('cgroup v2 membership cannot be safely resolved')
    root = Path('/sys/fs/cgroup')
    group = root / item[0].lstrip('/')
    if not group.is_dir() or not (group / 'cgroup.procs').is_file():
        fail('current cgroup is not mapped under /sys/fs/cgroup')
    return group

def create_group(parent):
    group = parent / ('magpie-dictionary-' + uuid.uuid4().hex)
    try:
        group.mkdir()
        if not all((group / f).is_file() for f in ('memory.max','memory.swap.max','memory.oom.group','cgroup.kill','memory.events','memory.peak')):
            fail('runner has no delegated hard memory/swap/kill/peak controls')
        for name,value in {'memory.max':str(LIMIT),'memory.swap.max':'0','memory.oom.group':'1'}.items():
            (group/name).write_text(value)
            if (group/name).read_text().strip() != value: fail('hard limit readback mismatch')
        return group
    except BaseException:
        if group.exists():
            group.rmdir()
        raise

def enter(group):
    (group/'cgroup.procs').write_text(str(os.getpid()))
    if current_group().resolve() != group.resolve(): fail('worker migration did not take effect')
    if (group/'memory.max').read_text().strip() != str(LIMIT): fail('worker hard limit disappeared')
    if (group/'memory.swap.max').read_text().strip() != '0': fail('worker swap cap disappeared')
    os.environ['DICTIONARY_RESOURCE_CGROUP'] = str(group)

def child():
    group=Path(sys.argv[2])
    enter(group)
    if sys.argv[1]=='--probe':
        # Small allocation verifies live accounting without touching the cap.
        data=bytearray(1<<20)
        print(json.dumps({'proof':'cgroup-v2-kernel-memory.max','limit_bytes':LIMIT,'swap_bytes':0,'current_bytes':int((group/'memory.current').read_text()),'synthetic_probe_bytes':len(data)}),flush=True)
        return
    os.execv(sys.argv[3],[sys.argv[3],'-test.v','-test.count=1','-test.timeout=110s','-test.run=^TestDictionaryValidationResource$'])

def cleanup_group(group):
    residual=bool((group/'cgroup.procs').read_text().strip())
    (group/'cgroup.kill').write_text('1')
    deadline=time.monotonic()+5
    while (group/'cgroup.procs').read_text().strip():
        if time.monotonic()>deadline: raise RuntimeError('owned cgroup did not empty; preserve for inspection')
        time.sleep(.05)
    print(json.dumps({'memory_peak_bytes':int((group/'memory.peak').read_text()),'memory_events':(group/'memory.events').read_text().splitlines(),'owned_cgroup_empty':True,'residual_members_killed':residual}),flush=True)
    group.rmdir()
    if residual: raise RuntimeError('guarded worker left descendants; stopped, no next resource stage')

@contextmanager
def guarded_group(parent):
    group=None
    try:
        owned.remaining_timeout(15)
        with owned.defer_cancellation():
            group=create_group(parent)
        yield group
    finally:
        with owned.finishing():
            if group is not None:
                cleanup_group(group)

def execute(group, args, timeout, env=None):
    process=None
    try:
        owned.remaining_timeout(timeout)
        with owned.defer_cancellation():
            effective_timeout=owned.remaining_timeout(timeout)
            process=subprocess.Popen([sys.executable,'-I','-B',str(Path(__file__).resolve()),*args],env=env,start_new_session=True)
        status=process.wait(timeout=owned.remaining_timeout(effective_timeout))
        if status: raise RuntimeError('guarded child failed; no automatic stress retry: '+str(status))
    finally:
        with owned.finishing():
            # The startup worker may not have joined the cgroup yet; its own
            # session is stopped first. guarded_group then empties only its
            # newly-created cgroup, including any descendants that setsid.
            if process is not None:
                try:
                    if process.poll() is None:
                        try:
                            os.killpg(process.pid,signal.SIGKILL)
                        except ProcessLookupError:
                            pass
                finally:
                    process.wait(timeout=10)

def main():
    if len(sys.argv)>1 and sys.argv[1] in ('--probe','--worker'):
        child();return
    if os.geteuid()==0: fail('guard must be usable as the ordinary runner account, not root')
    try:
        parent=current_group()
    except OSError as error:
        fail('runner cannot create delegated memory controls: ' + str(error))
    try:
        with guarded_group(parent) as group:
            execute(group,['--probe',str(group)],15)
    except OSError as error:
        fail('runner cannot create delegated memory controls: ' + str(error))
    if sys.argv[1:] == ['probe']: return
    if len(sys.argv)!=3 or sys.argv[1]!='run': raise ValueError('expected probe or run TEST_BINARY')
    binary=Path(sys.argv[2]).resolve(strict=True)
    if not binary.is_relative_to(Path(os.environ['RUNNER_TEMP']).resolve()): fail('test binary must be in RUNNER_TEMP')
    start=time.monotonic()
    for stage in ('100k','16mib','held','released'):
        remaining=min(480-(time.monotonic()-start),owned.remaining_timeout(120))
        if remaining<1: raise RuntimeError('BUDGET NOT RUN: resource work deadline expired before next full target stage')
        env=dict(os.environ,DICTIONARY_RESOURCE_STAGE=stage,GOMEMLIMIT='1536MiB')
        print(json.dumps({'stage':stage,'timeout_seconds':min(120,remaining),'hard_limit_bytes':LIMIT,'GOMEMLIMIT_is_only_soft':True}),flush=True)
        try:
            with guarded_group(parent) as group:
                execute(group,['--worker',str(group),str(binary)],min(120,remaining),env)
        except OSError as error:
            fail('delegated memory controls disappeared: ' + str(error))

if __name__=='__main__':
    try:
        with owned.cancellation_signals():
            main()
    except owned.Cancelled as error:
        print(str(error),file=sys.stderr)
        sys.exit(128+error.signum)
    except (OSError,ValueError,RuntimeError,subprocess.TimeoutExpired) as error:
        print(str(error),file=sys.stderr)
        sys.exit(1)
