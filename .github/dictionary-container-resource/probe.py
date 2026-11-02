"""Small environment proof only; never create cgroups, children or Go workloads.

GitHub's runner creates the container before the first step. The job's outer
timeout is the only bound on image pull/initialization; this program's alarm is
only a short bound on its own proof. Failure never selects a weaker fallback.
"""
import gc
import json
import os
from pathlib import Path
import platform
import signal
import stat
import sys
import time

LIMIT = 2 * 1024 ** 3
PROBE_BYTES = 1 << 20
CGROUP = Path('/sys/fs/cgroup')
OUTPUT = 'dictionary-container-env-proof.json'


def require(ok, reason):
    if not ok:
        raise RuntimeError('ENVIRONMENT NOT RUN: ' + reason)


def read(path):
    with Path(path).open('r', encoding='utf-8') as stream:
        text = stream.read(65537)
    require(len(text) <= 65536, 'unexpectedly large kernel metadata')
    return text.strip()


def events(text):
    pairs = [line.split() for line in text.splitlines()]
    require(all(len(pair) == 2 and pair[1].isdigit() for pair in pairs), 'invalid memory event counters')
    result = {key: int(value) for key, value in pairs}
    require(len(result) == len(pairs), 'duplicate memory event counter')
    require({'oom', 'oom_kill', 'oom_group_kill'} <= result.keys(), 'missing OOM counters')
    return result


def collect():
    status = dict(line.split(':', 1) for line in read('/proc/self/status').splitlines() if ':' in line)
    sockets = []
    # Only stat/access the runner's standard socket paths. Never connect or
    # call Docker. ContainerInfo in runner v2.338.2 mounts this host socket.
    for name in ('/var/run/docker.sock', '/run/docker.sock'):
        try:
            metadata = os.stat(name)
        except FileNotFoundError:
            sockets.append({'path': name, 'present': False})
        else:
            sockets.append({'path': name, 'present': True,
                'socket': stat.S_ISSOCK(metadata.st_mode),
                'readable': os.access(name, os.R_OK, effective_ids=True),
                'writable': os.access(name, os.W_OK, effective_ids=True)})
    writable = {}
    # These are standard runner CI-output mounts, not additional host mounts.
    # Failure does not chown/chmod them or retry as root.
    for key in ('GITHUB_ENV', 'GITHUB_WORKSPACE', 'RUNNER_TEMP', 'RUNNER_TOOL_CACHE', 'HOME'):
        value = os.environ.get(key, '')
        require(value and Path(value).is_absolute(), 'missing absolute runner path: ' + key)
        path = Path(value)
        require(path.is_file() if key == 'GITHUB_ENV' else path.is_dir(), 'runner path missing: ' + key)
        mask = os.W_OK if key == 'GITHUB_ENV' else os.R_OK | os.W_OK | os.X_OK
        writable[key] = os.access(path, mask, effective_ids=True)
    return {
        'uid': list(os.getresuid()), 'gid': list(os.getresgid()), 'groups': os.getgroups(),
        'caps': {key: status.get(key, '').strip() for key in ('CapInh', 'CapPrm', 'CapEff', 'CapBnd', 'CapAmb')},
        'no_new_privs': status.get('NoNewPrivs', '').strip(),
        'seccomp': status.get('Seccomp', '').strip(),
        'proc_pid': os.readlink('/proc/self'), 'pid': os.getpid(),
        'nspid': status.get('NSpid', '').split(),
        'machine': platform.machine(), 'python': platform.python_version(),
        'sockets': sockets, 'docker_host_present': bool(os.environ.get('DOCKER_HOST')),
        'runner_paths_accessible': writable,
        'membership': read('/proc/self/cgroup'), 'mountinfo': read('/proc/self/mountinfo'),
        'controls': {name: read(CGROUP / name) for name in ('memory.max', 'memory.swap.max', 'memory.oom.group')},
        'control_writable': {name: os.access(CGROUP / name, os.W_OK, effective_ids=True)
                             for name in ('memory.max', 'memory.swap.max', 'memory.oom.group', 'cgroup.procs')},
        'memory_current': int(read(CGROUP / 'memory.current')),
        'memory_peak': int(read(CGROUP / 'memory.peak')),
        'events': events(read(CGROUP / 'memory.events')),
    }


def verify(snapshot):
    require(snapshot['uid'] == [1001] * 3 and snapshot['gid'] == [1001] * 3, 'UID/GID or saved identity is not the fixed non-root account')
    require(set(snapshot['groups']) <= {1001}, 'unexpected supplementary group')
    require(set(snapshot['caps']) == {'CapInh', 'CapPrm', 'CapEff', 'CapBnd', 'CapAmb'}, 'incomplete capability evidence')
    require(all(value and int(value, 16) == 0 for value in snapshot['caps'].values()), 'Linux capabilities are not all dropped')
    require(snapshot['no_new_privs'] == '1' and snapshot['seccomp'] == '2', 'no-new-privileges/default seccomp proof missing')
    require(snapshot['proc_pid'] == str(snapshot['pid']) and snapshot['nspid'] == [str(snapshot['pid'])], 'procfs PID namespace mismatch')
    require(snapshot['machine'] == 'x86_64' and snapshot['python'] == '3.13.18', 'unexpected image platform or Python version')
    require(not snapshot['docker_host_present'], 'unexpected Docker control endpoint environment')
    require(len(snapshot['sockets']) == 2 and {item['path'] for item in snapshot['sockets']} == {'/var/run/docker.sock', '/run/docker.sock'}, 'incomplete Docker socket evidence')
    require(all(not item['present'] or (item['socket'] and not item['readable'] and not item['writable']) for item in snapshot['sockets']), 'host Docker socket accessible or unexpected file type')
    require(set(snapshot['runner_paths_accessible']) == {'GITHUB_ENV', 'GITHUB_WORKSPACE', 'RUNNER_TEMP', 'RUNNER_TOOL_CACHE', 'HOME'} and all(snapshot['runner_paths_accessible'].values()), 'non-root standard CI output/tool/home mount not accessible')
    require(snapshot['membership'] == '0::/', 'not the expected private unified cgroup namespace')
    mounts = []
    for line in snapshot['mountinfo'].splitlines():
        fields, separator, tail = line.partition(' - ')
        left, right = fields.split(), tail.split()
        require(separator and len(left) >= 6 and len(right) >= 3, 'unparseable mountinfo')
        if left[4] == '/sys/fs/cgroup':
            mounts.append((left[3], left[5].split(','), right[0]))
    require(len(mounts) == 1 and mounts[0][0] == '/' and 'ro' in mounts[0][1] and 'rw' not in mounts[0][1] and mounts[0][2] == 'cgroup2', 'cgroup2 is not the expected read-only private mount')
    require(snapshot['controls']['memory.max'] == str(LIMIT) and snapshot['controls']['memory.swap.max'] == '0', 'missing exact 2 GiB kernel memory cap or zero swap')
    require(snapshot['controls']['memory.oom.group'] in ('0', '1'), 'invalid OOM group metadata')
    require(set(snapshot['control_writable']) == {'memory.max', 'memory.swap.max', 'memory.oom.group', 'cgroup.procs'} and not any(snapshot['control_writable'].values()), 'cgroup control writable or evidence incomplete')
    require(0 < snapshot['memory_current'] < LIMIT - (4 << 20), 'insufficient safe space for the tiny accounting observation')
    require(0 < snapshot['memory_peak'] <= LIMIT, 'invalid cumulative job memory peak')
    require(all(snapshot['events'][key] == 0 for key in ('oom', 'oom_kill', 'oom_group_kill')), 'container already experienced an OOM event')


def prove():
    before = collect()
    verify(before)
    # Exactly 1 MiB, touched once. This is not a pressure/OOM test or proof of
    # the dictionary workload. No Go compile, matcher or child process starts.
    data = bytearray(PROBE_BYTES)
    for offset in range(0, len(data), 4096):
        data[offset] = 1
    try:
        after = collect()
        verify(after)
        require(after['events'] == before['events'], 'memory events changed during the tiny proof')
    finally:
        del data
        gc.collect()
    # Peak includes all container setup/steps; never label it a per-stage peak.
    return {'schema': 1, 'scope': 'environment-only; no Go/dictionary/native-child-lifecycle acceptance',
        'configured_image': 'docker.io/library/python@sha256:38b3a52e74d49330ab2f55255503669bcbbd759c847472816131b807b83cf2a6',
        'runtime_image_digest_requires_runner_pull_log_check': True,
        'uid': before['uid'], 'gid': before['gid'], 'capabilities_zero': True,
        'no_new_privs': True, 'seccomp_filter': True, 'docker_socket_access': False,
        'runner_paths_accessible': before['runner_paths_accessible'], 'cgroup_readonly': True,
        'hard_limit_bytes': LIMIT, 'swap_limit_bytes': 0,
        'oom_group': before['controls']['memory.oom.group'],
        'probe_bytes': PROBE_BYTES, 'memory_current_before': before['memory_current'],
        'memory_current_during': after['memory_current'], 'job_cumulative_peak_bytes': after['memory_peak'],
        'memory_events_before': before['events'], 'memory_events_after': after['events'],
        'post_stop_cleanup': 'NOT YET VERIFIED: independently check complete runner Stop containers logs; removal warning is failure',
        'initialization': 'outer job timeout only; this step alarm does not include image pull',
        'host_pull_and_daemon_not_covered_by_container_memory_cap': True, 'ok': True}


def main():
    def timeout(_signum, _frame):
        raise RuntimeError('ENVIRONMENT NOT RUN: tiny proof exceeded 10 seconds')
    signal.signal(signal.SIGALRM, timeout)
    signal.alarm(10)
    start = time.monotonic()
    try:
        result = prove()
        result['proof_seconds'] = time.monotonic() - start
        root = Path(os.environ['RUNNER_TEMP']).resolve(strict=True)
        # A single new synthetic evidence file, never replace existing data.
        with (root / OUTPUT).open('x', encoding='utf-8') as stream:
            json.dump(result, stream, indent=2)
            stream.write('\n')
        print(json.dumps(result, sort_keys=True))
    except (OSError, RuntimeError, ValueError, KeyError) as error:
        print(json.dumps({'ok': False, 'classification': 'ENVIRONMENT NOT RUN', 'error': str(error), 'no_fallback': True}), file=sys.stderr)
        return 1
    finally:
        signal.alarm(0)
    return 0


if __name__ == '__main__':
    sys.exit(main())
