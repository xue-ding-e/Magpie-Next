"""Six sequential full four-case fixture runs; no application asset changes."""
import hashlib,importlib.util,json,os,re,signal,subprocess,tempfile,time
from pathlib import Path
CONTROL=Path(__file__).resolve().parent;ROOT=CONTROL.parents[1];SOURCE=ROOT/'source';ACTIVE=None
spec=importlib.util.spec_from_file_location('inject',CONTROL/'inject.py');inject=importlib.util.module_from_spec(spec);spec.loader.exec_module(inject)
def stop(pid):
 if pid is not None:
  try:os.killpg(pid,signal.SIGKILL)
  except ProcessLookupError:pass
def terminate(signum,frame):
 stop(ACTIVE);raise SystemExit(124)
def classify(tap,code,expect):
 counts={k:[int(x) for x in re.findall(r'^# '+k+r' (\d+)$',tap,re.M)] for k in ('tests','pass','fail','cancelled','skipped','todo')}
 errors=re.findall(r'^  error: (.*(?:\n    [^\n]*)*)',tap,re.M)
 faults=any(s in tap for s in ('browserType.launch:',"Executable doesn't exist",'Host system is missing dependencies','MODULE_NOT_FOUND','SyntaxError:','Unmocked browser network refused'))
 complete=not faults and all(len(v)==1 for v in counts.values()) and counts['tests']==[4] and counts['cancelled']==counts['skipped']==counts['todo']==[0] and counts['pass'][0]+counts['fail'][0]==4
 passed=complete and code==0 and counts['pass']==[4] and counts['fail']==[0]
 failed=complete and code==1 and counts['pass']==[0] and counts['fail']==[4] and len(errors)==4
 needle={'old-delayed':'the last account was picked and saved: undefined','wrong-payload':'the last account was picked and saved: ["incorrect"]','http-error':'the settings save completed successfully','aborted-post':'page.waitForResponse: Timeout 5000ms exceeded'}
 matched=passed if expect=='pass' else failed and all(needle[expect] in e for e in errors)
 return dict(exit=code,counts=counts,errors=errors,complete=complete,expected_result_matched=bool(matched))
def main():
 global ACTIVE
 signal.signal(signal.SIGTERM,terminate)
 target=SOURCE/'internal/gui/tests/proto-menu-overflow.test.cjs';original=target.read_bytes();assert hashlib.sha256(original).hexdigest()=='b41ba6de19cc18d0e651bc663eb18864749a7ce32ab9d4fc96503d4f8102bf47'
 candidate=(CONTROL/'candidate.cjs').read_text();assert hashlib.sha256(candidate.encode()).hexdigest()=='746408358e760028a3f0b13b1ca6f157e75da3244c82004d7b3c2b95600a6e8e'
 package=CONTROL/'node_modules';assert json.loads((package/'playwright/package.json').read_text())['version']=='1.62.1'
 report=ROOT/'reports';report.mkdir(exist_ok=False);(report/'browser-revisions.json').write_bytes((package/'playwright-core/browsers.json').read_bytes());results=[]
 stages=[('old-delayed',original.decode(),'delayed','old-delayed'),('new-delayed',candidate,'delayed','pass'),('new-original-fixture',candidate,None,'pass'),('wrong-payload',candidate,'wrong-payload','wrong-payload'),('http-error',candidate,'http-error','http-error'),('aborted-post',candidate,'aborted-post','aborted-post')]
 try:
  for label,text,mode,expected in stages:
   out=report/label;out.mkdir();generated=inject.instrument(text,mode) if mode else text;target.write_text(generated);(out/'fixture-sha256.txt').write_text(hashlib.sha256(generated.encode()).hexdigest()+'\n')
   with tempfile.TemporaryDirectory(prefix='mp.',dir=os.environ['RUNNER_TEMP']) as temp:
    t=Path(temp);assert all(not (p/'.git').exists() for p in [t,*t.parents]);assert len(str(t/'playwright-webkit-xxxxxxxx/inspector-pipe').encode())<108
    home=t/'home';home.mkdir(mode=0o700);env=dict(os.environ,HOME=str(home),USERPROFILE=str(home),XDG_CONFIG_HOME=str(home/'.config'),XDG_CACHE_HOME=str(home/'.cache'),XDG_DATA_HOME=str(home/'.local/share'),XDG_STATE_HOME=str(home/'.local/state'),APPDATA=str(home/'AppData/Roaming'),LOCALAPPDATA=str(home/'AppData/Local'),TMPDIR=temp,TMP=temp,TEMP=temp,NODE_PATH=str(package),NODE_OPTIONS='--require='+str(CONTROL/'offline.cjs'),ARTIFACT_DIR=str(out/'screens'))
    for key in ('BROWSER','PLAYWRIGHT_CHANNEL','AUTH_FULL_REPORTS','AUTH_DIAG_TRACE','AUTH_DIAG_CLICK','AUTH_DIAG_GEOMETRY','AUTH_WIDTH_PROBE'):env.pop(key,None)
    start=time.monotonic()
    with (out/'result.tap').open('w') as stream:
     proc=subprocess.Popen(['node','--test','--test-reporter=tap','--test-concurrency=1',str(target)],cwd=SOURCE,env=env,stdout=stream,stderr=subprocess.STDOUT,start_new_session=True);ACTIVE=proc.pid
     try:code=proc.wait(timeout=90)
     except subprocess.TimeoutExpired:stop(proc.pid);proc.wait();code=124
     finally:stop(proc.pid);ACTIVE=None
    result=classify((out/'result.tap').read_text(),code,expected);result.update(stage=label,elapsed_seconds=time.monotonic()-start,process_reaped=proc.poll() is not None);results.append(result);(out/'summary.json').write_text(json.dumps(result,indent=2)+'\n')
   if not result['expected_result_matched']:break
 finally:
  target.write_bytes(original)
  subprocess.run(['git','diff','--exit-code'],cwd=SOURCE,check=True)
  summary=dict(product='7feed2b7e7a94ad0263ad3f44a1b0ab3b2fc065c',results=results,full_gui_passed=False,scope='Fixture timing causality only; no main/product assets/full-suite acceptance.')
  (report/'summary.json').write_text(json.dumps(summary,indent=2)+'\n');print(json.dumps(summary,indent=2))
 assert len(results)==6 and all(r['expected_result_matched'] for r in results),'Missing or unexpected real browser result; no blind retry'
if __name__=='__main__':main()
