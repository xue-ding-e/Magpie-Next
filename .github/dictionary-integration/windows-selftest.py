"""Native Windows Job Object ownership proof using only synthetic sleeping children."""
import importlib.util,json,os,sys,tempfile
from pathlib import Path
if sys.platform!='win32': raise SystemExit('ENVIRONMENT NOT RUN: Windows proof requires Windows')
p=Path(__file__).resolve().parent
spec=importlib.util.spec_from_file_location('owned_windows_probe',p/'owned_process.py')
owned=importlib.util.module_from_spec(spec);spec.loader.exec_module(owned)
with tempfile.TemporaryDirectory(prefix='dictionary-owned-proof-',dir=os.environ['RUNNER_TEMP']) as directory:
    root=Path(directory)
    normal=owned.run_owned([sys.executable,'-I','-B','-c','print("synthetic normal exit")'],cwd=root,environment=dict(os.environ),log_path=root/'normal.log',timeout=10)
    if normal!={'returncode':0,'timed_out':False,'orphaned':False,'clean':True}: raise SystemExit('normal ownership proof failed')
    for mode in ('timeout','orphan'):
        ready=root/(mode+'.ready')
        code='import subprocess,sys,time; from pathlib import Path; subprocess.Popen([sys.executable,"-I","-B","-c","import time; time.sleep(30)"]); Path(sys.argv[1]).write_text("synthetic child started"); '+('time.sleep(30)' if mode=='timeout' else 'sys.exit(0)')
        result=owned.run_owned([sys.executable,'-I','-B','-c',code,str(ready)],cwd=root,environment=dict(os.environ),log_path=root/(mode+'.log'),timeout=5 if mode=='timeout' else 10)
        expected={'returncode':None if mode=='timeout' else 0,'timed_out':mode=='timeout','orphaned':True,'clean':True}
        if not ready.is_file() or result!=expected: raise SystemExit('descendant ownership proof failed: '+mode+' '+repr(result))
        print(json.dumps({'scenario':mode,'owned':result,'child_started':True}))
print('PASS three native Windows ownership scenarios; no browser/Go/resource workload')
