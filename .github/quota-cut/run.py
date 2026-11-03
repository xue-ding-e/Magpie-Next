"""Single original file instrumented in isolated source checkout, never acceptance."""
import hashlib,json,os,re,shutil,signal,subprocess,tempfile,time
from pathlib import Path
import sys
sys.path.insert(0,str(Path(__file__).resolve().parent))
import gate
C=gate.CONTROL;ROOT=gate.ROOT

def terminate_group(proc):
    try: os.killpg(proc.pid,signal.SIGKILL)
    except ProcessLookupError: pass
    proc.wait(timeout=5)

def terminal(tap,code):
    vals={k:re.findall(r'^# '+k+r' (\d+)$',tap,re.M) for k in ('tests','pass','fail','cancelled','skipped','todo')}
    complete=all(len(v)==1 for v in vals.values())
    counts={k:int(v[0]) for k,v in vals.items()} if complete else None
    complete=bool(complete and counts['tests']==2 and all(counts[k]==0 for k in ('cancelled','skipped','todo')) and sum(counts[k] for k in ('pass','fail','cancelled','skipped','todo'))==2)
    return {'terminal_summary':complete,'counts':counts,'exit':code}

def execute(command,cwd,env,stream,seconds=120):
    proc=None;start=time.monotonic();timed_out=False;interrupted=False;handlers={}
    def stop(signum,frame):
        nonlocal interrupted
        interrupted=True
        if proc: terminate_group(proc)
    try:
        for sig in (signal.SIGTERM,signal.SIGINT): handlers[sig]=signal.signal(sig,stop)
        proc=subprocess.Popen(command,cwd=cwd,env=env,stdout=stream,stderr=subprocess.STDOUT,start_new_session=True)
        try: proc.wait(timeout=seconds)
        except subprocess.TimeoutExpired: timed_out=True
    finally:
        try:
            if proc: terminate_group(proc)
        finally:
            for sig,handler in handlers.items(): signal.signal(sig,handler)
    return {'exit':proc.returncode,'timeout':timed_out,'interrupted':interrupted,'process_reaped':proc.poll() is not None,'elapsed_seconds':time.monotonic()-start}

def validate_temp(tmp):
    assert all(not (p/'.git').exists() for p in (tmp,*tmp.parents))
    assert len(str(tmp/'playwright-webkit-xxxxxxxx/inspector-pipe').encode())<108

def main():
    report=ROOT/'reports';report.mkdir(exist_ok=False)
    result={'instrumented_diagnostic':True,'acceptance':False,'state':'preflight','assertion_evidence':False}
    fixture=None;original=None
    try:
        cfg=gate.validate();src=ROOT/'source';fixture=src/cfg['fixture'];original=fixture.read_bytes()
        result.update(source=cfg['source'],original_sha256=gate.digest(fixture),prior_test_seconds=cfg['prior_test_seconds'],prior_job_seconds=cfg['prior_job_seconds'],job_elapsed_status='pending official completed-job receipt')
        assert subprocess.check_output(['node','--version'],text=True).strip()=='v22.23.4'
        assert json.loads((C/'node_modules/playwright/package.json').read_text())['version']=='1.62.1'
        for name in ('source.json','control-hashes.json','prepare.py','offline.cjs'): shutil.copyfile(C/name,report/name)
        shutil.copyfile(C/'node_modules/playwright-core/browsers.json',report/'browser-revisions.json')
        generated=report/'instrumented.test.cjs'
        subprocess.run(['python3','-I','-B',str(C/'prepare.py'),str(fixture),str(generated)],check=True,timeout=5)
        result['instrumented_sha256']=gate.digest(generated)
        assert result['instrumented_sha256']==cfg['instrumented_sha256']
        fixture.write_bytes(generated.read_bytes())
        with tempfile.TemporaryDirectory(prefix='mq.',dir=os.environ['RUNNER_TEMP']) as td:
            tmp=Path(td);validate_temp(tmp)
            home=tmp/'home';home.mkdir(mode=0o700)
            # Allowlist avoids inheriting any credentials into synthetic fixtures.
            env={k:os.environ[k] for k in ('PATH','LANG','TZ','PLAYWRIGHT_BROWSERS_PATH') if k in os.environ}
            env.update(HOME=str(home),USERPROFILE=str(home),XDG_CONFIG_HOME=str(home/'config'),XDG_CACHE_HOME=str(home/'cache'),XDG_DATA_HOME=str(home/'data'),XDG_STATE_HOME=str(home/'state'),APPDATA=str(home/'AppData/Roaming'),LOCALAPPDATA=str(home/'AppData/Local'),TMPDIR=td,TMP=td,TEMP=td,NODE_PATH=str(C/'node_modules'),NODE_OPTIONS='--require='+str(C/'offline.cjs'),AUTH_FULL_REPORTS=str(report))
            with (report/'result.tap').open('w') as stream:
                result.update(execute(['node','--test','--test-reporter=tap','--test-concurrency=1',cfg['fixture']],src,env,stream))
        tap=(report/'result.tap').read_text(errors='replace');result.update(terminal(tap,result['exit']))
        observations=[]
        for line in tap.splitlines():
            if line.startswith('# QUOTA_CUT_DIAGNOSTIC '): observations.append(json.loads(line.split('QUOTA_CUT_DIAGNOSTIC ',1)[1]))
        missing=[]
        for obs in observations:
            p=report/f"quota-cut-{obs['engine']}-{obs['width']}.png"
            if not p.exists() or p.read_bytes()[:8]!=b'\x89PNG\r\n\x1a\n': missing.append(p.name)
        known_failures=0
        for block in re.split(r'(?m)^# Subtest: ',tap)[1:]:
            if re.search(r'(?m)^not ok ',block) and "code: 'ERR_ASSERTION'" in block and re.search(r'\d+px: .* is cut',block): known_failures+=1
        expected_captures=len(observations)
        capture_markers=tap.count('# QUOTA_DIAGNOSTIC_CAPTURE_OK ')
        result.update(observations=observations,missing_screenshots=missing,original_cut_assertion_failures=known_failures,
                      diagnostic_failure='QUOTA_DIAGNOSTIC_CAPTURE_FAILED' in tap or bool(missing) or capture_markers!=expected_captures)
        result['original_outcome_identified']=bool(result['counts'] and known_failures==result['counts']['fail'] and known_failures==len(observations))
        result['assertion_evidence']=bool(result['terminal_summary'] and not result['timeout'] and not result['interrupted'] and not result['diagnostic_failure'] and result['original_outcome_identified'])
        result['state']='complete' if result['assertion_evidence'] else 'incomplete-diagnostic'
    except BaseException as error:
        result.update(state='controller-error',error=repr(error));raise
    finally:
        try:
            if fixture is not None and original is not None:
                fixture.write_bytes(original);result['restored_sha256']=gate.digest(fixture)
                assert result['restored_sha256']==result['original_sha256']
                assert not gate.git('status','--porcelain',cwd=ROOT/'source')
                result['source_restored']=True
        finally: (report/'summary.json').write_text(json.dumps(result,indent=2)+'\n')
    assert result['assertion_evidence'] and result['exit']==0,'Diagnostic retained; original failure/incomplete is not acceptance.'
if __name__=='__main__': main()
