import hashlib
import json
import os
from pathlib import Path
import subprocess

ROOT=Path(__file__).resolve().parents[2]
C=ROOT/'.github/auth-paired-diagnostic'
PARENT='a29ecedbdce7568146e58a21edc4d6c35fe58331'
V5='b7888c94c70edf29b2275370d429ec077232a324'
V3='5d98742b2345e88b7cba8f1a6ef1735f51740667'
PRIOR='69513cff9acecd0d554a70c2c47dd05216da1306'
CONTROL_BASE='1f5ec6f9f194df6359b6f40ccee18e6e36fa2f86'
SOURCE='7feed2b7e7a94ad0263ad3f44a1b0ab3b2fc065c'
BASE='d65e4026f0856215a35232fabc4cbb3f98fa52d7'
def git(root,*args):return subprocess.check_output(['git',*args],cwd=root,text=True).strip()
assert os.environ['GITHUB_REPOSITORY']=='xue-ding-e/Magpie-Next'
assert os.environ['GITHUB_EVENT_NAME']=='push'
assert os.environ['GITHUB_REF']=='refs/heads/validation/auth-diagnosis-20261102'
head=git(ROOT,'rev-parse','HEAD');assert head==os.environ['GITHUB_SHA']
assert git(ROOT,'rev-list','--parents','-n','1',head).split()==[head,PARENT]
assert git(ROOT,'rev-list','--parents','-n','1',PARENT).split()==[PARENT,V5]
assert git(ROOT,'rev-list','--parents','-n','1',V5).split()==[V5,V3]
assert git(ROOT,'rev-list','--parents','-n','1',V3).split()==[V3,PRIOR]
assert git(ROOT,'rev-list','--parents','-n','1',PRIOR).split()==[PRIOR,CONTROL_BASE]
manifest=json.loads((C/'control-hashes.json').read_text())
paths={'.github/auth-paired-diagnostic/'+n for n in manifest}|{'.github/auth-paired-diagnostic/control-hashes.json','.github/auth-paired-diagnostic/gate.py','.github/workflows/auth-paired-diagnostic.yml'}
assert set(git(ROOT,'diff','--name-only',CONTROL_BASE,head).splitlines())==paths
for name in paths:assert git(ROOT,'ls-tree',head,'--',name).startswith('100644 blob ')
for name,sha in manifest.items():assert hashlib.sha256((C/name).read_bytes()).hexdigest()==sha
work=Path(os.environ['GITHUB_WORKSPACE'])
for label,sha in [('candidate',SOURCE),('baseline',BASE)]:
    root=work/label
    assert git(root,'rev-parse','HEAD')==sha
    assert not git(root,'status','--porcelain')
    for filename in ['library-all.test.cjs','agent-disconnect-preview.test.cjs']:
        assert git(root,'hash-object','internal/gui/tests/'+filename)==git(ROOT,'rev-parse',BASE+':internal/gui/tests/'+filename)
assert git(work/'candidate','rev-parse','HEAD^{tree}')=='50c8793a9ed93262b78123903f42ac8e72c4bee1'
source=json.loads((C/'source.json').read_text())
for name,sha in source['files'].items():assert hashlib.sha256((work/'candidate'/name).read_bytes()).hexdigest()==sha
assert set(git(ROOT,'diff','--name-only',BASE,SOURCE).splitlines())==set(source['files'])
tmp=Path(os.environ['RUNNER_TEMP']).resolve(strict=True)
assert not any((p/'.git').exists() for p in (tmp,*tmp.parents))
assert len(os.fsencode(str(tmp/'ma.XXXXXXXX')+'/org.chromium.Chromium.XXXXXX/SingletonSocket'))<108
lock=json.loads((C/'package-lock.json').read_text())
assert json.loads((C/'package.json').read_text())['dependencies']=={'playwright':'1.61.1'}
for n,v in lock['packages'].items():
    if n:
        assert n in ['node_modules/playwright','node_modules/playwright-core','node_modules/fsevents']
        assert v['resolved'].startswith('https://registry.npmjs.org/') and v['integrity'].startswith('sha512-')
print('Exact paired sources, original fixture files, controls, event and isolated temporary boundary verified')
