"""Pure local/mock checks; no Docker, Go, native-child or remote execution."""
import ast
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import unittest
from unittest import mock

CONTROL = Path(__file__).resolve().parent
ROOT = CONTROL.parents[1]


def module(name):
    spec = importlib.util.spec_from_file_location('container_env_' + name, CONTROL / (name + '.py'))
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


probe = module('probe')


def job_context_allowed(context):
    # Evaluate the actual workflow expression, not a separately restated guard.
    # Only this small equality/and/or subset is accepted; no general eval.
    # GitHub string == is case-insensitive. Our expected context strings are
    # ASCII; case aliases may reach initialization and are rejected by gate.py.
    text = (ROOT / '.github/workflows/local-dictionary-container-env.yml').read_text()
    conditions = re.findall(r'^    if: \$\{\{ (.+) \}\}$', text, re.M)
    if len(conditions) != 1:
        raise AssertionError('expected one pre-initialization job guard')
    tree = ast.parse(conditions[0].replace('&&', 'and').replace('||', 'or'), mode='eval')
    def visit(node):
        if isinstance(node, ast.Expression):
            return visit(node.body)
        if isinstance(node, ast.BoolOp) and isinstance(node.op, (ast.And, ast.Or)):
            values = [visit(value) for value in node.values]
            return all(values) if isinstance(node.op, ast.And) else any(values)
        if isinstance(node, ast.Compare) and len(node.ops) == 1 and isinstance(node.ops[0], ast.Eq):
            left, right = visit(node.left), visit(node.comparators[0])
            return left.casefold() == right.casefold() if isinstance(left, str) and isinstance(right, str) else left == right
        if isinstance(node, ast.Attribute) and isinstance(node.value, ast.Name) and node.value.id == 'github' and node.attr in ('repository', 'ref', 'event_name'):
            return context.get(node.attr, '')
        if isinstance(node, ast.Constant) and isinstance(node.value, str):
            return node.value
        raise AssertionError('unsupported job guard expression')
    return visit(tree)


def fixture():
    return {'uid': [1001] * 3, 'gid': [1001] * 3, 'groups': [1001],
        'caps': dict.fromkeys(('CapInh', 'CapPrm', 'CapEff', 'CapBnd', 'CapAmb'), '0000000000000000'),
        'no_new_privs': '1', 'seccomp': '2', 'proc_pid': '11', 'pid': 11, 'nspid': ['11'],
        'machine': 'x86_64', 'python': '3.13.18', 'docker_host_present': False,
        'sockets': [{'path': path, 'present': True, 'socket': True, 'readable': False, 'writable': False}
                    for path in ('/var/run/docker.sock', '/run/docker.sock')],
        'runner_paths_accessible': dict.fromkeys(('GITHUB_ENV', 'GITHUB_WORKSPACE', 'RUNNER_TEMP', 'RUNNER_TOOL_CACHE', 'HOME'), True),
        'membership': '0::/', 'mountinfo': '34 33 0:29 / /sys/fs/cgroup ro,nosuid,nodev,noexec,relatime - cgroup2 cgroup rw',
        'controls': {'memory.max': str(probe.LIMIT), 'memory.swap.max': '0', 'memory.oom.group': '0'},
        'control_writable': dict.fromkeys(('memory.max', 'memory.swap.max', 'memory.oom.group', 'cgroup.procs'), False),
        'memory_current': 20 << 20, 'memory_peak': 30 << 20,
        'events': {'low': 0, 'high': 0, 'max': 0, 'oom': 0, 'oom_kill': 0, 'oom_group_kill': 0}}


class EnvironmentProof(unittest.TestCase):
    def test_frozen_active_source_and_gate(self):
        source = json.loads((CONTROL / 'source.json').read_text())
        self.assertTrue(source['enabled'])
        self.assertEqual(source['source'], '203a1811105a97fa46cb62fcf9706f44535b0ebd')
        self.assertEqual(source['source_tree'], '0ceb41a3c81cf0b562f902fc8ad688733cdbf393')
        self.assertEqual(len(source['files']), 17)
        self.assertEqual(source['branch'], 'refs/heads/validation/local-dictionary-container-env-20261102')
        gate = module('gate')
        with mock.patch.dict(gate.os.environ, {}, clear=True), mock.patch.object(gate.subprocess, 'check_output') as launch:
            with self.assertRaisesRegex(SystemExit, 'wrong repository'):
                gate.main()
            launch.assert_not_called()

    def test_hashes_syntax_and_exact_inline_probe(self):
        hashes = json.loads((CONTROL / 'control-hashes.json').read_text())
        actual = {p.relative_to(ROOT).as_posix() for p in (ROOT / '.github').rglob('*') if p.is_file()}
        self.assertEqual(set(hashes), actual - {'.github/dictionary-container-env/control-hashes.json'})
        for path, digest in hashes.items():
            self.assertEqual(hashlib.sha256((ROOT / path).read_bytes()).hexdigest(), digest)
        for path in CONTROL.glob('*.py'):
            ast.parse(path.read_text())
        workflow = (ROOT / '.github/workflows/local-dictionary-container-env.yml').read_text()
        inline = workflow.split("          python3 -I -B - <<'PYPROBE'\n", 1)[1].split('          PYPROBE\n', 1)[0]
        self.assertEqual(''.join(line[10:] for line in inline.splitlines(keepends=True)), (CONTROL / 'probe.py').read_text())

    def test_one_active_job_and_narrow_options(self):
        text = (ROOT / '.github/workflows/local-dictionary-container-env.yml').read_text()
        source = json.loads((CONTROL / 'source.json').read_text())
        self.assertEqual(re.findall(r'^  ([a-z_-]+):\s*$', text, re.M)[-1:], ['container-env-proof'])
        self.assertIn("    if: ${{ github.repository == 'xue-ding-e/Magpie-Next' && github.ref == 'refs/heads/validation/local-dictionary-container-env-20261102' && (github.event_name == 'push' || github.event_name == 'workflow_dispatch') }}", text)
        self.assertIn('    timeout-minutes: 5', text)
        self.assertIn('      image: ' + source['container']['image'], text)
        self.assertIn('      options: ' + source['container']['options'], text)
        self.assertIn('branches: [validation/local-dictionary-container-env-20261102]', text)
        self.assertIn('  contents: read', text)
        self.assertEqual(set(re.findall(r'uses: (actions/[^\s]+)', text)), set(source['actions']))
        for forbidden in ('contents: write', '${{ secrets.', 'sudo ', '--privileged', '--cap-add', '--group-add', '--pid=host', '--cgroupns=host', 'setup-go@', 'workflow_run:', 'pull_request_target:'):
            self.assertNotIn(forbidden, text)
        self.assertNotRegex(text, r'(?m)^\s+(?:credentials|volumes):')
        tree = ast.parse((CONTROL / 'probe.py').read_text())
        imports = {node.names[0].name.split('.')[0] for node in ast.walk(tree) if isinstance(node, ast.Import)}
        self.assertFalse(imports & {'subprocess', 'socket', 'urllib', 'requests'})
        self.assertNotIn('write_text(', (CONTROL / 'probe.py').read_text())
        self.assertNotIn('os.kill', (CONTROL / 'probe.py').read_text())

    def test_job_guard_accepts_only_the_exact_two_events(self):
        context = {'repository': 'xue-ding-e/Magpie-Next', 'ref': 'refs/heads/validation/local-dictionary-container-env-20261102'}
        for event in ('push', 'workflow_dispatch'):
            self.assertTrue(job_context_allowed({**context, 'event_name': event}))

    def test_job_guard_rejects_wrong_context_before_initialization(self):
        context = {'repository': 'xue-ding-e/Magpie-Next', 'ref': 'refs/heads/validation/local-dictionary-container-env-20261102', 'event_name': 'workflow_dispatch'}
        cases = [('repository', 'yetone/magpie'), ('repository', 'other/Magpie-Next'),
                 ('ref', 'refs/heads/main'), ('ref', 'refs/tags/v0.0.0'),
                 ('ref', 'refs/heads/validation/local-dictionary-core-20261102'),
                 ('event_name', 'pull_request_target'), ('event_name', 'workflow_run'),
                 ('event_name', 'schedule')]
        for key, value in cases:
            with self.subTest(key=key, value=value):
                self.assertFalse(job_context_allowed({**context, key: value}))
        for key in context:
            self.assertFalse(job_context_allowed({name: value for name, value in context.items() if name != key}))

    def test_case_aliases_can_initialize_but_exact_source_gate_rejects(self):
        context = {'repository': 'xue-ding-e/Magpie-Next', 'ref': 'refs/heads/validation/local-dictionary-container-env-20261102', 'event_name': 'workflow_dispatch'}
        keys = {'repository': 'GITHUB_REPOSITORY', 'ref': 'GITHUB_REF', 'event_name': 'GITHUB_EVENT_NAME'}
        errors = {'repository': 'wrong repository', 'ref': 'wrong candidate branch', 'event_name': 'wrong event'}
        gate = module('gate')
        for key in context:
            alias = {**context, key: context[key].upper()}
            with self.subTest(key=key):
                self.assertTrue(job_context_allowed(alias))
                environment = {keys[name]: value for name, value in alias.items()}
                with mock.patch.dict(gate.os.environ, environment, clear=True), mock.patch.object(gate.subprocess, 'check_output') as launch:
                    with self.assertRaisesRegex(SystemExit, errors[key]):
                        gate.main()
                    launch.assert_not_called()

    def test_valid_metadata_and_small_proof_only(self):
        before, after = fixture(), fixture()
        after['memory_current'] += probe.PROBE_BYTES
        with mock.patch.object(probe, 'collect', side_effect=[before, after]):
            result = probe.prove()
        self.assertTrue(result['ok'])
        self.assertEqual(result['probe_bytes'], 1 << 20)
        self.assertEqual(result['oom_group'], '0')
        self.assertIn('NOT YET VERIFIED', result['post_stop_cleanup'])
        self.assertTrue(result['host_pull_and_daemon_not_covered_by_container_memory_cap'])

    def test_identity_capability_namespace_rejections(self):
        cases = [('uid', [0] * 3), ('gid', [0] * 3), ('groups', [1001, 999]),
                 ('no_new_privs', '0'), ('seccomp', '0'), ('proc_pid', '99'),
                 ('nspid', ['888', '11']), ('machine', 'aarch64'), ('python', '3.13.17'),
                 ('docker_host_present', True)]
        for key, value in cases:
            with self.subTest(key=key):
                snapshot = fixture(); snapshot[key] = value
                with self.assertRaises(RuntimeError): probe.verify(snapshot)
        for key in fixture()['caps']:
            snapshot = fixture(); snapshot['caps'][key] = '0000000000000001'
            with self.assertRaises(RuntimeError): probe.verify(snapshot)

    def test_socket_and_standard_mount_rejections(self):
        for key, value in (('socket', False), ('readable', True), ('writable', True)):
            snapshot = fixture(); snapshot['sockets'][0][key] = value
            with self.assertRaisesRegex(RuntimeError, 'Docker socket'): probe.verify(snapshot)
        for key in fixture()['runner_paths_accessible']:
            snapshot = fixture(); snapshot['runner_paths_accessible'][key] = False
            with self.assertRaisesRegex(RuntimeError, 'mount not accessible'): probe.verify(snapshot)

    def test_cgroup_mapping_and_hard_limit_rejections(self):
        cases = [('membership', '0::/hosted-agent'), ('mountinfo', 'bad'),
                 ('mountinfo', fixture()['mountinfo'].replace('ro,nosuid', 'rw,nosuid')),
                 ('mountinfo', fixture()['mountinfo'].replace(' - cgroup2 ', ' - tmpfs ')),
                 ('memory_current', probe.LIMIT), ('memory_peak', probe.LIMIT + 1)]
        for key, value in cases:
            with self.subTest(key=key):
                snapshot = fixture(); snapshot[key] = value
                with self.assertRaises(RuntimeError): probe.verify(snapshot)
        for key, value in (('memory.max', 'max'), ('memory.max', str(probe.LIMIT * 2)), ('memory.swap.max', '1')):
            snapshot = fixture(); snapshot['controls'][key] = value
            with self.assertRaisesRegex(RuntimeError, '2 GiB'): probe.verify(snapshot)
        for key in fixture()['control_writable']:
            snapshot = fixture(); snapshot['control_writable'][key] = True
            with self.assertRaisesRegex(RuntimeError, 'control writable'): probe.verify(snapshot)

    def test_incomplete_evidence_is_not_vacuously_safe(self):
        for key, value in (('caps', {}), ('sockets', []), ('runner_paths_accessible', {}), ('control_writable', {})):
            snapshot = fixture(); snapshot[key] = value
            with self.assertRaises(RuntimeError): probe.verify(snapshot)

    def test_oom_before_or_during_proof_is_failure(self):
        for key in ('oom', 'oom_kill', 'oom_group_kill'):
            snapshot = fixture(); snapshot['events'][key] = 1
            with self.assertRaisesRegex(RuntimeError, 'already experienced'): probe.verify(snapshot)
        before, after = fixture(), fixture(); after['events']['max'] = 1
        with mock.patch.object(probe, 'collect', side_effect=[before, after]):
            with self.assertRaisesRegex(RuntimeError, 'events changed'): probe.prove()

    def test_invalid_environment_never_allocates_probe(self):
        snapshot = fixture(); snapshot['uid'] = [0] * 3
        with mock.patch.object(probe, 'collect', return_value=snapshot), mock.patch.object(probe, 'bytearray', create=True) as allocate:
            with self.assertRaises(RuntimeError): probe.prove()
            allocate.assert_not_called()

    def test_event_parser_rejects_missing_duplicate_and_invalid(self):
        good = 'oom 0\noom_kill 0\noom_group_kill 0'
        self.assertEqual(probe.events(good)['oom'], 0)
        for text in ('oom 0', good + '\noom 0', good.replace('oom 0', 'oom -1'), good + '\nunknown'):
            with self.assertRaises(RuntimeError): probe.events(text)


if __name__ == '__main__':
    unittest.main()
