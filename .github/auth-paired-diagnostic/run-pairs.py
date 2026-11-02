"""Bounded diagnostics, not a product-success gate; original files unchanged."""
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import time

CONTROL = Path(__file__).resolve().parent
WORK = Path(os.environ['GITHUB_WORKSPACE']).resolve()
SOURCES = {'candidate':WORK/'candidate', 'baseline':WORK/'baseline'}
GROUPS = {'network-library':('library-all.test.cjs',240,600), 'webkit-preview-width':('agent-disconnect-preview.test.cjs',120,300)}
def summarize(tap, code, label, filename, elapsed, group, denied):
    counts={key:[int(x) for x in re.findall(r'^# '+key+r' (\d+)$',tap,re.M)] for key in ['tests','pass','fail','cancelled','skipped','todo']}
    launch_fault=any(x in tap for x in ['browserType.launch:', "Executable doesn't exist", 'Host system is missing dependencies', 'MODULE_NOT_FOUND', 'SyntaxError:'])
    safe=code in (0,1) and not launch_fault
    complete=safe and all(len(v)==1 for v in counts.values()) and counts['tests'][0]>0 and counts['skipped']==[0] and counts['todo']==[0] and counts['cancelled']==[0]
    return {'source':label,'file':filename,'exit':code,'elapsed_seconds':elapsed,'counts':counts,'complete_diagnostic':complete,'process_safe_to_continue':safe,'product_success':False,'hypothesis_seen': denied if group=='network-library' else 'the preview spans the body at 890x800' in tap}

def main(name):
    assert name in GROUPS
    if name=='webkit-preview-width':
        previous=json.loads((WORK/'diagnostic-reports/network-library/summary.json').read_text())
        assert previous.get('safe_to_continue') is True and len(previous.get('results',[]))==2 and all(r.get('process_safe_to_continue') is True for r in previous['results']), 'Previous pair did not safely terminate both sides'
    filename, per_run, total = GROUPS[name]
    report=WORK/'diagnostic-reports'/name
    report.mkdir(parents=True,exist_ok=True)
    start=time.monotonic()
    results=[]
    for label, source in SOURCES.items():
        assert time.monotonic()-start < total
        out=report/label
        out.mkdir()
        with tempfile.TemporaryDirectory(prefix='ma.',dir=os.environ['RUNNER_TEMP']) as tmp:
            home=Path(tmp)/'home';home.mkdir(mode=0o700)
            env=dict(os.environ, HOME=str(home), USERPROFILE=str(home), XDG_CONFIG_HOME=str(home/'.config'), XDG_CACHE_HOME=str(home/'.cache'), XDG_DATA_HOME=str(home/'.local/share'), XDG_STATE_HOME=str(home/'.local/state'), APPDATA=str(home/'AppData/Roaming'), LOCALAPPDATA=str(home/'AppData/Local'), TMPDIR=tmp, BROWSER='webkit', ARTIFACT_DIR=str(out/'screens'), AUTH_DIAG_DENIED=str(out/'denied.jsonl'), NODE_OPTIONS='--require='+str(CONTROL/'full-offline.cjs'))
            # File-load markers are not reused from the full run.
            env.pop('AUTH_FULL_REPORTS',None)
            command=['node','--test','--test-reporter=tap','--test-concurrency=1','internal/gui/tests/'+filename]
            before=time.monotonic()
            with (out/'result.tap').open('w') as stream:
                p=subprocess.Popen(command,cwd=source,env=env,stdout=stream,stderr=subprocess.STDOUT,start_new_session=True)
                try: code=p.wait(timeout=min(per_run,total-(time.monotonic()-start)))
                except subprocess.TimeoutExpired:
                    os.killpg(p.pid,signal.SIGKILL);p.wait();code=124
            tap=(out/'result.tap').read_text()
            result = summarize(tap, code, label, filename, time.monotonic()-before, name, (out/'denied.jsonl').exists())
            results.append(result)
            (out/'summary.json').write_text(json.dumps(result,indent=2))
            subprocess.run(['git','diff','--exit-code'],cwd=source,check=True)
            if not result['process_safe_to_continue']:
                break  # No second browser run after an incomplete timed-out process tree.
    summary={'group':name,'elapsed_seconds':time.monotonic()-start,'results':results,'safe_to_continue': len(results)==2 and all(r['process_safe_to_continue'] for r in results),'note':'A completed diagnostic is not a green product gate. Compare exact failure signatures/denied paths across fixed sources.'}
    (report/'summary.json').write_text(json.dumps(summary,indent=2))
    print(json.dumps(summary,indent=2))
    assert len(results)==2 and all(r['complete_diagnostic'] for r in results), 'Incomplete diagnostic, compile/launch failure or timeout is not reproduction'
    assert all(r['exit']==0 for r in results), 'Observed test failures retained; inspect paired evidence, not a product-success gate'

if __name__=='__main__':
    main(sys.argv[1])
