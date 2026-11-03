"""Lightweight tests only: fabricated TAP, source-proof mocks, harmless child processes."""
import json,os,subprocess,sys,tempfile
from pathlib import Path
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).resolve().parent))
import gate,run
cfg=json.loads((gate.C/'source.json').read_text());names=cfg['files'][0]['tests']
def tap(names,red=False):
    chunks=['TAP version 13']
    for i,n in enumerate(names,1):
        chunks.extend(['# Subtest: '+n,('not ok' if red else 'ok')+' '+str(i)+' - '+n])
        if red:chunks.extend(['  ---','  error: |-','    '+run.CUT,"  code: 'ERR_ASSERTION'","  name: 'AssertionError'",'  expected: false','  actual: true',"  operator: 'strictEqual'",'  ...'])
    chunks+=['1..'+str(len(names))]+['# '+k+' '+str(v) for k,v in dict(tests=len(names),pass_=0 if red else len(names),fail=len(names) if red else 0,cancelled=0,skipped=0,todo=0).items()]
    return '\n'.join(chunks).replace('# pass_ ','# pass ')
red=tap(names,True)
assert run.classify(red,1,names,True)['accepted']
for item in cfg['files']:assert run.classify(tap(item['tests']),0,item['tests'])['accepted']
for bad in (red.replace(run.CUT,'navigation failed'),red.replace('1100px:','1099px:'),red.replace("code: 'ERR_ASSERTION'","code: 'OTHER'"),red.replace('# tests 2','# tests 3'),red.replace('# skipped 0','# skipped 1'),red.replace('# tests 2',''),red+'\n# tests 2',red.replace('1..2','1..3'),red.replace(names[1],'wrong engine'),red.replace('not ok 2','ok 2')):
    assert not run.classify(bad,1,names,True)['accepted']
for code in (0,2,-9):assert not run.classify(red,code,names,True)['accepted']
assert not run.classify(red,1,names,True,timeout=True)['accepted']
assert not run.classify(red,1,names,True,interrupted=True)['accepted']
green=tap(names)
for bad,code in ((green,1),(green.replace('# pass 2','# pass 1'),0),(green.replace('# skipped 0','# skipped 1'),0),(green.replace('# tests 2',''),0),(green.replace('ok 2','not ok 2'),0)):
    assert not run.classify(bad,code,names)['accepted']
# Prove that parent summaries/nested subtest totals cannot be double-counted.
assert not run.classify(green+'\n'+green,0,names)['accepted']
with tempfile.TemporaryDirectory(prefix='quota-rg.') as td:
    t=Path(td)
    with (t/'child.log').open('w') as stream:
        passed=run.execute([sys.executable,'-c','print("synthetic")'],t,dict(os.environ),stream,2)
        timed=run.execute([sys.executable,'-c','import time;time.sleep(20)'],t,dict(os.environ),stream,.1)
    assert passed['exit']==0 and passed['process_reaped'] and not passed['timeout']
    assert timed['timeout'] and timed['process_reaped'] and timed['elapsed_seconds']<3
# Exact source, tree, author, single parent, changed file and per-file hash controls.
def fakegit(*a,cwd=None):
    which=Path(cwd).name
    if a==('rev-parse','HEAD'):return cfg[which]
    if a==('rev-parse','HEAD^{tree}'):return cfg[which+'_tree']
    if a==('status','--porcelain'):return ''
    if a==('rev-list','--parents','-n','1','HEAD'):return cfg['candidate']+' '+cfg['base']
    if a==('show','-s','--format=%an%n%ae%n%cn%n%ce','HEAD'):return '\n'.join([cfg['author_name'],cfg['author_email']]*2)
    if a==('diff','--name-only','HEAD^','HEAD'):return 'internal/gui/assets/app.css\ninternal/gui/tests/quota-single-window.test.cjs'
    raise AssertionError(a)
def fakesha(p):
    which=p.relative_to(gate.ROOT).parts[0]
    if str(p).endswith('/app.css'):return cfg[which+'_css_sha256']
    return next(i['sha256'] for i in cfg['files'] if str(p).endswith(i['path']))
def reject():
    try:gate.sourceproof(cfg,'candidate')
    except AssertionError:return
    raise AssertionError('invalid proof accepted')
with patch.object(gate,'git',fakegit),patch.object(gate,'sha',fakesha):
    for which in ('base','candidate'):assert gate.sourceproof(cfg,which)['clean']
    for target,value in ((('rev-parse','HEAD'),'wrongsource'),(('rev-parse','HEAD^{tree}'),'wrongtree'),(('status','--porcelain'),' M source'),(('rev-list','--parents','-n','1','HEAD'),cfg['candidate']+' '+cfg['base']+' other'),(('show','-s','--format=%an%n%ae%n%cn%n%ce','HEAD'),'wrongidentity'),(('diff','--name-only','HEAD^','HEAD'),'unexpected/file')):
        with patch.object(gate,'git',lambda *a,cwd=None,target=target,value=value:value if a==target else fakegit(*a,cwd=cwd)):reject()
    with patch.object(gate,'sha',lambda p:'tampered'):reject()
# Real frozen control hashes plus mocked controller Git metadata.
hashes=json.loads((gate.C/'control-hashes.json').read_text())
changes='\n'.join(set(hashes)|{gate.PREFIX+'control-hashes.json','.github/workflows/quota-single-window.yml'})
env=dict(GITHUB_REPOSITORY='xue-ding-e/Magpie-Next',GITHUB_EVENT_NAME='push',GITHUB_REF='refs/heads/'+cfg['branch'],GITHUB_SHA='controller')
def controller_git(*a,cwd=None):
    if a==('rev-parse','HEAD'):return 'controller'
    if a==('rev-list','--parents','-n','1','HEAD'):return 'controller '+cfg['candidate']
    if a==('show','-s','--format=%an%n%ae%n%cn%n%ce','HEAD'):return '\n'.join([cfg['author_name'],cfg['author_email']]*2)
    if a==('diff','--name-only','HEAD^','HEAD'):return changes
    raise AssertionError(a)
def reject_controller():
    try:gate.validate()
    except AssertionError:return
    raise AssertionError('invalid controller accepted')
with patch.object(gate,'git',controller_git),patch.object(gate,'sourceproof',lambda cfg,w:{'head':cfg[w]}),patch.dict(os.environ,env):
    gate.validate()
    for key,value in (('GITHUB_REF','refs/heads/main'),('GITHUB_EVENT_NAME','workflow_dispatch'),('GITHUB_REPOSITORY','wrong/repo')):
        with patch.dict(os.environ,{key:value}):reject_controller()
    with patch.object(gate,'git',lambda *a,**kw:'controller '+cfg['base'] if a[0]=='rev-list' else controller_git(*a,**kw)):reject_controller()
    with patch.object(gate,'sha',lambda p:'tampered'):reject_controller()
# Real scheduler exercised with synthetic output, not browser fixtures.
with tempfile.TemporaryDirectory(prefix='quota-scheduler.') as td:
    root=Path(td);control=root/'controls';control.mkdir()
    for name in ('source.json','control-hashes.json','offline.cjs'):
        (control/name).write_bytes((gate.C/name).read_bytes())
    for name,data in (('playwright/package.json',{'version':'1.62.1'}),('playwright-core/browsers.json',{'browsers':[]})):
        p=control/'node_modules'/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(json.dumps(data))
    for scenario,expected_calls in (('pass',6),('bad-red',1),('bad-green',3)):
        calls=[]
        def fake_execute(command,cwd,env,stream,seconds):
            which=Path(cwd).name;item=next(i for i in cfg['files'] if i['path']==command[-1]);calls.append((which,item['path']))
            assert seconds<=180
            red=which=='base';bad=(scenario=='bad-red' and red) or (scenario=='bad-green' and len(calls)==3)
            stream.write(tap(item['tests'],red) if not bad else 'incomplete synthetic output')
            return dict(exit=1 if red or bad else 0,timeout=False,interrupted=False,process_reaped=True,elapsed_seconds=0)
        proof={w:dict(head=cfg[w],tree=cfg[w+'_tree'],files={},clean=True) for w in ('base','candidate')}
        with patch.object(run,'ROOT',root),patch.object(run,'C',control),patch.object(gate,'validate',lambda:(cfg,proof)),patch.object(gate,'sourceproof',lambda cfg,w:proof[w]),patch.object(gate,'sha',lambda p:next(i['sha256'] for i in cfg['files'] if str(p).endswith(i['path']))),patch.object(run,'environment',lambda tmp,out:{}),patch.object(run,'execute',fake_execute),patch.object(subprocess,'check_output',lambda *a,**kw:'v22.23.4'),patch.dict(os.environ,{'RUNNER_TEMP':str(root)}):
            try:run.main()
            except AssertionError:assert scenario!='pass'
            else:assert scenario=='pass'
        assert len(calls)==expected_calls and calls[0][0]=='base'
        summary=json.loads((root/'reports/summary.json').read_text())
        assert summary['source_before']==summary['source_after'] and not summary['full_suite_passed']
        assert summary['state']==('targeted-redgreen-passed' if scenario=='pass' else 'failed')
        assert len(list((root/'reports').rglob('result.tap')))==expected_calls
        (root/'reports').rename(root/('reports-'+scenario))
print('PASS scheduler exact six-file sequence; bad baseline stops immediately; candidate failure stops later files; retains TAP and before/after source proofs.')
print('PASS red/green exact top-level TAP; missing/duplicate summary, exit/count/skip/timeout/signature/source/tree/parent/identity/hash negatives; owned process timeout/reap. No browser/CI/network/Go.')
