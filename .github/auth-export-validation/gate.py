import hashlib
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
CONTROL = ROOT / '.github/auth-export-validation'
SOURCE = '7feed2b7e7a94ad0263ad3f44a1b0ab3b2fc065c'
BASE = 'd65e4026f0856215a35232fabc4cbb3f98fa52d7'
CONTROL_PARENT = '33546050110ff59f6003153cae647f2fab7b868f'
BRANCH = 'refs/heads/validation/auth-export-20261102'

def git(*args):
    return subprocess.check_output(['git', *args], cwd=ROOT, text=True).strip()

def require(ok, reason):
    if not ok:
        raise SystemExit(reason)

# Linux sockaddr_un.sun_path is 108 bytes, including the terminating NUL.
# Chromium creates this socket directly below TMPDIR; keep the path inside
# this workspace but short enough, rather than disabling its singleton guard.
SOCKET_SUFFIX = '/org.chromium.Chromium.XXXXXX/SingletonSocket'
def socket_tmp_fits(directory):
    return len(os.fsencode(str(directory) + SOCKET_SUFFIX)) < 108

require(socket_tmp_fits('/' + 'x' * (106 - len(SOCKET_SUFFIX))), '107-byte boundary must fit')
require(not socket_tmp_fits('/' + 'x' * (107 - len(SOCKET_SUFFIX))), '108-byte boundary must fail')
require(not socket_tmp_fits('/home/runner/work/Magpie-Next/Magpie-Next/.validation-runtime/tmp'), 'Original failing path must be rejected')
require(socket_tmp_fits(ROOT / '.t'), 'Workspace temporary path exceeds Unix socket limit')
require(not (ROOT / '.t').is_symlink(), 'Temporary directory must not be a symlink')

require(os.environ.get('GITHUB_REPOSITORY') == 'xue-ding-e/Magpie-Next', 'Wrong repository')
require(os.environ.get('GITHUB_EVENT_NAME') == 'push' and os.environ.get('GITHUB_REF') == BRANCH, 'Wrong event or branch')
head = git('rev-parse', 'HEAD')
require(head == os.environ.get('GITHUB_SHA'), 'Wrong checked-out commit')
require(git('rev-list', '--parents', '-n', '1', head).split() == [head, CONTROL_PARENT], 'Fixup must have exactly the previous control parent')
require(git('rev-list', '--parents', '-n', '1', CONTROL_PARENT).split() == [CONTROL_PARENT, SOURCE], 'Previous control must have exactly the feature parent')
require(git('rev-list', '--parents', '-n', '1', SOURCE).split() == [SOURCE, BASE], 'Feature must have exactly the reviewed parent')
require(git('rev-parse', SOURCE + '^{tree}') == '50c8793a9ed93262b78123903f42ac8e72c4bee1', 'Wrong feature tree')
m = json.loads((CONTROL / 'source.json').read_text())
require(m['source'] == SOURCE and m['parent'] == BASE, 'Wrong source manifest')
require(set(git('diff', '--name-only', BASE, SOURCE).splitlines()) == set(m['files']), 'Feature path drift')
for name, digest in m['files'].items():
    require(hashlib.sha256((ROOT / name).read_bytes()).hexdigest() == digest, 'Feature hash drift: ' + name)
controls = {'.github/workflows/auth-export-validation.yml'} | {
    '.github/auth-export-validation/' + p for p in ['package.json', 'package-lock.json', 'source.json', 'offline.cjs', 'run.cjs', 'gate.py', 'control-hashes.json']
}
require(set(git('diff', '--name-only', SOURCE, head).splitlines()) == controls, 'Validation changed unexpected files')
for name in controls:
    require(git('ls-tree', head, '--', name).startswith('100644 blob '), 'Control must be a regular non-executable file')
for name, digest in json.loads((CONTROL / 'control-hashes.json').read_text()).items():
    require(hashlib.sha256((CONTROL / name).read_bytes()).hexdigest() == digest, 'Validation hash drift: ' + name)
lock = json.loads((CONTROL / 'package-lock.json').read_text())
require(json.loads((CONTROL / 'package.json').read_text())['dependencies'] == {'playwright': '1.61.1'}, 'Wrong dependency pin')
for name, entry in lock['packages'].items():
    if not name:
        continue
    require(name in {'node_modules/playwright', 'node_modules/playwright-core', 'node_modules/fsevents'}, 'Unexpected dependency')
    require(entry['resolved'].startswith('https://registry.npmjs.org/') and entry['integrity'].startswith('sha512-'), 'Non-official or unlocked dependency')
    if name != 'node_modules/fsevents':
        require(entry['version'] == '1.61.1', 'Wrong Playwright version')
print('Reviewed feature source, linear parents, control paths and dependency integrity verified.')
