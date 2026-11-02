import hashlib,json,os,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
C=ROOT/'.github/disconnect-flex-validation'
PRODUCT='75953a1f3da2e82a62c1b33559b8749a9cd5c20b'
PRODUCT_PARENT='2c53077ebb6bd8f34d34880a7ead19e18e54147b'
CONTROL_PARENT='c865c2922e750bb215d415cff6c076cb9201d61b'
BASE='7feed2b7e7a94ad0263ad3f44a1b0ab3b2fc065c'
def git(root,*args):return subprocess.check_output(['git',*args],cwd=root,text=True).strip()
assert os.environ['GITHUB_REPOSITORY']=='xue-ding-e/Magpie-Next'
assert os.environ['GITHUB_EVENT_NAME']=='push'
assert os.environ['GITHUB_REF']=='refs/heads/validation/disconnect-flex-20261102'
head=git(ROOT,'rev-parse','HEAD');assert head==os.environ['GITHUB_SHA']
assert git(ROOT,'rev-list','--parents','-n','1',head).split()==[head,CONTROL_PARENT]
assert git(ROOT,'rev-list','--parents','-n','1',CONTROL_PARENT).split()==[CONTROL_PARENT,PRODUCT_PARENT]
assert git(ROOT,'rev-list','--parents','-n','1',PRODUCT_PARENT).split()==[PRODUCT_PARENT,BASE]
m=json.loads((C/'hashes.json').read_text());paths={'.github/disconnect-flex-validation/'+n for n in m}|{'.github/disconnect-flex-validation/gate.py','.github/disconnect-flex-validation/hashes.json','.github/workflows/disconnect-flex-validation.yml'}
assert set(git(ROOT,'diff','--name-only',PRODUCT_PARENT,head).splitlines())==paths
for name in paths:assert git(ROOT,'ls-tree',head,'--',name).startswith('100644 blob ')
for name,h in m.items():assert hashlib.sha256((C/name).read_bytes()).hexdigest()==h
product=json.loads((C/'product.json').read_text());assert product['sha']==PRODUCT and product['parent']==PRODUCT_PARENT
work=Path(os.environ['GITHUB_WORKSPACE'])
assert git(work/'candidate','rev-list','--parents','-n','1',PRODUCT).split()==[PRODUCT,PRODUCT_PARENT]
assert git(work/'candidate','rev-parse',PRODUCT+'^{tree}')==product['tree']
assert set(git(work/'candidate','diff','--name-only',BASE,PRODUCT).splitlines())=={f['path'] for f in product['files']}
for name,sha in [('candidate',PRODUCT),('baseline',BASE)]:
 root=work/name;assert git(root,'rev-parse','HEAD')==sha;assert not git(root,'status','--porcelain')
for f in product['files']:
 assert hashlib.sha256((work/'candidate'/f['path']).read_bytes()).hexdigest()==f['sha256']
 assert hashlib.sha256((work/'baseline'/f['path']).read_bytes()).hexdigest()==f['base_sha256']
test='internal/gui/tests/agent-disconnect-preview.test.cjs'
assert (work/'candidate'/test).read_bytes().startswith((work/'baseline'/test).read_bytes()), 'Original assertions must remain unchanged'
for file in ['agent-disconnect.test.cjs','agent-leave-magpie.test.cjs','aside-native.test.cjs','modal-morph.test.cjs']:
 name='internal/gui/tests/'+file;assert (work/'candidate'/name).read_bytes()==(work/'baseline'/name).read_bytes()
tmp=Path(os.environ['RUNNER_TEMP']).resolve(strict=True)
assert not any((p/'.git').exists() for p in (tmp,*tmp.parents))
assert len(os.fsencode(str(tmp/'ma.XXXXXXXX')+'/org.chromium.Chromium.XXXXXX/SingletonSocket'))<108
assert json.loads((C/'package.json').read_text())['dependencies']=={'playwright':'1.61.1'}
for n,v in json.loads((C/'package-lock.json').read_text())['packages'].items():
 if n:assert n in ['node_modules/playwright','node_modules/playwright-core','node_modules/fsevents'] and v['resolved'].startswith('https://registry.npmjs.org/') and v['integrity'].startswith('sha512-')
print('Fixed product, baseline, original assertions, controls, event and dependencies verified')
