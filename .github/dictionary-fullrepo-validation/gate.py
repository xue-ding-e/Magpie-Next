"""Bind the full-repository validation to the reviewed product and controls."""
import hashlib
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
CONTROL = Path(__file__).resolve().parent
SOURCE = 'deb770e42bd210cb69b8b1970ad5d7c068e40dba'
TREE = 'fae10bb58361a5f20d7dcd0cbf58d929e81ead4f'
REF = 'refs/heads/validation/local-dictionary-fullrepo-20261104'


def git(*args):
    return subprocess.check_output(['git', *args], cwd=ROOT, text=True).strip()


def require(ok, reason):
    if not ok:
        raise SystemExit('NOT RUN: ' + reason)


require(os.environ.get('GITHUB_REPOSITORY') == 'xue-ding-e/Magpie-Next', 'repository')
require(os.environ.get('GITHUB_REF') == REF, 'validation ref')
require(os.environ.get('GITHUB_EVENT_NAME') == 'push', 'event')
head = git('rev-parse', 'HEAD')
require(head == os.environ.get('GITHUB_SHA'), 'checkout SHA')
require(git('rev-list', '--parents', '-n', '1', head).split() == [head, SOURCE], 'single reviewed product parent')
require(git('rev-parse', SOURCE + '^{tree}') == TREE, 'reviewed product tree')
manifest = json.loads((CONTROL / 'manifest.json').read_text())
require(manifest['source'] == SOURCE and manifest['tree'] == TREE and manifest['ref'] == REF, 'manifest binding')
hashes = manifest['control_hashes']
manifest_path = '.github/dictionary-fullrepo-validation/manifest.json'
require(set(git('diff', '--name-only', SOURCE, head).splitlines()) == set(hashes) | {manifest_path}, 'validation-only paths')
for name, digest in hashes.items():
    require(git('ls-tree', head, '--', name).startswith('100644 blob '), 'ordinary control file')
    require(hashlib.sha256((ROOT / name).read_bytes()).hexdigest() == digest, 'control hash')
require(not git('status', '--porcelain', '--untracked-files=all'), 'clean checkout')
print('PASS exact product source and validation controls; full-repository checks only, no GUI/ACL/package/release acceptance')
