"""Two version environments; one unchanged full width fixture, no CSS experiments."""
import json,os,re,signal,subprocess,sys,tempfile,time
from pathlib import Path
CONTROL=Path(__file__).resolve().parent
ROOT=CONTROL.parents[1]
SOURCE=ROOT/'source'
ACTIVE=None
def stop_group(pid):
 if pid is not None:
  try:os.killpg(pid,signal.SIGKILL)
  except ProcessLookupError:pass
def terminate(signum,frame):
 stop_group(ACTIVE)
 raise SystemExit(124)
def classify(tap,code):
 counts={k:[int(x) for x in re.findall(r'^# '+k+r' (\d+)$',tap,re.M)] for k in ('tests','pass','fail','cancelled','skipped','todo')}
 fault=any(s in tap for s in ('browserType.launch:',"Executable doesn't exist",'Host system is missing dependencies','MODULE_NOT_FOUND','SyntaxError:','Unmocked browser network refused'))
 complete=code in (0,1) and not fault and all(len(x)==1 for x in counts.values()) and counts['tests']==[2] and counts['cancelled']==counts['skipped']==counts['todo']==[0] and counts['pass'][0]+counts['fail'][0]==2 and ((code==0 and counts['pass']==[2] and counts['fail']==[0]) or (code==1 and counts['fail'][0]>0))
 return {'exit':code,'counts':counts,'complete':complete,'safe_to_continue':complete,'width_assertion_seen':'the preview spans the body at' in tap,'product_success':False}
def main():
 global ACTIVE
 signal.signal(signal.SIGTERM,terminate)
 report=ROOT/'reports';report.mkdir(exist_ok=False);results=[]
 for label,version in [('v1611','1.61.1'),('v1621','1.62.1')]:
  out=report/label;out.mkdir();package=CONTROL/'versions'/label/'node_modules';browsers=Path(os.environ['RUNNER_TEMP'])/('magpie-width-'+label)
  assert json.loads((package/'playwright/package.json').read_text())['version']==version
  (out/'browser-revisions.json').write_bytes((package/'playwright-core/browsers.json').read_bytes())
  with tempfile.TemporaryDirectory(prefix='ma.',dir=os.environ['RUNNER_TEMP']) as temp:
   t=Path(temp);assert all(not (p/'.git').exists() for p in [t,*t.parents]);assert len(str(t/'playwright-webkit-xxxxxxxx/inspector-pipe').encode())<108
   home=t/'home';home.mkdir(mode=0o700)
   env=dict(os.environ,HOME=str(home),USERPROFILE=str(home),XDG_CONFIG_HOME=str(home/'.config'),XDG_CACHE_HOME=str(home/'.cache'),XDG_DATA_HOME=str(home/'.local/share'),XDG_STATE_HOME=str(home/'.local/state'),APPDATA=str(home/'AppData/Roaming'),LOCALAPPDATA=str(home/'AppData/Local'),TMPDIR=temp,TMP=temp,TEMP=temp,NODE_PATH=str(package),NODE_OPTIONS='--require='+str(CONTROL/'offline.cjs'),PLAYWRIGHT_BROWSERS_PATH=str(browsers),BROWSER='webkit',ARTIFACT_DIR=str(out/'screens'))
   for key in ('PLAYWRIGHT_CHANNEL','AUTH_FULL_REPORTS','AUTH_DIAG_TRACE','AUTH_DIAG_CLICK','AUTH_DIAG_GEOMETRY','AUTH_WIDTH_PROBE'):env.pop(key,None)
   command=['node','--test','--test-reporter=tap','--test-concurrency=1','internal/gui/tests/agent-disconnect-preview.test.cjs'];start=time.monotonic()
   with (out/'result.tap').open('w') as stream:
    proc=subprocess.Popen(command,cwd=SOURCE,env=env,stdout=stream,stderr=subprocess.STDOUT,start_new_session=True)
    ACTIVE=proc.pid
    try:code=proc.wait(timeout=180)
    except subprocess.TimeoutExpired:stop_group(proc.pid);proc.wait();code=124
    finally:stop_group(proc.pid);ACTIVE=None
   result=classify((out/'result.tap').read_text(),code);result.update(version=version,elapsed_seconds=time.monotonic()-start)
   results.append(result);(out/'summary.json').write_text(json.dumps(result,indent=2)+'\n')
   subprocess.run(['git','diff','--exit-code'],cwd=SOURCE,check=True)
   if not result['safe_to_continue']:break
 summary={'product':'7feed2b7e7a94ad0263ad3f44a1b0ab3b2fc065c','results':results,'product_success':False,'scope':'Version comparison only; original assertions unchanged, no product fix or full-suite acceptance.'};(report/'summary.json').write_text(json.dumps(summary,indent=2)+'\n');print(json.dumps(summary,indent=2))
 assert len(results)==2 and all(r['complete'] for r in results),'Incomplete/environment-blocked version comparison'
 assert all(r['exit']==0 and r['counts']['pass']==[2] and r['counts']['fail']==[0] for r in results),'Original test failures retained; compare evidence, do not call diagnostic green'
if __name__=='__main__':main()
