"""No browsers: fabricated TAP, source/control mocks and harmless owned children."""
import json,os,subprocess,sys,tempfile
from pathlib import Path
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).resolve().parent))
import gate,run
cfg=json.loads((gate.C/'source.json').read_text());item=cfg['files'][0];names=item['tests']
def tap(fail=0,infra=False):
    lines=['TAP version 13']
    for i,n in enumerate(names,1):
        lines+=['# Subtest: '+n,('not ok' if i<=fail else 'ok')+' '+str(i)+' - '+n,'  ---',"  type: 'test'"]
        if i<=fail:lines += ["  failureType: 'testCodeFailure'","  code: 'LAUNCH_ERROR'" if infra else "  code: 'ERR_ASSERTION'","  name: 'Error'" if infra else "  name: 'AssertionError'",'  error: original synthetic assertion']
        lines+=['  ...']
    lines+=['1..6']+['# '+k+' '+str(v) for k,v in dict(tests=6,pass_=6-fail,fail=fail,cancelled=0,skipped=0,todo=0).items()]
    return '\n'.join(lines).replace('# pass_ ','# pass ')
for fails in (0,1,2,6):
    r=run.classify(tap(fails),int(bool(fails)),names);assert r['comparable'] and r['passed']==(fails==0)
for bad,code in ((tap(2),0),(tap(0),1),(tap(2),2),(tap(2).replace('# fail 2','# fail 1'),1),(tap(2).replace('# skipped 0','# skipped 1'),1),(tap().replace('# tests 6',''),0),(tap()+'\n# tests 6',0),(tap().replace('1..6','1..7'),0),(tap().replace(names[5],'wrongname'),0),(tap(2,True),1)):
    assert not run.classify(bad,code,names)['comparable']
for flag in ('timeout','interrupted'):assert not run.classify(tap(2),1,names,**{flag:True})['comparable']
with tempfile.TemporaryDirectory(prefix='fold-controls.') as td:
    t=Path(td)
    with (t/'child.log').open('w') as stream:
        good=run.execute([sys.executable,'-c','print("synthetic")'],t,dict(os.environ),stream,2)
        timed=run.execute([sys.executable,'-c','import time;time.sleep(20)'],t,dict(os.environ),stream,.1)
    assert good['exit']==0 and good['process_reaped']
    assert timed['timeout'] and timed['process_reaped'] and timed['elapsed_seconds']<3

def fakegit(*a,cwd=None):
    w=Path(cwd).name
    if a==('rev-parse','HEAD'):return cfg[w]
    if a==('rev-parse','HEAD^{tree}'):return cfg[w+'_tree']
    if a==('status','--porcelain'):return ''
    if a==('rev-list','--parents','-n','1','HEAD'):return cfg['candidate']+' '+cfg['base']
    if a==('show','-s','--format=%an%n%ae%n%cn%n%ce','HEAD'):return '\n'.join([cfg['author_name'],cfg['author_email']]*2)
    if a==('diff','--name-only','HEAD^','HEAD'):return 'internal/gui/assets/app.css\ninternal/gui/tests/quota-single-window.test.cjs'
    raise AssertionError(a)
def fakesha(p):return cfg[p.relative_to(gate.ROOT).parts[0]+'_css_sha256'] if str(p).endswith('/app.css') else item['sha256']
def rejected(fn):
    try:fn()
    except AssertionError:return
    raise AssertionError('invalid accepted')
with patch.object(gate,'git',fakegit),patch.object(gate,'sha',fakesha):
    for w in ('base','candidate'):assert gate.sourceproof(cfg,w)['files'][item['path']]==item['sha256']
    for target,value in ((('rev-parse','HEAD'),'wrongsource'),(('rev-parse','HEAD^{tree}'),'wrongtree'),(('status','--porcelain'),' M fixture'),(('rev-list','--parents','-n','1','HEAD'),'wrongparent'),(('show','-s','--format=%an%n%ae%n%cn%n%ce','HEAD'),'wrongidentity')):
        with patch.object(gate,'git',lambda *a,cwd=None,target=target,value=value:value if a==target else fakegit(*a,cwd=cwd)):rejected(lambda:gate.sourceproof(cfg,'candidate'))
    with patch.object(gate,'sha',lambda p:'tampered'):rejected(lambda:gate.sourceproof(cfg,'base'))
hashes=json.loads((gate.C/'control-hashes.json').read_text());changes='\n'.join(set(hashes)|{gate.PREFIX+'control-hashes.json','.github/workflows/quota-fold-pair.yml'})
def controller_git(*a,cwd=None):
    if a==('rev-parse','HEAD'):return 'controller'
    if a==('rev-list','--parents','-n','1','HEAD'):return 'controller '+cfg['candidate']
    if a==('show','-s','--format=%an%n%ae%n%cn%n%ce','HEAD'):return '\n'.join([cfg['author_name'],cfg['author_email']]*2)
    if a==('diff','--name-only','HEAD^','HEAD'):return changes
    raise AssertionError(a)
env=dict(GITHUB_REPOSITORY='xue-ding-e/Magpie-Next',GITHUB_EVENT_NAME='push',GITHUB_REF='refs/heads/'+cfg['branch'],GITHUB_SHA='controller')
with patch.object(gate,'git',controller_git),patch.object(gate,'sourceproof',lambda cfg,w:{'head':cfg[w]}),patch.dict(os.environ,env):
    gate.validate()
    for k,v in (('GITHUB_REF','refs/heads/main'),('GITHUB_REPOSITORY','wrong/repo'),('GITHUB_EVENT_NAME','workflow_dispatch')):
        with patch.dict(os.environ,{k:v}):rejected(gate.validate)
    with patch.object(gate,'sha',lambda p:'tampered'):rejected(gate.validate)
# Real scheduler; only process execution, temp-location guard and source reads mocked.
with tempfile.TemporaryDirectory(prefix='fold-scheduler.') as td:
    root=Path(td);control=root/'controls';control.mkdir()
    for n in ('source.json','control-hashes.json','offline.cjs'):(control/n).write_bytes((gate.C/n).read_bytes())
    for n,d in (('playwright/package.json',{'version':'1.62.1'}),('playwright-core/browsers.json',{'browsers':[]})):
        p=control/'node_modules'/n;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(json.dumps(d))
    for scenario,count in (('green',2),('ordinary-fail',2),('no-summary',1),('timeout',1),('launch',1)):
        calls=[]
        def fakeexecute(command,cwd,env,stream,seconds):
            w=Path(cwd).name;calls.append(w);assert seconds<=180
            failing=scenario in ('ordinary-fail','launch');stream.write('partial output' if scenario=='no-summary' else tap(2 if failing else 0,scenario=='launch'))
            return dict(exit=1 if failing else 0,timeout=scenario=='timeout',interrupted=False,process_reaped=True,elapsed_seconds=0)
        proof={w:dict(head=cfg[w],tree=cfg[w+'_tree'],files={item['path']:item['sha256']},clean=True) for w in ('base','candidate')}
        with patch.object(run,'ROOT',root),patch.object(run,'C',control),patch.object(gate,'validate',lambda:(cfg,proof)),patch.object(gate,'sourceproof',lambda cfg,w:proof[w]),patch.object(gate,'sha',lambda p:item['sha256']),patch.object(run,'environment',lambda tmp,out:{}),patch.object(run,'execute',fakeexecute),patch.object(subprocess,'check_output',lambda *a,**kw:'v22.23.4'),patch.dict(os.environ,{'RUNNER_TEMP':str(root)}):
            try:run.main()
            except SystemExit as e:assert scenario=='ordinary-fail' and e.code==1
            except AssertionError:assert scenario in ('no-summary','timeout','launch')
            else:assert scenario=='green'
        assert calls==['base','candidate'][:count]
        summary=json.loads((root/'reports/summary.json').read_text());assert summary['source_before']==summary['source_after'] and not summary['full_suite_passed']
        if scenario=='ordinary-fail':assert summary['state']=='pair-complete-red' and not summary['current_pair_passed']
        if scenario=='green':assert summary['state']=='pair-complete-green-this-run-only' and summary['prior_evidence_retained']
        assert len(list((root/'reports').rglob('result.tap')))==count
        (root/'reports').rename(root/('reports-'+scenario))
print('PASS consistent pass/fail outcomes; ordinary fail continues both sides but exits RED; missing summary/timeout/launch stops candidate; exact six-case counts/names/exit/skip/source/tree/hash/identity/ref gates; owned cleanup and retained TAP/proofs. No browser/CI/network/Go.')
