"""Focused CSS red/green, not complete product acceptance. Synthetic fixtures only."""
import hashlib,json,os,re,signal,subprocess,tempfile,time
from pathlib import Path
C=Path(__file__).resolve().parent
WORK=Path(os.environ['GITHUB_WORKSPACE']).resolve()
TEST='internal/gui/tests/agent-disconnect-preview.test.cjs'
FILES=[TEST,'internal/gui/tests/agent-disconnect.test.cjs','internal/gui/tests/agent-leave-magpie.test.cjs','internal/gui/tests/modal-morph.test.cjs']
def classify(tap,code,count,red=False):
    counts={k:[int(x) for x in re.findall(r'^# '+k+r' (\d+)$',tap,re.M)] for k in ['tests','pass','fail','cancelled','skipped','todo']}
    complete=all(len(v)==1 for v in counts.values()) and counts['tests']==[count] and all(counts[k]==[0] for k in ['cancelled','skipped','todo'])
    bad=any(x in tap for x in ['TimeoutError','browserType.launch:',"Executable doesn't exist",'MODULE_NOT_FOUND','SyntaxError:','Diagnostic evidence unavailable','Unmocked browser network refused'])
    accepted=complete and not bad and ((code==1 and counts['fail'][0]>0 and 'the preview spans the body at 890x800' in tap and 'ERR_ASSERTION' in tap) if red else (code==0 and counts['fail']==[0] and counts['pass']==[count]))
    return {'counts':counts,'exit':code,'complete':complete,'accepted':bool(accepted),'expected_red':red}
def main():
    report=WORK/'flex-reports';report.mkdir()
    old=WORK/'baseline';new=WORK/'candidate'
    # The same added test exercises old and new CSS. No baseline product change.
    assert subprocess.check_output(['git','status','--porcelain'],cwd=old,text=True)==''
    (old/TEST).write_bytes((new/TEST).read_bytes())
    assert subprocess.check_output(['git','diff','--name-only'],cwd=old,text=True).strip()==TEST
    stages=[('baseline-webkit',old,'webkit',[TEST],4,120,True),('candidate-chromium',new,'chromium',FILES,11,240,False),('candidate-webkit',new,'webkit',FILES,11,240,False),('candidate-aside-both',new,None,['internal/gui/tests/aside-native.test.cjs'],8,180,False)]
    results=[];start=time.monotonic()
    try:
      for name,source,engine,files,count,budget,red in stages:
        out=report/name;out.mkdir()
        with tempfile.TemporaryDirectory(prefix='ma.',dir=os.environ['RUNNER_TEMP']) as temp:
          home=Path(temp)/'home';home.mkdir(mode=0o700)
          env=dict(os.environ,HOME=str(home),USERPROFILE=str(home),XDG_CONFIG_HOME=str(home/'.config'),XDG_CACHE_HOME=str(home/'.cache'),XDG_DATA_HOME=str(home/'.local/share'),XDG_STATE_HOME=str(home/'.local/state'),APPDATA=str(home/'AppData/Roaming'),LOCALAPPDATA=str(home/'AppData/Local'),TMPDIR=temp,ARTIFACT_DIR=str(out/'screens'),NODE_OPTIONS='--require='+str(C/'offline.cjs'))
          for key in list(env):
            if key.startswith('AUTH_DIAG_') or key in ['AUTH_WIDTH_PROBE','AUTH_FULL_REPORTS','BROWSER']:env.pop(key,None)
          if engine:env['BROWSER']=engine
          before=time.monotonic()
          with (out/'result.tap').open('w') as log:
            p=subprocess.Popen(['node','--test','--test-reporter=tap','--test-concurrency=1',*files],cwd=source,env=env,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
            try:code=p.wait(timeout=budget)
            except subprocess.TimeoutExpired:os.killpg(p.pid,signal.SIGKILL);p.wait();code=124
          result=classify((out/'result.tap').read_text(),code,count,red);result.update(stage=name,elapsed_seconds=time.monotonic()-before);results.append(result)
          (out/'summary.json').write_text(json.dumps(result,indent=2))
          assert subprocess.check_output(['git','status','--porcelain'],cwd=new,text=True)==''
          assert subprocess.check_output(['git','diff','--name-only'],cwd=old,text=True).strip()==TEST
          assert result['accepted'], 'Focused stage failed; retain evidence, do not rerun or weaken assertions'
    finally:
      (report/'summary.json').write_text(json.dumps({'results':results,'elapsed_seconds':time.monotonic()-start,'complete':len(results)==4 and all(x['accepted'] for x in results),'not_full_acceptance':True},indent=2))
if __name__=='__main__':main()
