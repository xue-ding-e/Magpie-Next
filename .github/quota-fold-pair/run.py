"""Same original usage-fold fixture on two sources; ordinary assertion failures retained, never success."""
import json,os,re,shutil,signal,subprocess,sys,tempfile,time
from pathlib import Path
sys.path.insert(0,str(Path(__file__).resolve().parent))
import gate
C=gate.C;ROOT=gate.ROOT
def classify(tap,code,names,timeout=False,interrupted=False):
    fields={k:re.findall(r'^# '+k+r' (\d+)$',tap,re.M) for k in ('tests','pass','fail','cancelled','skipped','todo')}
    counts={k:int(v[0]) for k,v in fields.items()} if all(len(v)==1 for v in fields.values()) else None
    lines=re.findall(r'^(ok|not ok) (\d+) - (.+)$',tap,re.M)
    plan=re.findall(r'^1\.\.(\d+)$',tap,re.M)
    valid=bool(counts and len(names)==6 and counts['tests']==6 and counts['pass']+counts['fail']==6 and all(counts[k]==0 for k in ('cancelled','skipped','todo')) and plan==['6'] and [v[2] for v in lines]==names and [int(v[1]) for v in lines]==list(range(1,7)) and not timeout and not interrupted)
    blocks=re.split(r'(?m)^# Subtest: ',tap)[1:]
    ordinary=False
    if valid:
        failed=[b for b in blocks if re.search(r'(?m)^not ok ',b)]
        valid=bool(len(blocks)==6 and len(failed)==counts['fail'] and sum(v[0]=='ok' for v in lines)==counts['pass'] and code==(1 if failed else 0))
        ordinary=bool(valid and all("code: 'ERR_ASSERTION'" in b and "name: 'AssertionError'" in b and "failureType: 'testCodeFailure'" in b for b in failed))
    return dict(comparable=ordinary,terminal_complete=valid,counts=counts,passed=bool(ordinary and counts['fail']==0),ordinary_assertion_failures=counts['fail'] if ordinary else None)

def kill(proc):
    try:os.killpg(proc.pid,signal.SIGKILL)
    except ProcessLookupError:pass
    proc.wait(timeout=5)
def execute(command,cwd,env,stream,seconds):
    proc=None;interrupted=False;timed_out=False;handlers={};start=time.monotonic()
    def stop(sig,frame):
        nonlocal interrupted
        interrupted=True
        if proc:kill(proc)
    try:
        for sig in (signal.SIGINT,signal.SIGTERM):handlers[sig]=signal.signal(sig,stop)
        proc=subprocess.Popen(command,cwd=cwd,env=env,stdout=stream,stderr=subprocess.STDOUT,start_new_session=True)
        try:proc.wait(timeout=seconds)
        except subprocess.TimeoutExpired:timed_out=True
    finally:
        try:
            if proc:kill(proc)
        finally:
            for sig,handler in handlers.items():signal.signal(sig,handler)
    return dict(exit=proc.returncode,timeout=timed_out,interrupted=interrupted,process_reaped=proc.poll() is not None,elapsed_seconds=time.monotonic()-start)
def environment(tmp,out):
    assert all(not (p/'.git').exists() for p in (tmp,*tmp.parents))
    assert len(str(tmp/'playwright-webkit-xxxxxxxx/inspector-pipe').encode())<108
    home=tmp/'home';home.mkdir(mode=0o700)
    env={k:os.environ[k] for k in ('PATH','LANG','TZ','PLAYWRIGHT_BROWSERS_PATH') if k in os.environ}
    env.update(HOME=str(home),USERPROFILE=str(home),XDG_CONFIG_HOME=str(home/'config'),XDG_CACHE_HOME=str(home/'cache'),XDG_DATA_HOME=str(home/'data'),XDG_STATE_HOME=str(home/'state'),APPDATA=str(home/'AppData/Roaming'),LOCALAPPDATA=str(home/'AppData/Local'),TMPDIR=str(tmp),TMP=str(tmp),TEMP=str(tmp),NODE_PATH=str(C/'node_modules'),NODE_OPTIONS='--require='+str(C/'offline.cjs'),AUTH_FULL_REPORTS=str(out),ARTIFACT_DIR=str(out/'screens'))
    return env

def main():
    report=ROOT/'reports';report.mkdir(exist_ok=False)
    summary=dict(targeted_only=True,full_suite_passed=False,state='preflight',files=[],new_job_seconds=None,new_job_cost_status='pending official completed job receipt',prior_evidence_retained=True)
    cfg=None;started=time.monotonic()
    def save():
        summary['controller_elapsed_seconds']=time.monotonic()-started
        p=report/'summary.tmp';p.write_text(json.dumps(summary,indent=2,ensure_ascii=False)+'\n');p.replace(report/'summary.json')
    try:
        cfg,proof=gate.validate();summary.update(source_before=proof,prior_test_seconds=cfg['prior_test_seconds'],prior_job_seconds=cfg['prior_job_seconds'])
        assert subprocess.check_output(['node','--version'],text=True).strip()=='v22.23.4'
        assert json.loads((C/'node_modules/playwright/package.json').read_text())['version']=='1.62.1'
        for n in ('source.json','control-hashes.json','offline.cjs'):shutil.copyfile(C/n,report/n)
        shutil.copyfile(C/'node_modules/playwright-core/browsers.json',report/'browser-revisions.json');save()
        for which,items,budget in (('base',cfg['files'],180),('candidate',cfg['files'],180)):
            phase=time.monotonic()
            for item in items:
                remaining=budget-(time.monotonic()-phase)
                assert remaining>0,'phase budget exhausted'
                assert gate.sha(ROOT/which/item['path'])==item['sha256']
                key=which+'-'+Path(item['path']).stem;out=report/key;out.mkdir();(out/'screens').mkdir()
                record=dict(source=cfg[which],file=item['path'],sha256=item['sha256'],state='running',report=key);summary['files'].append(record);summary['state']='running';save()
                with tempfile.TemporaryDirectory(prefix='mq.',dir=os.environ['RUNNER_TEMP']) as td:
                    env=environment(Path(td),out)
                    with (out/'result.tap').open('w') as stream:
                        result=execute(['node','--test','--test-reporter=tap','--test-concurrency=1',item['path']],ROOT/which,env,stream,min(180,remaining))
                record.update(result);record.update(classify((out/'result.tap').read_text(errors='replace'),result['exit'],item['tests'],result['timeout'],result['interrupted']))
                record['state']='passed' if record['passed'] else 'ordinary-assertion-failed' if record['comparable'] else 'incomplete-or-infrastructure-failure';save()
                assert record['comparable'],key+': noncomparable result; stopped without advancing'
        summary['state']='pair-complete-green-this-run-only' if all(r['passed'] for r in summary['files']) else 'pair-complete-red'
        summary['current_pair_passed']=all(r['passed'] for r in summary['files'])
        summary['total_top_level_tests']=12
    except BaseException as e:
        summary.update(state='failed',error=repr(e));raise
    finally:
        try:
            if cfg is not None:summary['source_after']={which:gate.sourceproof(cfg,which) for which in ('base','candidate')}
        except BaseException as e:
            summary.update(state='source-proof-failed',source_proof_error=repr(e));raise
        finally:save()
    if not summary.get('current_pair_passed',False):raise SystemExit(1)
if __name__=='__main__':main()
