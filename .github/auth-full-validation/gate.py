import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
CONTROL = ROOT / '.github/auth-full-validation'
SOURCE = '7feed2b7e7a94ad0263ad3f44a1b0ab3b2fc065c'
PRIOR_CONTROL = '653701cbee64946bec6440eb05a86315228b7c86'
BASE = 'd65e4026f0856215a35232fabc4cbb3f98fa52d7'
BRANCHES = {'linux-gui': 'validation/auth-full-20261102', 'native': 'validation/auth-native-20261102'}

def require(ok, why):
    if not ok:
        raise SystemExit(why)

def git(*args):
    return subprocess.check_output(['git', *args], cwd=ROOT, text=True).strip()

phase = sys.argv[1] if len(sys.argv) == 2 else ''
require(phase in BRANCHES, 'Unknown validation phase')
require(os.environ.get('GITHUB_REPOSITORY') == 'xue-ding-e/Magpie-Next', 'Wrong repository')
require(os.environ.get('GITHUB_EVENT_NAME') == 'push' and os.environ.get('GITHUB_REF') == 'refs/heads/' + BRANCHES[phase], 'Wrong event or exact branch')
head = git('rev-parse', 'HEAD')
require(head == os.environ.get('GITHUB_SHA'), 'Wrong checkout')
require(git('rev-list', '--parents', '-n', '1', head).split() == [head, PRIOR_CONTROL], 'Control fix must have exactly the fixed prior control parent')
require(git('rev-list', '--parents', '-n', '1', PRIOR_CONTROL).split() == [PRIOR_CONTROL, SOURCE], 'Unexpected prior control parent')
require(git('rev-list', '--parents', '-n', '1', SOURCE).split() == [SOURCE, BASE], 'Unexpected product parent')
require(git('rev-parse', SOURCE + '^{tree}') == '50c8793a9ed93262b78123903f42ac8e72c4bee1', 'Wrong product tree')
source = json.loads((CONTROL / 'source.json').read_text())
require(source['source'] == SOURCE and source['parent'] == BASE, 'Wrong source manifest')
require(set(git('diff', '--name-only', BASE, SOURCE).splitlines()) == set(source['files']), 'Product path drift')
for name, digest in source['files'].items():
    require(hashlib.sha256((ROOT / name).read_bytes()).hexdigest() == digest, 'Product hash drift')
controls = json.loads((CONTROL / 'control-hashes.json').read_text())
paths = {'.github/auth-full-validation/' + name for name in controls} | {
    '.github/auth-full-validation/gate.py', '.github/auth-full-validation/control-hashes.json',
    '.github/workflows/auth-full-linux-gui.yml', '.github/workflows/auth-full-native.yml'
}
require(set(git('diff', '--name-only', SOURCE, head).splitlines()) == paths, 'Unexpected control paths')
for name in paths:
    require(git('ls-tree', head, '--', name).startswith('100644 blob '), 'Control must be a non-executable regular file')
for name, digest in controls.items():
    require(hashlib.sha256((CONTROL / name).read_bytes()).hexdigest() == digest, 'Control hash drift')
lock = json.loads((CONTROL / 'package-lock.json').read_text())
require(json.loads((CONTROL / 'package.json').read_text())['dependencies'] == {'playwright': '1.61.1'}, 'Wrong dependency pin')
for name, entry in lock['packages'].items():
    if not name:
        continue
    require(name in {'node_modules/playwright', 'node_modules/playwright-core', 'node_modules/fsevents'}, 'Unexpected package')
    require(entry['resolved'].startswith('https://registry.npmjs.org/') and entry['integrity'].startswith('sha512-'), 'Non-official or unlocked dependency')
    require(entry['version'] == ('2.3.2' if name.endswith('/fsevents') else '1.61.1'), 'Wrong dependency version')
require(len(list((ROOT / 'internal/gui/tests').glob('*.test.cjs'))) == 827, 'Full GUI inventory drift')
# The task-owned runner temporary directory must be outside Git worktrees.
if os.environ.get('RUNNER_OS') != 'Windows':
    suffix = '/org.chromium.Chromium.XXXXXX/SingletonSocket'
    fits = lambda directory: len(os.fsencode(str(directory) + suffix)) < 108
    require(fits('/' + 'x' * (106-len(suffix))) and not fits('/' + 'x' * (107-len(suffix))), 'Socket boundary regression')
    temporary_root = Path(os.environ['RUNNER_TEMP']).resolve(strict=True)
    require(not any((p / '.git').exists() for p in (temporary_root, *temporary_root.parents)), 'Runner temp inside repository')
    require(fits(temporary_root / 'ma.XXXXXXXX'), 'Unsafe browser temporary path length')
if phase == 'native':
    # No credential is needed to read this public repository's workflow run.
    # Native validation requires phase 1 to have completed on this exact same
    # control/source commit. No other SHA or workflow can satisfy the gate.
    url = 'https://api.github.com/repos/xue-ding-e/Magpie-Next/actions/runs?branch=validation%2Fauth-full-20261102&per_page=100'
    request = urllib.request.Request(url, headers={'Accept': 'application/vnd.github+json', 'User-Agent': 'magpie-auth-validation'})
    with urllib.request.urlopen(request, timeout=20) as response:
        runs = json.load(response)['workflow_runs']
    passed = [r for r in runs if r['head_sha'] == head and r['event'] == 'push' and r['path'] == '.github/workflows/auth-full-linux-gui.yml' and r['status'] == 'completed' and r['conclusion'] == 'success']
    require(len(passed) == 1, 'Exactly one successful phase 1 run on this commit is required')
    print('Verified phase 1 run', passed[0]['id'])
print('Verified fixed product, linear parent, all control hashes and phase', phase)
