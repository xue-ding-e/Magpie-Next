"""Bind one reviewed combined product tree and exactly the validation controls."""
import hashlib,json,os,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
CONTROL=Path(__file__).resolve().parent
SOURCE='7e9a490c3db9469cc290fb1ccc53831c9b823ef6'
TREE='fb989e8f2b5fee5692cb5dc7978fbf7dd8cc0b2d'
REF='refs/heads/validation/local-dictionary-integrated-v3-20261104'
def git(*args): return subprocess.check_output(['git',*args],cwd=ROOT,text=True).strip()
def require(ok,why):
    if not ok: raise SystemExit('NOT RUN: '+why)
require(os.environ.get('GITHUB_REPOSITORY')=='xue-ding-e/Magpie-Next','repository')
require(os.environ.get('GITHUB_REF')==REF,'ref')
require(os.environ.get('GITHUB_EVENT_NAME') in ('push','workflow_dispatch'),'event')
head=git('rev-parse','HEAD')
require(head==os.environ.get('GITHUB_SHA'),'checkout')
require(git('rev-list','--parents','-n','1',head).split()==[head,SOURCE],'single source parent')
require(git('rev-parse',SOURCE+'^{tree}')==TREE,'source tree')
hashes=json.loads((CONTROL/'control-hashes.json').read_text())
require(set(git('diff','--name-only',SOURCE,head).splitlines())==set(hashes)|{'.github/dictionary-integration/control-hashes.json'},'control paths')
for name,digest in hashes.items():
    require(git('ls-tree',head,'--',name).startswith('100644 blob '),'ordinary control file')
    require(hashlib.sha256((ROOT/name).read_bytes()).hexdigest()==digest,'control hash')
require(not git('status','--porcelain','--untracked-files=all'),'dirty checkout')
print('PASS exact combined source, linear parent and reviewed control-only delta; no release acceptance')
