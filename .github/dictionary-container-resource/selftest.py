"""Lightweight mocks only: no API, Popen, native lifecycle, Go or large corpus."""
import ast
import copy
from datetime import datetime, timezone
import hashlib
import importlib.util
import io
import json
import os
import re
from pathlib import Path
import tempfile
import unittest
from unittest import mock

CONTROL = Path(__file__).resolve().parent
ROOT = CONTROL.parents[1]


def module(name, file):
    spec = importlib.util.spec_from_file_location(name, CONTROL / file)
    value = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(value)
    return value


budget = module('resource_budget_test', 'budget.py')
runner = module('resource_runner_test', 'runner.py')


def job_context_allowed(context):
    # Evaluate the actual workflow expression, not a separately restated guard.
    # Only this small equality/and/or subset is accepted; no general eval.
    # GitHub string == is case-insensitive. Our expected context strings are
    # ASCII; case aliases may reach initialization and are rejected by budget.py/gate.py before workload acceptance.
    text = (ROOT / '.github/workflows/local-dictionary-container-resource.yml').read_text()
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


def metadata():
    env = {'GITHUB_REPOSITORY': budget.REPO, 'GITHUB_REF': 'refs/heads/' + budget.BRANCH,
        'GITHUB_EVENT_NAME': 'push', 'GITHUB_JOB': budget.JOB, 'GITHUB_RUN_ID': '12345',
        'GITHUB_RUN_ATTEMPT': '1', 'GITHUB_SHA': 'a' * 40}
    run = {'id': 12345, 'run_attempt': 1, 'head_sha': 'a' * 40, 'head_branch': budget.BRANCH,
        'repository': {'full_name': budget.REPO}, 'path': budget.WORKFLOW, 'event': 'push', 'status': 'in_progress'}
    job = {'id': 876, 'run_id': 12345, 'head_sha': 'a' * 40, 'name': budget.JOB,
        'status': 'in_progress', 'conclusion': None, 'started_at': '2026-11-02T20:00:00Z'}
    utc = datetime(2026, 11, 2, 20, 0, tzinfo=timezone.utc).timestamp()
    return env, run, {'total_count': 1, 'jobs': [job]}, utc


class Deadline(unittest.TestCase):
    def test_elapsed_initialization_is_deducted(self):
        env, run, jobs, utc = metadata()
        value = budget.derive(run, jobs, env, utc + 40, 1000)
        self.assertEqual(value['start_monotonic'], 960)
        self.assertEqual(value['deadline_monotonic'], 1380)
        self.assertEqual(value['cleanup_reserve_seconds'], 180)
        self.assertTrue(value['includes_container_initialization'])

    def test_environment_exactness_and_no_reruns(self):
        env, run, jobs, utc = metadata()
        variants = {'GITHUB_REPOSITORY': budget.REPO.upper(), 'GITHUB_REF': 'refs/heads/main',
            'GITHUB_EVENT_NAME': 'pull_request', 'GITHUB_JOB': 'other', 'GITHUB_RUN_ID': '12/3',
            'GITHUB_RUN_ATTEMPT': '2', 'GITHUB_SHA': 'G' * 40}
        for key, value in variants.items():
            with self.subTest(key=key), self.assertRaises(RuntimeError):
                budget.derive(run, jobs, dict(env, **{key: value}), utc + 5, 1000)

    def test_wrong_run_source_attempt_or_event_fail(self):
        env, run, jobs, utc = metadata()
        for key, value in {'id': 1, 'run_attempt': 2, 'head_sha': 'b' * 40,
            'head_branch': 'main', 'repository': {'full_name': 'other/repo'},
            'path': 'other.yml', 'event': 'workflow_dispatch', 'status': 'completed'}.items():
            with self.subTest(key=key), self.assertRaises(RuntimeError):
                budget.derive(dict(run, **{key: value}), jobs, env, utc + 5, 1000)

    def test_duplicate_missing_mismatched_or_terminal_jobs_fail(self):
        env, run, jobs, utc = metadata()
        variants = [{'total_count': 0, 'jobs': []}, {'total_count': 2, 'jobs': jobs['jobs'] * 2}]
        for key, value in {'name': 'other', 'run_id': 1, 'head_sha': 'b' * 40,
            'status': 'completed', 'conclusion': 'success', 'started_at': None, 'id': 0}.items():
            variants.append({'total_count': 1, 'jobs': [dict(jobs['jobs'][0], **{key: value})]})
        for variant in variants:
            with self.subTest(variant=variant), self.assertRaises(RuntimeError):
                budget.derive(run, variant, env, utc + 5, 1000)

    def test_expired_future_nonfinite_and_setup_margin_fail(self):
        env, run, jobs, utc = metadata()
        for elapsed in (-1, 240, 400, 601, float('nan'), float('inf')):
            with self.subTest(elapsed=elapsed), self.assertRaises(RuntimeError):
                budget.derive(run, jobs, env, utc + elapsed, 1000)

    def test_redirect_and_duplicate_json_never_fall_back(self):
        with self.assertRaises(RuntimeError):
            budget.NoRedirect().redirect_request(None)
        with self.assertRaises(RuntimeError):
            json.loads('{"a":1,"a":2}', object_pairs_hook=budget.unique_pairs)

    def test_public_fetch_is_bounded_and_has_no_credentials(self):
        response = mock.MagicMock()
        response.__enter__.return_value = response
        response.status = 200
        response.geturl.return_value = 'https://api.github.com/repos/' + budget.REPO + '/synthetic'
        response.headers = {'Date': 'Mon, 02 Nov 2026 20:00:00 GMT'}
        response.read.return_value = b'{}'
        opener = mock.Mock()
        opener.open.return_value = response
        utc = metadata()[3]
        with mock.patch.object(budget.urllib.request, 'build_opener', return_value=opener), \
             mock.patch.object(budget.time, 'monotonic', side_effect=[1000, 1000.25]), \
             mock.patch.object(budget.time, 'time', return_value=utc):
            data, upper, observed_mono = budget.fetch('/synthetic')
        self.assertEqual(data, {})
        self.assertEqual(upper, utc + 1.25)
        self.assertEqual(observed_mono, 1000.25)
        request = opener.open.call_args.args[0]
        self.assertNotIn('Authorization', dict(request.header_items()))
        self.assertEqual(opener.open.call_args.kwargs['timeout'], 4)
        response.read.assert_called_once_with(budget.MAX_BODY + 1)

    def test_lagging_clock_and_post_fetch_pause_do_not_extend_deadline(self):
        env, run, jobs, utc = metadata()
        # API observation at elapsed 40s, conservative UTC upper at 41s.
        # Then 10s of scheduling delay; local wall time is still 6s behind.
        upper = budget.advance_upper(utc + 41, 1000, 1010, utc + 44)
        self.assertEqual(upper, utc + 53)
        result = budget.derive(run, jobs, env, upper, 1010)
        self.assertEqual(result['deadline_monotonic'], 1377)
        self.assertLessEqual(result['deadline_monotonic'], 1010 + 420 - 50)

    def test_backwards_and_nonfinite_anchor_fail(self):
        for arguments in ((1000, 20, 19, 1000), (float('nan'), 20, 21, 1000), (1000, 20, 21, float('inf'))):
            with self.subTest(arguments=arguments), self.assertRaises(RuntimeError):
                budget.advance_upper(*arguments)

    def test_json_parse_delay_is_inside_fetch_time_bound(self):
        response = mock.MagicMock()
        response.__enter__.return_value = response
        response.status = 200
        response.geturl.return_value = 'https://api.github.com/repos/' + budget.REPO + '/synthetic'
        response.headers = {'Date': 'Mon, 02 Nov 2026 20:00:00 GMT'}
        response.read.return_value = b'{}'
        opener = mock.Mock(); opener.open.return_value = response
        clock = [1000]
        def slow_parse(*_args, **_kwargs):
            clock[0] += 6
            return {}
        with mock.patch.object(budget.urllib.request, 'build_opener', return_value=opener), \
             mock.patch.object(budget.time, 'monotonic', side_effect=lambda: clock[0]), \
             mock.patch.object(budget.json, 'loads', side_effect=slow_parse), \
             mock.patch.object(budget.time, 'time', return_value=metadata()[3]), self.assertRaisesRegex(RuntimeError, 'observation took too long'):
            budget.fetch('/synthetic')

    def test_fetch_rejects_oversize_redirect_pagination_and_clock_skew(self):
        for variant in ('body', 'url', 'pagination', 'encoding', 'clock'):
            response = mock.MagicMock()
            response.__enter__.return_value = response
            response.status = 200
            response.geturl.return_value = 'https://api.github.com/repos/' + budget.REPO + '/synthetic'
            response.headers = {'Date': 'Mon, 02 Nov 2026 20:00:00 GMT'}
            response.read.return_value = b'{}'
            if variant == 'body': response.read.return_value = b'x' * (budget.MAX_BODY + 1)
            if variant == 'url': response.geturl.return_value = 'https://other.invalid/'
            if variant == 'pagination': response.headers['Link'] = 'next'
            if variant == 'encoding': response.headers['Content-Encoding'] = 'gzip'
            opener = mock.Mock(); opener.open.return_value = response
            with self.subTest(variant=variant), mock.patch.object(budget.urllib.request, 'build_opener', return_value=opener), \
                 mock.patch.object(budget.time, 'monotonic', side_effect=[1000, 1000.25]), \
                 mock.patch.object(budget.time, 'time', return_value=metadata()[3] + (20 if variant == 'clock' else 0)), \
                 self.assertRaises(RuntimeError):
                budget.fetch('/synthetic')


class PhaseControl(unittest.TestCase):
    def test_oom_event_refuses_next_phase(self):
        snapshot = {'memory_current': 100, 'memory_peak': 200, 'events': {'oom': 1},
            'controls': {'memory.oom.group': '0'}}
        with mock.patch.object(runner.guard, 'collect', return_value=snapshot), \
             mock.patch.object(runner.guard, 'verify'), mock.patch.object(runner, 'write'), \
             mock.patch.object(runner, 'baseline', return_value={'oom': 0}), self.assertRaises(RuntimeError):
            runner.observe('synthetic')

    def test_timeout_or_orphan_is_not_success(self):
        success = {'returncode': 0, 'timed_out': False, 'orphaned': False, 'clean': True}
        for key, value in {'returncode': -9, 'timed_out': True, 'orphaned': True, 'clean': False}.items():
            with self.subTest(key=key), mock.patch.object(runner, 'observe'), mock.patch.object(runner, 'write'), \
                 mock.patch.object(runner, 'reports', return_value=Path('/synthetic')), \
                 mock.patch.object(runner.owned, 'run_owned', return_value=dict(success, **{key: value})), \
                 self.assertRaises(RuntimeError):
                runner.run_phase('synthetic', ['not-launched'], {}, 10)

    def test_missing_deadline_never_spawns(self):
        keys = ('DICTIONARY_JOB_START_MONOTONIC', 'DICTIONARY_JOB_DEADLINE_MONOTONIC', 'DICTIONARY_JOB_CAP_SECONDS')
        with tempfile.TemporaryDirectory() as directory, mock.patch.dict(os.environ, dict.fromkeys(keys, '')), \
             mock.patch.object(runner, 'observe'), mock.patch.object(runner, 'reports', return_value=Path(directory)), \
             mock.patch.object(runner.owned.subprocess, 'Popen') as popen, self.assertRaises(RuntimeError):
            runner.run_phase('synthetic', ['not-launched'], {}, 120)
        popen.assert_not_called()

    def test_missing_native_prevents_go(self):
        with mock.patch.object(runner, 'native_evidence', side_effect=RuntimeError('missing native')), \
             mock.patch.object(runner, 'run_phase') as phase, self.assertRaises(RuntimeError):
            runner.workload()
        phase.assert_not_called()

    def test_failed_stage_stops_serial_chain_and_cleans_only_owned_home(self):
        for failing in ('compile', '16mib'):
            phases = []
            def phase(name, *_args):
                phases.append(name)
                if name == failing: raise RuntimeError('synthetic stop')
            home = mock.Mock()
            home.create_home.return_value = {'home': '/synthetic-home'}
            with tempfile.TemporaryDirectory() as directory, mock.patch.dict(os.environ, {'RUNNER_TEMP': directory}), \
                 mock.patch.object(runner, 'native_evidence'), mock.patch.object(runner, 'run_phase', side_effect=phase), \
                 mock.patch.object(runner, 'read_json', return_value={key: '/synthetic-cache' for key in ('GOPATH', 'GOMODCACHE', 'GOCACHE')}), \
                 mock.patch.object(runner, 'reports', return_value=Path(directory)), \
                 mock.patch.object(runner, 'module', return_value=home), mock.patch.object(runner, 'install_fixtures'), \
                 mock.patch.object(runner, 'require_resource_pass'), mock.patch.object(runner, 'write') as write, \
                 mock.patch.object(runner.owned, 'remaining_timeout', return_value=120), \
                 mock.patch.object(runner.owned, 'linux_children', return_value=[]), self.assertRaises(RuntimeError):
                runner.workload()
            self.assertEqual(phases, ['go-env', 'compile'] if failing == 'compile' else ['go-env', 'compile', '100k', '16mib'])
            home.remove_home.assert_called_once()
            self.assertNotIn('workload-complete.json', [call.args[0] for call in write.call_args_list])

    def test_test_count_skip_and_missing_summary_fail(self):
        good = '=== RUN   TestDictionaryValidationResource\n--- PASS: TestDictionaryValidationResource (0.01s)\nPASS\n'
        variants = [good, '', good + good, good.replace('--- PASS:', '--- SKIP:'), good.replace('\nPASS\n', '\n'),
            good + '=== RUN   Extra\n']
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'synthetic.log'
            for index, text in enumerate(variants):
                path.write_text(text)
                if index == 0: runner.require_resource_pass(path)
                else:
                    with self.subTest(index=index), self.assertRaises(RuntimeError): runner.require_resource_pass(path)

    def test_missing_tail_never_produces_acceptance(self):
        with mock.patch.object(runner, 'observe'), mock.patch.object(runner, 'native_evidence'), \
             mock.patch.object(runner, 'reports', return_value=Path('/synthetic')), \
             mock.patch.object(runner, 'read_json', side_effect=FileNotFoundError('missing final marker')), \
             mock.patch.object(runner, 'write') as write, self.assertRaises(FileNotFoundError):
            runner.finalize()
        write.assert_not_called()


class FrozenControl(unittest.TestCase):
    def test_job_guard_accepts_canonical_two_events(self):
        base = {'repository': budget.REPO, 'ref': 'refs/heads/' + budget.BRANCH}
        for event in ('push', 'workflow_dispatch'):
            self.assertTrue(job_context_allowed(dict(base, event_name=event)))

    def test_wrong_repo_ref_and_event_rejected_before_initialization(self):
        base = {'repository': budget.REPO, 'ref': 'refs/heads/' + budget.BRANCH, 'event_name': 'push'}
        variants = [('repository', 'yetone/magpie'), ('repository', 'other/Magpie-Next'),
            ('ref', 'refs/heads/main'), ('ref', 'refs/tags/v0.0.0'),
            ('ref', 'refs/heads/validation/local-dictionary-container-env-20261102'),
            ('ref', 'refs/heads/validation/local-dictionary-core-20261102'),
            ('event_name', 'pull_request'), ('event_name', 'pull_request_target'),
            ('event_name', 'workflow_run'), ('event_name', 'schedule'), ('event_name', '')]
        for key, value in variants:
            with self.subTest(key=key, value=value):
                self.assertFalse(job_context_allowed(dict(base, **{key: value})))
        for key in base:
            self.assertFalse(job_context_allowed({k: v for k, v in base.items() if k != key}))

    def test_case_alias_can_initialize_but_budget_and_source_gates_reject(self):
        # GitHub equality ignores case. Never claim these aliases are blocked
        # before initialization/tiny proof; the exact Python gates stop workload.
        base = {'repository': budget.REPO, 'ref': 'refs/heads/' + budget.BRANCH, 'event_name': 'push'}
        names = {'repository': 'GITHUB_REPOSITORY', 'ref': 'GITHUB_REF', 'event_name': 'GITHUB_EVENT_NAME'}
        gate = module('resource_active_source_gate', 'gate.py')
        for key, env_name in names.items():
            alias = dict(base, **{key: base[key].upper()})
            self.assertTrue(job_context_allowed(alias))
            environment = dict(metadata()[0], **{env_name: alias[key]})
            with self.subTest(key=key), mock.patch.dict(os.environ, environment, clear=True), \
                 mock.patch.object(budget, 'fetch') as fetch, mock.patch.object(gate.subprocess, 'check_output') as git:
                with self.assertRaises(RuntimeError): budget.main()
                with self.assertRaises(SystemExit): gate.main()
                fetch.assert_not_called()
                git.assert_not_called()

    def test_direct_wrong_context_never_reaches_git_or_api(self):
        gate = module('resource_active_wrong_context_gate', 'gate.py')
        with mock.patch.dict(os.environ, {}, clear=True), mock.patch.object(budget, 'fetch') as fetch, \
             mock.patch.object(gate.subprocess, 'check_output') as git:
            with self.assertRaises(RuntimeError): budget.main()
            with self.assertRaisesRegex(SystemExit, 'wrong repository'): gate.main()
            fetch.assert_not_called()
            git.assert_not_called()

    def test_only_canonical_push_or_dispatch_no_extra_privileges(self):
        import yaml
        text = (ROOT / budget.WORKFLOW).read_text()
        workflow = yaml.safe_load(text)
        trigger = workflow.get('on', workflow.get(True))
        self.assertEqual(set(trigger), {'push', 'workflow_dispatch'})
        self.assertEqual(trigger['push'], {'branches': [budget.BRANCH]})
        job = workflow['jobs'][budget.JOB]
        self.assertEqual(set(job['container']), {'image', 'options'})
        self.assertNotIn('services', job)
        for forbidden in ('${{ secrets.', 'contents: write', 'actions: write', '--privileged',
            '--cap-add', '--group-add', '--pid=host', '--cgroupns=host', 'continue-on-error'):
            self.assertNotIn(forbidden, text)

    def test_source_enabled_fixed_and_seventeen_backend_files(self):
        source = json.loads((CONTROL / 'source.json').read_text())
        self.assertIs(source['enabled'], True)
        self.assertEqual(source['source'], '203a1811105a97fa46cb62fcf9706f44535b0ebd')
        self.assertEqual(source['source_tree'], '0ceb41a3c81cf0b562f902fc8ad688733cdbf393')
        self.assertEqual(len(source['files']), 17)
        self.assertTrue(all(path.endswith('.go') and '/assets/' not in path for path in source['files']))

    def test_yaml_enabled_guard_and_exact_inline_preflight(self):
        # PyYAML is used only by this local self-check, never by CI controls.
        import yaml
        workflow = yaml.safe_load((ROOT / budget.WORKFLOW).read_text())
        self.assertEqual(set(workflow['jobs']), {budget.JOB})
        job = workflow['jobs'][budget.JOB]
        self.assertEqual(job['if'], "${{ github.repository == 'xue-ding-e/Magpie-Next' && github.ref == 'refs/heads/validation/local-dictionary-container-resource-20261102' && (github.event_name == 'push' || github.event_name == 'workflow_dispatch') }}")
        self.assertEqual(job['timeout-minutes'], 10)
        source = json.loads((CONTROL / 'source.json').read_text())
        self.assertEqual(job['container'], {'image': source['container']['image'], 'options': source['container']['options']})
        self.assertEqual(workflow['permissions'], {'contents': 'read'})
        for index, name, delimiter in ((0, 'probe.py', 'PYPROBE'), (1, 'budget.py', 'PYBUDGET')):
            actual = job['steps'][index]['run'].split("<<'" + delimiter + "'\n", 1)[1].rsplit('\n' + delimiter, 1)[0]
            self.assertEqual(actual, (CONTROL / name).read_text().rstrip())
        self.assertTrue(all(step['timeout-minutes'] <= 7 for step in job['steps']))
        self.assertEqual([s['uses'] for s in job['steps'] if 'uses' in s], source['actions'])
        self.assertTrue(all('continue-on-error' not in s for s in job['steps']))

    def test_native_suite_exactly_five_reviewed_scenarios(self):
        tree = ast.parse((CONTROL / 'native_lifecycle.py').read_text())
        case = next(node for node in tree.body if isinstance(node, ast.ClassDef) and node.name == 'Lifecycle')
        names = {node.name for node in case.body if isinstance(node, ast.FunctionDef) and node.name.startswith('test_')}
        self.assertEqual(names, {'test_normal_exit_has_no_children', 'test_detached_descendant_is_owned_and_reaped',
            'test_timeout_reaps_detached_tree', 'test_sigterm_enters_cleanup', 'test_wait_exception_enters_cleanup'})

    def test_offline_fixture_and_workload_targets_are_not_downgraded(self):
        self.assertIn('func init() { testenv.Offline() }', (CONTROL / 'fixtures/localdictionary_offline_test.go').read_text())
        text = (CONTROL / 'fixtures/resource_test.go').read_text()
        for part in ('100000, 2400000', '100000, redact.MaxDictionaryBytes', 'case "held", "released":',
            'debug.FreeOSMemory()', 'released+(1<<20) >= held', '"memory.swap.max": "0"'):
            self.assertIn(part, text)
        self.assertEqual(runner.STAGES, ('100k', '16mib', 'held', 'released'))

    def test_manifest_bytes(self):
        hashes = json.loads((CONTROL / 'control-hashes.json').read_text())
        for path, expected in hashes.items():
            self.assertEqual(hashlib.sha256((ROOT / path).read_bytes()).hexdigest(), expected, path)
        actual = {str(p.relative_to(ROOT)) for p in ROOT.rglob('*') if p.is_file()}
        self.assertEqual(actual, set(hashes) | {str((CONTROL / 'control-hashes.json').relative_to(ROOT))})


if __name__ == '__main__':
    unittest.main(verbosity=2)
