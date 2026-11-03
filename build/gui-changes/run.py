"""Required changed-GUI checks; never a replacement for the full GUI suite."""
import hashlib,importlib.util,json,os,re,shutil,signal,stat,subprocess,sys,tempfile,time
from pathlib import Path
CONTROL=Path(__file__).resolve().parent;ROOT=CONTROL.parents[1];ACTIVE=None;PER_FILE_SECONDS=180
spec=importlib.util.spec_from_file_location('impact',CONTROL.parent/'next-package-impact.py');impact=importlib.util.module_from_spec(spec);spec.loader.exec_module(impact)
def git(*args):return subprocess.check_output(['git',*args],cwd=ROOT,stderr=subprocess.PIPE)
def valid_paths(paths):
 assert isinstance(paths,list) and 1<=len(paths)<=8 and len(set(paths))==len(paths),'Missing/oversized/duplicate GUI evidence'
 assert all(isinstance(p,str) and impact.TEST.fullmatch(p.encode('ascii')) for p in paths),'Untrusted GUI path'
 return sorted(paths)
def verify():
 assert os.environ['GITHUB_REPOSITORY']=='xue-ding-e/Magpie-Next'
 head=os.environ['GITHUB_SHA'];assert impact.SHA.fullmatch(head) and git('rev-parse','HEAD').decode().strip()==head
 assert not git('status','--porcelain','--untracked-files=no')
 event=os.environ['GITHUB_EVENT_NAME'];assert event in ('push','workflow_dispatch')
 before=os.environ['BEFORE_SHA'] if event=='push' else git('rev-parse','HEAD^').decode().strip()
 assert impact.SHA.fullmatch(before) and before!='0'*40
 for sha in (before,head):git('cat-file','-e',sha+'^{commit}')
 raw=git('diff','--raw','--no-abbrev','-z','--no-renames',before,head,'--');selected=valid_paths(impact.classify(raw)[3]);assert os.environ['GUI_REQUIRED']=='true'
 assert valid_paths(json.loads(os.environ['GUI_TESTS']))==selected,'Impact outputs differ from actual raw diff'
 files=[]
 for p in selected:
  file=ROOT/p;assert file.resolve()==file and file.is_file() and not file.is_symlink()
  assert all(not parent.is_symlink() for parent in file.parents)
  mode,typ,sha,name=git('ls-tree','HEAD','--',p).decode().strip().split(None,3);assert mode=='100644' and typ=='blob' and name==p
  b=file.read_bytes();assert hashlib.sha1(b'blob '+str(len(b)).encode()+b'\0'+b).hexdigest()==sha
  files.append({'path':p,'git_blob':sha,'sha256':hashlib.sha256(b).hexdigest()})
 return {'head':head,'before':before,'raw_sha256':hashlib.sha256(raw).hexdigest(),'files':files}
def counts(text,code):
 v={k:re.findall(r'^# '+k+r' (\d+)$',text,re.M) for k in ('tests','pass','fail','cancelled','skipped','todo')}
 c={k:int(x[0]) for k,x in v.items()} if all(len(x)==1 for x in v.values()) else None
 passed=bool(c and code==0 and c['tests']>0 and c['tests']==c['pass'] and all(c[k]==0 for k in ('fail','cancelled','skipped','todo')))
 return {'counts':c,'passed':passed,'exit':code}
def kill(pid):
 if pid:
  try:os.killpg(pid,signal.SIGKILL)
  except ProcessLookupError:pass
def terminate(signum,frame):kill(ACTIVE);raise SystemExit(124)
def owned():
 p=Path(os.environ['GUI_OWNED_ROOT']);parent=Path(os.environ['RUNNER_TEMP']).resolve()
 assert p.parent==parent and re.fullmatch(r'mgc\.[A-Za-z0-9]{6}',p.name) and not p.is_symlink()
 if p.exists():assert p.is_dir() and p.stat().st_uid==os.getuid()
 return p

def validate_temp(tmp):
 assert all(not (p/'.git').exists() for p in [tmp,*tmp.parents])
 assert len(str(tmp/'playwright-webkit-xxxxxxxx/inspector-pipe').encode())<108

def run():
 global ACTIVE
 proof=verify();assert subprocess.check_output(['node','--version'],text=True).strip()=='v22.23.4'
 assert json.loads((CONTROL/'node_modules/playwright/package.json').read_text())['version']=='1.62.1'
 root=owned();assert root.exists();report=ROOT/'reports/gui-changes';report.mkdir(parents=True,exist_ok=False);results=[]
 (report/'source-proof.json').write_text(json.dumps(proof,indent=2)+'\n');(report/'browser-revisions.json').write_bytes((CONTROL/'node_modules/playwright-core/browsers.json').read_bytes())
 signal.signal(signal.SIGTERM,terminate);signal.signal(signal.SIGINT,terminate)
 def save():
  (report/'summary.json').write_text(json.dumps({'source':proof,'results':results,'full_gui_suite_passed':False,'scope':'All cases in each changed GUI file only; complete827 requirements remain independent.'},indent=2)+'\n')
 save()
 for item in proof['files']:
  out=report/Path(item['path']).name;out.mkdir()
  with tempfile.TemporaryDirectory(prefix='t.',dir=root) as temp:
   tmp=Path(temp);validate_temp(tmp)
   home=tmp/'home';home.mkdir(mode=0o700);env=dict(os.environ,HOME=str(home),USERPROFILE=str(home),XDG_CONFIG_HOME=str(home/'.config'),XDG_CACHE_HOME=str(home/'.cache'),XDG_DATA_HOME=str(home/'.local/share'),XDG_STATE_HOME=str(home/'.local/state'),APPDATA=str(home/'AppData/Roaming'),LOCALAPPDATA=str(home/'AppData/Local'),TMPDIR=temp,TMP=temp,TEMP=temp,NODE_PATH=str(CONTROL/'node_modules'),NODE_OPTIONS='--require='+str(CONTROL/'offline.cjs'),ARTIFACT_DIR=str(out/'screens'))
   for k in ('BROWSER','PLAYWRIGHT_CHANNEL','AUTH_FULL_REPORTS','AUTH_DIAG_TRACE','AUTH_DIAG_CLICK','AUTH_DIAG_GEOMETRY','AUTH_WIDTH_PROBE'):env.pop(k,None)
   start=time.monotonic()
   with (out/'result.tap').open('w') as stream:
    proc=subprocess.Popen(['node','--test','--test-reporter=tap','--test-concurrency=1',item['path']],cwd=ROOT,env=env,stdout=stream,stderr=subprocess.STDOUT,start_new_session=True);ACTIVE=proc.pid
    try:code=proc.wait(timeout=PER_FILE_SECONDS)
    except subprocess.TimeoutExpired:kill(proc.pid);proc.wait();code=124
    finally:kill(proc.pid);proc.wait();ACTIVE=None
   result=counts((out/'result.tap').read_text(),code);result.update(file=item['path'],elapsed_seconds=time.monotonic()-start,process_reaped=True);results.append(result);save()
   if sum(p.stat().st_size for p in report.rglob('*') if p.is_file())>300*1024**2:raise AssertionError('Synthetic artifact budget exceeded')
 git('diff','--exit-code')
 assert len(results)==len(proof['files']) and all(r['passed'] for r in results),'Changed GUI files failed, skipped, empty or incomplete'
if __name__=='__main__':
 if sys.argv[1:]==['--verify']:print(json.dumps(verify(),indent=2))
 elif sys.argv[1:]==['--cleanup']:
  p=owned()
  if p.exists():shutil.rmtree(p)
 elif not sys.argv[1:]:run()
 else:raise AssertionError('Unknown mode')
