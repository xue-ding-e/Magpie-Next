"""Require every exact synthetic check to pass; no empty match or skip green."""
import json
import os
from pathlib import Path
import importlib.util
import sys

ROOT=Path(__file__).resolve().parents[2]
CONTROL=ROOT/'.github/dictionary-core-validation'
REPORT=ROOT/'.validation-runtime/reports'
REPORT.mkdir(parents=True,exist_ok=True)
phase=sys.argv[1]
if phase=='core':
    expected=json.loads((CONTROL/'tests.json').read_text())
    flags=['-race']
    timeout=660
else:
    raise SystemExit('unknown phase')
names=sorted({name.split('/')[0] for values in expected.values() for name in values})
command=['go','test','-mod=readonly','-tags=nogui','-p=1','-parallel=2','-count=1','-timeout=5m','-json',*flags,'-run=^('+'|'.join(names)+')$',*expected]
spec=importlib.util.spec_from_file_location('dictionary_owned_process',CONTROL/'owned_process.py')
owned=importlib.util.module_from_spec(spec)
spec.loader.exec_module(owned)
process_result=owned.run_owned(command,cwd=ROOT,environment=dict(os.environ),log_path=REPORT/(phase+'.jsonl'),stderr_path=REPORT/(phase+'.stderr'),timeout=timeout)
with (REPORT/(phase+'.jsonl')).open('r',encoding='utf-8') as stream:
    stdout=stream.read((16<<20)+1)
if len(stdout)>16<<20:
    raise SystemExit('test output exceeds evidence bound')
sys.stdout.write(stdout)
with (REPORT/(phase+'.stderr')).open('r',encoding='utf-8') as stream:
    sys.stderr.write(stream.read((16<<20)+1))
passed=set();skipped=[];failed=[]
for line in stdout.splitlines():
    try: item=json.loads(line)
    except json.JSONDecodeError:
        failed.append(('non-JSON diagnostic','build or runner output'))
        continue
    if item.get('Test'):
        key=(item.get('Package'),item['Test'])
        if item['Action']=='pass': passed.add(key)
        if item['Action']=='skip': skipped.append(key)
        if item['Action']=='fail': failed.append(key)
missing=[(package,name) for package,names in expected.items() for name in names if (package,name) not in passed]
ok=process_result['returncode']==0 and not process_result['timed_out'] and not process_result['orphaned'] and process_result['clean'] and not skipped and not failed and not missing
summary={'phase':phase,**process_result,'expected':sum(map(len,expected.values())),'missing':missing,'skipped':skipped,'failed':failed,'ok':ok}
(REPORT/(phase+'.json')).write_text(json.dumps(summary,indent=2)+'\n',encoding='utf-8')
print(json.dumps(summary))
if not ok: sys.exit(1)
