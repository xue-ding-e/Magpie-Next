import hashlib,json,os,subprocess
from pathlib import Path
CONTROL=Path(__file__).resolve().parent
ROOT=CONTROL.parents[1]
PREFIX='.github/quota-cut/'
def digest(p): return hashlib.sha256(p.read_bytes()).hexdigest()
def git(*args,cwd=ROOT): return subprocess.check_output(['git',*args],cwd=cwd,text=True).strip()
def validate():
    cfg=json.loads((CONTROL/'source.json').read_text())
    assert os.environ['GITHUB_REPOSITORY']=='xue-ding-e/Magpie-Next'
    assert os.environ['GITHUB_EVENT_NAME']=='push'
    assert os.environ['GITHUB_REF']=='refs/heads/'+cfg['branch']
    hashes=json.loads((CONTROL/'control-hashes.json').read_text())
    assert all(p.startswith(PREFIX) and '..' not in Path(p).parts for p in hashes)
    actual={str(p.relative_to(ROOT)) for p in CONTROL.rglob('*') if p.is_file() and 'node_modules' not in p.parts}
    assert actual==set(hashes)|{PREFIX+'control-hashes.json'}
    for p,h in hashes.items(): assert digest(ROOT/p)==h,p
    assert git('rev-parse','HEAD')==os.environ['GITHUB_SHA']
    assert git('rev-list','--parents','-n','1','HEAD').split()==[os.environ['GITHUB_SHA'],cfg['source']]
    assert set(git('diff','--name-only','HEAD^','HEAD').splitlines())==set(hashes)|{PREFIX+'control-hashes.json','.github/workflows/quota-cut.yml'}
    src=ROOT/'source'
    assert git('rev-parse','HEAD',cwd=src)==cfg['source']
    assert git('rev-parse','HEAD^{tree}',cwd=src)==cfg['tree']
    assert not git('status','--porcelain',cwd=src)
    assert digest(src/cfg['fixture'])==cfg['fixture_sha256']
    assert json.loads((CONTROL/'package.json').read_text())['dependencies']=={'playwright':'1.62.1'}
    return cfg
if __name__=='__main__': validate();print('Exact instrumented diagnostic controls verified; not acceptance.')
