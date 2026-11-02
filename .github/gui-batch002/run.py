"""Per-file terminal accounting, at most two independent original test processes."""
import hashlib,importlib.util,json,os,re,shutil,signal,subprocess,sys,tempfile,time
from pathlib import Path
CONTROL=Path(__file__).resolve().parent
ROOT=CONTROL.parents[1]
SOURCE=ROOT/'source'
ACTIVE={}
STOP=False

def kill(pid):
 try:os.killpg(pid,signal.SIGKILL)
 except ProcessLookupError:pass

def terminate(signum,frame):
 global STOP
 STOP=True
 for pid in list(ACTIVE):kill(pid)

def counts(tap,code):
 values={k:[int(x) for x in re.findall(r'^# '+k+r' (\d+)$',tap,re.M)] for k in ('tests','pass','fail','cancelled','skipped','todo')}
 complete=all(len(v)==1 for v in values.values())
 c={k:v[0] for k,v in values.items()} if complete else None
 consistent=bool(c and c['tests']>0 and sum(c[k] for k in ('pass','fail','cancelled','skipped','todo'))==c['tests'])
 passed=bool(consistent and code==0 and c['pass']==c['tests'] and all(c[k]==0 for k in ('fail','cancelled','skipped','todo')))
 return {'counts':c,'terminal_summary':complete,'counts_consistent':consistent,'passed':passed}

def observed_rss(pids):
 total=0
 for p in Path('/proc').iterdir():
  if not p.name.isdigit():continue
  try:
   stat=(p/'stat').read_text();fields=stat[stat.rfind(')')+2:].split()
   if int(fields[2]) in pids:total+=int(fields[21])*os.sysconf('SC_PAGE_SIZE')
  except (OSError,ValueError,IndexError):continue
 return total

def validate_temp(tmp):
 assert all(not (p/'.git').exists() for p in [tmp,*tmp.parents])
 assert len(str(tmp/'playwright-webkit-xxxxxxxx/inspector-pipe').encode())<108

def run():
 global STOP
 signal.signal(signal.SIGTERM,terminate);signal.signal(signal.SIGINT,terminate)
 manifest=json.loads((CONTROL/'inventory.json').read_text());selected=manifest['selected'];queue=list(selected)
 assert len(selected)==16 and len(set(selected))==16 and manifest['concurrency']==2
 budget=manifest['flow_budget'];assert budget['spent_test_seconds']+manifest['batch_seconds']<=budget['maximum_test_seconds']
 assert budget['spent_job_seconds']+45*60<=budget['maximum_job_seconds']
 report=ROOT/'reports';report.mkdir(exist_ok=False)
 for evidence in ('prior-ledger.json','prior-receipt.json'):shutil.copyfile(CONTROL/evidence,report/evidence)
 node_version=subprocess.check_output(['node','--version'],text=True).strip()
 assert json.loads((CONTROL/'node_modules/playwright/package.json').read_text())['version']=='1.62.1'
 (report/'browser-revisions.json').write_bytes((CONTROL/'node_modules/playwright-core/browsers.json').read_bytes())
 expected={x['path']:x for x in manifest['files']};assert len(expected)==827
 spec=importlib.util.spec_from_file_location('carry',CONTROL/'carry.py');carry=importlib.util.module_from_spec(spec);spec.loader.exec_module(carry)
 records=carry.seed_records(manifest,node_version)
 start=time.monotonic();peak=0;limit_reason=None
 def save():
  payload={'source':manifest['product'],'batch':manifest['batch_id'],'playwright':'1.62.1','node':node_version,'elapsed_seconds':time.monotonic()-start,'files':records,'peak_observed_rss':peak,'stop_reason':limit_reason,'complete_suite_passed':False,'note':'Only16 newly executed files;8 inherited source-bound prior passes are marked passed_prior, never counted as current execution. All827 plus visual10 remain mandatory.'}
  target=report/'ledger.json';temp=report/'ledger.tmp';temp.write_text(json.dumps(payload,indent=2)+'\n');temp.replace(target)
 save()
 try:
  while queue or ACTIVE:
   elapsed=time.monotonic()-start
   rss=observed_rss(set(ACTIVE));peak=max(peak,rss)
   report_size=sum(p.stat().st_size for p in report.rglob('*') if p.is_file())
   if STOP or elapsed>=manifest['batch_seconds'] or rss>manifest['max_observed_process_rss_bytes'] or report_size>manifest['max_report_bytes'] or shutil.disk_usage(report).free<manifest['minimum_free_bytes']:
    limit_reason=limit_reason or ('cancelled' if STOP else 'batch-time-or-resource-limit');STOP=True
    for pid in list(ACTIVE):kill(pid)
   while queue and len(ACTIVE)<2 and not STOP:
    name=queue.pop(0);file=SOURCE/name;assert hashlib.sha256(file.read_bytes()).hexdigest()==expected[name]['sha256']
    key=hashlib.sha256(name.encode()).hexdigest()[:16];out=report/key;out.mkdir()
    temporary=tempfile.TemporaryDirectory(prefix='ma.',dir=os.environ['RUNNER_TEMP']);tmp=Path(temporary.name)
    validate_temp(tmp)
    home=tmp/'home';home.mkdir(mode=0o700)
    env=dict(os.environ,HOME=str(home),USERPROFILE=str(home),XDG_CONFIG_HOME=str(home/'.config'),XDG_CACHE_HOME=str(home/'.cache'),XDG_DATA_HOME=str(home/'.local/share'),XDG_STATE_HOME=str(home/'.local/state'),APPDATA=str(home/'AppData/Roaming'),LOCALAPPDATA=str(home/'AppData/Local'),TMPDIR=str(tmp),TMP=str(tmp),TEMP=str(tmp),NODE_PATH=str(CONTROL/'node_modules'),NODE_OPTIONS='--require='+str(CONTROL/'offline.cjs'),ARTIFACT_DIR=str(out/'screens'))
    for k in ('BROWSER','PLAYWRIGHT_CHANNEL','AUTH_FULL_REPORTS','AUTH_DIAG_TRACE','AUTH_DIAG_CLICK','AUTH_DIAG_GEOMETRY','AUTH_WIDTH_PROBE'):env.pop(k,None)
    stream=(out/'result.tap').open('w');cmd=['node','--test','--test-reporter=tap','--test-concurrency=1',name]
    proc=subprocess.Popen(cmd,cwd=SOURCE,env=env,stdout=stream,stderr=subprocess.STDOUT,start_new_session=True)
    ACTIVE[proc.pid]={'proc':proc,'name':name,'out':out,'stream':stream,'temporary':temporary,'started':time.monotonic(),'timed_out':False}
    records[name].update(state='running',report=key,command=cmd,executed_in_batch=True);save()
   for pid,item in list(ACTIVE.items()):
    proc=item['proc'];duration=time.monotonic()-item['started']
    if proc.poll() is None and duration>=manifest['per_file_seconds']:item['timed_out']=True;kill(pid)
    if proc.poll() is None:continue
    kill(pid);proc.wait();item['stream'].close()
    tap=(item['out']/'result.tap').read_text();result=counts(tap,proc.returncode)
    state='timeout' if item['timed_out'] else 'interrupted' if STOP else 'passed' if result['passed'] else 'failed' if result['terminal_summary'] else 'incomplete'
    result.update(state=state,exit=proc.returncode,process_reaped=True,elapsed_seconds=duration,source=manifest['product'],file_sha256=expected[item['name']]['sha256'])
    records[item['name']].update(result);(item['out']/'summary.json').write_text(json.dumps(result,indent=2)+'\n')
    item['temporary'].cleanup();del ACTIVE[pid];save()
   if STOP and not ACTIVE:break
   if ACTIVE:time.sleep(.5)
 finally:
  for pid,item in list(ACTIVE.items()):
   kill(pid);item['proc'].wait();item['stream'].close();item['temporary'].cleanup();records[item['name']]['state']='interrupted';del ACTIVE[pid]
  save()
 subprocess.run(['git','diff','--exit-code'],cwd=SOURCE,check=True)
 assert not STOP and all(records[name]['state']=='passed' for name in selected),'Batch has failed/incomplete files; full-suite gate remains red'
if __name__=='__main__':run()
