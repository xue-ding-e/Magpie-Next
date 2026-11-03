"""Light local controls only. Never installs or launches Playwright."""
import hashlib,json,os,shutil,subprocess,sys,tempfile
from pathlib import Path
from unittest.mock import patch
import sys
sys.path.insert(0,str(Path(__file__).resolve().parent))
import gate,run
C=Path(__file__).resolve().parent
fixture=Path(sys.argv[1]) if len(sys.argv)>1 else gate.ROOT/'source/internal/gui/tests/quota-count-wrap.test.cjs'
original=fixture.read_bytes();before=hashlib.sha256(original).hexdigest()
with tempfile.TemporaryDirectory(prefix='quota-control.',dir='/tmp') as td:
    t=Path(td);generated=t/'instrumented.cjs'
    subprocess.run([sys.executable,'-I','-B',str(C/'prepare.py'),str(fixture),str(generated)],check=True,timeout=5)
    src=original.decode();out=generated.read_text()
    for line in src.splitlines():
        if 'assert.' in line: assert line in out
    assert out.count('waitForTimeout(300)')==1
    for mode in ('pass','cut','reject','hang'):
        subprocess.run(['node',str(C/'mock-test.cjs'),str(generated),mode],check=True,timeout=5)
    bad=t/'bad.cjs';bad.write_bytes(original+b'\n')
    p=subprocess.run([sys.executable,'-I','-B',str(C/'prepare.py'),str(bad),str(t/'bad-out')],capture_output=True,timeout=5)
    assert p.returncode!=0 and not (t/'bad-out').exists()
    with (t/'child.log').open('w') as stream:
        good=run.execute([sys.executable,'-c','print("owned")'],t,dict(os.environ),stream,2)
        hung=run.execute([sys.executable,'-c','import time; time.sleep(30)'],t,dict(os.environ),stream,.1)
    assert good['exit']==0 and good['process_reaped'] and not good['timeout']
    assert hung['timeout'] and hung['process_reaped'] and hung['elapsed_seconds']<3
    full='\n'.join('# '+k+' '+str(v) for k,v in dict(tests=2,pass_=0,fail=2,cancelled=0,skipped=0,todo=0).items()).replace('pass_','pass')
    assert run.terminal(full,1)['terminal_summary']
    for invalid in (full+'\n# tests 2',full.replace('# tests 2','# tests 3'),full.replace('# fail 2','')): assert not run.terminal(invalid,1)['terminal_summary']
    # Gate positive and negative controls with in-memory Git replies, real hash/file checks.
    root=t/'root';control=root/'.github/quota-cut';shutil.copytree(C,control,ignore=shutil.ignore_patterns('node_modules','__pycache__'))
    sf=root/'source/internal/gui/tests/quota-count-wrap.test.cjs';sf.parent.mkdir(parents=True);sf.write_bytes(original)
    cfg=json.loads((control/'source.json').read_text());hashes=json.loads((control/'control-hashes.json').read_text())
    changes='\n'.join(set(hashes)|{'.github/quota-cut/control-hashes.json','.github/workflows/quota-cut.yml'})
    env=dict(GITHUB_REPOSITORY='xue-ding-e/Magpie-Next',GITHUB_EVENT_NAME='push',GITHUB_REF='refs/heads/'+cfg['branch'],GITHUB_SHA='candidate')
    def git(*args,cwd=None):
        if args==('rev-parse','HEAD'): return cfg['source'] if cwd else 'candidate'
        if args==('rev-parse','HEAD^{tree}'): return cfg['tree']
        if args==('rev-list','--parents','-n','1','HEAD'): return 'candidate '+cfg['source']
        if args==('diff','--name-only','HEAD^','HEAD'): return changes
        if args==('status','--porcelain'): return ''
        raise AssertionError(args)
    def rejected():
        try: gate.validate()
        except (AssertionError,KeyError): return
        raise AssertionError('negative gate accepted')
    with patch.object(gate,'ROOT',root),patch.object(gate,'CONTROL',control),patch.object(gate,'git',git),patch.dict(os.environ,env):
        gate.validate()
        with patch.dict(os.environ,{'GITHUB_REF':'refs/heads/main'}): rejected()
        with patch.dict(os.environ,{'GITHUB_REPOSITORY':'wrong/repo'}): rejected()
        with patch.object(gate,'git',lambda *a,**kw:'candidate '+cfg['source']+' other' if a[0]=='rev-list' else git(*a,**kw)): rejected()
        with patch.object(gate,'git',lambda *a,**kw:'wrongtree' if a==('rev-parse','HEAD^{tree}') else git(*a,**kw)): rejected()
        sf.write_bytes(original+b'\n');rejected();sf.write_bytes(original)
        (control/'offline.cjs').write_text('tampered');rejected()
    # Exercise real runner generation/restoration/reporting without a browser or install.
    modules=control/'node_modules'
    for name,data in (('playwright/package.json',{'version':'1.62.1'}),('playwright-core/browsers.json',{'browsers':[]})):
        p=modules/name;p.parent.mkdir(parents=True,exist_ok=True);p.write_text(json.dumps(data))
    def fake_execute(command,cwd,env,stream):
        assert hashlib.sha256(sf.read_bytes()).hexdigest()==cfg['instrumented_sha256']
        assert env['NODE_OPTIONS']=='--require='+str(control/'offline.cjs')
        assert not any(k in env for k in ('GITHUB_TOKEN','AWS_ACCESS_KEY_ID'))
        stream.write(full.replace('# pass 0','# pass 2').replace('# fail 2','# fail 0'))
        return dict(exit=0,timeout=False,interrupted=False,process_reaped=True,elapsed_seconds=0)
    # Local managed /tmp has a .git sentinel; mock only the temp-location check
    # for this no-browser harness, leaving the shipped runner strict.
    with patch.object(run,'validate_temp',lambda tmp:None),patch.object(run,'ROOT',root),patch.object(run,'C',control),patch.object(gate,'validate',lambda:cfg),patch.object(gate,'git',lambda *a,**kw:''),patch.object(subprocess,'check_output',lambda *a,**kw:'v22.23.4'),patch.dict(os.environ,{'RUNNER_TEMP':str(t)}):
        with patch.object(run,'execute',fake_execute): run.main()
        assert sf.read_bytes()==original
        summary=json.loads((root/'reports/summary.json').read_text())
        assert summary['source_restored'] and summary['state']=='complete' and summary['acceptance'] is False
        (root/'reports').rename(root/'passed-reports')
        def fail_execute(*a,**kw): raise RuntimeError('simulated runner failure')
        with patch.object(run,'execute',fail_execute):
            try: run.main()
            except RuntimeError as e: assert str(e)=='simulated runner failure'
            else: raise AssertionError('expected simulated failure')
        assert sf.read_bytes()==original
        summary=json.loads((root/'reports/summary.json').read_text())
        assert summary['source_restored'] and summary['state']=='controller-error' and not summary['assertion_evidence']
        assert (root/'reports/instrumented.test.cjs').exists()

assert hashlib.sha256(fixture.read_bytes()).hexdigest()==before
print('PASS local generation hash, source gate positive/negative, complete/incomplete TAP, owned child timeout/reap, runner success/error restoration and retained evidence. No browser/CI/Go/network.')
