"""H1 diagnostic only: original 75s, one compiled binary, 1 then 3 processes."""
import hashlib
import json
import os
from pathlib import Path
import re
import runpy
import signal
import subprocess
import sys
import time

COMMON = runpy.run_path(str(Path(__file__).with_name('stream-budget-control.py')))
SOURCE, FILE = COMMON['SOURCE'], COMMON['FILE']
EXPECTED = {'TestPluginStreamLargeReplies'} | {'TestPluginStreamLargeReplies/'+s for s in ['JSON_fast','JSON_throttled','SSE_fast','SSE_throttled']}

def out(args, **kw):
    return subprocess.check_output(args, text=True, **kw).strip()

def processes(group):
    rows = out(['ps','-axo','pid=,pgid=,%cpu=,rss=']).splitlines()
    return [dict(pid=int(x[0]),pgid=int(x[1]),cpu_percent=float(x[2]),rss_kib=int(x[3])) for row in rows if (x:=row.split()) and int(x[1])==group]

def stop(proc):
    # Unknown ps/kill state fails closed: caller must retain that home.
    if proc.poll() is None or processes(proc.pid):
        try: os.killpg(proc.pid,signal.SIGTERM)
        except ProcessLookupError: pass
        try: proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            os.killpg(proc.pid,signal.SIGKILL);proc.wait(timeout=10)
        if processes(proc.pid):
            os.killpg(proc.pid,signal.SIGKILL);time.sleep(1)
    assert proc.poll() is not None and not processes(proc.pid), 'group exit unknown'

def main():
    assert sys.platform=='darwin'
    assert out(['git','rev-parse','HEAD'])==SOURCE
    assert not out(['git','status','--porcelain','--untracked-files=no'])
    assert out(['go','env','GOVERSION'])=='go1.26.10'
    assert out(['bun','--version'])=='1.4.2'
    original=Path(FILE).read_text()
    assert hashlib.sha256(original.encode()).hexdigest()=='ccf3702f7badc220acfd7a56f2cd19bbbe4c5b73e94de6de1394c997e62846e4'
    assert COMMON['BUDGET'] not in original
    source=COMMON['diagnostics'](original)
    source=COMMON['one'](source,'\t"net/http/httptest"\n','\t"net/http/httptest"\n\t"runtime"\n')
    source=COMMON['one'](source,'func TestPluginStreamLargeReplies(t *testing.T) {\n','func TestPluginStreamLargeReplies(t *testing.T) {\n\tfmt.Printf("H1 runtime NumCPU=%d GOMAXPROCS=%d\\n", runtime.NumCPU(), runtime.GOMAXPROCS(0))\n')
    root=Path(os.environ['RUNNER_TEMP'])
    binary=root/'magpie-h1-gateway.race'
    assert not binary.exists()
    helper=runpy.run_path('build/ci_home.py')
    envbase=os.environ.copy()
    envbase.pop("MAGPIE_GATEWAY_TEST_STAND_INS",None)
    for key in ['GOPATH','GOMODCACHE','GOCACHE']:envbase[key]=out(['go','env',key])
    print(json.dumps({'source':SOURCE,'diagnostic_sha256':hashlib.sha256(source.encode()).hexdigest(),'go':out(['go','version']),'bun':out(['bun','--version']),'hw_ncpu':out(['sysctl','-n','hw.ncpu']),'hw_memsize':out(['sysctl','-n','hw.memsize'])}),flush=True)
    summaries=[]
    try:
        Path(FILE).write_text(source)
        assert out(['git','diff','--name-only'])==FILE
        build=subprocess.Popen(['go','test','-race','-c','-o',str(binary),'./internal/gateway'],env=envbase,start_new_session=True)
        try:
            assert build.wait(timeout=900)==0,'race compilation failed'
            assert not processes(build.pid),'compiler group left descendants'
        finally:
            stop(build)
        binary_sha=hashlib.sha256(binary.read_bytes()).hexdigest()
        print('H1 binary_sha256='+binary_sha,flush=True)
        for count in [1,3]:
            assert hashlib.sha256(binary.read_bytes()).hexdigest()==binary_sha
            children=[]
            try:
                for i in range(count):
                    record=helper['create_home'](str(root));home=Path(record['home'])
                    env=envbase.copy()
                    for key,sub in {'HOME':'','USERPROFILE':'','XDG_CONFIG_HOME':'config','XDG_CACHE_HOME':'cache','XDG_DATA_HOME':'data','XDG_STATE_HOME':'state','APPDATA':'appdata','LOCALAPPDATA':'localappdata','TMPDIR':'tmp'}.items():
                        target=home/sub;target.mkdir(exist_ok=True);env[key]=str(target)
                    log=root/f'magpie-h1-{count}-{i}.log'
                    item=dict(record=record,home=home,log=log,proc=None);children.append(item)
                    with log.open('w') as f:
                        item['proc']=subprocess.Popen([str(binary),'-test.v','-test.count=1','-test.timeout=10m','-test.run=^TestPluginStreamLargeReplies$'],cwd='internal/gateway',env=env,stdout=f,stderr=subprocess.STDOUT,start_new_session=True)
                deadline=time.monotonic()+660
                while any(x['proc'].poll() is None for x in children):
                    print('H1 RESOURCE '+json.dumps({'arm_processes':count,'monotonic':time.monotonic(),'groups':[processes(x['proc'].pid) for x in children]}),flush=True)
                    if time.monotonic()>deadline:raise TimeoutError('H1 bounded observation expired')
                    time.sleep(5)
                for i,x in enumerate(children):
                    proc=x['proc'];text=x['log'].read_text()
                    print(f'H1 LOG arm={count} process={i}\n'+text,flush=True)
                    passed=set(re.findall(r'^\s*--- PASS: (\S+)',text,re.M))
                    skipped=set(re.findall(r'^\s*--- SKIP: (\S+)',text,re.M))
                    ran=set(re.findall(r'^=== RUN\s+(\S+)',text,re.M))
                    remaining=processes(proc.pid)
                    result=dict(processes=count,index=i,exit=proc.returncode,passed=sorted(passed),skipped=sorted(skipped),missing=sorted(EXPECTED-ran),survivors=remaining)
                    summaries.append(result);print('H1 RESULT '+json.dumps(result),flush=True)
                    assert not remaining,'unexpected survivor; do not start next arm'
            finally:
                cleanup_errors=[]
                for x in children:
                    try:
                        if x['proc'] is not None:stop(x['proc'])
                        helper['remove_home'](str(root),x['record'])
                        assert not x['home'].exists()
                    except BaseException as e:
                        print('H1 retaining unverified home '+str(x['home']),flush=True)
                        cleanup_errors.append(repr(e))
                assert not cleanup_errors,cleanup_errors
    finally:
        Path(FILE).write_text(original)
        # Binary is an owned temporary artifact; retain if cleanup was unknown.
    assert not out(['git','status','--porcelain','--untracked-files=no'])
    print('H1 SUMMARY '+json.dumps(summaries),flush=True)
    (root/'magpie-h1-summary.json').write_text(json.dumps(summaries,indent=2))
    assert len(summaries)==4
    for r in summaries:
        assert not r['missing'] and not r['skipped'],'incomplete experiment'
        assert r['exit']==0 and set(r['passed'])==EXPECTED,'observed test failure; classify logs, not a fix verdict'

if __name__=='__main__':main()
