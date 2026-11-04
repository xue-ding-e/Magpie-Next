"""Bind the reviewed backend tree; activation is still explicit and fail-closed."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

ROOT = Path(__file__).resolve().parents[2]
CONTROL = ROOT / '.github/dictionary-container-resource'

def require(ok, why):
    if not ok:
        raise SystemExit('dictionary-validation: ' + why)

def git(*args):
    return subprocess.check_output(['git', *args], cwd=ROOT, text=True).strip()

def main():
    m = json.loads((CONTROL / 'source.json').read_text())
    require(m['enabled'] is True, 'NOT ACTIVATED: container resource control review and queue approval required')
    require(m['actions_allowlist_verified'] is True, 'Actions allowlist has not been verified')
    require(isinstance(m['source'], str) and re.fullmatch('[0-9a-f]{40}', m['source']), 'verified core commit missing')
    require(os.environ.get('GITHUB_REPOSITORY') == m['repository'], 'wrong repository')
    require(os.environ.get('GITHUB_REF') == m['branch'], 'wrong candidate branch')
    require(os.environ.get('GITHUB_EVENT_NAME') in ('push', 'workflow_dispatch'), 'wrong event')
    head = git('rev-parse', 'HEAD')
    require(head == os.environ.get('GITHUB_SHA'), 'wrong checkout')
    for commit, parent in ((head, m['source']), (m['source'], m['base'])):
        require(git('rev-list', '--parents', '-n', '1', commit).split() == [commit, parent], 'nonlinear or wrong parent')
    for commit, tree in ((m['source'], m['source_tree']),):
        require(git('rev-parse', commit + '^{tree}') == tree, 'reviewed product tree changed')
    require(set(git('diff', '--name-only', m['base'], m['source']).splitlines()) == set(m['files']), 'product path drift')
    for name, digest in m['files'].items():
        require(hashlib.sha256((ROOT / name).read_bytes()).hexdigest() == digest, 'product bytes changed: ' + name)
    hashes = json.loads((CONTROL / 'control-hashes.json').read_text())
    expected = set(hashes) | {'.github/dictionary-container-resource/control-hashes.json'}
    require(set(git('diff', '--name-only', m['source'], head).splitlines()) == expected, 'unexpected validation changes')
    require(not git('status', '--porcelain', '--untracked-files=all'), 'dirty checkout before validation')
    for name, digest in hashes.items():
        require(git('ls-tree', head, '--', name).startswith('100644 blob '), 'nonregular control file')
        require(hashlib.sha256((ROOT / name).read_bytes()).hexdigest() == digest, 'control hash drift: ' + name)
    print('PASS reviewed backend tree, linear core source chain, control-only paths and hashes; container resource source only; native lifecycle and resource execution require subsequent gates; no UI or release acceptance')

if __name__ == '__main__':
    main()
