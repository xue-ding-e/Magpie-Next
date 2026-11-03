"""Exact single-parent validation controls; never a whole-suite waiver."""
import hashlib
import importlib.util
import json
import os
import subprocess
from pathlib import Path

CONTROL = Path(__file__).resolve().parent
ROOT = CONTROL.parents[1]
PREFIX = '.github/gui-batch004/'


def module(name):
    spec = importlib.util.spec_from_file_location(name, CONTROL / (name + '.py'))
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


def git(*args, cwd=ROOT):
    return subprocess.check_output(['git', *args], cwd=cwd, text=True).strip()


def validate():
    assert os.environ['GITHUB_REPOSITORY'] == 'xue-ding-e/Magpie-Next'
    assert os.environ['GITHUB_EVENT_NAME'] == 'push'
    assert os.environ['GITHUB_REF'] == 'refs/heads/validation/auth-gui-batch004-20261103'
    controls = json.loads((CONTROL / 'control-hashes.json').read_text())
    assert all(p.startswith(PREFIX) and '..' not in Path(p).parts for p in controls)
    actual = {str(p.relative_to(ROOT)) for p in CONTROL.rglob('*') if p.is_file()
              and 'node_modules' not in p.parts}
    assert actual == set(controls) | {PREFIX + 'control-hashes.json'}
    for path, digest in controls.items():
        assert hashlib.sha256((ROOT / path).read_bytes()).hexdigest() == digest, path
    source = json.loads((CONTROL / 'source.json').read_text())
    assert git('rev-parse', 'HEAD') == os.environ['GITHUB_SHA']
    assert git('rev-parse', 'HEAD^') == source['source']
    assert len(git('rev-list', '--parents', '-n', '1', 'HEAD').split()) == 2
    expected_changes = set(controls) | {PREFIX + 'control-hashes.json', '.github/workflows/gui-batch004.yml'}
    assert set(git('diff', '--name-only', 'HEAD^', 'HEAD').splitlines()) == expected_changes
    checkout = ROOT / 'source'
    assert git('rev-parse', 'HEAD', cwd=checkout) == source['source']
    assert git('rev-parse', 'HEAD^', cwd=checkout) == source['parent']
    assert git('rev-parse', 'HEAD^{tree}', cwd=checkout) == source['tree']
    assert not git('status', '--porcelain', cwd=checkout)
    migration = module('migration')
    migration.verify_source_continuity()
    manifest = json.loads((CONTROL / 'inventory.json').read_text())
    assert manifest['product'] == source['source'] and manifest['batch_id'] == 'recovery-004'
    assert manifest['concurrency'] == 2 and manifest['per_file_seconds'] == 600
    assert manifest['batch_seconds'] == 1800 and manifest['outer_seconds'] == 1860
    assert manifest['job_ceiling_seconds'] == 3300
    assert manifest['max_observed_process_rss_bytes'] == 6442450944
    assert manifest['max_report_bytes'] == 314572800 and manifest['minimum_free_bytes'] == 1073741824
    files = {item['path']: item for item in manifest['files']}
    actual_tests = {str(p.relative_to(checkout)) for p in (checkout / 'internal/gui/tests').glob('*.test.cjs')}
    assert len(files) == 827 and set(files) == actual_tests
    for name, item in files.items():
        data = (checkout / name).read_bytes()
        assert hashlib.sha256(data).hexdigest() == item['sha256'], name
        assert hashlib.sha1(b'blob ' + str(len(data)).encode() + b'\0' + data).hexdigest() == item['git_blob'], name
    seeded = migration.seed_records(manifest, 'v22.23.4')
    assert len(seeded) == 827
    assert sum(v['state'] == 'passed' for v in seeded.values()) == 31
    assert sum(v['state'] == 'failed' for v in seeded.values()) == 1
    assert sum(v['state'] == 'not_run' for v in seeded.values()) == 795
    assert all(v['executed_in_batch'] is False for v in seeded.values())
    assert json.loads((CONTROL / 'package.json').read_text())['dependencies'] == {'playwright': '1.62.1'}
    witnesses = json.loads((CONTROL / 'external-current-witnesses.json').read_text())
    assert witnesses['source'] == source['source'] and witnesses['accepted_into_suite'] is False
    assert isinstance(witnesses['files'], dict)
    for path, witness in witnesses['files'].items():
        assert path in files and witness['source'] == source['source']
        assert witness['file_sha256'] == files[path]['sha256']
        assert witness['original_fixture'] is True and witness['intentional_negative'] is False
        assert witness['accepted_into_suite'] is False
    budget = module('budget').admitted_budget(manifest)
    return manifest, budget


if __name__ == '__main__':
    validate()
    print('Frozen exact-source batch004 controls and cumulative receipt verified. Whole827/visual10 gate stays RED.')
