import hashlib,json,os,subprocess
from pathlib import Path
C=Path(__file__).resolve().parent;ROOT=C.parents[1];PREFIX='.github/quota-fold-pair/'
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def git(*args,cwd=ROOT):return subprocess.check_output(['git',*args],cwd=cwd,text=True).strip()
def sourceproof(cfg,which):
    src=ROOT/which;head=git('rev-parse','HEAD',cwd=src);tree=git('rev-parse','HEAD^{tree}',cwd=src)
    assert head==cfg[which] and tree==cfg[which+'_tree']
    assert not git('status','--porcelain',cwd=src)
    files={}
    for item in cfg['files']:
        if which=='base' and 'quota-single-window.test.cjs' in item['path']:continue
        actual=sha(src/item['path']);assert actual==item['sha256'];files[item['path']]=actual
    css='internal/gui/assets/app.css';files[css]=sha(src/css);assert files[css]==cfg[which+'_css_sha256']
    if which=='candidate':
        assert git('rev-list','--parents','-n','1','HEAD',cwd=src).split()==[cfg['candidate'],cfg['base']]
        assert git('show','-s','--format=%an%n%ae%n%cn%n%ce','HEAD',cwd=src).splitlines()==[cfg['author_name'],cfg['author_email']]*2
        assert set(git('diff','--name-only','HEAD^','HEAD',cwd=src).splitlines())=={css,'internal/gui/tests/quota-single-window.test.cjs'}
    return dict(head=head,tree=tree,files=files,clean=True)
def validate():
    cfg=json.loads((C/'source.json').read_text());hashes=json.loads((C/'control-hashes.json').read_text())
    assert os.environ['GITHUB_REPOSITORY']=='xue-ding-e/Magpie-Next'
    assert os.environ['GITHUB_EVENT_NAME']=='push'
    assert os.environ['GITHUB_REF']=='refs/heads/'+cfg['branch']
    assert all(p.startswith(PREFIX) and '..' not in Path(p).parts for p in hashes)
    actual={str(p.relative_to(ROOT)) for p in C.rglob('*') if p.is_file() and 'node_modules' not in p.parts}
    assert actual==set(hashes)|{PREFIX+'control-hashes.json'}
    for p,h in hashes.items():assert sha(ROOT/p)==h,p
    assert git('rev-parse','HEAD')==os.environ['GITHUB_SHA']
    assert git('rev-list','--parents','-n','1','HEAD').split()==[os.environ['GITHUB_SHA'],cfg['candidate']]
    assert git('show','-s','--format=%an%n%ae%n%cn%n%ce','HEAD').splitlines()==[cfg['author_name'],cfg['author_email']]*2
    assert set(git('diff','--name-only','HEAD^','HEAD').splitlines())==set(hashes)|{PREFIX+'control-hashes.json','.github/workflows/quota-fold-pair.yml'}
    assert [len(i['tests']) for i in cfg['files']]==[6]
    assert cfg['per_file_seconds']==180 and cfg['baseline_seconds']==180 and cfg['candidate_seconds']==180 and cfg['pair_seconds']==360
    assert json.loads((C/'package.json').read_text())['dependencies']=={'playwright':'1.62.1'}
    return cfg,{which:sourceproof(cfg,which) for which in ('base','candidate')}
if __name__=='__main__':validate();print('Exact unchanged fixtures and source controls verified; unchanged usage-fold comparison only.')
